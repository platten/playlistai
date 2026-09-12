package nlu

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ModelKind string

const (
	MiniLM             ModelKind = "minilm"
	DistilBERT         ModelKind = "distilbert"
	EmbeddingDimension           = 384
)

type WorkerConfig struct {
	Kind           ModelKind
	ModelDir       string
	RuntimeLibrary string
}

// HeadManifest describes an exported, task-adapted token head. The upstream
// DistilBERT encoder is deliberately not accepted as a trained intent model.
type HeadManifest struct {
	Version         int      `json:"version"`
	ModelSHA256     string   `json:"modelSHA256"`
	TokenizerSHA256 string   `json:"tokenizerSHA256"`
	ConfigSHA256    string   `json:"configSHA256"`
	Labels          []string `json:"labels"`
	OutputName      string   `json:"outputName"`
	MaxTokens       int      `json:"maxTokens"`
}

type Calibration struct {
	Version            int     `json:"version"`
	ModelSHA256        string  `json:"modelSHA256"`
	TokenizerSHA256    string  `json:"tokenizerSHA256"`
	ConfigSHA256       string  `json:"configSHA256"`
	HeadSHA256         string  `json:"headSHA256"`
	Reviewed           bool    `json:"reviewed"`
	Threshold          float64 `json:"threshold"`
	ValidationExamples int     `json:"validationExamples"`
}

// Proposal is a source-backed hypothesis, never an executable constraint or
// evidence of a track's musical properties. Labels remain untrusted inputs to
// the separate intent compiler, including labels that mention output roles.
type Proposal struct {
	Label string  `json:"label"`
	Text  string  `json:"text"`
	Start int     `json:"start"`
	End   int     `json:"end"`
	Score float64 `json:"score"`
}

type Result struct {
	Proposals []Proposal `json:"proposals,omitempty"`
	Abstained bool       `json:"abstained"`
	Reason    string     `json:"reason,omitempty"`
}

type modelSettings struct {
	tokenizer   *WordPiece
	head        *HeadManifest
	calibration *Calibration
	digest      string
	abstention  string
}

func readSettings(config WorkerConfig) (modelSettings, error) {
	var settings modelSettings
	if !filepath.IsAbs(config.ModelDir) || !filepath.IsAbs(config.RuntimeLibrary) {
		return settings, fmt.Errorf("nlu: native paths must be absolute")
	}
	return readModelSettings(config.ModelDir, config.Kind)
}

func readModelSettings(dir string, kind ModelKind) (modelSettings, error) {
	var settings modelSettings
	if kind != MiniLM && kind != DistilBERT {
		return settings, fmt.Errorf("nlu: unsupported model kind")
	}
	var architecture struct {
		ModelType  string `json:"model_type"`
		HiddenSize int    `json:"hidden_size"`
		Dim        int    `json:"dim"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return settings, err
	}
	if err = json.Unmarshal(raw, &architecture); err != nil {
		return settings, err
	}
	if kind == MiniLM && (architecture.ModelType != "bert" || architecture.HiddenSize != EmbeddingDimension) {
		return settings, fmt.Errorf("nlu: incompatible MiniLM architecture")
	}
	if kind == DistilBERT && (architecture.ModelType != "distilbert" || architecture.Dim != 768) {
		return settings, fmt.Errorf("nlu: incompatible DistilBERT architecture")
	}
	settings.digest, err = fileDigest(filepath.Join(dir, "model.onnx"))
	if err != nil {
		return settings, err
	}
	maxTokens := 256
	if kind == DistilBERT {
		maxTokens = 512
		settings.head, settings.calibration, settings.abstention = readHead(dir, settings.digest)
		if settings.head != nil {
			maxTokens = settings.head.MaxTokens
		}
	}
	settings.tokenizer, err = LoadWordPiece(filepath.Join(dir, "vocab.txt"), kind == MiniLM, maxTokens)
	return settings, err
}

func readHead(dir, modelDigest string) (*HeadManifest, *Calibration, string) {
	raw, err := os.ReadFile(filepath.Join(dir, "nlu-head.json"))
	if err != nil {
		return nil, nil, "trained_head_unavailable"
	}
	var head HeadManifest
	if json.Unmarshal(raw, &head) != nil || head.Version != 1 || head.ModelSHA256 != modelDigest || head.OutputName != "logits" || head.MaxTokens < 3 || head.MaxTokens > 512 || len(head.Labels) < 2 || len(head.Labels) > 256 {
		return nil, nil, "trained_head_incompatible"
	}
	vocabularyDigest, err := fileDigest(filepath.Join(dir, "vocab.txt"))
	if err != nil || vocabularyDigest != head.TokenizerSHA256 {
		return nil, nil, "trained_tokenizer_incompatible"
	}
	configDigest, err := fileDigest(filepath.Join(dir, "config.json"))
	if err != nil || configDigest != head.ConfigSHA256 {
		return nil, nil, "trained_config_incompatible"
	}
	seen := map[string]bool{}
	for _, label := range head.Labels {
		if seen[label] || (label != "O" && (!strings.HasPrefix(label, "B-") && !strings.HasPrefix(label, "I-"))) || len(label) > 100 || (label != "O" && len(label) < 3) {
			return nil, nil, "trained_head_incompatible"
		}
		seen[label] = true
	}
	if !seen["O"] {
		return nil, nil, "trained_head_incompatible"
	}
	for label := range seen {
		if strings.HasPrefix(label, "I-") && !seen["B-"+label[2:]] {
			return nil, nil, "trained_head_incompatible"
		}
	}
	headDigest := sha256.Sum256(raw)
	raw, err = os.ReadFile(filepath.Join(dir, "calibration.json"))
	if err != nil {
		return nil, nil, "calibration_unavailable"
	}
	var calibration Calibration
	if json.Unmarshal(raw, &calibration) != nil || calibration.Version != 1 || !calibration.Reviewed || calibration.ValidationExamples < 1 || calibration.Threshold < 0.5 || calibration.Threshold >= 1 || calibration.ModelSHA256 != modelDigest || calibration.HeadSHA256 != hex.EncodeToString(headDigest[:]) || calibration.TokenizerSHA256 != vocabularyDigest || calibration.ConfigSHA256 != configDigest {
		return nil, nil, "calibration_unreviewed_or_incompatible"
	}
	return &head, &calibration, ""
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// MeanPool normalizes a masked mean of token states, including CLS and SEP as
// the pinned sentence-transformer does. Masked padding never contributes.
func MeanPool(hidden []float32, mask []int64, dimension int) ([]float32, error) {
	if dimension <= 0 || len(mask) == 0 || len(hidden) != len(mask)*dimension {
		return nil, fmt.Errorf("nlu: invalid pooling shape")
	}
	out := make([]float32, dimension)
	sums := make([]float64, dimension)
	count := 0
	for token, included := range mask {
		if included != 0 && included != 1 {
			return nil, fmt.Errorf("nlu: invalid attention mask")
		}
		if included == 0 {
			continue
		}
		count++
		for d, value := range hidden[token*dimension : (token+1)*dimension] {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return nil, fmt.Errorf("nlu: nonfinite embedding")
			}
			sums[d] += float64(value)
		}
	}
	if count == 0 {
		return nil, fmt.Errorf("nlu: empty embedding")
	}
	var length float64
	for d := range sums {
		sums[d] /= float64(count)
		length += sums[d] * sums[d]
	}
	if length <= 1e-24 || math.IsInf(length, 0) {
		return nil, fmt.Errorf("nlu: invalid embedding norm")
	}
	length = math.Sqrt(length)
	for d := range sums {
		out[d] = float32(sums[d] / length)
	}
	return out, nil
}

func DecodeProposals(text string, encoding Encoding, logits []float32, head HeadManifest, calibration Calibration) (Result, error) {
	classes := len(head.Labels)
	if classes < 2 || len(logits) != len(encoding.Tokens)*classes || !calibration.Reviewed || calibration.Threshold < 0.5 || calibration.Threshold >= 1 {
		return Result{}, fmt.Errorf("nlu: invalid proposal contract")
	}
	var result Result
	var active *Proposal
	flush := func() {
		if active != nil {
			// Never promote a confidently tagged prefix/suffix of a word as a
			// complete artist or title when its other subwords were uncertain.
			if !insideWord(text, active.Start) && !insideWord(text, active.End) {
				active.Text = text[active.Start:active.End]
				result.Proposals = append(result.Proposals, *active)
			}
			active = nil
		}
	}
	for index, token := range encoding.Tokens {
		if token.Special {
			flush()
			continue
		}
		if token.Start < 0 || token.End <= token.Start || token.End > len(text) || !utf8.ValidString(text[:token.Start]) || !utf8.ValidString(text[:token.End]) {
			return Result{}, fmt.Errorf("nlu: invalid source span")
		}
		values := logits[index*classes : (index+1)*classes]
		best := 0
		for j, value := range values {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return Result{}, fmt.Errorf("nlu: nonfinite logits")
			}
			if value > values[best] {
				best = j
			}
		}
		var sum float64
		for _, value := range values {
			sum += math.Exp(float64(value) - float64(values[best]))
		}
		score, label := 1/sum, head.Labels[best]
		if score < calibration.Threshold {
			if active != nil && label == "I-"+active.Label {
				active = nil // The entire proposed mention lacked confidence.
			}
			flush()
			continue
		}
		if label == "O" {
			flush()
			continue
		}
		if len(label) < 3 || (label[:2] != "B-" && label[:2] != "I-") {
			return Result{}, fmt.Errorf("nlu: invalid token label")
		}
		if strings.HasPrefix(label, "B-") {
			flush()
			active = &Proposal{Label: label[2:], Start: token.Start, End: token.End, Score: score}
		} else if active != nil && active.Label == label[2:] && token.Start >= active.End {
			active.End = token.End
			active.Score = math.Min(active.Score, score)
		} else {
			flush()
		} // An orphan I-tag is not evidence of a complete mention.
	}
	flush()
	if len(result.Proposals) == 0 {
		result.Abstained = true
		result.Reason = "no_confident_proposals"
	}
	return result, nil
}

func insideWord(text string, index int) bool {
	if index <= 0 || index >= len(text) {
		return false
	}
	left, _ := utf8.DecodeLastRuneInString(text[:index])
	right, _ := utf8.DecodeRuneInString(text[index:])
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r) }
	return word(left) && word(right)
}
