package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/httpretry"
	"github.com/platten/playlistai/internal/logging"
)

const AcousticBrainzURL = "https://acousticbrainz.org"
const acousticNamespace = "acousticbrainz-projection-v1:"
const acousticFeatures = "rhythm.bpm;rhythm.danceability;tonal.key_key;tonal.key_scale;tonal.key_strength;lowlevel.average_loudness;lowlevel.dynamic_complexity"

type acousticClient struct {
	base string
	http *http.Client
}

type acousticThrottle struct {
	gate chan struct{}
	next time.Time
}

var acousticThrottles sync.Map

func newAcousticClient(base string) (*acousticClient, error) {
	base = strings.TrimRight(base, "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("invalid AcousticBrainz endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	local := u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
	if !local && base != AcousticBrainzURL {
		return nil, errors.New("AcousticBrainz endpoint overrides are restricted to local tests")
	}
	state, _ := acousticThrottles.LoadOrStore(base, &acousticThrottle{gate: make(chan struct{}, 1)})
	interval := time.Second
	if local {
		interval = 0
	}
	return &acousticClient{base: base, http: &http.Client{
		Timeout:       4 * time.Second,
		Transport:     &acousticTransport{state: state.(*acousticThrottle), base: http.DefaultTransport, interval: interval},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

type acousticTransport struct {
	state    *acousticThrottle
	base     http.RoundTripper
	interval time.Duration
}

func (t *acousticTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case t.state.gate <- struct{}{}:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	defer func() { <-t.state.gate }()
	// Optional evidence never holds a playlist through a provider backoff.
	wait := time.Until(t.state.next)
	if wait > t.interval {
		return nil, core.ErrUnavailable
	}
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	t.state.next = time.Now().Add(t.interval)
	started := time.Now()
	logging.Diagnostic(req.Context(), "api.call", map[string]any{"provider": "acousticbrainz", "method": req.Method, "url": req.URL.String(), "attempt": 1})
	resp, err := t.base.RoundTrip(req)
	status, contentLength := 0, int64(-1)
	if resp != nil {
		status, contentLength = resp.StatusCode, resp.ContentLength
	}
	errorDetail := ""
	if err != nil {
		errorDetail = err.Error()
	}
	logging.Diagnostic(req.Context(), "api.response", map[string]any{"provider": "acousticbrainz", "method": req.Method, "url": req.URL.String(), "status": status, "contentLength": contentLength, "elapsedMilliseconds": time.Since(started).Milliseconds(), "error": errorDetail})
	if err != nil && req.Context().Err() == nil || resp != nil && resp.StatusCode != http.StatusOK {
		t.state.next = time.Now().Add(time.Minute)
	}
	if resp != nil && (resp.StatusCode == 429 || resp.Header.Get("X-RateLimit-Remaining") == "0") {
		delay := httpretry.RetryAfter(resp.Header.Get("Retry-After"), time.Now())
		if seconds, err := strconv.ParseFloat(resp.Header.Get("X-RateLimit-Reset-In"), 64); err == nil && seconds > 0 && seconds <= 86400 {
			delay = max(delay, time.Duration(seconds*float64(time.Second)))
		}
		t.state.next = time.Now().Add(max(time.Second, delay))
	}
	return resp, err
}

// A narrow projection deliberately excludes uploaded filenames, tags, hashes,
// large spectral arrays, and demographic classifiers. No audio is downloaded.
type acousticDocument struct {
	Rhythm struct {
		BPM          *float64 `json:"bpm,omitempty"`
		Danceability *float64 `json:"danceability,omitempty"`
	} `json:"rhythm"`
	Tonal struct {
		Key      string   `json:"key_key,omitempty"`
		Scale    string   `json:"key_scale,omitempty"`
		Strength *float64 `json:"key_strength,omitempty"`
	} `json:"tonal"`
	Low struct {
		Loudness   *float64 `json:"average_loudness,omitempty"`
		Complexity *float64 `json:"dynamic_complexity,omitempty"`
	} `json:"lowlevel"`
	Metadata struct {
		Audio struct {
			Length *float64 `json:"length,omitempty"`
		} `json:"audio_properties"`
		Version map[string]string `json:"version,omitempty"`
	} `json:"metadata"`
	High map[string]acousticPrediction `json:"highlevel,omitempty"`
}

type acousticPrediction struct {
	Value   string             `json:"value"`
	Score   float64            `json:"probability"`
	Classes map[string]float64 `json:"all"`
	Version map[string]string  `json:"version"`
}

// high-level metadata.version contains nested objects; model versions are
// retained per classifier instead. Parse the two endpoints independently.
func acousticProjection(raw json.RawMessage, level string) (acousticDocument, error) {
	var doc acousticDocument
	if level == "high-level" {
		var high struct {
			High map[string]acousticPrediction `json:"highlevel"`
		}
		if err := json.Unmarshal(raw, &high); err != nil || high.High == nil {
			return doc, errors.New("invalid AcousticBrainz high-level document")
		}
		doc.High = map[string]acousticPrediction{}
		for key, p := range high.High {
			supported := strings.HasPrefix(key, "genre_") || strings.HasPrefix(key, "mood_") || key == "voice_instrumental" || key == "danceability" || key == "timbre" || key == "tonal_atonal"
			if !supported {
				continue
			}
			if p.Value == "" || p.Score < 0 || p.Score > 1 || len(p.Version) == 0 || len(p.Classes) == 0 {
				continue
			}
			score, ok := p.Classes[p.Value]
			valid := ok && score == p.Score
			for _, v := range p.Classes {
				valid = valid && v >= 0 && v <= 1
			}
			if valid {
				doc.High[key] = p
			}
		}
		return doc, nil
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Metadata.Version) == 0 {
		return doc, errors.New("invalid AcousticBrainz low-level document")
	}
	doc.High = nil
	positive := func(v **float64) {
		if *v != nil && **v < 0 {
			*v = nil
		}
	}
	for _, v := range []**float64{&doc.Rhythm.BPM, &doc.Rhythm.Danceability, &doc.Low.Loudness, &doc.Low.Complexity, &doc.Metadata.Audio.Length} {
		positive(v)
	}
	if doc.Tonal.Strength != nil && (*doc.Tonal.Strength < -1 || *doc.Tonal.Strength > 1) {
		doc.Tonal.Strength = nil
	}
	return doc, nil
}

// Per-recording entries survive different playlist batches. {} is a confirmed
// omission from a successful bulk response, never an outage or invalid JSON.
func (c *Client) acousticBatch(ctx context.Context, ids []string, level string) map[string]acousticDocument {
	out := map[string]acousticDocument{}
	var missing []string
	for _, id := range ids {
		key := metadataKey(c.acoustic.base, level+"/"+id, acousticNamespace)
		entry := c.readCache(ctx, key)
		var doc acousticDocument
		if entry.body != "" && json.Unmarshal([]byte(entry.body), &doc) == nil {
			out[id] = doc // stale evidence may be used during an outage
			age := time.Since(time.Unix(entry.fetched, 0))
			if age >= 0 && age < musicBrainzTTL {
				continue
			}
		}
		missing = append(missing, id)
	}
	only, _ := ctx.Value(cacheOnlyKey{}).(bool)
	if len(missing) == 0 || only || ctx.Err() != nil {
		return out
	}
	sort.Strings(missing)
	gate := metadataKey(c.acoustic.base, level+"/batch/"+strings.Join(missing, ";"), acousticNamespace)
	owner, done, epoch := c.takeFetch(gate)
	if !owner {
		select {
		case <-done:
			return c.acousticBatch(context.WithValue(ctx, cacheOnlyKey{}, true), ids, level)
		case <-ctx.Done():
			return out
		}
	}
	defer c.finishFetch(gate)
	// Another owner may have completed between our first cache read and gate.
	remaining := missing[:0]
	for _, id := range missing {
		entry := c.readCache(ctx, metadataKey(c.acoustic.base, level+"/"+id, acousticNamespace))
		age := time.Since(time.Unix(entry.fetched, 0))
		var doc acousticDocument
		if entry.body != "" && age >= 0 && age < musicBrainzTTL && json.Unmarshal([]byte(entry.body), &doc) == nil {
			out[id] = doc
		} else {
			remaining = append(remaining, id)
		}
	}
	missing = remaining
	if len(missing) == 0 {
		return out
	}
	values := url.Values{"recording_ids": {strings.Join(missing, ";")}}
	if level == "low-level" {
		values.Set("features", acousticFeatures)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.acoustic.base+"/api/v1/"+level+"?"+values.Encode(), nil)
	if err != nil {
		return out
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")
	resp, err := c.acoustic.http.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return out
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
		return out
	}
	// Reject provider error envelopes and unrelated success-shaped JSON.
	wanted := map[string]bool{"mbid_mapping": true}
	for _, id := range missing {
		wanted[id] = true
	}
	for key := range envelope {
		if !wanted[key] {
			return out
		}
	}
	for _, id := range missing {
		doc := acousticDocument{}
		if value, ok := envelope[id]; ok {
			var offsets map[string]json.RawMessage
			if json.Unmarshal(value, &offsets) != nil || offsets["0"] == nil {
				continue
			}
			var err error
			doc, err = acousticProjection(offsets["0"], level)
			if err != nil {
				continue
			}
		}
		encoded, err := json.Marshal(doc)
		if err != nil {
			continue
		}
		c.writeCache(ctx, metadataKey(c.acoustic.base, level+"/"+id, acousticNamespace), encoded, epoch)
		out[id] = doc
	}
	return out
}

// Generation callers cap enrichment at 25 identities per stage. Optional
// lookup gets at most eight seconds per call; cached reads remain offline.
func (c *Client) acousticTracks(ctx context.Context, tracks []core.EnrichedTrack, limit int) {
	if c.acoustic == nil || ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ids := []string{}
	seen := map[string]bool{}
	for i := range tracks {
		t := &tracks[i]
		if !t.Matched || t.IdentityStatus != core.ResolutionResolved {
			t.Acoustic = nil
			continue
		}
		id, err := uuid.Parse(t.RecordingID)
		if err != nil || id == uuid.Nil || t.Acoustic != nil {
			continue
		}
		key := id.String()
		if !seen[key] && len(ids) < limit {
			ids = append(ids, key)
			seen[key] = true
		}
	}
	for start := 0; start < len(ids) && ctx.Err() == nil; start += 25 {
		batch := ids[start:min(start+25, len(ids))]
		low := c.acousticBatch(ctx, batch, "low-level")
		high := c.acousticBatch(ctx, batch, "high-level")
		for i := range tracks {
			t := &tracks[i]
			id := strings.ToLower(t.RecordingID)
			inBatch := false
			for _, key := range batch {
				inBatch = inBatch || id == key
			}
			if !inBatch || !t.Matched || t.IdentityStatus != core.ResolutionResolved {
				continue
			}
			a := &core.AcousticCharacteristics{SchemaVersion: 1, RecordingID: id, Source: c.acoustic.base, Submission: 0, LowStatus: "unavailable", HighStatus: "unavailable"}
			if d, ok := low[id]; ok {
				a.LowStatus = "missing"
				if len(d.Metadata.Version) > 0 {
					a.LowStatus = "available"
					a.Low = &core.AcousticMeasurements{BPM: d.Rhythm.BPM, Danceability: d.Rhythm.Danceability, AverageLoudness: d.Low.Loudness, DynamicComplexity: d.Low.Complexity, Key: d.Tonal.Key, Scale: d.Tonal.Scale, KeyStrength: d.Tonal.Strength, AnalyzedSeconds: d.Metadata.Audio.Length, Version: d.Metadata.Version}
				}
			}
			if d, ok := high[id]; ok {
				a.HighStatus = "missing"
				if len(d.High) > 0 {
					a.HighStatus = "available"
					a.Predictions = map[string]core.AcousticPrediction{}
					for k, p := range d.High {
						a.Predictions[k] = core.AcousticPrediction{Value: p.Value, Score: p.Score, Classes: p.Classes, Version: p.Version}
					}
				}
			}
			t.Acoustic = a
		}
	}
}
