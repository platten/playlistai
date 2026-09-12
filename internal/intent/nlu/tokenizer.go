package nlu

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Token offsets are half-open UTF-8 byte positions in the original text.
// Special tokens have no source span. Normalization never rewrites the source.
type Token struct {
	ID      int64 `json:"id"`
	Start   int   `json:"start"`
	End     int   `json:"end"`
	Special bool  `json:"special"`
}

type Encoding struct {
	IDs           []int64 `json:"ids"`
	AttentionMask []int64 `json:"attentionMask"`
	TypeIDs       []int64 `json:"typeIds"`
	Tokens        []Token `json:"tokens"`
}

var ErrInputTooLong = errors.New("nlu: text exceeds model input limit")

// WordPiece implements the BERT normalizer, punctuation/CJK splitting and
// greedy WordPiece segmentation used by the pinned English encoder artifacts.
// These artifacts differ in lowercasing and accent stripping only. Vocabulary
// and special token IDs always come from their own vocab.txt.
type WordPiece struct {
	vocab     map[string]int64
	lowercase bool
	maxTokens int
}

func LoadWordPiece(path string, lowercase bool, maxTokens int) (*WordPiece, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if maxTokens < 3 || maxTokens > 512 {
		return nil, fmt.Errorf("nlu: invalid tokenizer capacity")
	}
	t := &WordPiece{vocab: make(map[string]int64), lowercase: lowercase, maxTokens: maxTokens}
	s := bufio.NewScanner(f)
	for s.Scan() {
		word := strings.TrimSuffix(s.Text(), "\r")
		if _, exists := t.vocab[word]; exists || word == "" {
			return nil, fmt.Errorf("nlu: invalid tokenizer vocabulary")
		}
		t.vocab[word] = int64(len(t.vocab))
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	for _, special := range []string{"[PAD]", "[UNK]", "[CLS]", "[SEP]", "[MASK]"} {
		if _, ok := t.vocab[special]; !ok {
			return nil, fmt.Errorf("nlu: tokenizer special token missing")
		}
	}
	return t, nil
}

type sourceRune struct {
	value      rune
	start, end int
}

func (t *WordPiece) Encode(text string) (Encoding, error) {
	if len(text) > 16<<10 {
		return Encoding{}, ErrInputTooLong
	}
	if !utf8.ValidString(text) {
		return Encoding{}, fmt.Errorf("nlu: invalid UTF-8 input")
	}
	out := Encoding{}
	appendToken := func(token Token) {
		out.Tokens = append(out.Tokens, token)
		out.IDs = append(out.IDs, token.ID)
		out.AttentionMask = append(out.AttentionMask, 1)
		out.TypeIDs = append(out.TypeIDs, 0)
	}
	appendToken(Token{ID: t.vocab["[CLS]"], Special: true})
	for cursor := 0; cursor < len(text); {
		// BERT's added special tokens bypass the normalizer even when they
		// occur immediately beside ordinary text. They remain source-bearing.
		matched := false
		for _, special := range []string{"[PAD]", "[UNK]", "[CLS]", "[SEP]", "[MASK]"} {
			if strings.HasPrefix(text[cursor:], special) {
				appendToken(Token{ID: t.vocab[special], Start: cursor, End: cursor + len(special)})
				cursor += len(special)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		end := len(text)
		for _, special := range []string{"[PAD]", "[UNK]", "[CLS]", "[SEP]", "[MASK]"} {
			if index := strings.Index(text[cursor:], special); index >= 0 && cursor+index < end {
				end = cursor + index
			}
		}
		for _, word := range t.basicTokens(text[cursor:end], cursor) {
			for _, token := range t.pieces(word) {
				appendToken(token)
			}
		}
		cursor = end
	}
	// Truncating an intent silently can erase an exclusion at the end. The
	// caller must fall back rather than accept a prefix as the whole request.
	if len(out.IDs)+1 > t.maxTokens {
		return Encoding{}, ErrInputTooLong
	}
	appendToken(Token{ID: t.vocab["[SEP]"], Special: true})
	return out, nil
}

func (t *WordPiece) basicTokens(text string, base int) [][]sourceRune {
	var words [][]sourceRune
	var word []sourceRune
	flush := func() {
		if len(word) > 0 {
			words = append(words, word)
			word = nil
		}
	}
	for index, r := range text {
		if r == 0 || r == utf8.RuneError || ((unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r)) && r != '\t' && r != '\n' && r != '\r') {
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || unicode.Is(unicode.Zs, r) {
			flush()
			continue
		}
		start, end := base+index, base+index+utf8.RuneLen(r)
		punctuation := isBERTPunctuation(r) || isCJK(r)
		if punctuation {
			flush()
		}
		value := string(r)
		if t.lowercase {
			// İ expands to i + combining dot in Unicode full lowercase;
			// subsequent accent stripping has the same result as ToLower.
			value = norm.NFD.String(string(unicode.ToLower(r)))
		}
		for _, normalized := range value {
			if t.lowercase && unicode.Is(unicode.Mn, normalized) {
				continue
			}
			word = append(word, sourceRune{normalized, start, end})
		}
		if punctuation {
			flush()
		}
	}
	flush()
	return words
}

func (t *WordPiece) pieces(word []sourceRune) []Token {
	unknown := func() []Token {
		return []Token{{ID: t.vocab["[UNK]"], Start: word[0].start, End: word[len(word)-1].end}}
	}
	if len(word) > 100 {
		return unknown()
	}
	var out []Token
	for start := 0; start < len(word); {
		matched := false
		for end := len(word); end > start; end-- {
			var b strings.Builder
			if start > 0 {
				b.WriteString("##")
			}
			for _, r := range word[start:end] {
				b.WriteRune(r.value)
			}
			if id, ok := t.vocab[b.String()]; ok {
				out = append(out, Token{ID: id, Start: word[start].start, End: word[end-1].end})
				start = end
				matched = true
				break
			}
		}
		if !matched {
			return unknown()
		}
	}
	return out
}

func isBERTPunctuation(r rune) bool {
	return (r >= 33 && r <= 47) || (r >= 58 && r <= 64) || (r >= 91 && r <= 96) || (r >= 123 && r <= 126) || unicode.IsPunct(r)
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) || (r >= 0x20000 && r <= 0x2A6DF) || (r >= 0x2A700 && r <= 0x2B73F) || (r >= 0x2B740 && r <= 0x2B81F) || (r >= 0x2B820 && r <= 0x2CEAF) || (r >= 0xF900 && r <= 0xFAFF) || (r >= 0x2F800 && r <= 0x2FA1F)
}
