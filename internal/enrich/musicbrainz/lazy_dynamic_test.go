package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
		_ = json.NewEncoder(w).Encode(map[string]any{"recordings": rows, "count": 100})
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	track, err := stream.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := cat.Meta(track.ID)
	if !ok || meta.Ref.RecordingIdentity != track.ID || meta.PreviewURL != "" || len(stream.Snapshot().Tracks) != 80 {
		t.Fatalf("identity or registration budget changed: %+v tracks=%d", meta, len(stream.Snapshot().Tracks))
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
