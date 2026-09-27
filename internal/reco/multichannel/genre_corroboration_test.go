package multichannel

import (
	"context"
	"errors"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/ports"
)

type tagCriterionCatalog struct {
	creditCatalog
	isrc string
}

func (c tagCriterionCatalog) Meta(id string) (core.TrackMeta, bool) {
	meta, ok := c.creditCatalog.Meta(id)
	meta.MusicBrainzRecording = "fixture-recording:" + id
	meta.ISRC = c.isrc
	return meta, ok
}
func (c tagCriterionCatalog) CriterionEvidence(_ context.Context, id string, criterion core.MusicalCriterion) core.EvidenceState {
	if _, ok := c.Meta(id); ok && criterion.Kind == "genre" && criterion.Value == "house" {
		return core.EvidenceMatch
	}
	return core.EvidenceUnknown
}

func genreCorroborationFixture() (*Orchestrator, core.MusicIntent, core.EnrichedTrack) {
	base := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "tagged", Display: "Artist - Song"})
	cat := tagCriterionCatalog{creditCatalog: creditCatalog{Catalog: base, annotations: map[string][]core.MetadataAnnotation{"tagged": {{Kind: "genre", Value: "house", Origin: "embedded_tag", SourceKey: "GENRE"}}}}}
	meta, _ := cat.Meta("tagged")
	track := citedGenreFixture(meta.Ref)
	o := New(cat, nil, base, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
	intent := enhancedIntent(1)
	intent.References = nil
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "house", Scope: "playlist", Strength: "essential"}}
	return o, intent, track
}

func TestImportedGenreNeedsIndependentCorroboration(t *testing.T) {
	o, intent, track := genreCorroborationFixture()
	candidate := core.Candidate{Track: track.Ref}
	got, _, err := o.filterEnhancedEssential(context.Background(), []core.Candidate{candidate}, intent.EssentialCriteria)
	if err != nil || len(got) != 1 || got[0].FitTier != fitClose {
		t.Fatalf("tag-only essential genre should remain a close suggestion: %+v %v", got, err)
	}
	clause := core.AudioClause{Kind: "genre", Text: "house", Scope: "playlist"}
	assessment := o.assessClause(context.Background(), track.Ref.ID, clause, core.AudioAssessment{})
	if assessment.State != core.EvidenceUnknown || len(assessment.Claims) != 1 || assessment.Claims[0].Method != "embedded_tag" || assessment.Claims[0].Locator != "GENRE" {
		t.Fatalf("aggregate tag lookup became independent proof: %+v", assessment)
	}
	intent.EssentialCriteria[0].Strength = "required"
	got, _, err = o.filterEnhancedEssential(context.Background(), []core.Candidate{candidate}, intent.EssentialCriteria)
	if err != nil || len(got) != 0 {
		t.Fatalf("tag-only strict genre passed: %+v %v", got, err)
	}
	intent.EssentialCriteria = nil
	intent.Preferences.Genres = []core.IntentPreference{{Value: "house", Influence: core.InfluencePositive}}
	if tier, _ := o.enhancedTier(context.Background(), candidate, intent); tier != fitClose || !o.enhancedMetadataFallback(context.Background(), candidate, intent) {
		t.Fatal("soft tag preference lost its honest close fallback")
	}
	o.enhanced = false
	if got := o.bestCriterion(context.Background(), track.Ref.ID, core.MusicalCriterion{Kind: "genre", Value: "house"}); got != core.EvidenceMatch {
		t.Fatal("other recommendation policy changed")
	}
}

func TestCopiedGenreTagsAndSimilarityDoNotCreateCorroboration(t *testing.T) {
	o, _, track := genreCorroborationFixture()
	feature := completeStyleFeature(track.Ref.ID, "house")
	sem := &semanticFixture{features: map[string]core.TrackFeatures{track.Ref.ID: feature}, positive: []core.SemanticHit{{TrackID: track.Ref.ID, Score: 1}}}
	o.features, o.scorer = sem, sem
	weak := citedGenreFixture(track.Ref, "house").Claims[0]
	weak.Method = "community_tag"
	o.knowledge.Tracks[0].Claims = []core.RecordingClaim{weak, weak, weak}
	clause := core.AudioClause{Kind: "genre", Text: "house", Scope: "playlist"}
	o.packedAssessments = map[string]core.AudioAssessment{track.Ref.ID: {TrackID: track.Ref.ID, AnalysisID: "same-classifier-family", LibraryCoverage: &core.LibraryCLAPCoverage{CoveredSeconds: 30}, Clauses: []core.AudioClauseAssessment{{Clause: clause, State: core.EvidenceUnknown, Score: 1, ScoreAvailable: true}}}}
	got := o.assessClause(context.Background(), track.Ref.ID, clause, core.AudioAssessment{})
	if got.State != core.EvidenceUnknown || got.Conflict || len(got.Claims) < 6 {
		t.Fatalf("copies of weak evidence bought verification: %+v", got)
	}
	methods := map[string]bool{}
	for _, claim := range got.Claims {
		methods[claim.Method] = true
	}
	for _, method := range []string{"embedded_tag", "sidecar_tag", "community_tag", "audio_similarity", "text_index"} {
		if !methods[method] {
			t.Fatalf("lost %s provenance: %+v", method, got)
		}
	}
}

func TestIndependentRecordingOrAcceptedAudioCanCorroborateGenre(t *testing.T) {
	o, intent, track := genreCorroborationFixture()
	clause := core.AudioClause{Kind: "genre", Text: "house", Scope: "playlist"}
	preview := core.AudioAssessment{TrackID: track.Ref.ID, AnalysisID: "validated-preview-fixture", Coverage: &core.PreviewCoverage{Available: true, CoveredSeconds: 30}, Clauses: []core.AudioClauseAssessment{{Clause: clause, State: core.EvidenceMatch}}}
	if !o.strongClause(context.Background(), track.Ref.ID, clause, preview) {
		t.Fatal("accepted applicable audio could not corroborate genre")
	}
	o.knowledge.Tracks[0] = citedGenreFixture(track.Ref, "house")
	if tier, _ := o.enhancedTier(context.Background(), core.Candidate{Track: track.Ref}, intent); tier != fitStrong {
		t.Fatal("independent cited recording statement could not establish genre")
	}
}

func TestTagOnlyCandidateAcquiresEvidenceAndKeepsCompletedPartialClaims(t *testing.T) {
	o, intent, track := genreCorroborationFixture()
	calls := 0
	o.WithRecordingVerifier(recordingVerifierFunc(func(_ context.Context, got core.EnrichedTrack, _ []core.MusicalCriterion) (core.EnrichedTrack, error) {
		calls++
		got.Claims = citedGenreFixture(got.Ref, "house").Claims
		return got, context.DeadlineExceeded // completed fact survives optional source timeout
	}))
	if err := o.verifyRecording(context.Background(), track.Ref, intent); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("ordinary tag skipped bounded verification")
	}
	if tier, _ := o.enhancedTier(context.Background(), core.Candidate{Track: track.Ref}, intent); tier != fitStrong {
		t.Fatal("completed verification fact discarded with optional timeout")
	}
	if err := o.verifyRecording(context.Background(), track.Ref, intent); err != nil || calls != 1 {
		t.Fatal("completed evidence reacquired")
	}
}

func TestRecordingVerificationPreservesCanonicalLocalISRC(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		o, intent, track := genreCorroborationFixture()
		cat := o.cat.(tagCriterionCatalog)
		cat.isrc = "US-ABC-24-00001"
		o.cat = cat
		if conflict {
			o.knowledge.Tracks[0].ISRC = "GBABC2400002"
		}
		calls := 0
		o.WithRecordingVerifier(recordingVerifierFunc(func(_ context.Context, got core.EnrichedTrack, _ []core.MusicalCriterion) (core.EnrichedTrack, error) {
			calls++
			if got.ISRC != "USABC2400001" {
				t.Fatalf("catalog ISRC not forwarded: %+v", got)
			}
			return got, errors.New("optional unavailable")
		}))
		if err := o.verifyRecording(context.Background(), track.Ref, intent); err != nil {
			t.Fatal(err)
		}
		if conflict && calls != 0 || !conflict && calls != 1 {
			t.Fatal("conflicting canonical identities reached verifier")
		}
	}
	if recordingISRCConflict(core.EnrichedTrack{ISRC: "GBABC2400002"}, "8711111111111") {
		t.Fatal("barcode-like value treated as canonical ISRC contradiction")
	}
}

func TestQualityTargetRequiresSupportedJourneyOrder(t *testing.T) {
	cat := testCatalog()
	o := New(cat, nil, cat, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	tracks := refs(cat, "audio", "cooc")
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{citedGenreFixture(tracks[0], "classical"), citedGenreFixture(tracks[1], "rock")}}
	intent := enhancedIntent(2)
	intent.Mode = core.ModeJourney
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "classical", Scope: "journey_start"}, {Kind: "genre", Value: "rock", Scope: "journey_end"}}
	assembly := candidateAssembly{sequence: ports.SequenceResult{Tracks: tracks}}
	if !o.qualityTargetMet(context.Background(), assembly, intent) {
		t.Fatal("supported journey order missed quality target")
	}
	assembly.sequence.Tracks = []core.TrackRef{tracks[1], tracks[0]}
	for _, track := range tracks {
		if tier, _ := o.enhancedTier(context.Background(), core.Candidate{Track: track}, intent); tier != fitStrong {
			t.Fatal("fixture no longer exercises per-track any-stage strength")
		}
	}
	if o.qualityTargetMet(context.Background(), assembly, intent) {
		t.Fatal("any-stage strength incorrectly proved actual journey order")
	}
	playlist := core.Playlist{Tracks: assembly.sequence.Tracks, Intent: intent, Outcome: core.GenerationOutcome{State: core.OutcomeFulfilled}}
	o.annotateEnhancedFit(context.Background(), &playlist)
	if playlist.Outcome.State != core.OutcomePartial {
		t.Fatal("unsupported journey order reported fulfilled")
	}
}
