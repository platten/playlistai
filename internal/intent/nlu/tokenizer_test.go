package nlu

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testTokenizer(t *testing.T, lowercase bool, maxTokens int) *WordPiece {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vocab.txt")
	words := []string{"[PAD]", "[UNK]", "[CLS]", "[SEP]", "[MASK]", "aerosmith", "Aerosmith", "lo", "##ff", "##ler", "L", "##ö", "quiet", "music", "not", ",", "!", "中", "文", "i", "ab", "hurt", "by", "nine", "inch", "nails", "é", "e", "##s"}
	if err := os.WriteFile(path, []byte(strings.Join(words, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tokenizer, err := LoadWordPiece(path, lowercase, maxTokens)
	if err != nil {
		t.Fatal(err)
	}
	return tokenizer
}

func TestWordPieceKeepsOriginalOccurrencesAndUTF8Offsets(t *testing.T) {
	uncased := testTokenizer(t, true, 256)
	for _, tc := range []struct {
		text  string
		ids   []int64
		spans [][2]int
	}{
		{"Aerosmith, not Aerosmith!", []int64{2, 5, 15, 14, 5, 16, 3}, [][2]int{{0, 0}, {0, 9}, {9, 10}, {11, 14}, {15, 24}, {24, 25}, {0, 0}}},
		{"Löffler", []int64{2, 7, 8, 9, 3}, [][2]int{{0, 0}, {0, 3}, {3, 5}, {5, 8}, {0, 0}}},
		{"Lo\u0308ffler", []int64{2, 7, 8, 9, 3}, [][2]int{{0, 0}, {0, 2}, {4, 6}, {6, 9}, {0, 0}}},
		{"中\u00a0文", []int64{2, 17, 18, 3}, [][2]int{{0, 0}, {0, 3}, {5, 8}, {0, 0}}},
		{"🎸 Aerosmith", []int64{2, 1, 5, 3}, [][2]int{{0, 0}, {0, 4}, {5, 14}, {0, 0}}},
		{"a\u200db", []int64{2, 20, 3}, [][2]int{{0, 0}, {0, 5}, {0, 0}}},
		{"İ", []int64{2, 19, 3}, [][2]int{{0, 0}, {0, 2}, {0, 0}}},
		{"é", []int64{2, 27, 3}, [][2]int{{0, 0}, {0, 2}, {0, 0}}},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got, err := uncased.Encode(tc.text)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.IDs, tc.ids) {
				t.Fatalf("IDs %v, want %v", got.IDs, tc.ids)
			}
			for i, token := range got.Tokens {
				if [2]int{token.Start, token.End} != tc.spans[i] {
					t.Errorf("token %d span %d:%d, want %v", i, token.Start, token.End, tc.spans[i])
				}
				if got.AttentionMask[i] != 1 || got.TypeIDs[i] != 0 {
					t.Error("unexpected mask or segment")
				}
			}
		})
	}
	cased, err := testTokenizer(t, false, 512).Encode("Aerosmith é")
	if err != nil || !reflect.DeepEqual(cased.IDs, []int64{2, 6, 26, 3}) {
		t.Fatalf("cased normalization: %v, %v", cased.IDs, err)
	}
}

func TestWordPieceSpecialsUnknownAndNoSilentTruncation(t *testing.T) {
	tokenizer := testTokenizer(t, true, 256)
	got, err := tokenizer.Encode("quiet[MASK]music")
	if err != nil || !reflect.DeepEqual(got.IDs, []int64{2, 12, 4, 13, 3}) {
		t.Fatalf("added special handling: %v, %v", got.IDs, err)
	}
	if got.Tokens[2].Start != 5 || got.Tokens[2].End != 11 || got.Tokens[2].Special {
		t.Fatal("source special token lost its occurrence")
	}
	got, err = tokenizer.Encode("quietxyz")
	if err != nil || !reflect.DeepEqual(got.IDs, []int64{2, 1, 3}) {
		t.Fatal("a partially segmentable word must be wholly unknown")
	}
	if _, err = tokenizer.Encode(strings.Repeat("quiet ", 255)); !errors.Is(err, ErrInputTooLong) {
		t.Fatalf("overlong text silently truncated: %v", err)
	}
	if _, err = tokenizer.Encode(string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	got, err = tokenizer.Encode(strings.Repeat("a", 101))
	if err != nil || !reflect.DeepEqual(got.IDs, []int64{2, 1, 3}) {
		t.Fatal("overlong word must map to one unknown token")
	}
}

func TestWordPieceRejectsMalformedVocabulary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vocab.txt")
	for _, raw := range []string{"hello\n", "[UNK]\n[UNK]\n", "\n"} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadWordPiece(path, false, 512); err == nil {
			t.Fatal("malformed vocabulary accepted")
		}
	}
}
