// Command intenteval benchmarks a local GGUF against the implemented intent
// contract using the application's grammar-constrained llama.cpp client.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/evaluation"
	"github.com/platten/playlistai/internal/intent/llama"
	"github.com/platten/playlistai/internal/intent/modelmgr"
	"github.com/platten/playlistai/internal/intent/rules"
	"github.com/platten/playlistai/internal/ports"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "intenteval:", err)
		os.Exit(1)
	}
}

func run() error {
	var datasetPath, modelPath, modelID, runtimePath, outputPath, markdownPath, caseID, backend string
	var repeat, nctx, threads, gpuLayers int
	flag.StringVar(&datasetPath, "dataset", "", "versioned evaluation dataset JSON")
	flag.StringVar(&backend, "backend", "llama", "parser backend: llama or rules")
	flag.StringVar(&modelPath, "model", "", "local GGUF path")
	flag.StringVar(&modelID, "model-id", "", "stable model/artifact label")
	flag.StringVar(&runtimePath, "runtime", "", "llama or llama-server path")
	flag.StringVar(&outputPath, "output", "intent-model-report.json", "JSON report path")
	flag.StringVar(&markdownPath, "markdown", "intent-model-report.md", "Markdown report path")
	flag.StringVar(&caseID, "case", "", "optional single intent case ID")
	flag.IntVar(&repeat, "repeat", 1, "attempts per labeled case")
	flag.IntVar(&nctx, "n-ctx", 4096, "llama context size")
	flag.IntVar(&threads, "threads", 0, "llama CPU threads; zero lets the runtime decide")
	flag.IntVar(&gpuLayers, "gpu-layers", -1, "GPU layers; negative forces CPU for comparable runs")
	flag.Parse()
	if datasetPath == "" {
		return fmt.Errorf("-dataset is required")
	}
	dataset, err := evaluation.LoadDataset(datasetPath)
	if err != nil {
		return err
	}
	if caseID != "" {
		var selected []evaluation.IntentCase
		for _, item := range dataset.IntentCases {
			if item.ID == caseID {
				selected = append(selected, item)
			}
		}
		if len(selected) == 0 {
			return fmt.Errorf("intent case %q not found", caseID)
		}
		dataset.IntentCases = selected
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	var parser ports.IntentParser
	var identity evaluation.IntentModelIdentity
	closeParser := func() {}
	switch backend {
	case "rules":
		parser = rules.New()
		identity = evaluation.IntentModelIdentity{ID: "rules/v2", Runtime: "built-in Go"}
	case "llama":
		if modelPath == "" || runtimePath == "" {
			return fmt.Errorf("-model and -runtime are required for the llama backend")
		}
		if modelID == "" {
			modelID = strings.TrimSuffix(filepath.Base(modelPath), filepath.Ext(modelPath))
		}
		if err := modelmgr.ValidateGGUF(modelPath); err != nil {
			return err
		}
		stat, err := os.Stat(modelPath)
		if err != nil {
			return err
		}
		hash, err := fileSHA256(modelPath)
		if err != nil {
			return err
		}
		localParser, err := llama.New(ctx, llama.Options{BinaryPath: runtimePath, ModelPath: modelPath, NCtx: nctx, NThreads: threads, GPULayers: gpuLayers, StartTimeout: 5 * time.Minute})
		if err != nil {
			return err
		}
		parser = localParser
		identity = evaluation.IntentModelIdentity{ID: modelID, Path: modelPath, ArtifactBytes: stat.Size(), SHA256: hash, Runtime: runtimeVersion(runtimePath)}
		closeParser = func() { _ = localParser.Close() }
	default:
		return fmt.Errorf("unknown backend %q", backend)
	}
	defer closeParser()
	report, err := evaluation.EvaluateIntentModel(ctx, parser, dataset, identity, repeat)
	if err != nil {
		return err
	}
	if err := ensureParent(outputPath); err != nil {
		return err
	}
	if err := evaluation.WriteIntentModelReportJSON(outputPath, report); err != nil {
		return err
	}
	if markdownPath != "" {
		if err := ensureParent(markdownPath); err != nil {
			return err
		}
		if err := evaluation.WriteIntentModelReportMarkdown(markdownPath, report); err != nil {
			return err
		}
	}
	fmt.Printf("wrote %s", outputPath)
	if markdownPath != "" {
		fmt.Printf(" and %s", markdownPath)
	}
	fmt.Println()
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // operator-supplied local model
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func runtimeVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, path, "--version").CombinedOutput() //nolint:gosec // operator-supplied runtime
	if err != nil {
		return filepath.Base(path) + " (version unavailable)"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(raw)), "\n")
	return line
}

func ensureParent(path string) error {
	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return nil
	}
	return os.MkdirAll(parent, 0o755)
}
