package bridge

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/ports"
)

func TestCSVConfirmsNormalizedDestination(t *testing.T) {
	for _, accept := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "replace"}[accept], func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "音楽 mix.csv")
			if err := os.WriteFile(target, []byte("previous export"), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			chosen, canceled, overwrite, err := chooseCSVTarget("mix.csv", func(name, directory, message string) (string, error) {
				calls++
				if calls == 1 {
					return strings.TrimSuffix(target, ".csv"), nil
				}
				if filepath.Join(directory, name) != target || !strings.Contains(message, "already exists") {
					t.Fatal("did not ask for the actual CSV destination")
				}
				if !accept {
					return "", nil
				}
				return target, nil
			})
			if err != nil || calls != 2 || overwrite != accept || canceled == accept || accept && chosen != target {
				t.Fatalf("overwrite=%v canceled=%v calls=%d err=%v", overwrite, canceled, calls, err)
			}
			if !canceled {
				if err := writeCSVFile(target, []byte("new export"), overwrite); err != nil {
					t.Fatal(err)
				}
			}
			got, err := os.ReadFile(target)
			want := "previous export"
			if accept {
				want = "new export"
			}
			if err != nil || string(got) != want {
				t.Fatalf("contents=%q err=%v", got, err)
			}
		})
	}
}

func TestCSVFileCreatedAsPickerClosesRequiresReconfirmation(t *testing.T) {
	for _, accept := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "replace"}[accept], func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "new.csv")
			calls := 0
			chosen, canceled, overwrite, err := chooseCSVTarget("new.csv", func(name, directory, message string) (string, error) {
				calls++
				if calls == 1 {
					// The native picker accepted an absent destination, then another
					// process created it before the app received the selected name.
					if err := os.WriteFile(target, []byte("concurrent file"), 0600); err != nil {
						t.Fatal(err)
					}
					return target, nil
				}
				if calls != 2 || filepath.Join(directory, name) != target || !strings.Contains(message, "already exists") {
					t.Fatal("did not re-confirm the first observed file")
				}
				if !accept {
					return "", nil
				}
				return target, nil
			})
			if err != nil || calls != 2 || canceled == accept || overwrite != accept || accept && chosen != target {
				t.Fatalf("path=%q canceled=%v overwrite=%v calls=%d err=%v", chosen, canceled, overwrite, calls, err)
			}
			if !canceled {
				if err := writeCSVFile(chosen, []byte("approved export"), overwrite); err != nil {
					t.Fatal(err)
				}
			}
			want := "concurrent file"
			if accept {
				want = "approved export"
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != want {
				t.Fatalf("contents=%q err=%v", got, err)
			}
		})
	}
}

func TestCSVConfirmationDoesNotTransferToChangedTargetOrIdentity(t *testing.T) {
	for _, changedPath := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed identity", true: "changed target"}[changedPath], func(t *testing.T) {
			dir := t.TempDir()
			target, replacement := filepath.Join(dir, "first.csv"), filepath.Join(dir, "other.csv")
			for _, path := range []string{target, replacement} {
				if err := os.WriteFile(path, []byte(path), 0600); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			_, canceled, overwrite, err := chooseCSVTarget("first.csv", func(name, directory, _ string) (string, error) {
				calls++
				if calls == 1 {
					return target, nil
				}
				if calls == 2 {
					if changedPath {
						return replacement, nil
					}
					// Keep the original file under a different name so the new
					// identity cannot be confused with a recycled inode/file ID.
					if err := os.Rename(target, filepath.Join(dir, "preserved.csv")); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(replacement, target); err != nil {
						t.Fatal(err)
					}
					return target, nil
				}
				want := target
				if changedPath {
					want = replacement
				}
				if calls != 3 || filepath.Join(directory, name) != want {
					t.Fatal("changed destination did not receive its own confirmation")
				}
				return "", nil
			})
			if err != nil || !canceled || overwrite || calls != 3 {
				t.Fatalf("canceled=%v overwrite=%v calls=%d err=%v", canceled, overwrite, calls, err)
			}
		})
	}
}

func TestCSVFailedPublicationPreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mix.csv")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("replacement denied")
	err := publishCSVFile(target, []byte("new"), true, func(string, string) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatal("replacement failure hidden", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "original" {
		t.Fatal("original was damaged", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary export leaked", entries, err)
	}
	if err := writeCSVFile(target, []byte("racing export"), false); err == nil {
		t.Fatal("no-replace publication overwrote existing file")
	}
	got, err = os.ReadFile(target)
	if err != nil || string(got) != "original" {
		t.Fatal("concurrent destination was damaged", err)
	}
}

func TestCSVPublishesNewUnicodeDestination(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "音楽 — Łódź.csv")
	if err := writeCSVFile(target, []byte("complete playlist"), false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "complete playlist" {
		t.Fatalf("contents=%q err=%v", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary export leaked", entries, err)
	}
}

func TestCSVHeadlessAndNonregularDestinationsCannotOverwrite(t *testing.T) {
	target := filepath.Join(t.TempDir(), "existing.csv")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeCSVFile(target, []byte("headless"), false); err == nil {
		t.Fatal("headless overwrite permitted")
	}
	if _, canceled, overwrite, err := chooseCSVTarget("mix.csv", func(string, string, string) (string, error) { return target, nil }); err != nil || canceled || !overwrite {
		t.Fatal("exact dialog confirmation lost", err)
	}
	directory := target + ".directory.csv"
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := chooseCSVTarget("mix.csv", func(string, string, string) (string, error) { return directory, nil }); err == nil {
		t.Fatal("directory accepted as CSV")
	}
	if path, canceled, overwrite, err := chooseCSVTarget("mix.csv", func(string, string, string) (string, error) { return target + ".new", nil }); err != nil || canceled || overwrite || path != target+".new.csv" {
		t.Fatal("new file requires overwrite", err)
	}
}

func TestSoundiizNormalLogsDoNotContainShareCapability(t *testing.T) {
	for _, fail := range []bool{false, true} {
		var logs bytes.Buffer
		api := &API{log: slog.New(slog.NewTextHandler(&logs, nil))}
		url := "https://soundiiz.com/go/import-playlist/private-fixture-token"
		result := api.presentSoundiizHandoff(ports.ExportResult{Location: url, Count: 2}, func(got string) error {
			if got != url {
				t.Fatal("opener did not receive exact share URL")
			}
			if fail {
				return errors.New("cannot open " + url)
			}
			return nil
		})
		if result.URL != url || result.Opened == fail || result.Count != 2 {
			t.Fatalf("export result lost: %+v", result)
		}
		if strings.Contains(logs.String(), "private-fixture-token") || strings.Contains(logs.String(), url) {
			t.Fatal("share capability leaked to normal logs", logs.String())
		}
	}
}
