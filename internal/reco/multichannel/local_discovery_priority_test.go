package multichannel

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type metadataPriorityCatalog struct{ *fakes.Catalog }

func (c metadataPriorityCatalog) CriterionEvidence(_ context.Context, id string, criterion core.MusicalCriterion) core.EvidenceState {
	if _, ok := c.Meta(id); ok && id != "unknown" && criterion.Kind == "genre" && criterion.Value == "electronic" {
		return core.EvidenceMatch
	}
	return core.EvidenceUnknown
}

func (c metadataPriorityCatalog) Meta(id string) (core.TrackMeta, bool) {
	m, ok := c.Catalog.Meta(id)
	if ok && id != "unknown" {
		m.Annotations = []core.MetadataAnnotation{{Kind: "genre", Value: "electronic", Origin: "embedded_tag", SourceKey: "GENRE"}}
	}
	return m, ok
}

type metadataPriorityRetriever struct{ poolRetriever }

func (*metadataPriorityRetriever) SupportsIntentMetadata() bool { return true }

func (r *metadataPriorityRetriever) Retrieve(ctx context.Context, request ports.RetrievalRequest) ([]core.Candidate, error) {
	candidates, err := r.poolRetriever.Retrieve(ctx, request)
	for i := range candidates {
		candidates[i].Sources = []core.RetrievalEvidence{{Channel: "library_metadata", QueryWeight: 1, Rank: i + 1, LibrarySource: &core.LibraryEvidenceSource{PackID: "fixture"}}}
		candidates[i].Scores.RetrievalFusion = 1
		candidates[i].Available.RetrievalFusion = true
	}
	return candidates, err
}

type metadataPriorityStream struct {
	snapshot core.KnowledgeSnapshot
	tracks   []core.TrackRef
	before   func()
	block    bool
	pulls    int
}

func (s *metadataPriorityStream) OpenCandidates(intent core.MusicIntent, _ ports.Catalog, _ ports.ReferenceResolver) ports.MusicCandidateStream {
	if intent.Knowledge != nil {
		s.snapshot = *intent.Knowledge
	}
	// Real MusicBrainz marks even a newly opened stream as recorded. The
	// orchestrator must distinguish that from an incoming replay snapshot.
	s.snapshot.DiscoveryRecorded = true
	return s
}
func (s *metadataPriorityStream) Snapshot() *core.KnowledgeSnapshot { return &s.snapshot }
func (s *metadataPriorityStream) Next(ctx context.Context) (core.TrackRef, error) {
	s.pulls++
	if s.before != nil {
		s.before()
	}
	if s.block {
		<-ctx.Done()
		return core.TrackRef{}, ctx.Err()
	}
	if len(s.tracks) == 0 {
		return core.TrackRef{}, io.EOF
	}
	track := s.tracks[0]
	s.tracks = s.tracks[1:]
	return track, nil
}

func localPriorityFixture() (metadataPriorityCatalog, core.MusicIntent) {
	cat := metadataPriorityCatalog{fakes.NewCatalog(2,
		fakes.CatalogTrack{ID: "pack:fixture:local:one", Display: "Local - One", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "provider:two", Display: "Provider - Two", Audio: []float32{1, 0}, Track: []float32{1, 0}},
		fakes.CatalogTrack{ID: "unknown", Display: "Unknown - Unknown", Audio: []float32{1, 0}, Track: []float32{1, 0}},
	)}
	intent := testIntent(1)
	intent.References, intent.Seeds = nil, core.IntentSeeds{}
	intent.VerificationPolicy = core.BestAvailable
	intent.Controls.RecommendationMode = core.EnhancedHybrid
	intent.Preferences.Genres = []core.IntentPreference{{Value: "electronic", Influence: core.InfluencePositive}}
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "electronic", Scope: "playlist"}}
	return cat, intent
}

func TestEnhancedLocalMetadataPrecedesBlockingProvider(t *testing.T) {
	cat, intent := localPriorityFixture()
	intent.Count, intent.Controls.TotalTrackCount = 2, 2
	r := &metadataPriorityRetriever{poolRetriever{candidates: candidatesForTracks(refs(cat, "unknown", "pack:fixture:local:one", "provider:two"))}}
	s := &metadataPriorityStream{block: true}
	o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig()).WithCandidateSource(s)
	o.retriever = r
	progress := 0
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pl, err := o.BuildRecommendation(ctx, ports.RecommendationRequest{Intent: intent, Progress: ports.ProgressFunc(func(_ string, _, _ int64, _ string) { progress++ })})
	if err != nil || len(pl.Tracks) != 2 || pl.Outcome.State != core.OutcomeFulfilled {
		t.Fatalf("local metadata did not complete before provider: tracks=%v outcome=%+v err=%v retrieves=%d progress=%d", pl.Tracks, pl.Outcome, err, len(r.calls), progress)
	}
	for _, track := range pl.Tracks {
		if track.ID == "unknown" {
			t.Fatal("missing metadata bypassed the essential criterion")
		}
	}
	if len(r.calls) != 1 || s.pulls != 0 || progress == 0 {
		t.Fatalf("retrieval=%d provider pulls=%d", len(r.calls), s.pulls)
	}
}

func TestEnhancedProviderContinuesAfterLocalMetadata(t *testing.T) {
	cat, intent := localPriorityFixture()
	intent.Count, intent.Controls.TotalTrackCount = 2, 2
	r := &metadataPriorityRetriever{poolRetriever{candidates: candidatesForTracks(refs(cat, "pack:fixture:local:one"))}}
	progress := 0
	s := &metadataPriorityStream{tracks: refs(cat, "pack:fixture:local:one", "provider:two"), before: func() {
		if len(r.calls) == 0 || progress == 0 {
			t.Error("provider called before local retrieval and fit progress")
		}
	}}
	o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig()).WithCandidateSource(s)
	o.retriever = r
	pl, err := o.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, Progress: ports.ProgressFunc(func(_ string, _, _ int64, note string) {
		if note == "Checking candidates for the final selection" {
			progress++
		}
	})})
	if err != nil || len(pl.Tracks) != 2 || pl.Tracks[0].ID == pl.Tracks[1].ID || s.pulls == 0 {
		t.Fatalf("provider continuation/dedup: tracks=%v pulls=%d err=%v", pl.Tracks, s.pulls, err)
	}
}

func TestInterruptedSearchDoesNotClaimCatalogExhaustion(t *testing.T) {
	cat, intent := localPriorityFixture()
	intent.Count, intent.Controls.TotalTrackCount = 2, 2
	r := &metadataPriorityRetriever{poolRetriever{candidates: candidatesForTracks(refs(cat, "pack:fixture:local:one"))}}
	stop := make(chan struct{})
	s := &metadataPriorityStream{block: true, before: func() { close(stop) }}
	o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig()).WithCandidateSource(s)
	o.retriever = r
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pl, err := o.BuildRecommendation(ctx, ports.RecommendationRequest{Intent: intent, StopChecking: stop})
	if err != nil || len(pl.Tracks) != 1 || pl.Outcome.State != core.OutcomePartial {
		t.Fatalf("stopped result: tracks=%v outcome=%+v err=%v", pl.Tracks, pl.Outcome, err)
	}
	found := false
	for _, reason := range pl.Outcome.Reasons {
		if reason.Code == "eligible_tracks_exhausted" {
			t.Fatal("stopped search falsely claims exhaustion")
		}
		found = found || reason.Code == "search_incomplete"
	}
	if !found {
		t.Fatal("missing incomplete search reason")
	}
}

func TestMetadataBatchRanksEvidenceWithoutPackMembershipBonus(t *testing.T) {
	var candidates []core.Candidate
	for i := range 600 {
		candidates = append(candidates, core.Candidate{Track: core.TrackRef{ID: fmt.Sprintf("base:%03d", i)}})
	}
	for _, id := range []string{"pack:late", "base:recording-alias"} {
		candidates = append(candidates, core.Candidate{Track: core.TrackRef{ID: id}, Sources: []core.RetrievalEvidence{{LibrarySource: &core.LibraryEvidenceSource{PackID: "fixture"}}}})
	}
	candidates[0].Scores.RetrievalFusion = .9
	candidates[len(candidates)-2].Scores.RetrievalFusion = .8
	candidates[len(candidates)-1].Scores.RetrievalFusion = .7
	got := boundedMetadataCandidates(candidates, 512)
	if len(got) != 512 || got[0].Track.ID != "base:000" || got[1].Track.ID != "pack:late" || got[2].Track.ID != "base:recording-alias" {
		t.Fatal("membership displaced common retrieval relevance", got[:3])
	}
	for i := range candidates {
		candidates[i].Sources = nil
	}
	withoutProvenance := boundedMetadataCandidates(candidates, 512)
	for i := range got {
		if got[i].Track.ID != withoutProvenance[i].Track.ID {
			t.Fatal("source provenance changed ordering")
		}
	}
	if candidates[0].Track.ID != "base:000" {
		t.Fatal("source-owned candidate slice mutated")
	}
}

func TestMetadataCapabilitySurvivesAudioWrappers(t *testing.T) {
	for _, metadata := range []bool{false, true} {
		var base ports.CandidateRetriever = &poolRetriever{}
		if metadata {
			base = &metadataPriorityRetriever{}
		}
		cached := &cachedAudioRetriever{base: base}
		mert := &mertAudioRetriever{base: cached}
		if cached.SupportsIntentMetadata() != metadata || mert.SupportsIntentMetadata() != metadata || (&cachedAudioRetriever{base: mert}).SupportsIntentMetadata() != metadata {
			t.Fatal("audio wrappers changed metadata capability", metadata)
		}
	}
}

func TestEnhancedLocalMetadataReplayPreservesSelection(t *testing.T) {
	cat, intent := localPriorityFixture()
	intent.Count, intent.Controls.TotalTrackCount = 2, 2
	r := &metadataPriorityRetriever{poolRetriever{candidates: candidatesForTracks(refs(cat, "pack:fixture:local:one", "provider:two"))}}
	s := &metadataPriorityStream{block: true}
	o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig()).WithCandidateSource(s)
	o.retriever = &mertAudioRetriever{base: &cachedAudioRetriever{base: r}, cfg: DefaultConfig()}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	first, err := o.Build(ctx, intent)
	if err != nil || len(first.Tracks) != 2 || first.Intent.Knowledge == nil || !first.Intent.Knowledge.DiscoveryRecorded {
		t.Fatalf("initial generation failed: %+v %v", first.Outcome, err)
	}
	replay, err := o.Build(ctx, first.Intent)
	if err != nil || !reflect.DeepEqual(first.Tracks, replay.Tracks) || !reflect.DeepEqual(first.Outcome, replay.Outcome) || s.pulls != 0 {
		t.Fatalf("local replay changed: first=%v replay=%v pulls=%d err=%v", first.Tracks, replay.Tracks, s.pulls, err)
	}
}

func TestExpiredAudioSessionReportsBoundedSearch(t *testing.T) {
	cat, intent := localPriorityFixture()
	service, _ := cachedAudioService(t, cat.Catalog, "pack:fixture:local:one")
	session, err := service.BeginWithBudget(context.Background(), intent, cat.CatalogVersion(), nil, -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig())
	o.enhanced, o.bestAvailable, o.audioSession = true, true, session
	s := &metadataPriorityStream{block: true}
	_, notices, err := o.collectIteratively(context.Background(), nil, s, intent, ports.RecommendationRequest{Intent: intent}, newEligibility(intent, nil, nil), nil, nil, nil, 42)
	if err != nil || s.pulls != 0 {
		t.Fatalf("expired session started discovery: pulls=%d err=%v", s.pulls, err)
	}
	found := false
	for _, notice := range notices {
		found = found || notice.Code == "analysis_limit"
	}
	if !found {
		t.Fatal("analysis budget omitted from stop notices", notices)
	}
}

type nonAdvancingMetadataStream struct{ metadataPriorityStream }

func (s *nonAdvancingMetadataStream) OpenCandidates(core.MusicIntent, ports.Catalog, ports.ReferenceResolver) ports.MusicCandidateStream {
	return s
}

func (s *nonAdvancingMetadataStream) Next(context.Context) (core.TrackRef, error) {
	s.pulls++
	// No catalog membership: this is cheap, requires no audio or ranking, and
	// simulates a provider that never signals EOF before our attempt bound.
	return core.TrackRef{ID: "missing-recording"}, nil
}

func TestDiscoveryAttemptBudgetDoesNotClaimExhaustion(t *testing.T) {
	cat, intent := localPriorityFixture()
	intent.Count, intent.Controls.TotalTrackCount = 2, 2
	r := &metadataPriorityRetriever{poolRetriever{candidates: candidatesForTracks(refs(cat, "pack:fixture:local:one"))}}
	s := &nonAdvancingMetadataStream{}
	o := New(cat, fakes.NewSimilarityEngine(cat.Catalog), cat, DefaultConfig()).WithCandidateSource(s)
	o.retriever = r
	pl, err := o.Build(context.Background(), intent)
	if err != nil || len(pl.Tracks) != 1 || pl.Outcome.State != core.OutcomePartial || s.pulls == 0 || s.pulls > iterativeAttempts {
		t.Fatalf("attempt-bounded result: tracks=%v outcome=%+v pulls=%d err=%v", pl.Tracks, pl.Outcome, s.pulls, err)
	}
	found := false
	for _, reason := range pl.Outcome.Reasons {
		if reason.Code == "eligible_tracks_exhausted" {
			t.Fatal("attempt limit falsely claims exhaustion")
		}
		found = found || reason.Code == "search_incomplete"
	}
	if !found {
		t.Fatal("missing incomplete search reason", pl.Outcome)
	}
}
