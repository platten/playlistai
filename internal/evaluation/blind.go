package evaluation

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type BlindTrack struct {
	ID     string `json:"id"`
	Artist string `json:"artist"`
	Title  string `json:"title"`
}
type BlindPlaylist struct {
	Label  string       `json:"label"`
	Tracks []BlindTrack `json:"tracks"`
}
type BlindPair struct {
	PairID   string        `json:"pairId"`
	CaseID   string        `json:"caseId"`
	Prompt   string        `json:"prompt"`
	Left     BlindPlaylist `json:"left"`
	Right    BlindPlaylist `json:"right"`
	Judgment BlindJudgment `json:"judgment"`
}
type BlindJudgment struct {
	Winner     string `json:"winner"`
	Confidence int    `json:"confidence"`
	Notes      string `json:"notes"`
}
type BlindKey struct {
	PairID          string           `json:"pairId"`
	LeftVariant     string           `json:"leftVariant"`
	RightVariant    string           `json:"rightVariant"`
	LeftGeneration  GenerationRecord `json:"leftGeneration"`
	RightGeneration GenerationRecord `json:"rightGeneration"`
}
type BlindBundle struct {
	Version        int         `json:"version"`
	DatasetName    string      `json:"datasetName"`
	CatalogVersion string      `json:"catalogVersion"`
	Seed           string      `json:"seed"`
	Pairs          []BlindPair `json:"pairs"`
}
type BlindKeyBundle struct {
	Version     int        `json:"version"`
	DatasetName string     `json:"datasetName"`
	Keys        []BlindKey `json:"keys"`
}

// WriteBlindComparison randomizes left/right deterministically and writes the
// identity key separately so listening can remain blind.
func WriteBlindComparison(report Report, dataset Dataset, catalog ports.Catalog, leftVariant, rightVariant, seed, outputPath, keyPath string) error {
	left, ok := findVariant(report.HeldOutTest, leftVariant)
	if !ok {
		return fmt.Errorf("blind comparison: variant %q not found", leftVariant)
	}
	right, ok := findVariant(report.HeldOutTest, rightVariant)
	if !ok {
		return fmt.Errorf("blind comparison: variant %q not found", rightVariant)
	}
	byCase := map[string]RecommendationCase{}
	for _, item := range dataset.RecommendationCases {
		byCase[item.ID] = item
	}
	rightCases := map[string]CaseMetrics{}
	for _, item := range right.Cases {
		rightCases[item.CaseID] = item
	}
	bundle := BlindBundle{Version: ContractVersion, DatasetName: report.DatasetName, CatalogVersion: report.CatalogVersion, Seed: seed, Pairs: []BlindPair{}}
	keys := BlindKeyBundle{Version: ContractVersion, DatasetName: report.DatasetName, Keys: []BlindKey{}}
	for _, leftCase := range left.Cases {
		rightCase, found := rightCases[leftCase.CaseID]
		if !found || leftCase.Error != "" || rightCase.Error != "" {
			continue
		}
		sum := sha256.Sum256([]byte(report.DatasetName + "\x00" + leftCase.CaseID + "\x00" + seed))
		pairID := fmt.Sprintf("%x", sum[:8])
		leftGeneration, rightGeneration := leftCase.Generation, rightCase.Generation
		leftName, rightName := left.Name, right.Name
		if binary.LittleEndian.Uint64(sum[8:16])&1 == 1 {
			leftGeneration, rightGeneration = rightGeneration, leftGeneration
			leftName, rightName = rightName, leftName
		}
		caseInfo := byCase[leftCase.CaseID]
		bundle.Pairs = append(bundle.Pairs, BlindPair{PairID: pairID, CaseID: leftCase.CaseID, Prompt: caseInfo.Prompt, Left: blindPlaylist("A", leftGeneration, catalog), Right: blindPlaylist("B", rightGeneration, catalog), Judgment: BlindJudgment{}})
		keys.Keys = append(keys.Keys, BlindKey{PairID: pairID, LeftVariant: leftName, RightVariant: rightName, LeftGeneration: leftGeneration, RightGeneration: rightGeneration})
	}
	if len(bundle.Pairs) == 0 {
		return errors.New("blind comparison: no common successful held-out cases")
	}
	if err := writeJSON(outputPath, bundle); err != nil {
		return err
	}
	return writeJSON(keyPath, keys)
}

func findVariant(results []VariantResult, name string) (VariantResult, bool) {
	for _, item := range results {
		if item.Name == name {
			return item, true
		}
	}
	return VariantResult{}, false
}
func blindPlaylist(label string, g GenerationRecord, cat ports.Catalog) BlindPlaylist {
	out := BlindPlaylist{Label: label, Tracks: []BlindTrack{}}
	for _, id := range g.TrackIDs {
		track := core.TrackRef{ID: id}
		if meta, ok := cat.Meta(id); ok {
			track = meta.Ref
		}
		out.Tracks = append(out.Tracks, BlindTrack{ID: track.ID, Artist: track.Artist, Title: track.Title})
	}
	return out
}
func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o600)
}
