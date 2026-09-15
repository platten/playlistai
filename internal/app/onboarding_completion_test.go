package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/config"
)

func TestCompleteSetupWaitsForNativeValidation(t *testing.T) {
	c := &Container{cfg: testConfig(t)}
	c.analysis.startup.pending = true
	// A readiness check must not wait behind the inference initialization lock.
	c.analysis.mu.Lock()
	defer c.analysis.mu.Unlock()
	r, err := c.SetupReadiness()
	if err != nil || !r.Pending {
		t.Fatalf("readiness=%+v err=%v", r, err)
	}
	if err := c.CompleteSetup(); err == nil || !strings.Contains(err.Error(), "still being validated") {
		t.Fatal("pending native validation was not surfaced", err)
	}
	if config.LoadPrefs(c.cfg.DataDir).OnboardingDone {
		t.Fatal("pending validation marked setup done")
	}
}

func TestCompletionChecksAllSupportedFreshCapabilitiesAndOnlyLegacyRepairs(t *testing.T) {
	r := SetupReadiness{Catalog: SetupCapability{Ready: true, Supported: true}, Model: SetupCapability{Supported: true}, Analysis: SetupCapability{Supported: true, Required: true}, MERT: SetupCapability{Required: true}, Preview: SetupCapability{Supported: true}}
	if got := r.CompletionSteps(); !reflect.DeepEqual(got, []string{"model", "analysis", "preview"}) {
		t.Fatal("fresh requirements lost", got)
	}
	r.Onboarded = true
	if got := r.CompletionSteps(); !reflect.DeepEqual(got, []string{"analysis"}) {
		t.Fatal("legacy choices or unsupported features reopened", got)
	}
	r.Analysis.Ready = true
	if got := r.CompletionSteps(); len(got) != 0 {
		t.Fatal("complete repaired setup blocked", got)
	}
}

func TestCompleteSetupRejectsMissingAssetsAndUnreadablePreferences(t *testing.T) {
	c := &Container{cfg: testConfig(t), previewName: config.PreviewOff}
	if err := c.CompleteSetup(); err == nil {
		t.Fatal("fresh required model and previews bypassed")
	}
	if config.LoadPrefs(c.cfg.DataDir).OnboardingDone {
		t.Fatal("incomplete setup persisted")
	}
	if err := (config.Prefs{OnboardingDone: true, ModelDisabled: true, PreviewProvider: "off"}).Save(c.cfg.DataDir); err != nil {
		t.Fatal(err)
	}
	if err := c.CompleteSetup(); err != nil {
		t.Fatal("legacy explicit choices not preserved", err)
	}
	path := filepath.Join(c.cfg.DataDir, "prefs.json")
	if err := os.WriteFile(path, []byte("{corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.CompleteSetup(); err == nil {
		t.Fatal("corrupt preferences reported saved")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "{corrupt" {
		t.Fatal("corrupt evidence overwritten", err)
	}
}
