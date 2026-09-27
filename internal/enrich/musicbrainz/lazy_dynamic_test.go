package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/catalog"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type forbiddenCandidatePreview struct{ t *testing.T }

func (p forbiddenCandidatePreview) ResolveAudioPreview(context.Context, core.TrackRef, core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
	p.t.Error("discovery eagerly resolved preview")
	return core.ResolvedAudioPreview{}, errors.New("preview unavailable")
}

func TestDynamicOnlinePageYieldsWithoutPreviewFanout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws/2/artist" {
			fmt.Fprintf(w, `{"artists":[{"id":%q,"name":"Outside Artist"}],"count":1}`, contextArtistID)
			return
		}
		rows := make([]mbRecording, 100)
		for i := range rows {
			rows[i] = mbRecording{ID: fmt.Sprintf("aaaaaaaa-1111-2222-3333-%012d", i+1), Title: fmt.Sprintf("Song %d (Live)", i), ArtistCredit: []mbArtistCredit{{Name: "Outside Artist"}}}
			rows[i].ArtistCredit[0].Artist.ID = contextArtistID
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"recordings": rows, "recording-count": 100})
	}))
	defer server.Close()
	client := newClient(t, server.URL, time.Nanosecond)
	client.candidatePreview = forbiddenCandidatePreview{t}
	base := fakes.NewCatalog(2)
	cat, err := catalog.OpenDynamic(base, base, filepath.Join(t.TempDir(), "dynamic.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	intent := core.MusicIntent{Seed: "42", Count: 10, Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid}, Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "classical", Influence: core.InfluencePositive}}}}
	stream := client.OpenCandidates(intent, cat, cat)
	// Race+coverage Windows runners can spend several seconds registering the
	// bounded page in SQLite; this remains a finite regression-test deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	track, err := stream.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := cat.Meta(track.ID)
	if !ok || meta.Ref.RecordingIdentity != track.ID || meta.PreviewURL != "" || len(stream.Snapshot().Tracks) != 8 {
		t.Fatalf("identity or registration budget changed: %+v tracks=%d", meta, len(stream.Snapshot().Tracks))
	}
	if len(stream.Snapshot().Notices) != 0 {
		t.Fatal("registration budget warned before the candidate stream was exhausted")
	}
	for i := 1; i < 80; i++ {
		if _, err := stream.Next(ctx); err != nil {
			t.Fatalf("candidate %d: %v", i, err)
		}
	}
	if _, err := stream.Next(ctx); err != io.EOF {
		t.Fatalf("candidate exhaustion: %v", err)
	}
	if got := stream.Snapshot().Notices; len(got) != 1 || !strings.Contains(got[0], "80-recording limit") {
		t.Fatalf("missing limit notice at exhaustion: %v", got)
	}
	if _, err := stream.Next(ctx); err != io.EOF || len(stream.Snapshot().Notices) != 1 {
		t.Fatalf("repeated exhaustion changed notices: %v, %v", err, stream.Snapshot().Notices)
	}
	// Checking a yielded track uses the real audio session. An unavailable
	// deferred preview stays unknown, never becoming affirmative eligibility.
	store, err := audio.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	preview := &deferredUnknownPreview{}
	service := &audio.Service{Resolver: preview, Analyzer: lazyAnalyzer{}, Store: store, Authorized: true, ParityValidated: true}
	intent.VerificationPolicy = core.BestAvailable
	session, err := service.Begin(ctx, intent, cat.CatalogVersion(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	assessment, err := session.Check(ctx, track, false)
	if err != nil || preview.calls != 1 || assessment.Eligible || assessment.AnalysisID != "" {
		t.Fatalf("deferred check calls=%d assessment=%+v err=%v", preview.calls, assessment, err)
	}
}

func TestDynamicRegistrationRotatesArtistsBeforeSpendingBudget(t *testing.T) {
	otherArtist := "22222222-2222-2222-2222-222222222222"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws/2/artist" {
			fmt.Fprintf(w, `{"artists":[{"id":%q,"name":"First Artist"},{"id":%q,"name":"Second Artist"}],"count":2}`, contextArtistID, otherArtist)
			return
		}
		id, name, prefix := contextArtistID, "First Artist", "aaaaaaaa"
		if r.URL.Query().Get("artist") == otherArtist {
			id, name, prefix = otherArtist, "Second Artist", "bbbbbbbb"
		}
		rows := make([]mbRecording, 100)
		for i := range rows {
			rows[i] = mbRecording{ID: fmt.Sprintf("%s-1111-2222-3333-%012d", prefix, i+1), Title: fmt.Sprintf("Song %d", i),
				ArtistCredit: []mbArtistCredit{{Name: name}}}
			rows[i].ArtistCredit[0].Artist.ID = id
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"recordings": rows, "recording-count": 100})
	}))
	defer server.Close()
	client := newClient(t, server.URL, time.Nanosecond)
	base := fakes.NewCatalog(2)
	cat, err := catalog.OpenDynamic(base, base, filepath.Join(t.TempDir(), "dynamic.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	intent := core.MusicIntent{Seed: "42", Count: 10, Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid},
		Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "classical", Influence: core.InfluencePositive}}}}
	stream := client.OpenCandidates(intent, cat, cat).(*candidateStream)
	var artists []string
	for i := 0; i < 16; i++ {
		track, err := stream.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		artists = append(artists, track.Artist)
	}
	if artists[0] == artists[1] || stream.dynamicRegistrations != 16 || len(stream.Snapshot().Notices) != 0 {
		t.Fatalf("budget spent before artist rotation: artists=%v registrations=%d notices=%v", artists, stream.dynamicRegistrations, stream.Snapshot().Notices)
	}
	for i := 1; i < 8; i++ {
		if artists[2*i] != artists[0] || artists[2*i+1] != artists[1] {
			t.Fatalf("candidate stream lost artist rotation: %v", artists)
		}
	}
}

func TestDynamicRegistrationBudgetCountsOnlyRegisteredRecordings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws/2/artist" {
			fmt.Fprintf(w, `{"artists":[{"id":%q,"name":"Outside Artist"}],"count":1}`, contextArtistID)
			return
		}
		rows := make([]mbRecording, 21)
		for i := range rows {
			id := fmt.Sprintf("invalid-%d", i)
			if i == 20 {
				id = acousticTestID
			}
			rows[i] = mbRecording{ID: id, Title: fmt.Sprintf("Song %d", i), ArtistCredit: []mbArtistCredit{{Name: "Outside Artist"}}}
			rows[i].ArtistCredit[0].Artist.ID = contextArtistID
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"recordings": rows, "recording-count": len(rows)})
	}))
	defer server.Close()
	client := newClient(t, server.URL, time.Nanosecond)
	base := fakes.NewCatalog(2)
	cat, err := catalog.OpenDynamic(base, base, filepath.Join(t.TempDir(), "dynamic.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	intent := core.MusicIntent{Seed: "42", Count: 1, Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid},
		Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "classical", Influence: core.InfluencePositive}}}}
	stream := client.OpenCandidates(intent, cat, cat).(*candidateStream)
	track, err := stream.Next(context.Background())
	if err != nil || track.ID != "musicbrainz:"+acousticTestID || stream.dynamicRegistrations != 1 {
		t.Fatalf("invalid rows spent registration budget: track=%+v registrations=%d err=%v", track, stream.dynamicRegistrations, err)
	}
	if _, err := stream.Next(context.Background()); err != io.EOF || stream.dynamicRegistrations != 1 || len(stream.Snapshot().Notices) != 0 {
		t.Fatalf("invalid rows exhausted registration budget: registrations=%d notices=%v err=%v", stream.dynamicRegistrations, stream.Snapshot().Notices, err)
	}
}

type deferredUnknownPreview struct{ calls int }

func (p *deferredUnknownPreview) ResolveAudioPreview(context.Context, core.TrackRef, core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
	p.calls++
	return core.ResolvedAudioPreview{Identity: core.PreviewIdentity{Status: core.ResolutionUnresolved, Provider: "deezer"}}, nil
}

type lazyAnalyzer struct{}

func (lazyAnalyzer) Identity() core.AudioModelIdentity {
	return core.AudioModelIdentity{Model: "fixture", Revision: "1", Dimension: 2, Preprocessing: audio.PreprocessingVersion}
}
func (lazyAnalyzer) EmbedAudio(context.Context, []float32) ([]float32, error) {
	return nil, errors.New("unavailable preview must not reach inference")
}
func (lazyAnalyzer) EmbedText(context.Context, string) ([]float32, error) {
	return []float32{1, 0}, nil
}

func TestDynamicRegistrationCancellationAndLegacyReplay(t *testing.T) {
	client := &Client{candidatePreview: forbiddenCandidatePreview{t}}
	cat := &dynamicCatalogFixture{Catalog: fakes.NewCatalog(2)}
	r := mbRecording{ID: acousticTestID, Title: "Song", ArtistCredit: []mbArtistCredit{{Name: "Artist"}}}
	r.ArtistCredit[0].Artist.ID = contextArtistID
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var snapshot core.KnowledgeSnapshot
	if err := client.addDynamicKnowledgeRecording(ctx, r, cat, &snapshot); !errors.Is(err, context.Canceled) || len(cat.registered) != 0 {
		t.Fatalf("cancel err=%v registered=%d", err, len(cat.registered))
	}
	intent := core.MusicIntent{Seed: "42", Controls: core.IntentControls{RecommendationMode: core.EnhancedHybrid}, Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "classical", Influence: core.InfluencePositive}}}}
	old := core.TrackRef{ID: "deezer:77", Artist: "Artist", Title: "Song"}
	intent.Knowledge = &core.KnowledgeSnapshot{DiscoveryRecorded: true, Discovery: []core.TrackRef{old}, DiscoveryCatalog: cat.CatalogVersion()}
	intent.Knowledge.DiscoveryKey = discoveryKeyVersion(intent, cat.CatalogVersion(), "discovery/v5")
	intent.Knowledge.DiscoveryRequestKey = discoveryKeyVersion(intent, "", "discovery/v5")
	if intent.Knowledge.DiscoveryKey == discoveryKey(intent, cat.CatalogVersion()) {
		t.Fatal("fresh discovery version was not changed")
	}
	stream := client.OpenCandidates(intent, cat, cat)
	got, err := stream.Next(context.Background())
	if err != nil || got.ID != old.ID {
		t.Fatalf("legacy replay=%+v err=%v", got, err)
	}
	intent.Knowledge.DiscoveryKey = discoveryKeyVersion(intent, "replaced-catalog", "discovery/v5")
	stream = client.OpenCandidates(intent, cat, cat)
	if _, err := stream.Next(context.Background()); !errors.Is(err, ports.ErrDiscoveryGenerationMismatch) {
		t.Fatalf("legacy mismatch accepted: %v", err)
	}
}
