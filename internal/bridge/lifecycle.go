package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
)

const (
	recommendationAlgorithmVersion = "deejai/v4"
	profileVersion                 = "none/v1"
	profileSnapshot                = "none"
	maxParsedIntentCacheEntries    = 64
)

type StageTiming struct {
	Stage        string `json:"stage"`
	Milliseconds int64  `json:"milliseconds"`
}

type ParserStatus struct {
	Backend          string `json:"backend"`
	RequestedBackend string `json:"requestedBackend"`
	FallbackUsed     bool   `json:"fallbackUsed"`
	FallbackReason   string `json:"fallbackReason"`
}

type GenerationStatus struct {
	State              string           `json:"state"` // complete | partial
	ParsedIntentReused bool             `json:"parsedIntentReused"`
	Parser             ParserStatus     `json:"parser"`
	PartialReasons     []PlaylistNotice `json:"partialReasons"`
	Timings            []StageTiming    `json:"timings"`
}

type Reproducibility struct {
	ID                string       `json:"id"`
	CatalogVersion    string       `json:"catalogVersion"`
	AlgorithmVersion  string       `json:"algorithmVersion"`
	IntentFingerprint string       `json:"intentFingerprint"`
	ProfileVersion    string       `json:"profileVersion"`
	ProfileSnapshot   string       `json:"profileSnapshot"`
	RNGSeed           core.RNGSeed `json:"rngSeed"`
}

type parsedIntentEntry struct {
	intent  core.MusicIntent
	outcome app.ParseOutcome
}

type intentCache struct {
	mu      sync.Mutex
	entries map[string]parsedIntentEntry
}

type activeOperation struct {
	id     uint64
	cancel context.CancelFunc
}

type operationSet struct {
	mu     sync.Mutex
	nextID uint64
	active map[string]activeOperation
}

func (s *operationSet) begin(parent context.Context, group string) (context.Context, func() bool, func()) {
	s.mu.Lock()
	if s.active == nil {
		s.active = make(map[string]activeOperation)
	}
	if previous, ok := s.active[group]; ok {
		previous.cancel()
	}
	s.nextID++
	id := s.nextID
	ctx, cancel := context.WithCancel(parent)
	s.active[group] = activeOperation{id: id, cancel: cancel}
	s.mu.Unlock()
	current := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		operation, ok := s.active[group]
		return ok && operation.id == id
	}
	finish := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if operation, ok := s.active[group]; ok && operation.id == id {
			delete(s.active, group)
		}
		cancel()
	}
	return ctx, current, finish
}

func (s *operationSet) cancel(group string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if operation, ok := s.active[group]; ok {
		operation.cancel()
		delete(s.active, group)
	}
}

func (c *intentCache) get(key string) (parsedIntentEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	return entry, ok
}

func (c *intentCache) put(key string, entry parsedIntentEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || len(c.entries) >= maxParsedIntentCacheEntries {
		c.entries = make(map[string]parsedIntentEntry)
	}
	c.entries[key] = entry
}

func (a *API) parseIntentCached(ctx context.Context, input ports.IntentInput, progress ports.Progress) (parsedIntentEntry, bool, error) {
	key, err := a.intentCacheKey(input)
	if err != nil {
		return parsedIntentEntry{}, false, err
	}
	if entry, ok := a.intentCache.get(key); ok {
		return entry, true, nil
	}
	outcome, err := a.app.ParseIntentDetailed(ctx, input, progress)
	if err != nil {
		return parsedIntentEntry{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return parsedIntentEntry{}, false, err
	}
	entry := parsedIntentEntry{intent: outcome.Intent.Normalized(), outcome: outcome}
	a.intentCache.put(key, entry)
	return entry, false, nil
}

func (a *API) intentCacheKey(input ports.IntentInput) (string, error) {
	return hashIntentCacheKey(input, a.app.ParserIdentity(), schema.Version)
}

func hashIntentCacheKey(input ports.IntentInput, parserIdentity string, schemaVersion int) (string, error) {
	payload := struct {
		Prompt         string          `json:"prompt"`
		ParserIdentity string          `json:"parserIdentity"`
		SchemaVersion  int             `json:"schemaVersion"`
		NowPlaying     *core.TrackRef  `json:"nowPlaying"`
		RecentTracks   []core.TrackRef `json:"recentTracks"`
		Locale         string          `json:"locale"`
	}{input.Prompt, parserIdentity, schemaVersion, input.NowPlaying, input.RecentTracks, input.Locale}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func generationIdentity(intent core.MusicIntent, catalogVersion string) (Reproducibility, error) {
	intent = intent.Normalized()
	raw, err := json.Marshal(struct {
		CatalogVersion   string           `json:"catalogVersion"`
		AlgorithmVersion string           `json:"algorithmVersion"`
		Intent           core.MusicIntent `json:"intent"`
		ProfileVersion   string           `json:"profileVersion"`
		ProfileSnapshot  string           `json:"profileSnapshot"`
	}{catalogVersion, recommendationAlgorithmVersion, intent, profileVersion, profileSnapshot})
	if err != nil {
		return Reproducibility{}, fmt.Errorf("generation identity: %w", err)
	}
	intentRaw, err := json.Marshal(intent)
	if err != nil {
		return Reproducibility{}, fmt.Errorf("intent fingerprint: %w", err)
	}
	allSum, intentSum := sha256.Sum256(raw), sha256.Sum256(intentRaw)
	return Reproducibility{
		ID: hex.EncodeToString(allSum[:]), CatalogVersion: catalogVersion,
		AlgorithmVersion:  recommendationAlgorithmVersion,
		IntentFingerprint: hex.EncodeToString(intentSum[:]),
		ProfileVersion:    profileVersion, ProfileSnapshot: profileSnapshot, RNGSeed: intent.Seed,
	}, nil
}

func parserStatus(outcome app.ParseOutcome) ParserStatus {
	return ParserStatus{
		Backend: outcome.Backend, RequestedBackend: outcome.RequestedBackend,
		FallbackUsed: outcome.FallbackUsed, FallbackReason: outcome.FallbackReason,
	}
}
