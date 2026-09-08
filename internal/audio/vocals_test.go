package audio

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

type vocalEncoder struct {
	testAnalyzer
	textCalls   int
	textFailure bool
}

func (a *vocalEncoder) EmbedText(ctx context.Context, text string) ([]float32, error) {
	a.textCalls++
	if a.textFailure {
		return nil, errors.New("text unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, p := range instrumentalPrompts {
		if text == p {
			return []float32{1, 0}, nil
		}
	}
	for _, p := range vocalPrompts {
		if text == p {
			return []float32{0, 1}, nil
		}
	}
	return []float32{-1, 0}, nil
}

func TestVocalScreeningRequiresEverySegmentAndReusesFeatures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		segments [][]float32
		state    core.EvidenceState
	}{
		{"instrumental", [][]float32{{1, 0}, {1, 0}}, core.EvidenceMatch},
		{"later singing", [][]float32{{1, 0}, {0, 1}}, core.EvidenceMismatch},
		{"uncertain", [][]float32{{0.70710677, 0.70710677}}, core.EvidenceUnknown},
		{"weak instrumental lead", [][]float32{{0.71414284, 0.7}}, core.EvidenceUnknown},
		{"non musical", [][]float32{{-1, 0}}, core.EvidenceUnknown},
		{"missing", nil, core.EvidenceUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, a, resolver, _ := testService(t)
			encoder := &vocalEncoder{testAnalyzer: *a}
			service.Analyzer = encoder
			service.Policy = Policy{} // the recommended parity-only bundle is sufficient
			intent := core.MusicIntent{HardConstraints: []core.HardConstraint{{Kind: "exclude_vocals"}}}
			strictIntent := audioIntent()
			strictIntent.VerificationPolicy = core.VerifiedOnly
			if service.Ready() || !service.ReadyFor(intent) || service.ReadyFor(strictIntent) {
				t.Fatal("uncalibrated model enabled strict musical-fit decisions")
			}
			track := core.TrackRef{ID: "checked", Artist: "Fixture", Title: "Recording"}
			record := core.AudioAnalysis{ID: tc.name, CatalogVersion: "catalog", TrackID: track.ID, TrackKey: core.ProvisionalRecordingKey(track), Model: encoder.Identity(), AudioSHA256: strings.Repeat("0", 64), Identity: core.PreviewIdentity{Status: core.ResolutionResolved, Provider: "deezer", ProviderID: track.ID}}
			for i, v := range tc.segments {
				record.Segments = append(record.Segments, core.AudioSegment{StartSeconds: float64(i * 10), EndSeconds: float64((i + 1) * 10), Embedding: v})
			}
			// Empty records are deliberately checked directly; the store correctly rejects them.
			if len(tc.segments) == 0 {
				session, _ := service.Begin(context.Background(), intent, "catalog", nil)
				defer session.Close()
				state, _ := session.instrumentalEvidence(record)
				if state != tc.state {
					t.Fatal(state)
				}
				return
			}
			record.ID = ""
			record.ID = Fingerprint(record)
			if err := service.Store.Put(context.Background(), record); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				session, err := service.Begin(context.Background(), intent, "catalog", nil)
				if err != nil {
					t.Fatal(err)
				}
				got, err := session.Check(context.Background(), track, false)
				if err != nil || got.Eligible != (tc.state == core.EvidenceMatch) || len(got.Clauses) != 1 || got.Clauses[0].State != tc.state || got.PolicyVersion != SimilarityPolicyVersion+"+"+VocalPolicyVersion {
					t.Fatalf("%+v %v", got, err)
				}
				_, _ = session.Check(context.Background(), track, false)
				if session.Snapshot().CacheHits != 1 {
					t.Fatal("features were not reused")
				}
				session.Close()
			}
			if resolver.calls != 0 || encoder.textCalls != 2*(len(instrumentalPrompts)+len(vocalPrompts)+len(otherPrompts)) {
				t.Fatal("unexpected audio retrieval or repeated text encoding")
			}
		})
	}
}

func TestVocalScreeningAbstainsOnInvalidIdentityVectorsAndTextFailure(t *testing.T) {
	service, a, _, _ := testService(t)
	encoder := &vocalEncoder{testAnalyzer: *a}
	service.Analyzer = encoder
	session, _ := service.Begin(context.Background(), core.MusicIntent{HardConstraints: []core.HardConstraint{{Kind: "require_instrumental"}}}, "catalog", nil)
	defer session.Close()
	record := core.AudioAnalysis{Model: encoder.Identity(), Identity: core.PreviewIdentity{Status: core.ResolutionAmbiguous}, Segments: []core.AudioSegment{{EndSeconds: 10, Embedding: []float32{1, 0}}}}
	if state, _ := session.instrumentalEvidence(record); state != core.EvidenceUnknown {
		t.Fatal("ambiguous identity passed")
	}
	record.Identity.Status = core.ResolutionResolved
	record.Segments[0].Embedding = []float32{1}
	if state, _ := session.instrumentalEvidence(record); state != core.EvidenceUnknown {
		t.Fatal("invalid vector passed")
	}
	clear(session.queries)
	encoder.textFailure = true
	record.Segments[0].Embedding = []float32{1, 0}
	if state, _ := session.instrumentalEvidence(record); state != core.EvidenceUnknown {
		t.Fatal("failed text inference passed")
	}
}
