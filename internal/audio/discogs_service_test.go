package audio

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
)

func TestDiscogsResampleFiltersAliasAndKeepsObservedLength(t *testing.T) {
	makeTone := func(frequency float64) DecodedPCM {
		pcm := DecodedPCM{SampleRate: 48000, Channels: 1, Samples: make([]float32, 48000*3)}
		for i := range pcm.Samples {
			pcm.Samples[i] = float32(.25 * math.Sin(2*math.Pi*frequency*float64(i)/48000))
		}
		return pcm
	}
	measure := func(samples []float32) float64 {
		var sum float64
		for _, v := range samples {
			sum += float64(v) * float64(v)
		}
		return math.Sqrt(sum / float64(len(samples)))
	}
	pass, err := DiscogsResample(context.Background(), makeTone(440))
	if err != nil || len(pass) != 48000 {
		t.Fatalf("passband: length=%d err=%v", len(pass), err)
	}
	alias, err := DiscogsResample(context.Background(), makeTone(12000))
	if err != nil {
		t.Fatal(err)
	}
	if measure(pass) < .16 || measure(alias) > .025 {
		t.Fatalf("resampler pass=%g alias=%g", measure(pass), measure(alias))
	}
}

func TestDiscogsEvidenceCacheRequiresSameAudioAndModelFingerprint(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	identity := core.PreviewIdentity{PolicyVersion: core.PreviewIdentityPolicyVersion, Status: core.ResolutionResolved, Provider: "deezer", ProviderID: "recording-1"}
	encoder := core.MusicClassifierIdentity{Model: "discogs-effnet-bsdynamic-1", Revision: "1", WeightsSHA256: strings.Repeat("a", 64), MetadataSHA256: strings.Repeat("b", 64)}
	head := core.MusicClassifierHead{Kind: "mood", Model: core.MusicClassifierIdentity{Model: "relaxed", Revision: "1", WeightsSHA256: strings.Repeat("c", 64), MetadataSHA256: strings.Repeat("d", 64)}, Classes: []string{"relaxed", "non_relaxed"}, Scores: []float32{.7, .3}}
	evidence := core.MusicClassifierEvidence{Version: core.MusicClassifierEvidenceVersion, Encoder: encoder, Preprocessing: DiscogsPreprocessing, Runtime: DiscogsRuntime, AudioSHA256: strings.Repeat("e", 64), Source: "deezer-preview", SourceID: identity.ProviderID, License: "noncommercial", Coverage: core.LibraryCLAPCoverage{CoveredSeconds: 8, Incomplete: true, PartialReason: "preview only", Segments: []core.LibraryAudioInterval{{StartSeconds: 0, EndSeconds: 8}}}, Heads: []core.MusicClassifierHead{head}}
	if err := store.PutClassifier(context.Background(), "catalog", "track", "key", identity, evidence); err != nil {
		t.Fatal(err)
	}
	if usage, err := store.Usage(context.Background()); err != nil || usage.Records != 1 {
		t.Fatalf("classifier cache usage=%+v err=%v", usage, err)
	}
	if found, hit, err := store.FindClassifier(context.Background(), "catalog", "track", "key", evidence.AudioSHA256, identity.ProviderID, evidence.Fingerprint()); err != nil || !hit || found.Heads[0].Scores[0] != .7 {
		t.Fatalf("expected cache hit, hit=%v err=%v evidence=%+v", hit, err, found)
	}
	for _, query := range []struct {
		hash, source, fingerprint string
	}{{strings.Repeat("f", 64), identity.ProviderID, evidence.Fingerprint()}, {evidence.AudioSHA256, "other", evidence.Fingerprint()}, {evidence.AudioSHA256, identity.ProviderID, strings.Repeat("a", 64)}} {
		if _, hit, err := store.FindClassifier(context.Background(), "catalog", "track", "key", query.hash, query.source, query.fingerprint); err != nil || hit {
			t.Fatalf("wrong cache hit=%v err=%v", hit, err)
		}
	}
}

func TestDiscogsSessionOnlyRunsForSupportedFacets(t *testing.T) {
	service, _, _, _ := testService(t)
	service.Classifier = &DiscogsClassifier{}
	for _, tc := range []struct {
		kind, strength string
		useful         bool
	}{{"texture", "essential", false}, {"instrumentation", "essential", true}, {"mood", "essential", true}, {"mood", "required", false}} {
		intent := core.MusicIntent{EssentialCriteria: []core.MusicalCriterion{{Kind: tc.kind, Value: "piano", Scope: "playlist", Strength: tc.strength}}, VerificationPolicy: core.BestAvailable}
		session, err := service.BeginWithBudget(context.Background(), intent, "catalog", nil, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if (session.service.Classifier != nil) != tc.useful {
			t.Errorf("%s/%s classifier useful=%v", tc.kind, tc.strength, session.service.Classifier != nil)
		}
		session.Close()
	}
}

func TestDiscogsNativeScoresAreStoredAndReusedWithoutAudio(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	worker := &DiscogsWorker{Executable: executable, ModelDir: "test:discogs-healthy"}
	defer worker.Close()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	encoder := core.MusicClassifierIdentity{Model: "discogs-effnet-bsdynamic-1", Revision: "1", WeightsSHA256: strings.Repeat("a", 64), MetadataSHA256: strings.Repeat("b", 64)}
	model := DiscogsModel{Encoder: encoder}
	for i, spec := range []struct {
		kind  string
		count int
	}{{"style", 400}, {"instrumentation", 40}, {"vocal", 2}, {"mood", 2}} {
		classes := make([]string, spec.count)
		for j := range classes {
			classes[j] = fmt.Sprintf("class-%d-%d", i, j)
		}
		identity := core.MusicClassifierIdentity{Model: fmt.Sprintf("head-%d", i), Revision: "1", WeightsSHA256: strings.Repeat(fmt.Sprintf("%x", i+1), 64), MetadataSHA256: strings.Repeat(fmt.Sprintf("%x", i+5), 64)}
		model.Heads = append(model.Heads, core.MusicClassifierHead{Kind: spec.kind, Model: identity, Classes: classes})
	}
	classifier := &DiscogsClassifier{Worker: worker, Model: model, Store: store}
	ref := core.TrackRef{ID: "track", Artist: "Fixture", Title: "Tone"}
	identity := core.PreviewIdentity{PolicyVersion: core.PreviewIdentityPolicyVersion, Status: core.ResolutionResolved, Provider: "deezer", ProviderID: "preview-1"}
	pcm := DecodedPCM{SampleRate: discogsRate, Channels: 1, Samples: make([]float32, 3*discogsRate)}
	hash := strings.Repeat("e", 64)
	evidence, err := classifier.AnalyzeDecoded(context.Background(), ref, "catalog", identity, hash, pcm)
	if err != nil || len(evidence.Heads) != 4 || evidence.Coverage.CoveredSeconds != 3 {
		t.Fatalf("native classifier evidence=%+v err=%v", evidence, err)
	}
	_ = worker.Close()
	if _, err := classifier.AnalyzeDecoded(context.Background(), ref, "catalog", identity, hash, pcm); err != nil {
		t.Fatalf("verified cached scores required another inference: %v", err)
	}
}
