// audiopack assembles previously exported CLAP artifacts without Python.
// It never publishes or activates a bundle, and cannot manufacture calibration.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
)

type options struct {
	sourceURL, modelLicense                                                      string
	export, source, worker, runtime, licenses, platform, baseURL, policy, output string
	memory                                                                       int64
	builtin                                                                      bool
	audioOutput, textOutput                                                      string
	textUnpadded                                                                 bool
}

func main() {
	var o options
	flag.StringVar(&o.export, "export", "", "directory containing exported ONNX graphs and parity report")
	flag.StringVar(&o.source, "source", "", "directory containing vocabulary and merges")
	flag.StringVar(&o.worker, "worker", "", "compiled Go analysis worker")
	flag.StringVar(&o.runtime, "runtime", "", "native ONNX Runtime 1.26.0 library")
	flag.StringVar(&o.licenses, "licenses", "", "complete dependency license notices")
	flag.StringVar(&o.platform, "platform", "", "target OS/architecture, for example linux/amd64")
	flag.StringVar(&o.baseURL, "artifact-base-url", "https://unpublished.invalid", "HTTPS artifact base URL")
	flag.StringVar(&o.policy, "policy", "", "optional frozen development calibration policy JSON")
	flag.StringVar(&o.output, "output", "", "bundle output directory")
	flag.Int64Var(&o.memory, "memory-bytes", 0, "platform memory budget")
	flag.BoolVar(&o.builtin, "builtin-worker", false, "create a v2 bundle using the application's native worker")
	flag.StringVar(&o.audioOutput, "audio-output", "embedding", "audio ONNX output name for v2 bundles")
	flag.StringVar(&o.textOutput, "text-output", "embedding", "text ONNX output name for v2 bundles")
	flag.BoolVar(&o.textUnpadded, "text-unpadded", false, "text graph accepts variable-length input_ids without attention_mask")
	flag.StringVar(&o.sourceURL, "model-source-url", "", "custom model provenance URL (defaults to its Hugging Face model ID)")
	flag.StringVar(&o.modelLicense, "model-license", "", "custom model license attribution (full notices are still required)")
	flag.Parse()
	if err := assemble(o); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Assembled local bundle. Automatic musical-fit decisions require a reviewed calibration policy.")
}

func assemble(o options) error {
	for _, value := range []string{o.export, o.source, o.runtime, o.licenses, o.output, o.platform} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("audiopack: all artifact paths, output and target platform are required")
		}
	}
	if !o.builtin && o.worker == "" {
		return fmt.Errorf("audiopack: worker path or --builtin-worker required")
	}
	u, err := url.Parse(o.baseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || o.memory <= 0 {
		return fmt.Errorf("audiopack: HTTPS artifact base and positive memory budget required")
	}
	var report struct {
		audio.ParityReport
		Passed        bool   `json:"passed"`
		Model         string `json:"model"`
		Preprocessing string `json:"preprocessing"`
	}
	if err := readJSON(filepath.Join(o.export, "parity.json"), &report); err != nil {
		return err
	}
	if !report.Passed || !report.Valid() || report.Model == "" || report.Preprocessing != audio.PreprocessingVersion {
		return fmt.Errorf("audiopack: incompatible or unvalidated export")
	}
	if err := audio.CheckPreprocessing(filepath.Join(o.export, "preprocessing.json")); err != nil {
		return err
	}
	m := audio.BundleManifest{Version: 1, ID: "music-clap-cpu-v1", Label: "Music CLAP · CPU", Platform: o.platform, MemoryBytes: o.memory, License: "Apache-2.0 model; MIT runtime; GPL-3.0 worker", SourceURL: "https://huggingface.co/laion/larger_clap_music", Parity: report.ParityReport, Model: core.AudioModelIdentity{Model: report.Model, Revision: report.ReferenceRevision, Preprocessing: report.Preprocessing, Runtime: "onnxruntime/1.26.0/cpu", Dimension: 512}}
	if o.builtin {
		m.Version = 2
		m.ID = "custom-clap-cpu-v2"
		m.Label = report.Model + " · CPU"
		m.SourceURL = "https://huggingface.co/" + report.Model
		m.License = "See bundled model and dependency license notices"
		m.ONNXOutputNames = []string{o.audioOutput, o.textOutput}
		m.TextUnpadded = o.textUnpadded
	}
	if o.sourceURL != "" {
		m.SourceURL = o.sourceURL
	}
	if o.modelLicense != "" {
		m.License = o.modelLicense
	}
	if o.policy != "" {
		if err := readJSON(o.policy, &m.Policy); err != nil {
			return err
		}
		if !m.Policy.Valid() {
			return fmt.Errorf("audiopack: invalid development calibration policy")
		}
	}
	inputs := []struct{ role, path string }{
		{"runtime", o.runtime}, {"audio_model", filepath.Join(o.export, "audio.onnx")}, {"text_model", filepath.Join(o.export, "text.onnx")},
		{"vocabulary", filepath.Join(o.source, "vocab.json")}, {"merges", filepath.Join(o.source, "merges.txt")}, {"preprocessing", filepath.Join(o.export, "preprocessing.json")}, {"license", o.licenses}, {"health", filepath.Join(o.export, "health.json")},
	}
	if !o.builtin {
		inputs = append(inputs, struct{ role, path string }{"worker", o.worker})
	}
	names := map[string]bool{}
	for _, input := range inputs {
		name := filepath.Base(input.path)
		if names[name] || name == "bundle.json" {
			return fmt.Errorf("audiopack: duplicate or reserved artifact filename %s", name)
		}
		names[name] = true
	}
	if err := os.MkdirAll(o.output, 0o700); err != nil {
		return err
	}
	for _, input := range inputs {
		name := filepath.Base(input.path)
		target := filepath.Join(o.output, name)
		if err := copyArtifact(input.path, target); err != nil {
			return err
		}
		f, err := os.Open(target)
		if err != nil {
			return err
		}
		h := sha256.New()
		size, err := io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return err
		}
		if size == 0 {
			return fmt.Errorf("audiopack: empty artifact %s", name)
		}
		m.Artifacts = append(m.Artifacts, audio.BundleArtifact{Role: input.role, Name: name, URL: strings.TrimRight(o.baseURL, "/") + "/" + url.PathEscape(name), Size: size, SHA256: hex.EncodeToString(h.Sum(nil))})
	}
	if o.builtin {
		m.Model.Weights = m.EmbeddingFingerprint()
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	staged := filepath.Join(o.output, "bundle.json.tmp")
	if err := os.WriteFile(staged, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(staged, filepath.Join(o.output, "bundle.json"))
}

func readJSON(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(value)
}

func copyArtifact(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("audiopack: artifact must be a regular file")
	}
	if other, err := os.Stat(target); err == nil && os.SameFile(info, other) {
		return nil
	}
	staged := target + ".tmp"
	out, err := os.OpenFile(staged, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(staged, target)
}
