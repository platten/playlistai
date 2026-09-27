package bridge

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type submittedIdentityKnowledge struct {
	calls          int
	match          bool
	started        chan struct{}
	generatedProof *core.IdentityCorroboration
}

func (k *submittedIdentityKnowledge) CorroborateArtistReferences(ctx context.Context, intent core.MusicIntent) (core.MusicIntent, error) {
	k.calls++
	if k.started != nil {
		close(k.started)
		<-ctx.Done()
		return intent, ctx.Err()
	}
	if k.match {
		intent = intent.Normalized()
		intent.References[0].Grounding.Corroboration = &core.IdentityCorroboration{
			SelectedID: "11111111-1111-4111-8111-111111111111", Method: core.ArtistCoperformanceMethod,
			Supports: []core.IdentityCorroborationSupport{{
				AnchorID:     "33333333-3333-4333-8333-333333333333",
				RecordingIDs: []string{"44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555"},
				Source:       core.ContextSource{Provider: "MusicBrainz", URL: "https://musicbrainz.org/ws/2/artist/33333333-3333-4333-8333-333333333333?inc=recording-rels", Revision: strings.Repeat("a", 64), License: "CC0-1.0"},
			}},
		}
	}
	return intent, nil
}

func (k *submittedIdentityKnowledge) ResolveMusic(_ context.Context, intent core.MusicIntent, _ ports.Catalog, _ ports.ReferenceResolver, _ ports.Progress) (core.MusicIntent, error) {
	k.generatedProof = intent.References[0].Grounding.Corroboration
	intent.References[0].TrackID = "seed0001"
	intent.References[0].Resolution = nil
	return intent, nil
}

func submittedIdentityFixture(t *testing.T) (*API, string, ports.IntentInput) {
	t.Helper()
	a := New(newLoadedContainer(t), nil)
	input := ports.IntentInput{Prompt: "like Justice, 1 track", SessionID: "corroboration"}
	entry, _, err := a.parseIntentCached(context.Background(), input, nil)
	if err != nil || len(entry.intent.References) != 1 || entry.cacheKey == "" {
		t.Fatalf("prepare cached interpretation: %+v, %v", entry, err)
	}
	entry.intent.References[0].Grounding = &core.IdentityGrounding{
		Provider: "MusicBrainz", MatchedSpelling: "Justice", MatchType: "canonical", SnapshotVersion: "fixture/v1",
		Candidates: []core.IdentityCandidate{
			{Kind: core.ReferenceArtist, ID: "11111111-1111-4111-8111-111111111111", Name: "Justice"},
			{Kind: core.ReferenceArtist, ID: "22222222-2222-4222-8222-222222222222", Name: "Justice"},
		},
	}
	a.intentCache.put(entry.cacheKey, entry)
	return a, entry.cacheKey, input
}

func TestSubmittedPreviewCorroboratesBeforeAmbiguityGateAndReusesProof(t *testing.T) {
	for _, match := range []bool{false, true} {
		t.Run(map[bool]string{false: "unresolved", true: "corroborated"}[match], func(t *testing.T) {
			a, key, input := submittedIdentityFixture(t)
			knowledge := &submittedIdentityKnowledge{match: match}
			a.app.Knowledge = knowledge
			session := IntentSessionContext{SessionID: input.SessionID}
			local, err := a.ParseIntentWithContext(context.Background(), input.Prompt, session)
			if err != nil || knowledge.calls != 0 || len(local.ResolutionIssues) != 1 || local.ResolutionIssues[0].Status != core.ResolutionAmbiguous {
				t.Fatalf("unsubmitted preview performed online work or lost ambiguity: %+v, %v", local.ResolutionIssues, err)
			}
			session.GenerationID = "submitted"
			preview, err := a.ParseIntentWithContext(context.Background(), input.Prompt, session)
			if err != nil || knowledge.calls != 1 || len(preview.ResolutionIssues) != 1 {
				t.Fatalf("submitted preview missed identity check: %+v, %v", preview.ResolutionIssues, err)
			}
			if !match {
				if preview.ResolutionIssues[0].Status != core.ResolutionAmbiguous {
					t.Fatal("missing proof bypassed the user choice")
				}
				return
			}
			if preview.ResolutionIssues[0].Status != core.ResolutionUnresolved || preview.Intent.Knowledge != nil {
				t.Fatal("corroboration fabricated a catalog seed or completed preparation")
			}
			preview.Intent.References[0].Grounding.Corroboration.SelectedID = "changed-by-caller"
			cached, ok := a.intentCache.get(key)
			if !ok || cached.intent.References[0].Grounding.Corroboration.SelectedID == "changed-by-caller" {
				t.Fatal("returned preview mutated the saved identity proof")
			}
			useRecommendationEngine(a, &deadlineCheckingRecommendationEngine{})
			generated, err := a.GenerateFromPromptWithContext(context.Background(), input.Prompt, session)
			if err != nil || !generated.Status.ParsedIntentReused || knowledge.generatedProof == nil || knowledge.calls != 1 {
				t.Fatalf("generation did not consume the submitted proof: reused=%v proof=%+v calls=%d err=%v", generated.Status.ParsedIntentReused, knowledge.generatedProof, knowledge.calls, err)
			}
		})
	}
}

func TestCanceledSubmittedCorroborationDoesNotPopulateCache(t *testing.T) {
	a, _, input := submittedIdentityFixture(t)
	knowledge := &submittedIdentityKnowledge{started: make(chan struct{})}
	a.app.Knowledge = knowledge
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := a.ParseIntentWithContext(ctx, input.Prompt, IntentSessionContext{SessionID: input.SessionID, GenerationID: "submitted"})
		done <- err
	}()
	<-knowledge.started
	a.intentCache.clear()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preview: %v", err)
	}
	if len(a.intentCache.entries) != 0 {
		t.Fatal("canceled identity evidence repopulated an invalidated cache")
	}
}

type cacheBlockedIdentityKnowledge struct {
	submittedIdentityKnowledge
	cache *intentCache
	ready chan struct{}
}

func (k *cacheBlockedIdentityKnowledge) CorroborateArtistReferences(ctx context.Context, intent core.MusicIntent) (core.MusicIntent, error) {
	intent, err := k.submittedIdentityKnowledge.CorroborateArtistReferences(ctx, intent)
	k.cache.mu.Lock()
	close(k.ready)
	return intent, err
}

func TestSubmittedProofCancellationWhileWaitingForCache(t *testing.T) {
	a, key, input := submittedIdentityFixture(t)
	knowledge := &cacheBlockedIdentityKnowledge{submittedIdentityKnowledge: submittedIdentityKnowledge{match: true}, cache: &a.intentCache, ready: make(chan struct{})}
	a.app.Knowledge = knowledge
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := a.ParseIntentWithContext(ctx, input.Prompt, IntentSessionContext{SessionID: input.SessionID, GenerationID: "submitted"})
		done <- err
	}()
	<-knowledge.ready
	// Observe the actual contended write rather than assuming a sleep lets
	// generation pass its earlier cancellation check.
	blocked := false
	deadline := time.Now().Add(time.Second)
	stack := make([]byte, 1<<20)
	for time.Now().Before(deadline) {
		trace := string(stack[:runtime.Stack(stack, true)])
		if strings.Contains(trace, "(*intentCache).putIfCurrent(") || strings.Contains(trace, "(*intentCache).put(") {
			blocked = true
			break
		}
		runtime.Gosched()
	}
	cancel()
	a.intentCache.mu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("contended preview cancellation: %v", err)
	}
	if !blocked {
		t.Fatal("did not observe the contended cache write")
	}
	cached, _ := a.intentCache.get(key)
	if cached.intent.References[0].Grounding.Corroboration != nil {
		t.Fatal("canceled submitted proof entered a reusable cache entry")
	}
}
