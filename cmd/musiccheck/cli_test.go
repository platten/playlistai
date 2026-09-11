package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func commandArgs(t *testing.T, values ...string) {
	t.Helper()
	old := os.Args
	os.Args = append([]string{"musiccheck"}, values...)
	t.Cleanup(func() { os.Args = old })
}

func TestOfflineReplayCommandModes(t *testing.T) {
	dir := t.TempDir()
	prompts := filepath.Join(dir, "prompts.json")
	replay := filepath.Join(dir, "replay.json")
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, OriginalDescription: "Justice", References: []core.IntentReference{{Kind: core.ReferenceArtist, Query: "Justice", Influence: core.InfluencePositive}}, Controls: core.IntentControls{TotalTrackCount: 3}}.Normalized()
	write := func(path string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(prompts, []promptCase{{Prompt: "Justice", Artist: "Justice", Count: 3}})
	write(replay, []result{{Prompt: "Justice", Intent: intent, ParsedIntent: intent}})
	base := []string{"-catalog", "../../internal/catalog/testdata", "-prompts", prompts, "-replay", replay}
	for _, mode := range []string{"parse", "deejai", "multichannel", "raw parsed"} {
		t.Run(mode, func(t *testing.T) {
			out := filepath.Join(dir, strings.ReplaceAll(mode, " ", "-")+".json")
			values := append(append([]string{}, base...), "-output", out)
			switch mode {
			case "parse":
				values = append(values, "-parse-only")
			case "deejai":
				values = append(values, "-mode", "deejai_only", "-count", "4")
			case "raw parsed":
				values = append(values, "-replay-parsed", "-parse-only")
			}
			commandArgs(t, values...)
			if err := run(); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			var got []result
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || !got[0].Replayed || len(got[0].Errors) != 0 {
				t.Fatalf("bad replay: %+v", got)
			}
			if mode == "deejai" && len(got[0].Playlist.Tracks) != 4 {
				t.Fatal("count override lost")
			}
		})
	}
	t.Run("no cases", func(t *testing.T) {
		commandArgs(t, append(base, "-case", "absent")...)
		if err := run(); err == nil || !strings.Contains(err.Error(), "no prompt cases") {
			t.Fatalf("missing selection not reported: %v", err)
		}
	})
	write(replay, []result{})
	t.Run("missing replay prompt", func(t *testing.T) {
		commandArgs(t, append(base, "-output", filepath.Join(dir, "failed.json"))...)
		if run() == nil {
			t.Fatal("missing replay passed")
		}
	})
}

func TestMusiccheckCLIValidation(t *testing.T) {
	for name, values := range map[string][]string{"unknown flag": {"-bogus"}, "mode": {"-mode", "invalid"}, "parsed without replay": {"-replay-parsed"}, "negative count": {"-count", "-1"}, "cache without bundle": {"-cached-audio-only"}, "missing replay": {"-replay", filepath.Join(t.TempDir(), "missing")}} {
		t.Run(name, func(t *testing.T) {
			commandArgs(t, values...)
			if run() == nil {
				t.Fatal("invalid command accepted")
			}
		})
	}
}

func TestMusiccheckHelpDoesNotStartModel(t *testing.T) {
	commandArgs(t, "-help")
	main()
}
