package nlu

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestMeanPoolMasksPaddingAndNormalizes(t *testing.T) {
	got, err := MeanPool([]float32{1, 0, 99, 99, 0, 1}, []int64{1, 0, 1}, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got {
		if math.Abs(float64(v)-1/math.Sqrt(2)) > 1e-6 {
			t.Fatalf("wrong masked mean %v", got)
		}
	}
	for _, tc := range []struct {
		values []float32
		mask   []int64
		dim    int
	}{
		{[]float32{1}, []int64{1}, 2},
		{[]float32{0, 0}, []int64{1}, 2},
		{[]float32{1, 0}, []int64{0}, 2},
		{[]float32{1, 0}, []int64{2}, 2},
		{[]float32{float32(math.NaN()), 0}, []int64{1}, 2},
	} {
		if _, err := MeanPool(tc.values, tc.mask, tc.dim); err == nil {
			t.Error("invalid pooling accepted")
		}
	}
}

func writeJSON(t *testing.T, path string, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestUntrainedOrUnreviewedHeadAlwaysAbstains(t *testing.T) {
	dir := t.TempDir()
	digest := "frozen-test-model-digest"
	if head, _, reason := readHead(dir, digest); head != nil || reason != "trained_head_unavailable" {
		t.Fatal("base encoder treated as trained")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("test config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vocab.txt"), []byte("test vocab"), 0600); err != nil {
		t.Fatal(err)
	}
	configDigest, err := fileDigest(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	vocabDigest, err := fileDigest(filepath.Join(dir, "vocab.txt"))
	if err != nil {
		t.Fatal(err)
	}
	head := HeadManifest{Version: 1, ModelSHA256: digest, ConfigSHA256: configDigest, TokenizerSHA256: vocabDigest, Labels: []string{"O", "B-artist:similarity", "I-artist:similarity"}, OutputName: "logits", MaxTokens: 512}
	raw := writeJSON(t, filepath.Join(dir, "nlu-head.json"), head)
	h := sha256.Sum256(raw)
	calibration := Calibration{Version: 1, ModelSHA256: digest, ConfigSHA256: configDigest, TokenizerSHA256: vocabDigest, HeadSHA256: hex.EncodeToString(h[:]), Threshold: 0.9, ValidationExamples: 30}
	writeJSON(t, filepath.Join(dir, "calibration.json"), calibration)
	if got, _, _ := readHead(dir, digest); got != nil {
		t.Fatal("unreviewed calibration accepted")
	}
	calibration.Reviewed = true
	writeJSON(t, filepath.Join(dir, "calibration.json"), calibration)
	if got, _, reason := readHead(dir, digest); got == nil || reason != "" {
		t.Fatalf("reviewed matching head rejected: %s", reason)
	}
	if got, _, _ := readHead(dir, "changed-model"); got != nil {
		t.Fatal("calibration survived model replacement")
	}
	head.Labels[1] = "B-artist:exclude_output"
	writeJSON(t, filepath.Join(dir, "nlu-head.json"), head)
	if got, _, _ := readHead(dir, digest); got != nil {
		t.Fatal("calibration survived label/head replacement")
	}
}

func TestDecodeProposalsPreservesDistinctArtistRoles(t *testing.T) {
	text := "Aerosmith, not Aerosmith"
	encoding, err := testTokenizer(t, true, 256).Encode(text)
	if err != nil {
		t.Fatal(err)
	}
	head := HeadManifest{Labels: []string{"O", "B-artist:similarity", "I-artist:similarity", "B-artist:exclude_output", "I-artist:exclude_output"}}
	calibration := Calibration{Reviewed: true, Threshold: 0.9}
	logits := make([]float32, len(encoding.Tokens)*len(head.Labels))
	for i := range encoding.Tokens {
		logits[i*5] = 10
	}
	logits[5], logits[6] = 0, 10
	logits[20], logits[23] = 0, 10
	got, err := DecodeProposals(text, encoding, logits, head, calibration)
	if err != nil {
		t.Fatal(err)
	}
	if got.Abstained || len(got.Proposals) != 2 {
		t.Fatalf("missing proposals: %+v", got)
	}
	if got.Proposals[0].Text != "Aerosmith" || got.Proposals[0].Start != 0 || got.Proposals[0].Label != "artist:similarity" || got.Proposals[1].Start != 15 || got.Proposals[1].Label != "artist:exclude_output" {
		t.Fatalf("roles or occurrences collapsed: %+v", got)
	}
	// A model confidence is only a proposal. Unreviewed calibration cannot
	// activate a head, even when every emitted logit is very confident.
	calibration.Reviewed = false
	if _, err := DecodeProposals(text, encoding, logits, head, calibration); err == nil {
		t.Fatal("unreviewed proposals accepted")
	}
}

func TestDecodeRejectsOrphansLowConfidenceAndNonfiniteLogits(t *testing.T) {
	encoding, err := testTokenizer(t, true, 256).Encode("Aerosmith")
	if err != nil {
		t.Fatal(err)
	}
	head := HeadManifest{Labels: []string{"O", "B-artist", "I-artist"}}
	calibration := Calibration{Reviewed: true, Threshold: 0.9}
	logits := make([]float32, len(encoding.Tokens)*3)
	got, err := DecodeProposals("Aerosmith", encoding, logits, head, calibration)
	if err != nil || !got.Abstained {
		t.Fatal("ambiguous scores became an entity")
	}
	logits[5] = 10
	got, err = DecodeProposals("Aerosmith", encoding, logits, head, calibration)
	if err != nil || !got.Abstained {
		t.Fatal("orphan continuation became an entity")
	}
	logits[3] = float32(math.Inf(1))
	if _, err = DecodeProposals("Aerosmith", encoding, logits, head, calibration); err == nil {
		t.Fatal("nonfinite logits accepted")
	}
}

func TestDecodeAbstainsForPartlyConfidentWordPieceMention(t *testing.T) {
	text := "Löffler"
	encoding, err := testTokenizer(t, true, 256).Encode(text)
	if err != nil {
		t.Fatal(err)
	}
	head := HeadManifest{Labels: []string{"O", "B-artist", "I-artist"}}
	calibration := Calibration{Reviewed: true, Threshold: 0.9}
	logits := make([]float32, len(encoding.Tokens)*3)
	logits[4] = 10  // confident "Lo"
	logits[8] = 1   // uncertain continuation "ff"
	logits[11] = 10 // confident orphan "ler"
	got, err := DecodeProposals(text, encoding, logits, head, calibration)
	if err != nil || !got.Abstained {
		t.Fatalf("partial artist accepted: %+v, %v", got, err)
	}
	logits[8] = 10
	got, err = DecodeProposals(text, encoding, logits, head, calibration)
	if err != nil || got.Abstained || len(got.Proposals) != 1 || got.Proposals[0].Text != text {
		t.Fatalf("complete confident artist rejected: %+v, %v", got, err)
	}
}
