package multichannel

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
)

type automaticCatalogFixture struct {
	*fakes.Catalog
	annotations  map[string][]core.MetadataAnnotation
	observations map[string]core.AudioAssessment
	recordings   map[string]core.EnrichedTrack
	mert         map[string]core.LibraryVector
	frozen       bool
	t            *testing.T
}

func (c *automaticCatalogFixture) read() {
	c.t.Helper()
	if c.frozen {
		c.t.Fatal("source catalog read after batch freeze")
	}
}
func (c *automaticCatalogFixture) Meta(id string) (core.TrackMeta, bool) {
	c.read()
	m, ok := c.Catalog.Meta(id)
	m.Annotations = c.annotations[id]
	return m, ok
}
func (c *automaticCatalogFixture) Vectors(id string) (ports.Vectors, bool) {
	c.read()
	return c.Catalog.Vectors(id)
}
func (c *automaticCatalogFixture) BindLibraryQueries(core.AudioModelIdentity, []core.AudioClauseVector) {
}
func (c *automaticCatalogFixture) LibraryAssessment(ctx context.Context, id string) (core.AudioAssessment, bool, error) {
	c.read()
	v, ok := c.observations[id]
	return v, ok, ctx.Err()
}
func (c *automaticCatalogFixture) LibraryCLAPVector(ctx context.Context, _ string) (core.LibraryVector, bool, error) {
	c.read()
	return core.LibraryVector{}, false, ctx.Err()
}
func (c *automaticCatalogFixture) LibraryRecordingMetadata(ctx context.Context, id string) (core.EnrichedTrack, bool, error) {
	c.read()
	v, ok := c.recordings[id]
	return v, ok, ctx.Err()
}
func (*automaticCatalogFixture) LibraryTrackFeatures(context.Context, string) (core.LibraryTrackFeatures, bool) {
	return core.LibraryTrackFeatures{}, false
}
func (*automaticCatalogFixture) LibraryPreferenceScore(context.Context, string, core.MusicIntent, string) (float64, bool) {
	return 0, false
}

func automaticFixture(t *testing.T, count int) (*automaticCatalogFixture, core.MusicIntent, *poolRetriever) {
	t.Helper()
	tracks := []fakes.CatalogTrack{}
	for i := 0; i < 4; i++ {
		tracks = append(tracks, fakes.CatalogTrack{ID: fmt.Sprint(i), Display: fmt.Sprintf("Artist %d - Song %d", i, i), Audio: []float32{1, 0}, Track: []float32{1, 0}})
	}
	c := &automaticCatalogFixture{Catalog: fakes.NewCatalog(2, tracks...), annotations: map[string][]core.MetadataAnnotation{}, observations: map[string]core.AudioAssessment{}, recordings: map[string]core.EnrichedTrack{}, t: t}
	intent := enhancedIntent(count)
	// Generic musical-fit tests do not request the legacy helper's nonexistent
	// "seed" recording. Explicit reference tests supply their own identities.
	intent.References = nil
	intent.Seeds = core.IntentSeeds{}
	intent.Controls.RecommendationMode = core.Automatic
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "house", Scope: "playlist"}}
	intent.Seed = core.NewRNGSeed(42)
	var candidates []core.Candidate
	for i := 0; i < 4; i++ {
		id := fmt.Sprint(i)
		m, _ := c.Catalog.Meta(id)
		candidates = append(candidates, core.Candidate{Track: m.Ref, Sources: []core.RetrievalEvidence{{Channel: "metadata", Rank: i + 1}}})
	}
	return c, intent, &poolRetriever{candidates: candidates}
}
func automaticSupport(c *automaticCatalogFixture, intent core.MusicIntent, id string, kinds ...string) {
	execution := intent
	execution.Controls.RecommendationMode = core.EnhancedHybrid
	a := core.AudioAssessment{TrackID: id, AnalysisID: "packed:" + id, LibraryCoverage: &core.LibraryCLAPCoverage{CoveredSeconds: 30}}
	for _, clause := range audio.Clauses(execution) {
		for _, text := range kinds {
			if text == clause.Text {
				c.annotations[id] = append(c.annotations[id], core.MetadataAnnotation{Kind: clause.Kind, Value: text, Origin: "embedded_tag", SourceKey: strings.ToUpper(clause.Kind)})
				state := core.EvidenceUnknown
				if clause.Kind == "vocal" {
					// This fixture supplies a signed contrast assessment. Packed
					// raw cosine is separately tested as insufficient below.
					state = core.EvidenceMatch
				}
				a.Clauses = append(a.Clauses, core.AudioClauseAssessment{Clause: clause, Score: .6, ScoreAvailable: true, State: state})
			}
		}
	}
	c.observations[id] = a
}
func TestAutomaticPreparedAgreementAndFreeze(t *testing.T) {
	c, intent, r := automaticFixture(t, 2)
	automaticSupport(c, intent, "0", "house")
	automaticSupport(c, intent, "1", "house")
	// A native tag alone is plausible; a copied classifier tag is not a second source.
	c.annotations["2"] = []core.MetadataAnnotation{{Kind: "genre", Value: "house", Origin: "embedded_tag"}}
	c.annotations["3"] = []core.MetadataAnnotation{{Kind: "genre", Value: "house", Origin: "classifier_output", SourceKey: "AB:GENRE"}}
	engine := NewAutomatic(c, nil, r, DefaultConfig())
	got, err := engine.BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, Progress: ports.ProgressFunc(func(_ string, _, _ int64, note string) {
		if strings.HasPrefix(note, "Selecting") {
			c.frozen = true
		}
	})})
	if err != nil || len(got.Tracks) != 2 || got.Outcome.State != core.OutcomeFulfilled {
		t.Fatalf("result=%+v err=%v", got, err)
	}
	for _, f := range got.FitAssessments {
		if f.Assessment.State != core.AutomaticStrong {
			t.Fatal(f)
		}
	}
	c.frozen = false
	again, err := engine.Build(context.Background(), intent)
	if err != nil || !reflect.DeepEqual(got.IDs(), again.IDs()) {
		t.Fatal("non-deterministic", again, err)
	}
}

func TestAutomaticCoverageAndConjunction(t *testing.T) {
	for _, collective := range []bool{true, false} {
		t.Run(fmt.Sprint(collective), func(t *testing.T) {
			c, intent, r := automaticFixture(t, 2)
			intent.EssentialCriteria = append(intent.EssentialCriteria, core.MusicalCriterion{Kind: "genre", Value: "techno", Scope: "playlist"})
			if collective {
				for i := range intent.EssentialCriteria {
					intent.EssentialCriteria[i].CoverageGroup = "mix"
				}
			}
			automaticSupport(c, intent, "0", "house")
			automaticSupport(c, intent, "1", "techno")
			got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
			want := 0
			if collective {
				want = 2
			}
			if err != nil || len(got.Tracks) != want {
				t.Fatalf("result=%+v err=%v", got, err)
			}
			if collective && got.Outcome.State != core.OutcomeFulfilled {
				t.Fatal(got.Outcome)
			}
		})
	}
}
func TestAutomaticJourneyAndMissingCoverage(t *testing.T) {
	c, intent, r := automaticFixture(t, 2)
	intent.Mode = core.ModeJourney
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "house", Scope: "journey_start"}, {Kind: "genre", Value: "techno", Scope: "journey_end"}}
	automaticSupport(c, intent, "0", "techno")
	automaticSupport(c, intent, "1", "house")
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || !reflect.DeepEqual(got.IDs(), []string{"1", "0"}) || got.Outcome.State != core.OutcomeFulfilled {
		t.Fatalf("journey=%+v err=%v", got, err)
	}
	delete(c.observations, "0")
	got, err = NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || got.Outcome.State == core.OutcomeFulfilled {
		t.Fatal("missing stage falsely fulfilled", got, err)
	}
}
func TestAutomaticVocalUncertaintyAndVeto(t *testing.T) {
	c, intent, r := automaticFixture(t, 2)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "vocal", Value: "instrumental", Scope: "playlist", Strength: "required"}}
	automaticSupport(c, intent, "0", "instrumental")
	automaticSupport(c, intent, "1", "instrumental")
	c.annotations["1"] = append(c.annotations["1"], core.MetadataAnnotation{Kind: "vocal", Value: "vocals", Origin: "embedded_tag"})
	// No observation, including an instrumental tag alone, may pad the count.
	c.annotations["2"] = []core.MetadataAnnotation{{Kind: "vocal", Value: "instrumental", Origin: "embedded_tag"}}
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || !reflect.DeepEqual(got.IDs(), []string{"0"}) || got.Outcome.State != core.OutcomePartial {
		t.Fatalf("vocal result=%+v err=%v", got, err)
	}
}
func TestAutomaticConcreteExclusionAndDateUnknown(t *testing.T) {
	c, intent, r := automaticFixture(t, 2)
	automaticSupport(c, intent, "0", "house")
	automaticSupport(c, intent, "1", "house")
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_artist", Value: "Artist 0", Supported: true}}
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || !reflect.DeepEqual(got.IDs(), []string{"1"}) {
		t.Fatal(got, err)
	}
	intent.Temporal = []core.TemporalRequirement{{Basis: "original_release", StartYear: 1990, EndYear: 1999, Scope: "playlist"}}
	got, err = NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 0 {
		t.Fatal("unknown date passed", got, err)
	}
}
func TestAutomaticCancellationAndStoppedPreparation(t *testing.T) {
	c, intent, r := automaticFixture(t, 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(ctx, intent); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	close(stop)
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).BuildRecommendation(context.Background(), ports.RecommendationRequest{Intent: intent, StopChecking: stop})
	if err != nil || got.Outcome.State == core.OutcomeFulfilled || len(got.Tracks) != 0 {
		t.Fatal(got, err)
	}
}
func TestAutomaticBalancedCapAndSeed(t *testing.T) {
	var first, second []core.Candidate
	for i := 0; i < 600; i++ {
		first = append(first, core.Candidate{Track: core.TrackRef{ID: fmt.Sprint(i)}, Sources: []core.RetrievalEvidence{{Channel: "metadata", Rank: 1}}})
	}
	second = []core.Candidate{{Track: core.TrackRef{ID: "late"}, Sources: []core.RetrievalEvidence{{Channel: "musicgraph", Rank: 1}}}}
	got := automaticBalancedPool([][]core.Candidate{first, second}, 512, 42)
	if len(got) != 512 || got[1].Track.ID != "late" {
		t.Fatal("channel starved", len(got), got[:2])
	}
	reverse := append([]core.Candidate(nil), first...)
	for i, j := 0, len(reverse)-1; i < j; i, j = i+1, j-1 {
		reverse[i], reverse[j] = reverse[j], reverse[i]
	}
	if !reflect.DeepEqual(got, automaticBalancedPool([][]core.Candidate{reverse, second}, 512, 42)) {
		t.Fatal("input order changed equal-rank pool")
	}
	if reflect.DeepEqual(got, automaticBalancedPool([][]core.Candidate{first, second}, 512, 43)) {
		t.Fatal("seed did not vary ties")
	}
}
func TestAutomaticWorkDeadlinePreservesCompletedBatch(t *testing.T) {
	c, intent, r := automaticFixture(t, 1)
	automaticSupport(c, intent, "0", "house")
	engine := NewAutomatic(c, nil, r, DefaultConfig()).WithFamiliarityReader(func(ctx context.Context, ref core.TrackRef) (float64, bool) {
		if ref.ID == "1" {
			<-ctx.Done()
		}
		return 0, false
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	got, err := engine.Build(ctx, intent)
	if err != nil || len(got.Tracks) != 1 || got.Outcome.State != core.OutcomePartial || ctx.Err() != nil {
		t.Fatalf("result=%+v err=%v parent=%v", got, err, ctx.Err())
	}
}

func TestAutomaticSnapshotPreservesFitAndCandidateDecisions(t *testing.T) {
	c, intent, r := automaticFixture(t, 1)
	automaticSupport(c, intent, "0", "house")
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || got.Search == nil || got.Search.Result == nil || got.Search.Considered != 4 || got.Search.Eligible != 1 {
		t.Fatal(got, err)
	}
	if err := got.Search.Validate(); err != nil {
		t.Fatal(err)
	}
	copy := automaticCopy(got)
	if !reflect.DeepEqual(got, copy) {
		t.Fatal("lossy playlist roundtrip")
	}
	if copy.Intent.Controls.RecommendationMode != core.Automatic {
		t.Fatal("legacy execution view leaked")
	}
	copy.Search.Candidates[0].Decision = "tampered"
	if copy.Search.Validate() == nil {
		t.Fatal("fit ledger not fingerprinted")
	}
	if got.Search.Validate() != nil {
		t.Fatal("returned result aliases frozen evidence")
	}
}
func TestAutomaticRequiredTracksCountAndCannotBypassFit(t *testing.T) {
	c, intent, r := automaticFixture(t, 2)
	automaticSupport(c, intent, "0", "house")
	automaticSupport(c, intent, "1", "house")
	intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "0", Influence: core.InfluencePositive}}
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 2 {
		t.Fatal(got, err)
	}
	n := 0
	for _, track := range got.Tracks {
		if track.ID == "0" {
			n++
		}
	}
	if n != 1 {
		t.Fatal("required count", got.IDs())
	}
	delete(c.observations, "0")
	got, err = NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 0 || got.Outcome.State != core.OutcomeNeedsClarification {
		t.Fatal("required bypass", got, err)
	}
	intent.RequiredTracks = append(intent.RequiredTracks, core.IntentReference{Kind: core.ReferenceTrack, TrackID: "missing", Influence: core.InfluencePositive})
	got, err = NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || got.Outcome.State != core.OutcomeNeedsClarification {
		t.Fatal("missing required vanished", got, err)
	}
}
func TestAutomaticSourceFamilyCopiesCannotCorroborate(t *testing.T) {
	b := newAutomaticBatch()
	clause := core.AudioClause{Kind: "genre", Text: "house", Scope: "playlist", Essential: true}
	b.meta["x"] = core.TrackMeta{Annotations: []core.MetadataAnnotation{{Kind: "genre", Value: "house", Origin: "classifier_output", SourceKey: "AB:GENRE"}, {Kind: "genre", Value: "house", Origin: "acousticbrainz"}}}
	b.features["x"] = core.TrackFeatures{Styles: []core.FeatureValue{{Value: "house", Missingness: core.FeatureKnown, Confidence: .95, Provenance: []core.FeatureProvenance{{Source: "acousticbrainz", SourceID: "x", SourceVersion: "1", ModelVersion: "1", Confidence: .95}}}}}
	got := automaticClauseFit(b, "x", clause, .15)
	if got.State == core.AutomaticStrong || len(got.Signals) != 1 {
		t.Fatal("duplicate family", got)
	}
	b.meta["x"] = core.TrackMeta{Annotations: []core.MetadataAnnotation{{Kind: "genre", Value: "house", Origin: "embedded_tag"}}}
	got = automaticClauseFit(b, "x", clause, .15)
	if got.State != core.AutomaticStrong {
		t.Fatal("independent supplied feature", got)
	}
}
func TestAutomaticMixedStrictORAndIndependentCoverageSets(t *testing.T) {
	c, intent, r := automaticFixture(t, 1)
	intent.EssentialCriteria[0].Group = "either"
	intent.Preferences.VocalPreference = &core.IntentPreference{Value: "vocals", Influence: core.InfluenceNegative, Strength: "required", Group: "either", Scope: "playlist"}
	automaticSupport(c, intent, "0", "house")
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 1 {
		t.Fatal("OR lost alternative", got, err)
	}
	// Two collective sets remain independent per-track requirements.
	c, intent, r = automaticFixture(t, 1)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "house", Scope: "playlist", CoverageGroup: "a"}, {Kind: "genre", Value: "jazz", Scope: "playlist", CoverageGroup: "b"}}
	automaticSupport(c, intent, "0", "house")
	got, err = NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 0 {
		t.Fatal("independent coverage collapsed", got, err)
	}
}
func TestAutomaticPreparationDeadlineBeforeBatchIsPartial(t *testing.T) {
	c, intent, r := automaticFixture(t, 1)
	engine := NewAutomatic(c, nil, r, DefaultConfig()).WithIntentPreparer(func(ctx context.Context, i core.MusicIntent, _ ports.Catalog, _ ports.ReferenceResolver) (core.MusicIntent, error) {
		<-ctx.Done()
		return i, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	got, err := engine.Build(ctx, intent)
	if err != nil || got.Outcome.State != core.OutcomePartial || got.Search == nil || ctx.Err() != nil {
		t.Fatal(got, err, ctx.Err())
	}
}

func TestAutomaticSourceParsedAbsenceIsEstimatedAndKeepsExtraUnsupported(t *testing.T) {
	c, _, r := automaticFixture(t, 1)
	intent, err := rules.New().Parse(context.Background(), ports.IntentInput{Prompt: "Make a 1-track instrumental playlist. No vocals."})
	if err != nil {
		t.Fatal(err)
	}
	intent.Controls.RecommendationMode = core.Automatic
	intent.Seed = core.NewRNGSeed(42)
	normalized := intent.Normalized()
	if len(normalized.Unsupported) == 0 {
		t.Fatal("fixture lacks native unsupported annotation")
	}
	execution := normalized
	execution.Controls.RecommendationMode = core.EnhancedHybrid
	c.annotations["0"] = []core.MetadataAnnotation{{Kind: "vocal", Value: "instrumental", Origin: "embedded_tag"}}
	assessment := core.AudioAssessment{TrackID: "0", AnalysisID: "prepared:0", LibraryCoverage: &core.LibraryCLAPCoverage{CoveredSeconds: 30}}
	for _, clause := range audio.Clauses(execution) {
		if clause.Kind == "vocal" {
			score := .6
			if strings.Contains(clause.Text, "vocal") {
				score = -.6
			}
			// Explicit signed contrast fixture, not the absolute score below.
			assessment.Clauses = append(assessment.Clauses, core.AudioClauseAssessment{Clause: clause, Score: score, ScoreAvailable: true, State: core.EvidenceMatch})
		}
	}
	c.observations["0"] = assessment
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 1 || got.Outcome.State != core.OutcomeFulfilled {
		t.Fatal(got.Outcome, got.IDs(), err)
	}
	if !reflect.DeepEqual(got.Intent.Unsupported, normalized.Unsupported) {
		t.Fatal("source intent mutated")
	}
	intent.Unsupported = append(intent.Unsupported, core.UnsupportedRequirement{Text: "gapless crossfades", Reason: "unsupported playback"})
	got, err = NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 1 || got.Outcome.State != core.OutcomePartial {
		t.Fatal("extra request falsely fulfilled", got.Outcome, err)
	}
}
func TestAutomaticDoesNotReuseInferredModelAnchors(t *testing.T) {
	c, intent, r := automaticFixture(t, 1)
	automaticSupport(c, intent, "0", "house")
	intent.InferredAnchors = []core.InferredAnchor{{Reference: core.IntentReference{Kind: core.ReferenceTrack, TrackID: "2", Influence: core.InfluencePositive}, Suitability: core.AnchorSuitability{State: core.EvidenceMatch}}}
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(r.calls) != 1 || len(r.calls[0].Intent.InferredAnchors) != 0 || len(got.Intent.InferredAnchors) != 1 {
		t.Fatal("inferred anchor policy changed saved intent or leaked into retrieval", got, err)
	}
}

func TestAutomaticKnownVocalContradictionIsNotReversed(t *testing.T) {
	c, intent, r := automaticFixture(t, 1)
	intent.EssentialCriteria = nil
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_vocals", Value: "vocals", Supported: true}}
	execution := intent
	execution.Controls.RecommendationMode = core.EnhancedHybrid
	clauses := audio.Clauses(execution)
	a := core.AudioAssessment{TrackID: "0", AnalysisID: "packed:0", LibraryCoverage: &core.LibraryCLAPCoverage{CoveredSeconds: 30}}
	for _, cl := range clauses {
		a.Clauses = append(a.Clauses, core.AudioClauseAssessment{Clause: cl, State: core.EvidenceMismatch, Score: .6, ScoreAvailable: true})
	}
	c.observations["0"] = a
	c.annotations["0"] = []core.MetadataAnnotation{{Kind: "vocal", Value: "instrumental", Origin: "embedded_tag"}}
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tracks) != 0 {
		t.Fatalf("known observed vocal contradiction admitted %v, fit=%+v", got.IDs(), got.FitAssessments)
	}
}

func TestAutomaticArtistOnlyRejectsKnownRivalMBID(t *testing.T) {
	c, intent, r := automaticFixture(t, 1)
	c.Catalog = fakes.NewCatalog(2, fakes.CatalogTrack{ID: "0", Display: "Shared - Seed", Audio: []float32{1, 0}}, fakes.CatalogTrack{ID: "1", Display: "Shared - Rival", Audio: []float32{1, 0}})
	for i := range r.candidates {
		if m, ok := c.Catalog.Meta(r.candidates[i].Track.ID); ok {
			r.candidates[i].Track = m.Ref
		}
	}
	desired := "11111111-1111-4111-8111-111111111111"
	rival := "22222222-2222-4222-8222-222222222222"
	intent.References = []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Shared", TrackID: "0", Influence: core.InfluencePositive, Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: desired, Artist: "Shared", Representatives: []core.WeightedTrack{{TrackID: "0", Weight: 1}}}}}}
	intent.HardConstraints = []core.HardConstraint{{Kind: "require_artist", Value: "Shared", Supported: true}}
	automaticSupport(c, intent, "1", "house")
	m, _ := c.Catalog.Meta("1")
	c.recordings["1"] = core.EnrichedTrack{Ref: m.Ref, IdentityStatus: core.ResolutionResolved, ArtistIDs: []string{rival}, AllArtists: []string{"Shared"}}
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tracks) != 0 {
		t.Fatalf("artist-only selected a known different artist MBID: %v", got.IDs())
	}
}

func TestAutomaticArtistMembershipUnknownAndAliasExclusions(t *testing.T) {
	c, intent, r := automaticFixture(t, 1)
	automaticSupport(c, intent, "1", "house")
	wanted := "11111111-1111-4111-8111-111111111111"
	intent.References = []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Chosen", Influence: core.InfluencePositive, Resolution: &core.ReferenceResolution{Status: core.ResolutionResolved, Selected: &core.ResolutionCandidate{Kind: core.ReferenceArtist, EntityID: wanted, Artist: "Artist 1"}}}}
	intent.HardConstraints = []core.HardConstraint{{Kind: "require_artist", Value: "Chosen", Supported: true}}
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 0 {
		t.Fatal("unknown artist ID accepted", got, err)
	}
	meta, _ := c.Catalog.Meta("1")
	c.recordings["1"] = core.EnrichedTrack{Ref: meta.Ref, IdentityStatus: core.ResolutionResolved, ArtistIDs: []string{wanted}}
	got, err = NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || !reflect.DeepEqual(got.IDs(), []string{"1"}) {
		t.Fatal("matching artist ID rejected", got, err)
	}
	intent.HardConstraints = nil
	intent.References[0].Influence = core.InfluenceNegative
	intent.References[0].Resolution.Selected.Artist = "Different language alias"
	got, err = NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 0 {
		t.Fatal("excluded identity alias admitted", got, err)
	}
}
func TestAutomaticKnownSignedMatchIsNotReversed(t *testing.T) {
	c, intent, r := automaticFixture(t, 1)
	intent.EssentialCriteria = nil
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_vocals", Value: "vocals", Supported: true}}
	execution := intent
	execution.Controls.RecommendationMode = core.EnhancedHybrid
	c.annotations["0"] = []core.MetadataAnnotation{{Kind: "vocal", Value: "instrumental", Origin: "embedded_tag"}}
	a := core.AudioAssessment{TrackID: "0", AnalysisID: "prepared:0", LibraryCoverage: &core.LibraryCLAPCoverage{CoveredSeconds: 30}}
	for _, clause := range audio.Clauses(execution) {
		a.Clauses = append(a.Clauses, core.AudioClauseAssessment{Clause: clause, State: core.EvidenceMatch})
	}
	c.observations["0"] = a
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || !reflect.DeepEqual(got.IDs(), []string{"0"}) {
		t.Fatal(got, err)
	}
}

func TestAutomaticRawVocalCosineCannotConfirmInstrumental(t *testing.T) {
	c, intent, r := automaticFixture(t, 1)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "vocal", Value: "instrumental", Scope: "playlist", Strength: "required"}}
	automaticSupport(c, intent, "0", "instrumental")
	assessment := c.observations["0"]
	for i := range assessment.Clauses {
		assessment.Clauses[i].State = core.EvidenceUnknown
		assessment.Clauses[i].Score = .95 // Native pooled CLAP cosine is not a voice contrast.
	}
	c.observations["0"] = assessment
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 0 || got.Outcome.State == core.OutcomeFulfilled {
		t.Fatal("raw instrumental cosine admitted", got.IDs(), got.Outcome, err)
	}
	for _, candidate := range got.Search.Candidates {
		if candidate.Track.ID == "0" && candidate.FitAssessment.State == core.AutomaticStrong {
			t.Fatal("raw cosine became strong voice evidence")
		}
	}
}

func TestAutomaticPartialDescriptionIsPlausibleNotUnknownPadding(t *testing.T) {
	c, intent, r := automaticFixture(t, 1)
	intent.EssentialCriteria = nil
	intent.Preferences.Instrumentation = []core.IntentPreference{{Value: "piano", Influence: core.InfluencePositive, Scope: "playlist", Strength: "preferred"}}
	intent.Preferences.Moods = []core.IntentPreference{{Value: "reflective", Influence: core.InfluencePositive, Scope: "playlist", Strength: "preferred"}}
	automaticSupport(c, intent, "0", "piano")
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 1 || got.FitAssessments[0].Assessment.State != core.AutomaticPlausible || got.Outcome.State != core.OutcomePartial {
		t.Fatal(got, err)
	}
	delete(c.observations, "0")
	delete(c.annotations, "0")
	got, err = NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 0 {
		t.Fatal("unknown count padding", got, err)
	}
}

func (c *automaticCatalogFixture) LibraryVector(ctx context.Context, id string) (core.LibraryVector, bool, error) {
	c.read()
	v, ok := c.mert[id]
	return v, ok, ctx.Err()
}
func (*automaticCatalogFixture) LibraryDSPPreference(context.Context, string, core.MusicIntent) (float64, bool) {
	return 0, false
}
func TestAutomaticPreparedMERTAloneDoesNotAdmitReferenceOutsider(t *testing.T) {
	c, intent, r := automaticFixture(t, 1)
	c.Catalog = fakes.NewCatalog(2, fakes.CatalogTrack{ID: "0", Display: "Reference - Song"}, fakes.CatalogTrack{ID: "1", Display: "Related - Song"})
	intent.EssentialCriteria = nil
	intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "0", Influence: core.InfluencePositive}}
	c.mert = map[string]core.LibraryVector{"0": {Source: core.LibraryEvidenceSource{SpaceID: "full-compatible-contract", Scope: "sampled_audio"}, Values: []float32{1, 0}}, "1": {Source: core.LibraryEvidenceSource{SpaceID: "full-compatible-contract", Scope: "sampled_audio"}, Values: []float32{.8, .6}}}
	got, err := NewAutomatic(c, nil, r, DefaultConfig()).Build(context.Background(), intent)
	if err != nil || len(got.Tracks) != 0 || got.Outcome.State != core.OutcomePartial {
		t.Fatal(got, err)
	}
}
