// enhancedeval compares frozen, derived-only embedding cohorts offline. It does
// not train models, fetch audio, or establish musical suitability.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
)

const maxInputBytes = 64 << 20

type cohortTrack struct {
	ID                  string    `json:"id"`
	RecordingID         string    `json:"recordingId"`
	CatalogAudio        []float64 `json:"catalogAudio,omitempty"`
	CatalogCooccurrence []float64 `json:"catalogCooccurrence,omitempty"`
	CLAP                []float64 `json:"clap,omitempty"`
	MERT                []float64 `json:"mert,omitempty"`
}
type adjacency struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type cohort struct {
	Version          int               `json:"version"`
	Name             string            `json:"name"`
	Provenance       string            `json:"provenance"`
	Spaces           map[string]string `json:"spaces"`
	Tracks           []cohortTrack     `json:"tracks"`
	HeldOutAdjacency []adjacency       `json:"heldOutAdjacency,omitempty"`
}
type evalPolicy struct {
	Version                   string  `json:"version"`
	CatalogAudioWeight        float64 `json:"catalogAudioWeight"`
	CatalogCooccurrenceWeight float64 `json:"catalogCooccurrenceWeight"`
	HybridCatalogWeight       float64 `json:"hybridCatalogWeight"`
	HybridMERTWeight          float64 `json:"hybridMertWeight"`
	CandidatePolicy           string  `json:"candidatePolicy"`
}

var policy = evalPolicy{"enhancedeval/v1", .5, .5, .85, .15, "same complete-case candidate pool for every method; exclude query recording; ties by track ID"}

type neighbor struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}
type queryResult struct {
	ID        string                `json:"id"`
	Neighbors map[string][]neighbor `json:"neighbors"`
}
type metrics struct {
	Pairs              int     `json:"pairs"`
	MeanReciprocalRank float64 `json:"meanReciprocalRank"`
	RecallAt1          float64 `json:"recallAt1"`
	RecallAt5          float64 `json:"recallAt5"`
	MeanRank           float64 `json:"meanRank"`
}
type evaluation struct {
	Version          string             `json:"version"`
	Cohort           string             `json:"cohort"`
	Provenance       string             `json:"provenance"`
	InputSHA256      string             `json:"inputSha256"`
	PolicySHA256     string             `json:"policySha256"`
	Policy           evalPolicy         `json:"policy"`
	Spaces           map[string]string  `json:"spaces"`
	TotalTracks      int                `json:"totalTracks"`
	ComparableTracks int                `json:"comparableTracks"`
	ExcludedTracks   []string           `json:"excludedTracks"`
	HeldOutPairs     int                `json:"heldOutPairs"`
	EvaluatedPairs   int                `json:"evaluatedPairs"`
	SkippedPairs     int                `json:"skippedPairs"`
	Metrics          map[string]metrics `json:"metrics,omitempty"`
	Queries          []queryResult      `json:"queries"`
	Limitations      []string           `json:"limitations"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("enhancedeval", flag.ContinueOnError)
	input := flags.String("input", "", "derived-only cohort JSON")
	top := flags.Int("top", 5, "neighbors per method (1..20)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *input == "" || flags.NArg() != 0 {
		return errors.New("usage: enhancedeval -input cohort.json [-top 5]")
	}
	if *top < 1 || *top > 20 {
		return errors.New("top must be between 1 and 20")
	}
	f, err := os.Open(*input)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		return err
	}
	if len(b) > maxInputBytes {
		return errors.New("input exceeds 64 MiB")
	}
	report, err := evaluate(b, *top)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func evaluate(b []byte, top int) (evaluation, error) {
	var data cohort
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return evaluation{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return evaluation{}, errors.New("input must contain exactly one JSON object")
	}
	if err := validate(data); err != nil {
		return evaluation{}, err
	}
	p, _ := json.Marshal(policy)
	report := evaluation{Version: "enhancedeval-report/v1", Cohort: data.Name, Provenance: data.Provenance, InputSHA256: digest(b), PolicySHA256: digest(p), Policy: policy, Spaces: data.Spaces, TotalTracks: len(data.Tracks), HeldOutPairs: len(data.HeldOutAdjacency), ExcludedTracks: []string{}, Queries: []queryResult{}, Limitations: []string{
		"Offline, fixed-cohort comparison; no training, network access or audio acquisition.",
		"Cosines and adjacency retrieval are not probabilities or musical suitability judgments.",
		"Only complete cases are compared; excluded and skipped counts describe coverage bias.",
		"Hybrid is a frozen transition proxy (.85 catalog + .15 MERT), not full production ranking with DSP, intent, taste, eligibility or sequencing constraints.",
		"Held-out adjacency must be independent of model and weight selection; small cohorts and near-duplicate leakage cannot establish superiority.",
	}}
	var comparable []cohortTrack
	for _, track := range data.Tracks {
		if len(track.CatalogAudio) == 0 || len(track.CatalogCooccurrence) == 0 || len(track.CLAP) == 0 || len(track.MERT) == 0 {
			report.ExcludedTracks = append(report.ExcludedTracks, track.ID)
		} else {
			comparable = append(comparable, track)
		}
	}
	sort.Slice(comparable, func(i, j int) bool { return comparable[i].ID < comparable[j].ID })
	sort.Strings(report.ExcludedTracks)
	report.ComparableTracks = len(comparable)
	methods := []string{"catalog", "clap", "mert", "hybrid"}
	ranks := map[string]map[string]map[string]int{}
	for _, query := range comparable {
		result := queryResult{ID: query.ID, Neighbors: map[string][]neighbor{}}
		ranks[query.ID] = map[string]map[string]int{}
		all := map[string][]neighbor{}
		for _, candidate := range comparable {
			if query.RecordingID == candidate.RecordingID {
				continue
			}
			catalog := policy.CatalogAudioWeight*cosine(query.CatalogAudio, candidate.CatalogAudio) + policy.CatalogCooccurrenceWeight*cosine(query.CatalogCooccurrence, candidate.CatalogCooccurrence)
			mert := cosine(query.MERT, candidate.MERT)
			scores := map[string]float64{"catalog": catalog, "clap": cosine(query.CLAP, candidate.CLAP), "mert": mert, "hybrid": policy.HybridCatalogWeight*catalog + policy.HybridMERTWeight*mert}
			for _, method := range methods {
				all[method] = append(all[method], neighbor{candidate.ID, scores[method]})
			}
		}
		for _, method := range methods {
			list := all[method]
			sort.Slice(list, func(i, j int) bool {
				if list[i].Score != list[j].Score {
					return list[i].Score > list[j].Score
				}
				return list[i].ID < list[j].ID
			})
			ranks[query.ID][method] = map[string]int{}
			for index, n := range list {
				ranks[query.ID][method][n.ID] = index + 1
			}
			result.Neighbors[method] = append([]neighbor{}, list[:min(top, len(list))]...)
		}
		report.Queries = append(report.Queries, result)
	}
	if len(data.HeldOutAdjacency) > 0 {
		report.Metrics = map[string]metrics{}
	}
	for _, pair := range data.HeldOutAdjacency {
		if ranks[pair.From]["catalog"][pair.To] == 0 {
			report.SkippedPairs++
			continue
		}
		report.EvaluatedPairs++
		for _, method := range methods {
			rank := ranks[pair.From][method][pair.To]
			m := report.Metrics[method]
			m.Pairs++
			m.MeanReciprocalRank += 1 / float64(rank)
			m.MeanRank += float64(rank)
			if rank == 1 {
				m.RecallAt1++
			}
			if rank <= 5 {
				m.RecallAt5++
			}
			report.Metrics[method] = m
		}
	}
	for method, m := range report.Metrics {
		n := float64(m.Pairs)
		m.MeanReciprocalRank /= n
		m.MeanRank /= n
		m.RecallAt1 /= n
		m.RecallAt5 /= n
		report.Metrics[method] = m
	}
	return report, nil
}

func validate(data cohort) error {
	if data.Version != 1 || data.Name == "" || data.Provenance == "" {
		return errors.New("version 1, cohort name and provenance are required")
	}
	if len(data.Tracks) < 2 || len(data.Tracks) > 256 || len(data.HeldOutAdjacency) > 1024 {
		return errors.New("cohort requires 2..256 tracks and at most 1024 adjacency pairs")
	}
	ids, recordings := map[string]bool{}, map[string]bool{}
	dimensions := map[string]int{}
	for _, track := range data.Tracks {
		if track.ID == "" || track.RecordingID == "" || ids[track.ID] || recordings[track.RecordingID] {
			return errors.New("track and recording IDs must be nonempty and unique")
		}
		ids[track.ID], recordings[track.RecordingID] = true, true
		for space, vector := range map[string][]float64{"catalogAudio": track.CatalogAudio, "catalogCooccurrence": track.CatalogCooccurrence, "clap": track.CLAP, "mert": track.MERT} {
			if len(vector) == 0 {
				continue
			}
			if data.Spaces[space] == "" || len(vector) > 2048 {
				return fmt.Errorf("%s requires pinned space identity and <=2048 dimensions", space)
			}
			if n := dimensions[space]; n != 0 && n != len(vector) {
				return fmt.Errorf("incompatible dimensions in %s", space)
			}
			dimensions[space] = len(vector)
			var norm float64
			for _, value := range vector {
				if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 1e10 {
					return errors.New("vector values must be bounded finite numbers")
				}
				norm += value * value
			}
			if norm == 0 {
				return errors.New("zero vector must be represented as missing")
			}
		}
	}
	seen := map[adjacency]bool{}
	for _, pair := range data.HeldOutAdjacency {
		if !ids[pair.From] || !ids[pair.To] || pair.From == pair.To || seen[pair] {
			return errors.New("adjacency requires distinct existing IDs and unique directed pairs")
		}
		seen[pair] = true
	}
	return nil
}

func cosine(a, b []float64) float64 {
	var dot, na, nb float64
	for i, v := range a {
		dot += v * b[i]
		na += v * v
		nb += b[i] * b[i]
	}
	return math.Max(-1, math.Min(1, dot/math.Sqrt(na*nb)))
}
