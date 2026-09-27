package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	listeningeval "github.com/platten/playlistai/internal/evaluation"
)

func readListeningJSON(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxInputBytes {
		return fmt.Errorf("listening input exceeds 64 MiB")
	}
	return json.Unmarshal(raw, value)
}

func runListening(pool, output, keyPath, judgments, seed, baseline, treatment string, frozen bool, out io.Writer) error {
	if pool != "" {
		outputAbs, _ := filepath.Abs(output)
		keyAbs, _ := filepath.Abs(keyPath)
		if output == "" || outputAbs == keyAbs {
			return fmt.Errorf("blind-output and key must be separate files")
		}
		var runs []listeningeval.RelevanceRun
		for _, path := range strings.Split(pool, ",") {
			var report struct {
				ListeningRuns []listeningeval.RelevanceRun `json:"listeningRuns"`
			}
			if err := readListeningJSON(path, &report); err != nil {
				return err
			}
			runs = append(runs, report.ListeningRuns...)
		}
		bundle, keys, err := listeningeval.PoolRelevanceRuns(runs, seed)
		if err != nil {
			return err
		}
		for _, item := range []struct {
			path  string
			value any
		}{{keyPath, keys}, {output, bundle}} {
			raw, marshalErr := json.MarshalIndent(item.value, "", "  ")
			if marshalErr != nil {
				return marshalErr
			}
			if err = os.WriteFile(item.path, append(raw, '\n'), 0600); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintf(out, "Prepared %d blind cases; keep the identity key away from listeners.\n", len(bundle.Cases))
		return err
	}
	var bundle listeningeval.RelevanceBundle
	var keys listeningeval.RelevanceKeys
	if err := readListeningJSON(judgments, &bundle); err != nil {
		return err
	}
	if err := readListeningJSON(keyPath, &keys); err != nil {
		return err
	}
	report, err := listeningeval.EvaluateRelevance(bundle, keys, baseline, treatment, frozen)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
