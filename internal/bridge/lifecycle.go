package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
)

const (
	defaultRecommendationAlgorithmVersion = "unversioned"
	generationOperation                   = "generation"
	maxParsedIntentCacheEntries           = 64
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
	State              string               `json:"state"` // fulfilled | partial | unsupported | needs_clarification
	ParsedIntentReused bool                 `json:"parsedIntentReused"`
	Parser             ParserStatus         `json:"parser"`
	PartialReasons     []PlaylistNotice     `json:"partialReasons"`
	Reasons            []core.OutcomeReason `json:"reasons"`
	Timings            []StageTiming        `json:"timings"`
}

type Reproducibility struct {
	EvidenceSnapshot   string       `json:"evidenceSnapshot"`
	ID                 string       `json:"id"`
	CatalogVersion     string       `json:"catalogVersion"`
	AlgorithmVersion   string       `json:"algorithmVersion"`
	IntentFingerprint  string       `json:"intentFingerprint"`
	ProfileVersion     string       `json:"profileVersion"`
	ProfileSnapshot    string       `json:"profileSnapshot"`
	ContextFingerprint string       `json:"contextFingerprint"`
	RNGSeed            core.RNGSeed `json:"rngSeed"`
}

func audioIdentity(generation, evidence string) string {
	sum := sha256.Sum256([]byte(generation + "\x00" + evidence))
	return hex.EncodeToString(sum[:])
}

type parsedIntentEntry struct {
	intent  core.MusicIntent
	outcome app.ParseOutcome
}

type intentCache struct {
	mu      sync.Mutex
	entries map[string]parsedIntentEntry
	epoch   uint64
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

func (s *operationSet) cancelAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for group, operation := range s.active {
		operation.cancel()
		delete(s.active, group)
	}
}

// Settings that replace evidence or parser resources invalidate both the
// preview interpretation and the one shared prompt/rebuild operation.
func (a *API) cancelRecommendationWork() {
	a.operations.cancel("intent-preview")
	a.operations.cancel(generationOperation)
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

func (c *intentCache) scopedKey(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fmt.Sprintf("%d:%s", c.epoch, key)
}

func (c *intentCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++ // late parses may finish, but their earlier keys cannot be reused
	c.entries = nil
}

func (a *API) parseIntentCached(ctx context.Context, input ports.IntentInput, progress ports.Progress) (parsedIntentEntry, bool, error) {
	key, err := a.intentCacheKey(input)
	if err != nil {
		return parsedIntentEntry{}, false, err
	}
	key = a.intentCache.scopedKey(key)
	if entry, ok := a.intentCache.get(key); ok {
		entry.intent = a.confirmSubmittedGenre(ctx, input, entry.intent, entry.outcome.Backend)
		a.intentCache.put(key, entry)
		return entry, true, nil
	}
	outcome, err := a.app.ParseIntentDetailed(ctx, input, progress)
	if err != nil {
		return parsedIntentEntry{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return parsedIntentEntry{}, false, err
	}
	name := rules.BareGenreQuery(input.Prompt)
	if cached, ok := a.app.Knowledge.(ports.CachedGenreKnowledge); ok && !input.SkipMetadata && outcome.Backend == "rules" && name != "" && cached.IsCachedGenre(ctx, name) {
		outcome.Intent.OriginalDescription = input.Prompt
		outcome.Intent = rules.ApplyConfirmedGenre(outcome.Intent, name)
	}
	entry := parsedIntentEntry{intent: outcome.Intent.Normalized(), outcome: outcome}
	entry.intent = a.confirmSubmittedGenre(ctx, input, entry.intent, outcome.Backend)
	a.intentCache.put(key, entry)
	return entry, false, nil
}

// Explicitly submitted parsing can check provider genre identity before the UI
// offers misleading artist alternatives. Legacy preview calls remain offline.
func (a *API) confirmSubmittedGenre(ctx context.Context, input ports.IntentInput, intent core.MusicIntent, backend string) core.MusicIntent {
	if input.SkipMetadata {
		return intent
	}
	name := rules.BareGenreQuery(input.Prompt)
	provider, ok := a.app.Knowledge.(ports.GenreNameKnowledge)
	if !ok || backend != "rules" || input.GenerationID == "" || name == "" || len(intent.EssentialCriteria) > 0 || len(intent.Preferences.Genres) > 0 {
		return intent
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	graph, err := provider.GenreNames(ctx)
	if err != nil {
		return intent
	}
	for _, node := range graph.Nodes {
		if node.ID == graph.ID(name) {
			intent.OriginalDescription = input.Prompt
			return rules.ApplyConfirmedGenre(intent, name)
		}
	}
	return intent
}

func (a *API) intentCacheKey(input ports.IntentInput) (string, error) {
	return hashIntentCacheKey(input, a.app.ParserIdentity(), schema.Version)
}

func hashIntentCacheKey(input ports.IntentInput, parserIdentity string, schemaVersion int) (string, error) {
	payload := struct {
		SkipMetadata   bool            `json:"skipMetadata"`
		Prompt         string          `json:"prompt"`
		SessionID      string          `json:"sessionId"`
		ParserIdentity string          `json:"parserIdentity"`
		SchemaVersion  int             `json:"schemaVersion"`
		NowPlaying     *core.TrackRef  `json:"nowPlaying"`
		RecentTracks   []core.TrackRef `json:"recentTracks"`
		Locale         string          `json:"locale"`
	}{input.SkipMetadata, input.Prompt, input.SessionID, parserIdentity, schemaVersion, input.NowPlaying, input.RecentTracks, input.Locale}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func generationIdentity(intent core.MusicIntent, catalogVersion, algorithmVersion, profileVersion, profileSnapshot string, recentSelections ...[]core.TrackRef) (Reproducibility, error) {
	intent = intent.Normalized()
	recent := []core.TrackRef{}
	if len(recentSelections) > 0 {
		recent = append(recent, recentSelections[0]...)
	}
	raw, err := json.Marshal(struct {
		CatalogVersion   string           `json:"catalogVersion"`
		AlgorithmVersion string           `json:"algorithmVersion"`
		Intent           core.MusicIntent `json:"intent"`
		ProfileVersion   string           `json:"profileVersion"`
		ProfileSnapshot  string           `json:"profileSnapshot"`
		RecentSelections []core.TrackRef  `json:"recentSelections"`
	}{catalogVersion, algorithmVersion, intent, profileVersion, profileSnapshot, recent})
	if err != nil {
		return Reproducibility{}, fmt.Errorf("generation identity: %w", err)
	}
	intentRaw, err := json.Marshal(intent)
	if err != nil {
		return Reproducibility{}, fmt.Errorf("intent fingerprint: %w", err)
	}
	contextRaw, err := json.Marshal(recent)
	if err != nil {
		return Reproducibility{}, fmt.Errorf("generation context fingerprint: %w", err)
	}
	allSum, intentSum, contextSum := sha256.Sum256(raw), sha256.Sum256(intentRaw), sha256.Sum256(contextRaw)
	return Reproducibility{
		ID: hex.EncodeToString(allSum[:]), CatalogVersion: catalogVersion,
		AlgorithmVersion:   algorithmVersion,
		IntentFingerprint:  hex.EncodeToString(intentSum[:]),
		ContextFingerprint: hex.EncodeToString(contextSum[:]),
		ProfileVersion:     profileVersion, ProfileSnapshot: profileSnapshot, RNGSeed: intent.Seed,
	}, nil
}

func parserStatus(outcome app.ParseOutcome) ParserStatus {
	return ParserStatus{
		Backend: outcome.Backend, RequestedBackend: outcome.RequestedBackend,
		FallbackUsed: outcome.FallbackUsed, FallbackReason: outcome.FallbackReason,
	}
}
