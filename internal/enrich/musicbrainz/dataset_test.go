package musicbrainz

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/metadata"
)

func localDataset(t *testing.T, catalogVersion string) string {
	t.Helper()
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	_, err := io.WriteString(gz, `<releases><release id="1"><title>Fixture</title><artists><artist><id>7</id><name>A</name></artist></artists><genres><genre>Electronic</genre></genres><tracklist><track><position>1</position><title>One</title><extraartists><artist><id>9</id><name>Composer</name><role>Composed By</role></artist></extraartists></track><track><position>2</position><title>Two</title><artists><artist><id>7</id><name>A</name></artist><artist><id>8</id><name>Blocked</name></artist></artists></track></tracklist></release></releases>`)
	if err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "releases.xml.gz")
	if err := os.WriteFile(path, raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "discogs.sqlite")
	_, err = metadata.Build(context.Background(), metadata.BuildOptions{Output: out, Date: "20260901", CatalogVersion: catalogVersion,
		Tracks: []core.TrackRef{{ID: "a1", Artist: "A", Title: "One"}, {ID: "a2", Artist: "A", Title: "Two"}},
		Inputs: []metadata.Input{{Path: path, SHA256: fmt.Sprintf("%x", sha256.Sum256(raw.Bytes()))}}})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLocalDatasetBeforeNetworkAndOfflineReplay(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"count":0,"artists":[]}`)
	}))
	defer server.Close()
	cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "a1", Display: "A - One"}, fakes.CatalogTrack{ID: "a2", Display: "A - Two"})
	c, err := New(Config{UserAgent: "fixture", MirrorURL: server.URL, Interval: time.Nanosecond, DatasetPath: localDataset(t, cat.CatalogVersion())})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	intent := core.MusicIntent{Seed: "42", Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "Electronic", Influence: core.InfluencePositive}}}}
	intent, err = c.PrepareMusic(context.Background(), intent, cat, cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream := c.OpenCandidates(intent, cat, cat)
	var first []core.TrackRef
	for range 2 {
		track, err := stream.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		first = append(first, track)
		if evidence := stream.Snapshot().DiscoveryEvidence[track.ID]; len(evidence) != 1 || evidence[0].Channel != "discogs_dump" {
			t.Fatal(evidence)
		}
	}
	if calls != 0 {
		t.Fatalf("local hits made %d HTTP requests", calls)
	}
	if len(stream.Snapshot().Tracks) != 0 {
		t.Fatal("release tags promoted to recording evidence")
	}
	if _, err := c.Graph(context.Background(), []string{"Electronic"}); err != nil || calls != 0 {
		t.Fatal("Discogs IDs sent to MusicBrainz", err, calls)
	}
	intent.Knowledge = stream.Snapshot()
	replay := c.OpenCandidates(intent, cat, cat)
	var second []core.TrackRef
	for range 2 {
		track, err := replay.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		second = append(second, track)
	}
	if !reflect.DeepEqual(first, second) || calls != 0 {
		t.Fatal("offline replay changed")
	}
	if _, err := stream.Next(context.Background()); err != io.EOF || calls != 1 {
		t.Fatal("exhaustion did not fall back", err, calls)
	}
	intent.Knowledge = nil
	intent.Constraints.ArtistsExclude = []string{"Blocked"}
	filtered := c.OpenCandidates(intent, cat, cat)
	track, err := filtered.Next(context.Background())
	if err != nil || track.ID != "a1" {
		t.Fatal("secondary credit exclusion bypassed", track, err)
	}
	if _, err := filtered.Next(context.Background()); err != io.EOF {
		t.Fatal("blocked recording returned", err)
	}
	if err := c.ClearCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.MetadataStatus().DatasetTracks != 2 || !c.dataset.HasGenre(context.Background(), "Electronic") {
		t.Fatal("clearing API cache removed bulk index")
	}
}

func TestComposerEnrichmentSurvivesMissingOnlineMatchAndSerialization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"count":0,"recordings":[]}`)
	}))
	defer server.Close()
	c, err := New(Config{UserAgent: "fixture", MirrorURL: server.URL, Interval: time.Nanosecond, DatasetPath: localDataset(t, "fixture")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tracks, err := c.Enrich(context.Background(), []core.TrackRef{{ID: "a1", Artist: "A", Title: "One"}}, nil)
	if err != nil || len(tracks) != 1 || tracks[0].Matched || len(tracks[0].ComposerCredits) != 1 {
		t.Fatal(tracks, err)
	}
	credit := tracks[0].ComposerCredits[0]
	if credit.Name != "Composer" || credit.Scope != "track" || !strings.HasSuffix(credit.Source, "/release/1") {
		t.Fatal(credit)
	}
	raw, err := json.Marshal(tracks)
	if err != nil {
		t.Fatal(err)
	}
	var loaded []core.EnrichedTrack
	if err := json.Unmarshal(raw, &loaded); err != nil || !reflect.DeepEqual(tracks, loaded) {
		t.Fatal(loaded, err)
	}
}

func TestMissingIncompatibleAndInvalidDatasetFallback(t *testing.T) {
	for _, mode := range []string{"missing", "incompatible", "invalid", "unknown_genre"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_, _ = io.WriteString(w, `{"count":0,"artists":[]}`)
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "missing.sqlite")
			if mode == "incompatible" {
				path = localDataset(t, "old-catalog")
			}
			if mode == "unknown_genre" {
				path = localDataset(t, "fake:v1")
			}
			if mode == "invalid" {
				if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			c, err := New(Config{UserAgent: "fixture", MirrorURL: server.URL, Interval: time.Nanosecond, DatasetPath: path})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "a1", Display: "A - One"})
			genre := "Electronic"
			if mode == "unknown_genre" {
				genre = "Unknown"
			}
			intent := core.MusicIntent{Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: genre, Influence: core.InfluencePositive}}}}
			_, err = c.OpenCandidates(intent, cat, cat).Next(context.Background())
			if err != io.EOF || calls != 1 {
				t.Fatal("API fallback missing", err, calls)
			}
			if c.MetadataStatus().DatasetError != (mode == "invalid") {
				t.Fatal("invalid dataset status")
			}
		})
	}
}
