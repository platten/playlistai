package audio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func tokenizerFiles(t *testing.T, vocabulary any, merges string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	vocabPath, mergesPath := filepath.Join(dir, "vocab.json"), filepath.Join(dir, "merges.txt")
	raw, err := json.Marshal(vocabulary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vocabPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mergesPath, []byte(merges), 0o600); err != nil {
		t.Fatal(err)
	}
	return vocabPath, mergesPath
}

func tinyVocabulary() map[string]int64 {
	return map[string]int64{"<s>": 0, "<pad>": 1, "</s>": 2, "<unk>": 3, "hello": 10, "Ġhello": 11, "Ã©": 12, "a": 13, "ĉ": 14}
}

func TestTokenizerBPERanksUTF8PaddingAndContextBoundary(t *testing.T) {
	vocab, merges := tokenizerFiles(t, tinyVocabulary(), "# fixture byte-level merges\n\nh e\nhe l\nhel l\nhell o\nĠ hello\nÃ ©\n")
	tokenizer, err := LoadTokenizer(vocab, merges)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		text   string
		prefix []int64
	}{
		{"hello hello", []int64{0, 10, 11, 2}},
		{"é", []int64{0, 12, 2}},
		{"\t", []int64{0, 14, 2}},
		{"", []int64{0, 2}},
	} {
		ids, mask, err := tokenizer.Encode(tc.text)
		if err != nil || len(ids) != 77 || len(mask) != 77 || !reflect.DeepEqual(ids[:len(tc.prefix)], tc.prefix) {
			t.Fatalf("encode %q: ids=%v mask=%v err=%v", tc.text, ids, mask, err)
		}
		for index := range ids {
			if index < len(tc.prefix) && mask[index] != 1 || index >= len(tc.prefix) && (mask[index] != 0 || ids[index] != 1) {
				t.Fatalf("padding/attention mismatch for %q at %d", tc.text, index)
			}
		}
	}
	ids, mask, err := tokenizer.Encode(strings.Repeat("a", 75))
	if err != nil || len(ids) != 77 || ids[76] != 2 || mask[76] != 1 {
		t.Fatalf("maximum token context rejected: ids=%v err=%v", ids, err)
	}
	for _, invalid := range []string{strings.Repeat("a", 76), "missing-vocabulary"} {
		if ids, mask, err := tokenizer.Encode(invalid); err == nil || ids != nil || mask != nil {
			t.Fatal("oversized/incomplete clause was silently truncated", invalid)
		}
	}
}

func TestTokenizerLoadingRejectsMissingMalformedAndIncompleteArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		vocab  any
		merges string
	}{
		{"missing-specials", map[string]int64{"a": 1}, ""},
		{"wrong-json-shape", []string{"a"}, ""},
		{"malformed-merge", tinyVocabulary(), "one two three"},
		{"oversized-merge", tinyVocabulary(), strings.Repeat("a", 70*1024)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vocab, merges := tokenizerFiles(t, tc.vocab, tc.merges)
			if _, err := LoadTokenizer(vocab, merges); err == nil {
				t.Fatal("incompatible tokenizer loaded")
			}
		})
	}
	if _, err := LoadTokenizer(filepath.Join(t.TempDir(), "missing.json"), "missing.txt"); err == nil {
		t.Fatal("missing vocabulary accepted")
	}
	vocab, _ := tokenizerFiles(t, tinyVocabulary(), "")
	if _, err := LoadTokenizer(vocab, filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Fatal("missing merges accepted")
	}
}
