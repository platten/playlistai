package multichannel

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestEnhancedStrictVocalAbsenceNeedsWholeRecordingEvidence(t *testing.T) {
	for _, proof := range []string{"preview only", "instrumental classifier", "community tag", "cited absence", "cited vocal contradiction"} {
		t.Run(proof, func(t *testing.T) {
			cat := testCatalog()
			service, _ := cachedAudioService(t, cat)
			engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
			intent := enhancedIntent(2)
			intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_vocals", Value: "true"}}
			intent.Knowledge = &core.KnowledgeSnapshot{}
			features := &semanticFixture{features: map[string]core.TrackFeatures{}}
			for _, ref := range refs(cat, "audio", "last") {
				if proof == "instrumental classifier" {
					feature := completeStyleFeature(ref.ID, "ambient")
					feature.VocalEvidence = feature.Styles[0]
					feature.VocalEvidence.Value = "instrumental"
					feature.VocalEvidence.Confidence = .99466166528842
					features.features[ref.ID] = feature
					engine.features = features
				}
				if proof == "community tag" || proof == "cited absence" || proof == "cited vocal contradiction" {
					// This cited full-recording statement is independent synthetic
					// ground truth, unlike the high-confidence classifier above.
					track := citedGenreFixture(ref, "instrumental")
					track.Claims[0].Kind = "vocal"
					if proof == "community tag" {
						track.Claims[0].Method = "community_tag"
					}
					if proof == "cited vocal contradiction" {
						voice := track.Claims[0]
						voice.Value, voice.Method = "vocals", "recording_credit"
						track.Claims = append(track.Claims, voice)
					}
					intent.Knowledge.Tracks = append(intent.Knowledge.Tracks, track)
				}
			}
			result, err := engine.Build(context.Background(), intent)
			if err != nil {
				t.Fatal(err)
			}
			if proof == "cited absence" {
				if len(result.Tracks) != 2 || result.Outcome.State == core.OutcomeNeedsClarification {
					t.Fatalf("cited absence rejected: ids=%v outcome=%+v", result.IDs(), result.Outcome)
				}
				for _, assessment := range result.Assessments {
					if len(assessment.Criteria) != 1 || assessment.Criteria[0].State != core.EvidenceMatch {
						t.Fatalf("cited absence not supported: %+v", assessment)
					}
				}
			} else if len(result.Tracks) != 0 || result.Outcome.State == core.OutcomeFulfilled {
				t.Fatalf("unverified absence admitted: ids=%v outcome=%+v", result.IDs(), result.Outcome)
			}
		})
	}
}

func TestEnhancedStrictVocalAbsenceDoesNotBypassRequiredTrack(t *testing.T) {
	cat := testCatalog()
	service, _ := cachedAudioService(t, cat)
	engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service })
	intent := enhancedIntent(1)
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_vocals", Value: "true"}}
	intent.RequiredTracks = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "audio", Influence: core.InfluencePositive}}
	result, err := engine.Build(context.Background(), intent)
	if err != nil || len(result.Tracks) != 0 || result.Outcome.State != core.OutcomeNeedsClarification || len(result.Intent.RequiredTracks) != 1 || len(result.Outcome.Reasons) == 0 || result.Outcome.Reasons[0].Code != "required_track_evidence_unconfirmed" {
		t.Fatalf("required preview bypassed absence proof: ids=%v outcome=%+v err=%v", result.IDs(), result.Outcome, err)
	}
}

func TestNativeInstrumentalClassifierIsOnlyAbsenceHint(t *testing.T) {
	cat := testCatalog()
	feature := completeStyleFeature("audio", "ambient")
	feature.VocalEvidence = feature.Styles[0]
	feature.VocalEvidence.Value = "instrumental"
	feature.VocalEvidence.Confidence = .99466166528842
	features := &semanticFixture{features: map[string]core.TrackFeatures{"audio": feature}}
	o := NewWithSemantic(cat, nil, cat, features, nil, DefaultConfig())
	o.enhanced, o.bestAvailable = true, true
	clause := core.AudioClause{Kind: "vocal", Text: "vocals", Scope: "playlist", Negative: true, Strict: true}
	assessment := o.assessClause(context.Background(), "audio", clause, core.AudioAssessment{})
	if assessment.State != core.EvidenceUnknown || len(assessment.Claims) != 1 || assessment.Claims[0].Method != "native_vocal_hint" || o.strongClause(context.Background(), "audio", clause, core.AudioAssessment{}) {
		t.Fatalf("classifier proved whole-recording absence: %+v", assessment)
	}
	feature.VocalEvidence.Value = "vocal"
	features.features["audio"] = feature
	if assessment := o.assessClause(context.Background(), "audio", clause, core.AudioAssessment{}); assessment.State != core.EvidenceMismatch {
		t.Fatalf("vocal contradiction lost: %+v", assessment)
	}
	o.enhanced = false
	feature.VocalEvidence.Value = "instrumental"
	features.features["audio"] = feature
	if state := o.clauseFitState(context.Background(), "audio", clause, core.AudioAssessment{}); state != core.EvidenceMatch {
		t.Fatalf("other recommendation policy changed: %v", state)
	}
}
