package audio

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// RobertaTokenizer implements the byte-level BPE used by music CLAP's text
// branch. Bundles supply the exact vocabulary and merges, with parity fixtures.
type RobertaTokenizer struct {
	vocabulary map[string]int64
	merges     map[string]int
	bytes      [256]rune
}

func LoadTokenizer(vocabularyPath, mergesPath string) (*RobertaTokenizer, error) {
	raw, err := os.ReadFile(vocabularyPath)
	if err != nil {
		return nil, err
	}
	t := &RobertaTokenizer{merges: map[string]int{}}
	if err := json.Unmarshal(raw, &t.vocabulary); err != nil {
		return nil, err
	}
	for _, special := range []string{"<s>", "</s>", "<pad>", "<unk>"} {
		if _, ok := t.vocabulary[special]; !ok {
			return nil, fmt.Errorf("audio: incompatible tokenizer")
		}
	}
	file, err := os.Open(mergesPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	rank := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pair := strings.Fields(line)
		if len(pair) != 2 {
			return nil, fmt.Errorf("audio: invalid tokenizer merge")
		}
		t.merges[pair[0]+"\x00"+pair[1]] = rank
		rank++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	next := rune(256)
	for i := range t.bytes {
		if i >= 33 && i <= 126 || i >= 161 && i <= 172 || i >= 174 && i <= 255 {
			t.bytes[i] = rune(i)
		} else {
			t.bytes[i] = next
			next++
		}
	}
	return t, nil
}

func (t *RobertaTokenizer) Encode(text string) ([]int64, []int64, error) {
	ids := []int64{t.vocabulary["<s>"]}
	for _, piece := range preTokens(text) {
		parts := make([]string, len(piece))
		for i, b := range []byte(piece) {
			parts[i] = string(t.bytes[b])
		}
		for len(parts) > 1 {
			best, index := int(^uint(0)>>1), -1
			for i := 0; i < len(parts)-1; i++ {
				if rank, ok := t.merges[parts[i]+"\x00"+parts[i+1]]; ok && rank < best {
					best, index = rank, i
				}
			}
			if index < 0 {
				break
			}
			left, right := parts[index], parts[index+1]
			next := make([]string, 0, len(parts))
			for i := 0; i < len(parts); i++ {
				if i+1 < len(parts) && parts[i] == left && parts[i+1] == right {
					next = append(next, left+right)
					i++
				} else {
					next = append(next, parts[i])
				}
			}
			parts = next
		}
		for _, part := range parts {
			id, ok := t.vocabulary[part]
			if !ok {
				return nil, nil, fmt.Errorf("audio: tokenizer has missing vocabulary")
			}
			ids = append(ids, id)
		}
	}
	// Silently truncating an essential clause would change the request.
	if len(ids) > 76 {
		return nil, nil, fmt.Errorf("audio: clause exceeds 77-token context; refine into shorter clauses")
	}
	ids = append(ids, t.vocabulary["</s>"])
	mask := make([]int64, 77)
	for i := range ids {
		mask[i] = 1
	}
	for len(ids) < 77 {
		ids = append(ids, t.vocabulary["<pad>"])
	}
	return ids, mask, nil
}

func preTokens(text string) []string {
	r := []rune(text)
	var out []string
	class := func(c rune) int {
		if unicode.IsLetter(c) {
			return 1
		}
		if unicode.IsNumber(c) {
			return 2
		}
		if unicode.IsSpace(c) {
			return 3
		}
		return 4
	}
	for i := 0; i < len(r); {
		start := i
		contraction := ""
		for _, suffix := range []string{"'s", "'t", "'re", "'ve", "'m", "'ll", "'d"} {
			if strings.HasPrefix(string(r[i:]), suffix) {
				contraction = suffix
				break
			}
		}
		if contraction != "" {
			i += len([]rune(contraction))
			out = append(out, contraction)
			continue
		}
		if r[i] == ' ' && i+1 < len(r) && class(r[i+1]) != 3 {
			i++
		}
		kind := class(r[i])
		i++
		for i < len(r) && class(r[i]) == kind {
			i++
		}
		if kind == 3 && i < len(r) && i-start > 1 {
			i--
		}
		out = append(out, string(r[start:i]))
	}
	return out
}
