package multichannel

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func recordingClaimFixture() (*Orchestrator, core.EnrichedTrack, core.RecordingClaim) {
	cat := testCatalog()
	track := core.EnrichedTrack{Ref: refs(cat, "audio")[0], IdentityStatus: core.ResolutionResolved, RecordingID: "recording-1"}
	claim := core.RecordingClaim{Kind: "genre", Value: "ambient", State: core.EvidenceMatch, Scope: "recording", EntityID: track.RecordingID, RecordingID: track.RecordingID,
		Source: core.ContextSource{Provider: "musicbrainz", URL: "https://example.test/recording/recording-1", Revision: "content-sha"}, Locator: "genres[0]", Method: "linked_statement"}
	o := New(cat, nil, cat, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	return o, track, claim
}

func TestRecordingClaimsRespectScopeIdentityAndSource(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*core.RecordingClaim)
		want   core.EvidenceState
	}{
		{"recording statement", func(*core.RecordingClaim) {}, core.EvidenceMatch},
		{"artist hint", func(c *core.RecordingClaim) { c.Scope = "artist" }, core.EvidenceUnknown},
		{"album hint", func(c *core.RecordingClaim) { c.Scope = "release" }, core.EvidenceUnknown},
		{"another recording", func(c *core.RecordingClaim) { c.RecordingID = "recording-2" }, core.EvidenceUnknown},
		{"another entity", func(c *core.RecordingClaim) { c.EntityID = "recording-2" }, core.EvidenceUnknown},
		{"unversioned source", func(c *core.RecordingClaim) { c.Source.Revision = "" }, core.EvidenceUnknown},
		{"community tag", func(c *core.RecordingClaim) { c.Method = "community_tag" }, core.EvidenceUnknown},
		{"quoted model hint", func(c *core.RecordingClaim) { c.Method = "quoted_statement" }, core.EvidenceUnknown},
		{"model assertion", func(c *core.RecordingClaim) { c.Method = "model_assertion" }, core.EvidenceUnknown},
		{"sampled only", func(c *core.RecordingClaim) { c.Coverage = &core.PreviewCoverage{Available: true, CoveredSeconds: 30} }, core.EvidenceUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			o, track, claim := recordingClaimFixture()
			test.change(&claim)
			track.Claims = []core.RecordingClaim{claim}
			o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
			assessment := o.assessClause(context.Background(), track.Ref.ID, core.AudioClause{Kind: "genre", Text: "ambient"}, core.AudioAssessment{})
			if assessment.State != test.want || len(assessment.Claims) != 1 {
				t.Fatalf("assessment=%+v want=%s", assessment, test.want)
			}
		})
	}
}

func TestRecordingClaimConflictAndLinkedComposer(t *testing.T) {
	o, track, claim := recordingClaimFixture()
	opposing := claim
	opposing.State, opposing.Source.Provider = core.EvidenceMismatch, "independent-source"
	track.Claims = []core.RecordingClaim{claim, opposing}
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
	got := o.assessClause(context.Background(), track.Ref.ID, core.AudioClause{Kind: "genre", Text: "ambient"}, core.AudioAssessment{})
	if got.State != core.EvidenceUnknown || !got.Conflict || len(got.Claims) != 2 {
		t.Fatalf("conflicting sources became verified: %+v", got)
	}
	claim.Kind, claim.Value, claim.Scope, claim.EntityID, claim.Method = "composer", "Composer Name", "work", "work-1", "recording_credit"
	track.Claims = []core.RecordingClaim{claim}
	o.knowledge.Tracks[0] = track
	if got := o.bestCriterion(context.Background(), track.Ref.ID, core.MusicalCriterion{Kind: "composer", Value: "Composer Name"}); got != core.EvidenceMatch {
		t.Fatalf("linked work composer was lost: %s", got)
	}
	claim.Kind, claim.Value = "instrumentation", "piano"
	o.knowledge.Tracks[0].Claims = []core.RecordingClaim{claim}
	if got := o.bestCriterion(context.Background(), track.Ref.ID, core.MusicalCriterion{Kind: "instrumentation", Value: "piano"}); got != core.EvidenceUnknown {
		t.Fatalf("work instrumentation leaked onto recording: %s", got)
	}
}

func TestVocalPreviewContradictsMetadataAndCannotProveWholeRecordingAbsence(t *testing.T) {
	o, track, claim := recordingClaimFixture()
	claim.Kind, claim.Value = "vocal", "instrumental"
	track.Claims = []core.RecordingClaim{claim}
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
	clause := core.AudioClause{Kind: "vocal", Text: "vocals", Negative: true, Strict: true}
	preview := core.AudioAssessment{TrackID: track.Ref.ID, AnalysisID: "observed", Coverage: &core.PreviewCoverage{Available: true, CoveredSeconds: 30}, Clauses: []core.AudioClauseAssessment{{Clause: clause, State: core.EvidenceMismatch}}}
	got := o.assessClause(context.Background(), track.Ref.ID, clause, preview)
	if got.State != core.EvidenceMismatch || !got.Conflict {
		t.Fatalf("vocal observation hidden by instrumental claim: %+v", got)
	}
	for _, fact := range got.Claims {
		if fact.Method == "audio_observation" && fact.State != core.EvidenceMatch {
			t.Fatal("detected vocals were stored as absent because the request was negative")
		}
	}
	o.knowledge.Tracks[0].Claims = nil
	preview.Clauses[0].State = core.EvidenceMatch
	if got := o.assessClause(context.Background(), track.Ref.ID, clause, preview); got.State != core.EvidenceUnknown {
		t.Fatalf("instrumental excerpt proved whole-recording absence: %+v", got)
	}
	if o.strongClause(context.Background(), track.Ref.ID, clause, preview) {
		t.Fatal("sampled instrumental passage proved whole-recording absence")
	}
	claim.Value, claim.Method = "vocal", "recording_credit"
	o.knowledge.Tracks[0].Claims = []core.RecordingClaim{claim}
	if got := o.assessClause(context.Background(), track.Ref.ID, clause, core.AudioAssessment{}); got.State != core.EvidenceMismatch {
		t.Fatalf("explicit credited vocals did not oppose no-vocals: %+v", got)
	}
}

func TestCommunityTagSupportsCloseSuggestionButNotStrictRequirement(t *testing.T) {
	o, track, claim := recordingClaimFixture()
	claim.Method = "community_tag"
	track.Claims = []core.RecordingClaim{claim}
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
	intent := enhancedIntent(1)
	intent.References = nil
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "ambient", Scope: "playlist"}}
	intent.Preferences.Genres = []core.IntentPreference{{Value: "ambient", Influence: core.InfluencePositive}}
	candidate := core.Candidate{Track: track.Ref}
	if !o.enhancedMetadataFallback(context.Background(), candidate, intent) {
		t.Fatal("identity-grounded community tag lost as weak support")
	}
	if tier, _ := o.enhancedTier(context.Background(), candidate, intent); tier != fitClose {
		t.Fatal("weak tag acquired strong tier")
	}
	intent.EssentialCriteria[0].Strength = "required"
	if o.enhancedMetadataFallback(context.Background(), candidate, intent) {
		t.Fatal("weak tag bypassed strict requirement")
	}
}

func TestRecordingInstrumentPresenceCannotSatisfyProminence(t *testing.T) {
	o, track, claim := recordingClaimFixture()
	claim.Kind, claim.Value, claim.Method = "instrumentation", "piano", "recording_credit"
	track.Claims = []core.RecordingClaim{claim}
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
	presence := core.AudioClause{Kind: "instrumentation", Text: "piano", Scope: "playlist"}
	preview := core.AudioAssessment{TrackID: track.Ref.ID, Clauses: []core.AudioClauseAssessment{{Clause: presence, State: core.EvidenceMatch}}}
	prominent := presence
	prominent.Degree = "mostly"
	if got := o.assessClause(context.Background(), track.Ref.ID, prominent, preview); got.State != core.EvidenceUnknown {
		t.Fatalf("instrument credit or presence-only preview proved prominence: %+v", got)
	}
	if got := o.assessClause(context.Background(), track.Ref.ID, presence, preview); got.State != core.EvidenceMatch {
		t.Fatalf("applicable instrument presence evidence lost: %+v", got)
	}
}

type recordingVerifierFunc func(context.Context, core.EnrichedTrack, []core.MusicalCriterion) (core.EnrichedTrack, error)

func (f recordingVerifierFunc) VerifyRecording(ctx context.Context, track core.EnrichedTrack, criteria []core.MusicalCriterion) (core.EnrichedTrack, error) {
	return f(ctx, track, criteria)
}

func TestRecordingVerificationIsBoundedOutsideScoringAndFrozen(t *testing.T) {
	o, track, claim := recordingClaimFixture()
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
	intent := enhancedIntent(1)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "ambient"}}
	calls := 0
	o.WithRecordingVerifier(recordingVerifierFunc(func(_ context.Context, got core.EnrichedTrack, criteria []core.MusicalCriterion) (core.EnrichedTrack, error) {
		calls++
		if len(criteria) == 0 {
			t.Fatal("request facets missing")
		}
		got.RecordingID, got.IdentityStatus = track.RecordingID, core.ResolutionResolved
		got.Claims = []core.RecordingClaim{claim}
		return got, nil
	}))
	if err := o.verifyRecording(context.Background(), track.Ref, intent); err != nil {
		t.Fatal(err)
	}
	if err := o.verifyRecording(context.Background(), track.Ref, intent); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		o.bestCriterion(context.Background(), track.Ref.ID, intent.EssentialCriteria[0])
		o.enhancedTier(context.Background(), core.Candidate{Track: track.Ref}, intent)
	}
	if calls != 1 || len(o.knowledge.Tracks[0].Claims) != 0 {
		t.Fatal("scoring acquired evidence or input snapshot was mutated")
	}
	o.search = &core.SearchSnapshot{Candidates: []core.Candidate{{Track: track.Ref}}}
	result := core.Playlist{Intent: intent}
	o.freezeRecordingEvidence(&result)
	if result.Intent.Knowledge == nil || len(result.Intent.Knowledge.Tracks) != 1 || len(result.Intent.Knowledge.Tracks[0].Claims) != 1 {
		t.Fatal("completed source claim missing from saved evidence")
	}
	for i := 0; i < recordingVerificationLimit+3; i++ {
		ref := track.Ref
		ref.ID = fmt.Sprintf("candidate-%d", i)
		// The ceiling concerns recording-fact acquisition after identity is
		// established; unresolved identity attempts have a separate subquota.
		identified := track
		identified.Ref = ref
		o.knowledge.Tracks = append(o.knowledge.Tracks, identified)
		if err := o.verifyRecording(context.Background(), ref, intent); err != nil {
			t.Fatal(err)
		}
	}
	if calls != recordingVerificationLimit {
		t.Fatalf("verification ceiling calls=%d", calls)
	}
}

func TestRecordingVerificationHonorsCancellationStopAndReplay(t *testing.T) {
	for _, mode := range []string{"cancel", "stop", "replay", "other policy"} {
		t.Run(mode, func(t *testing.T) {
			o, track, _ := recordingClaimFixture()
			intent := enhancedIntent(1)
			intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "ambient"}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "cancel":
				cancel()
			case "stop":
				stop := make(chan struct{})
				close(stop)
				o.verificationStop = stop
			case "replay":
				o.replayingEvidence = true
			default:
				o.enhanced = false
			}
			o.WithRecordingVerifier(recordingVerifierFunc(func(ctx context.Context, track core.EnrichedTrack, _ []core.MusicalCriterion) (core.EnrichedTrack, error) {
				if ctx.Err() == nil {
					t.Fatal("unexpected provider request")
				}
				return track, ctx.Err()
			}))
			err := o.verifyRecording(ctx, track.Ref, intent)
			if mode == "cancel" && !errors.Is(err, context.Canceled) || mode != "cancel" && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQualityTargetRequiresCompleteSupportedPlaylist(t *testing.T) {
	cat, intent := localPriorityFixture()
	o := New(cat, nil, cat, DefaultConfig())
	o.requestContext = context.Background()
	o.enhanced, o.bestAvailable = true, true
	intent.Count = 2
	assembly := candidateAssembly{sequence: ports.SequenceResult{Tracks: refs(cat, "pack:fixture:local:one", "provider:two")}}
	if !o.qualityTargetMet(context.Background(), assembly, intent) {
		t.Fatal("supported completed playlist missed target")
	}
	intent.HardConstraints = []core.HardConstraint{{Kind: core.HardConstraintIncludeOtherArtists, Value: "true"}}
	intent.References = nil
	for _, track := range assembly.sequence.Tracks {
		intent.References = append(intent.References, core.IntentReference{Kind: core.ReferenceArtist, Query: track.Artist})
	}
	if o.qualityTargetMet(context.Background(), assembly, intent) {
		t.Fatal("quality stop bypassed explicit other-artists requirement")
	}
	intent.HardConstraints = nil
	assembly.sequence.Tracks[1] = refs(cat, "unknown")[0]
	if o.qualityTargetMet(context.Background(), assembly, intent) {
		t.Fatal("count alone satisfied quality target")
	}
	intent.EssentialCriteria, intent.Preferences = nil, core.SemanticPreferences{}
	if o.qualityTargetMet(context.Background(), assembly, intent) {
		t.Fatal("uncalibrated reference similarity became verified quality")
	}
}

func TestRejectedCandidateRetainsFacetEvidenceAndReason(t *testing.T) {
	o, track, claim := recordingClaimFixture()
	claim.State = core.EvidenceMismatch
	track.Claims = []core.RecordingClaim{claim}
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
	o.search = &core.SearchSnapshot{}
	intent := enhancedIntent(1)
	intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "ambient"}}
	candidate := core.Candidate{Track: track.Ref}
	o.admitCandidateAssessment(candidate)
	o.recordCandidateDecision(context.Background(), candidate, intent, "rejected", "essential_criterion_unverified_or_contradicted")
	got := o.search.Candidates[0]
	if got.Decision != "rejected" || len(got.DecisionReasons) != 1 || len(got.Criteria) != 1 || got.Criteria[0].State != core.EvidenceMismatch || len(got.Criteria[0].Claims) != 1 {
		t.Fatalf("rejection audit lost: %+v", got)
	}
}

func TestRecordingMetadataMergePreservesReleaseTrackPair(t *testing.T) {
	local := core.EnrichedTrack{ReleaseID: "release-a", ReleaseTrackID: "track-a"}
	got := mergeRecordingMetadata(core.EnrichedTrack{}, local)
	if got.ReleaseID != "release-a" || got.ReleaseTrackID != "track-a" {
		t.Fatal(got)
	}
	got = mergeRecordingMetadata(core.EnrichedTrack{ReleaseID: "release-b"}, local)
	if got.ReleaseTrackID != "" {
		t.Fatal("track from another release paired with saved release")
	}
	got = mergeRecordingMetadata(core.EnrichedTrack{ReleaseID: "release-a", ReleaseTrackID: "saved-track"}, local)
	if got.ReleaseTrackID != "saved-track" {
		t.Fatal("saved release track replaced")
	}
}

func TestVocalPresenceAndMostlyInstrumentalKeepSampleScope(t *testing.T) {
	o, track, _ := recordingClaimFixture()
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
	for _, test := range []struct {
		clause core.AudioClause
		strong bool
	}{
		{core.AudioClause{Kind: "vocal", Text: "vocals"}, true},
		{core.AudioClause{Kind: "vocal", Text: "instrumental", Degree: "mostly"}, false},
	} {
		preview := core.AudioAssessment{TrackID: track.Ref.ID, Coverage: &core.PreviewCoverage{Available: true, CoveredSeconds: 30}, Clauses: []core.AudioClauseAssessment{{Clause: test.clause, State: core.EvidenceMatch}}}
		got := o.assessClause(context.Background(), track.Ref.ID, test.clause, preview)
		if got.State != core.EvidenceMatch || o.strongClause(context.Background(), track.Ref.ID, test.clause, preview) != test.strong {
			t.Fatalf("sample distinction lost clause=%+v assessment=%+v", test.clause, got)
		}
	}
}

// Cited facts are explicit fixture ground truth, separate from the feature
// labels whose reliability other tests exercise. No real web claim is made.
func citedGenreFixture(ref core.TrackRef, values ...string) core.EnrichedTrack {
	track := core.EnrichedTrack{Ref: ref, IdentityStatus: core.ResolutionResolved, RecordingID: "fixture-recording:" + ref.ID}
	for _, value := range values {
		track.Claims = append(track.Claims, core.RecordingClaim{Kind: "genre", Value: value, State: core.EvidenceMatch,
			Scope: "recording", EntityID: track.RecordingID, RecordingID: track.RecordingID,
			Source: core.ContextSource{Provider: "independent-fixture-review", URL: "https://fixture.invalid/recording/" + ref.ID, Revision: "review-v1"}, Locator: "genre", Method: "linked_statement"})
	}
	return track
}

func addCitedStyleFixture(o *Orchestrator, features *semanticFixture) {
	snapshot := core.KnowledgeSnapshot{}
	if o.knowledge != nil {
		snapshot = *o.knowledge
		snapshot.Tracks = append([]core.EnrichedTrack(nil), snapshot.Tracks...)
	}
	for row := 0; row < o.cat.Len(); row++ {
		id := o.cat.ID(row)
		feature, ok := features.features[id]
		if !ok {
			continue
		}
		meta, _ := o.cat.Meta(id)
		var values []string
		for _, style := range feature.Styles {
			values = append(values, style.Value)
		}
		snapshot.Tracks = append(snapshot.Tracks, citedGenreFixture(meta.Ref, values...))
	}
	o.knowledge = &snapshot
}

func TestRecordingContradictionPersistsWithoutRetagging(t *testing.T) {
	for _, different := range []string{"", "recording", "ref"} {
		t.Run("different-"+different, func(t *testing.T) {
			o, track, claim := recordingClaimFixture()
			o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
			intent := enhancedIntent(1)
			intent.EssentialCriteria = []core.MusicalCriterion{{Kind: "genre", Value: "ambient"}}
			o.WithRecordingVerifier(recordingVerifierFunc(func(_ context.Context, got core.EnrichedTrack, _ []core.MusicalCriterion) (core.EnrichedTrack, error) {
				got.IdentityStatus = core.ResolutionAmbiguous
				got.Matched = true
				got.Claims = []core.RecordingClaim{claim}
				if different == "recording" {
					got.RecordingID = "other"
				}
				if different == "ref" {
					got.Ref.ID = "other"
				}
				return got, nil
			}))
			if err := o.verifyRecording(context.Background(), track.Ref, intent); err != nil {
				t.Fatal(err)
			}
			got, found := o.verifiedRecordings[track.Ref.ID]
			if different != "" {
				if found {
					t.Fatal("unrelated identity quarantined")
				}
				return
			}
			if !found || got.IdentityStatus != core.ResolutionAmbiguous || got.Matched || len(got.Claims) != 0 {
				t.Fatalf("conflict lost: %+v", got)
			}
			known, ok := o.knowledgeTrackContext(context.Background(), track.Ref.ID)
			if !ok || known.IdentityStatus != core.ResolutionAmbiguous {
				t.Fatal("prior catalog facts hid conflict")
			}
			o.search = &core.SearchSnapshot{Candidates: []core.Candidate{{Track: track.Ref}}}
			result := core.Playlist{Intent: intent}
			o.freezeRecordingEvidence(&result)
			if result.Intent.Knowledge == nil || len(result.Intent.Knowledge.Tracks) != 1 || result.Intent.Knowledge.Tracks[0].IdentityStatus != core.ResolutionAmbiguous {
				t.Fatal("saved evidence lost ambiguity")
			}
		})
	}
}
