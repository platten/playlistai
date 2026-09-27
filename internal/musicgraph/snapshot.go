// Package musicgraph holds prepared, public discovery priors. Popularity and
// graph relationships are not evidence of a recording's musical attributes.
package musicgraph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	Version          = "prepared-music-graph/v1"
	MaxSnapshotBytes = 64 << 20
	MaxBatch         = 1000
	MaxTopRecordings = 100
	MaxNeighbors     = 20
)

// Source identifies the public response, not a private request or listener.
type Source struct {
	Provider       string    `json:"provider"`
	URL            string    `json:"url"`
	RetrievedAt    time.Time `json:"retrievedAt"`
	ResponseSHA256 string    `json:"responseSha256"`
	License        string    `json:"license"`
}

type Counts struct {
	UniqueListeners *int64 `json:"uniqueListeners"`
	ListenCount     *int64 `json:"listenCount"`
}

type Artist struct {
	MBID string `json:"mbid"`
	Counts
	Source Source `json:"source"`
}

type Recording struct {
	MBID        string   `json:"mbid"`
	ArtistMBIDs []string `json:"artistMbids"`
	Title       string   `json:"title,omitempty"`
	DurationMS  int64    `json:"durationMs,omitempty"`
	Counts
	Source Source `json:"source"`
	// CountsSource is separate from the radio relationship used for identity.
	CountsSource *Source `json:"countsSource,omitempty"`
}

type ArtistRecordings struct {
	ArtistMBID     string   `json:"artistMbid"`
	RecordingMBIDs []string `json:"recordingMbids"`
	Source         Source   `json:"source"`
}

// Neighbor is a directed discovery relationship, not a genre or similarity
// probability. RecordingMBIDs are suggestions for the neighbor, not the seed.
type Neighbor struct {
	SeedArtistMBID string   `json:"seedArtistMbid"`
	ArtistMBID     string   `json:"artistMbid"`
	RecordingMBIDs []string `json:"recordingMbids"`
	Source         Source   `json:"source"`
}

// DiscoveryOmission records an entire omitted seed batch, never partially
// accepted malformed rows. Popularity for the seed remains independently usable.
type DiscoveryOmission struct {
	ArtistMBID string  `json:"artistMbid"`
	Reason     string  `json:"reason"`
	Source     *Source `json:"source,omitempty"`
}

type Snapshot struct {
	Version          string              `json:"version"`
	PreparedAt       time.Time           `json:"preparedAt"`
	Artists          []Artist            `json:"artists"`
	Recordings       []Recording         `json:"recordings"`
	TopRecordings    []ArtistRecordings  `json:"topRecordings"`
	Neighbors        []Neighbor          `json:"neighbors"`
	OmittedDiscovery []DiscoveryOmission `json:"omittedDiscovery,omitempty"`
}

func validID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v != uuid.Nil && v.String() == id
}

func validHash(v string) bool {
	b, err := hex.DecodeString(v)
	return err == nil && len(b) == sha256.Size && v == strings.ToLower(v)
}

func (s Source) valid() bool {
	return s.Provider == "listenbrainz" && s.License == "CC0-1.0" &&
		!s.RetrievedAt.IsZero() && validHash(s.ResponseSHA256) && validEndpoint(s.URL)
}

func (c Counts) valid() bool {
	return (c.UniqueListeners == nil || *c.UniqueListeners >= 0) &&
		(c.ListenCount == nil || *c.ListenCount >= 0) &&
		(c.UniqueListeners == nil || c.ListenCount == nil || *c.UniqueListeners <= *c.ListenCount)
}

func validIDs(ids []string, limit int) bool {
	seen := make(map[string]bool, len(ids))
	if len(ids) > limit {
		return false
	}
	for _, id := range ids {
		if !validID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func (s Snapshot) Validate() error {
	if s.Version != Version || s.PreparedAt.IsZero() || len(s.Artists) > 100000 || len(s.Recordings) > 200000 {
		return errors.New("unsupported or oversized music graph snapshot")
	}
	artists := map[string]bool{}
	for _, a := range s.Artists {
		if !validID(a.MBID) || artists[a.MBID] || !a.valid() || !a.Source.valid() || a.Source.URL != apiBase+"/1/popularity/artist" || a.Source.RetrievedAt.After(s.PreparedAt) {
			return errors.New("invalid or duplicate music graph artist")
		}
		artists[a.MBID] = true
	}
	recordings := map[string]Recording{}
	for _, r := range s.Recordings {
		if _, exists := recordings[r.MBID]; exists {
			return errors.New("duplicate music graph recording")
		}
		if !validID(r.MBID) || len(r.ArtistMBIDs) == 0 || !validIDs(r.ArtistMBIDs, 100) || !r.valid() || !r.Source.valid() || r.Source.RetrievedAt.After(s.PreparedAt) || r.DurationMS < 0 || len(r.Title) > 4096 {
			return errors.New("invalid music graph recording")
		}
		if r.CountsSource != nil && (!r.CountsSource.valid() || r.CountsSource.URL != apiBase+"/1/popularity/recording" || r.CountsSource.RetrievedAt.After(s.PreparedAt)) {
			return errors.New("invalid recording count source")
		}
		if strings.Contains(r.Source.URL, "/top-recordings-for-artist/") {
			id := strings.TrimPrefix(r.Source.URL, apiBase+"/1/popularity/top-recordings-for-artist/")
			if !slices.Contains(r.ArtistMBIDs, id) {
				return errors.New("recording source artist mismatch")
			}
		} else if !strings.Contains(r.Source.URL, "/lb-radio/artist/") {
			return errors.New("invalid recording source")
		}
		recordings[r.MBID] = r
	}
	check := func(artist string, ids []string, source Source) bool {
		if !validID(artist) || !validIDs(ids, MaxTopRecordings) || !source.valid() || source.RetrievedAt.After(s.PreparedAt) {
			return false
		}
		for _, id := range ids {
			r, exists := recordings[id]
			if !exists || !slices.Contains(r.ArtistMBIDs, artist) {
				return false
			}
		}
		return true
	}
	top := map[string]bool{}
	for _, t := range s.TopRecordings {
		if top[t.ArtistMBID] || (t.Source.URL != topURL(t.ArtistMBID) && t.Source.URL != radioURL(t.ArtistMBID)) || !check(t.ArtistMBID, t.RecordingMBIDs, t.Source) {
			return errors.New("invalid top recording relationship")
		}
		top[t.ArtistMBID] = true
	}
	edges := map[string]bool{}
	neighborCounts := map[string]int{}
	for _, n := range s.Neighbors {
		key := n.SeedArtistMBID + "/" + n.ArtistMBID
		neighborCounts[n.SeedArtistMBID]++
		if !validID(n.SeedArtistMBID) || n.Source.URL != radioURL(n.SeedArtistMBID) || n.SeedArtistMBID == n.ArtistMBID || edges[key] || neighborCounts[n.SeedArtistMBID] > MaxNeighbors || !check(n.ArtistMBID, n.RecordingMBIDs, n.Source) {
			return errors.New("invalid neighboring artist relationship")
		}
		edges[key] = true
	}
	omitted := map[string]bool{}
	for _, omission := range s.OmittedDiscovery {
		if !validID(omission.ArtistMBID) || !artists[omission.ArtistMBID] || omitted[omission.ArtistMBID] || top[omission.ArtistMBID] || neighborCounts[omission.ArtistMBID] > 0 {
			return errors.New("invalid omitted discovery seed")
		}
		if omission.Reason != "unavailable" && omission.Reason != "invalid_response" && omission.Reason != "conflicting_recording_artist" {
			return errors.New("invalid omitted discovery reason")
		}
		if source := omission.Source; source != nil {
			if !source.valid() || source.URL != radioURL(omission.ArtistMBID) || source.RetrievedAt.After(s.PreparedAt) {
				return errors.New("invalid omitted discovery source")
			}
		} else if omission.Reason != "unavailable" {
			return errors.New("omitted malformed discovery requires response provenance")
		}
		omitted[omission.ArtistMBID] = true
	}
	return nil
}

// Decode accepts only the versioned projection, never arbitrary provider data.
func Decode(r io.Reader) (Snapshot, error) {
	var s Snapshot
	b, err := io.ReadAll(io.LimitReader(r, MaxSnapshotBytes+1))
	if err != nil {
		return s, err
	}
	if len(b) > MaxSnapshotBytes {
		return s, errors.New("music graph exceeds size limit")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&s); err != nil {
		return s, err
	}
	if d.Decode(new(any)) != io.EOF {
		return s, errors.New("trailing music graph data")
	}
	return s, s.Validate()
}

// Write publishes a new immutable artifact. Existing paths are never replaced;
// updating installations must activate a new versioned file after verification.
func Write(ctx context.Context, path string, snapshot Snapshot) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := snapshot.Validate(); err != nil {
		return "", err
	}
	// Canonical top-level ordering makes identical inputs reproducible. Caller
	// slices are copied so artifact preparation cannot reorder a shared input.
	snapshot.Artists = slices.Clone(snapshot.Artists)
	snapshot.Recordings = slices.Clone(snapshot.Recordings)
	snapshot.TopRecordings = slices.Clone(snapshot.TopRecordings)
	snapshot.Neighbors = slices.Clone(snapshot.Neighbors)
	snapshot.OmittedDiscovery = slices.Clone(snapshot.OmittedDiscovery)
	slices.SortFunc(snapshot.OmittedDiscovery, func(a, b DiscoveryOmission) int { return strings.Compare(a.ArtistMBID, b.ArtistMBID) })
	slices.SortFunc(snapshot.Artists, func(a, b Artist) int { return strings.Compare(a.MBID, b.MBID) })
	slices.SortFunc(snapshot.Recordings, func(a, b Recording) int { return strings.Compare(a.MBID, b.MBID) })
	slices.SortFunc(snapshot.TopRecordings, func(a, b ArtistRecordings) int { return strings.Compare(a.ArtistMBID, b.ArtistMBID) })
	slices.SortFunc(snapshot.Neighbors, func(a, b Neighbor) int {
		return strings.Compare(a.SeedArtistMBID+"/"+a.ArtistMBID, b.SeedArtistMBID+"/"+b.ArtistMBID)
	})
	b, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	if len(b) > MaxSnapshotBytes {
		return "", errors.New("music graph exceeds size limit")
	}
	if path == "" {
		return "", errors.New("explicit output path required")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".musicgraph-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(b); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	// Hard-link publication is atomic and cannot overwrite a concurrent writer.
	if err = os.Link(f.Name(), path); err != nil {
		return "", fmt.Errorf("publish music graph: %w", err)
	}
	return digest(b), nil
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
