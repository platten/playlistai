package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

// DiscogsFiles are the original, unmodified ONNX exports and metadata from
// Essentia. Each download is independently pinned before it can be activated.
var DiscogsFiles = []struct {
	Name, URL, SHA256 string
	Size              int64
}{
	{"encoder.onnx", "https://essentia.upf.edu/models/feature-extractors/discogs-effnet/discogs-effnet-bsdynamic-1.onnx", "a280825b334797cf677939db8cd5762c0392aedd0ca6415dbc1cd083f045e43c", 18027718},
	{"encoder.json", "https://essentia.upf.edu/models/feature-extractors/discogs-effnet/discogs-effnet-bsdynamic-1.json", "a2e85b2e7372d5f8e0f35bdd6aeae1139f101087d183d0b2fb60b0ea0f01a0ff", 14986},
	{"instrument.onnx", "https://essentia.upf.edu/models/classification-heads/mtg_jamendo_instrument/mtg_jamendo_instrument-discogs-effnet-1.onnx", "9ae2d9e763d66bd8eed654d1ac3aa171e6539cb8a0e11f3dcd53df1428980802", 2706492},
	{"instrument.json", "https://essentia.upf.edu/models/classification-heads/mtg_jamendo_instrument/mtg_jamendo_instrument-discogs-effnet-1.json", "7d02204c6451b5615e2968ec6364bbae3b915c886e608f05f00d3a38dc5177c4", 3382},
	{"vocal.onnx", "https://essentia.upf.edu/models/classification-heads/voice_instrumental/voice_instrumental-discogs-effnet-1.onnx", "20155e4c439714b0c45c08644b73c8e12d9dccb173bd4ab9934bf1e5aee837ca", 514114},
	{"vocal.json", "https://essentia.upf.edu/models/classification-heads/voice_instrumental/voice_instrumental-discogs-effnet-1.json", "43ac2c3b055dfaed20f6232e0f10636c287f1c5a6e5bd02c5585860031964c8f", 2013},
	{"mood.onnx", "https://essentia.upf.edu/models/classification-heads/mood_relaxed/mood_relaxed-discogs-effnet-1.onnx", "8ba6515a1e5943a72b3b475e3a25fc7a2ff04142c3eaa6aa0716fca371efdfff", 514101},
	{"mood.json", "https://essentia.upf.edu/models/classification-heads/mood_relaxed/mood_relaxed-discogs-effnet-1.json", "86c0fe1c2c6d49bf08537bc2d3a602204feaede011ed119c1fc6c36270f60e6a", 2364},
	{"LICENSE", "https://essentia.upf.edu/models/LICENSE", "f0c076094cc08acb265ea3304ec522cf331917d19bdd271ed69f10ef174f3ea4", 69392},
}

const DiscogsPreprocessing = "mono-sinc64-16k-effnet-mel128-hop62-repeat-last-mean/v1"
const DiscogsRuntime = "onnxruntime/1.26.0/cpu"

type DiscogsModel struct {
	Encoder core.MusicClassifierIdentity
	Heads   []core.MusicClassifierHead
}

func (m DiscogsModel) Fingerprint() string {
	return (core.MusicClassifierEvidence{Version: core.MusicClassifierEvidenceVersion, Encoder: m.Encoder, Preprocessing: DiscogsPreprocessing, Runtime: DiscogsRuntime, Heads: m.Heads}).Fingerprint()
}

func DiscogsDownloadBytes() int64 {
	var total int64
	for _, file := range DiscogsFiles {
		total += file.Size
	}
	return total
}

// ReadDiscogsModel verifies every original file before any graph is loaded.
func ReadDiscogsModel(ctx context.Context, dir string) (DiscogsModel, error) {
	for _, file := range DiscogsFiles {
		path := filepath.Join(dir, file.Name)
		entry, err := os.Lstat(path)
		if err != nil {
			return DiscogsModel{}, err
		}
		if !entry.Mode().IsRegular() || entry.Size() != file.Size {
			return DiscogsModel{}, fmt.Errorf("invalid discogs asset %s", file.Name)
		}
		f, err := os.Open(path)
		if err != nil {
			return DiscogsModel{}, err
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size || !os.SameFile(entry, info) {
			_ = f.Close()
			return DiscogsModel{}, fmt.Errorf("invalid discogs asset %s", file.Name)
		}
		h := sha256.New()
		_, err = io.Copy(h, &contextReader{ctx: ctx, reader: f})
		closeErr := f.Close()
		if err != nil || closeErr != nil || hex.EncodeToString(h.Sum(nil)) != file.SHA256 {
			return DiscogsModel{}, fmt.Errorf("discogs asset failed verification: %s", file.Name)
		}
	}
	model := DiscogsModel{}
	for _, head := range []struct {
		kind, name, identity string
		revision             string
	}{
		{"style", "encoder", "discogs-effnet-bsdynamic-1", "1"},
		{"instrumentation", "instrument", "mtg_jamendo_instrument-discogs-effnet-1", "1"},
		{"vocal", "vocal", "voice_instrumental-discogs-effnet-1", "2"},
		{"mood", "mood", "mood_relaxed-discogs-effnet-1", "2"},
	} {
		var metadata struct {
			Classes []string `json:"classes"`
		}
		raw, err := os.ReadFile(filepath.Join(dir, head.name+".json"))
		if err != nil || json.Unmarshal(raw, &metadata) != nil || len(metadata.Classes) == 0 {
			return DiscogsModel{}, fmt.Errorf("invalid discogs class metadata: %s", head.name)
		}
		weights, labels := "", ""
		for _, f := range DiscogsFiles {
			if f.Name == head.name+".onnx" {
				weights = f.SHA256
			}
			if f.Name == head.name+".json" {
				labels = f.SHA256
			}
		}
		identity := core.MusicClassifierIdentity{Model: head.identity, Revision: head.revision, WeightsSHA256: weights, MetadataSHA256: labels}
		if head.kind == "style" {
			model.Encoder = identity
		}
		model.Heads = append(model.Heads, core.MusicClassifierHead{Kind: head.kind, Model: identity, Classes: metadata.Classes})
	}
	if len(model.Heads[0].Classes) != 400 || len(model.Heads[1].Classes) != 40 || len(model.Heads[2].Classes) != 2 || len(model.Heads[3].Classes) != 2 {
		return DiscogsModel{}, fmt.Errorf("incompatible discogs class dimensions")
	}
	for _, head := range model.Heads {
		seen := map[string]bool{}
		for _, label := range head.Classes {
			if strings.TrimSpace(label) == "" || seen[label] {
				return DiscogsModel{}, fmt.Errorf("invalid discogs class list")
			}
			seen[label] = true
		}
	}
	return model, nil
}
