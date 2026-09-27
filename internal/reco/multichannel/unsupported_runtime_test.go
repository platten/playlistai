package multichannel

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
)

func TestSourceParsedVocalProofSupersedesStaticCapabilityOnly(t *testing.T) {
	for _, test := range []struct {
		name, proof string
		extra       bool
	}{
		{"cited whole-recording absence", "linked_statement", false},
		{"weak absence stays unknown", "community_tag", false},
		{"unrelated parser requirement preserved", "linked_statement", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			prompt := "Make a 2-track instrumental playlist. No vocals."
			var intent core.MusicIntent
			var err error
			if test.extra {
				prompt += " Use gapless crossfades."
				wire := schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 2, Unsupported: []schema.WireUnsupported{{Text: "gapless crossfades", Reason: "playback transition not supported", Span: "gapless crossfades"}}}
				raw, marshalErr := json.Marshal(wire)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				intent, err = schema.ParseForPrompt(raw, prompt)
			} else {
				intent, err = rules.New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
			}
			if err != nil {
				t.Fatal(err)
			}
			intent.Controls.RecommendationMode, intent.VerificationPolicy, intent.Seed = core.EnhancedHybrid, core.BestAvailable, "42"
			intent.References = []core.IntentReference{{Kind: core.ReferenceTrack, TrackID: "seed", Influence: core.InfluencePositive}}
			intent.Knowledge = &core.KnowledgeSnapshot{}
			ids := []string{"seed", "audio", "last"}
			for i := range enhancedMinimumComparisons {
				ids = append(ids, fmt.Sprintf("filler-%03d", i))
			}
			var rows []fakes.CatalogTrack
			for _, id := range ids {
				rows = append(rows, fakes.CatalogTrack{ID: id, Display: "Artist " + id + " - Song", Audio: []float32{1, 0}, Track: []float32{1, 0}})
			}
			cat := fakes.NewCatalog(2, rows...)
			service, _ := cachedAudioService(t, cat, ids...)
			for _, ref := range refs(cat, "audio", "last") {
				// Independent synthetic full-recording evidence; the remaining
				// candidates provide comparison opportunity, not extra matches.
				track := citedGenreFixture(ref, "instrumental")
				track.Claims[0].Kind, track.Claims[0].Method = "vocal", test.proof
				intent.Knowledge.Tracks = append(intent.Knowledge.Tracks, track)
			}
			engine := New(cat, fakes.NewSimilarityEngine(cat), cat, DefaultConfig()).WithAudioProvider(func() *audio.Service { return service }).WithCandidateSource(&fixtureDiscovery{})
			engine.retriever = &metadataPriorityRetriever{poolRetriever{candidates: candidatesForTracks(refs(cat, ids[1:]...)), pageSize: enhancedChannelBatch}}
			result, err := engine.Build(context.Background(), intent)
			if err != nil || result.Search == nil {
				t.Fatalf("build error=%v", err)
			}
			if len(result.Intent.Unsupported) != len(intent.Unsupported) {
				t.Fatal("runtime assessment mutated the stored parsing contract")
			}
			if test.proof != "linked_statement" {
				if len(result.Tracks) != 0 || result.Outcome.State == core.OutcomeFulfilled || result.Search.StopReason == "quality_target" {
					t.Fatalf("weak absence became proof: ids=%v outcome=%+v stop=%s", result.IDs(), result.Outcome, result.Search.StopReason)
				}
			} else if test.extra {
				if len(result.Tracks) != 2 || result.Outcome.State != core.OutcomePartial || result.Search.StopReason == "quality_target" {
					t.Fatalf("unsupported extra request hidden: ids=%v outcome=%+v stop=%s", result.IDs(), result.Outcome, result.Search.StopReason)
				}
			} else if len(result.Tracks) != 2 || result.Outcome.State != core.OutcomeFulfilled || result.Search.StopReason != "quality_target" || result.Search.Considered < enhancedMinimumComparisons {
				t.Fatalf("static capability blocked proved request: ids=%v outcome=%+v stop=%s considered=%d", result.IDs(), result.Outcome, result.Search.StopReason, result.Search.Considered)
			}
		})
	}
}

func TestRuntimeProofRequiresExactRegistryAnnotationAndEvidence(t *testing.T) {
	o, track, claim := recordingClaimFixture()
	claim.Kind, claim.Value = "vocal", "instrumental"
	track.Claims = []core.RecordingClaim{claim}
	o.knowledge = &core.KnowledgeSnapshot{Tracks: []core.EnrichedTrack{track}}
	intent := enhancedIntent(1)
	intent.HardConstraints = []core.HardConstraint{{Kind: "exclude_vocals", Value: "vocals", Evidence: []core.SourceEvidence{{Text: "No vocals", Start: 0, End: 9, Explicit: true}}}}
	intent = intent.Normalized()
	if len(intent.Unsupported) != 1 || !o.runtimeRequirementProved(context.Background(), track.Ref.ID, intent, intent.Unsupported[0]) {
		t.Fatalf("proved registry annotation not recognized: %+v", intent.Unsupported)
	}
	for _, edit := range []func(*core.UnsupportedRequirement){
		func(u *core.UnsupportedRequirement) { u.Reason = "parser cannot interpret this extra condition" },
		func(u *core.UnsupportedRequirement) { u.Text = "another condition" },
		func(u *core.UnsupportedRequirement) { u.Evidence = nil },
	} {
		unsupported := intent.Unsupported[0]
		edit(&unsupported)
		if o.runtimeRequirementProved(context.Background(), track.Ref.ID, intent, unsupported) {
			t.Fatal("unrelated or differently sourced requirement was cleared")
		}
	}
	o.knowledge.Tracks[0].Claims[0].Method = "community_tag"
	intent.HardConstraints[0].RuntimeEnforced = true
	if o.runtimeRequirementProved(context.Background(), track.Ref.ID, intent, intent.Unsupported[0]) {
		t.Fatal("runtime capability flag or weak tag became affirmative evidence")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if o.runtimeRequirementProved(ctx, track.Ref.ID, intent, intent.Unsupported[0]) {
		t.Fatal("canceled assessment resolved a requirement")
	}
}
