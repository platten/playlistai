package bridge

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/platten/playlistai/internal/app"
	"github.com/platten/playlistai/internal/config"
)

func TestOnboardingFlow(t *testing.T) {
	t.Parallel()
	api := New(newTestContainer(t), nil)

	if api.GetOnboarded() {
		t.Fatal("a fresh container should not be onboarded")
	}
	if err := api.CompleteOnboarding(); err != nil {
		t.Fatalf("CompleteOnboarding: %v", err)
	}
	if !api.GetOnboarded() {
		t.Fatal("GetOnboarded should be true after CompleteOnboarding")
	}
}

func TestSetupStatusPolicy(t *testing.T) {
	t.Parallel()
	ready := app.SetupCapability{Ready: true, Supported: true, Required: true}
	optional := app.SetupCapability{Supported: true}
	repair := app.SetupCapability{Supported: true, Required: true}
	for _, tc := range []struct {
		name             string
		readiness        app.SetupReadiness
		pending, repairs []string
		needs            bool
	}{
		{"fresh all ready", app.SetupReadiness{Catalog: ready, Metadata: ready, Model: ready, Intent: ready, Analysis: ready, Preview: ready}, []string{}, []string{}, true},
		{"completed upgrade ready", app.SetupReadiness{Onboarded: true, Catalog: ready, Metadata: ready, Model: ready, Intent: ready, Analysis: ready, Preview: ready}, []string{}, []string{}, false},
		{"new optional assets do not nag", app.SetupReadiness{Onboarded: true, Catalog: ready, Metadata: optional, Model: optional, Intent: optional, Analysis: optional, Preview: ready}, []string{"metadata", "model", "intent", "analysis"}, []string{}, false},
		{"ordered repairs", app.SetupReadiness{Onboarded: true, Catalog: repair, Metadata: repair, Model: repair, Intent: repair, Analysis: repair, Preview: optional}, []string{"catalog", "metadata", "model", "intent", "analysis", "preview"}, []string{"catalog", "metadata", "model", "intent", "analysis"}, true},
		{"unsupported optional choices omitted", app.SetupReadiness{Onboarded: true, Catalog: ready, Intent: app.SetupCapability{Required: true}, Analysis: app.SetupCapability{Required: true}, Preview: ready}, []string{}, []string{}, false},
		{"new MERT optional does not reopen wizard", app.SetupReadiness{Onboarded: true, Analysis: ready, MERT: optional}, []string{"mert"}, []string{}, false},
		{"MERT repairs follow analysis", app.SetupReadiness{Onboarded: true, Analysis: repair, MERT: repair, Preview: optional}, []string{"analysis", "mert", "preview"}, []string{"analysis", "mert"}, true},
		{"ready MERT skipped", app.SetupReadiness{Onboarded: true, MERT: ready}, []string{}, []string{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := setupStatus(tc.readiness)
			if got.Onboarded != tc.readiness.Onboarded || got.NeedsSetup != tc.needs || !reflect.DeepEqual(got.PendingSteps, tc.pending) || !reflect.DeepEqual(got.RepairSteps, tc.repairs) {
				t.Fatalf("status=%+v; pending=%v repairs=%v needs=%v", got, tc.pending, tc.repairs, tc.needs)
			}
		})
	}
}

func TestSetupStatusRealContainerPreservesSkippedDefaultsAndPreferences(t *testing.T) {
	t.Parallel()
	c := newTestContainer(t)
	api := New(c, nil)
	if err := api.CompleteOnboarding(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Config().DataDir, "prefs.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	status, err := api.GetSetupStatus()
	if err != nil || !status.Onboarded || status.NeedsSetup || len(status.RepairSteps) != 0 {
		t.Fatalf("explicitly skipped bare build reopened setup: %+v %v", status, err)
	}
	if slices.Contains(status.PendingSteps, "catalog") || slices.Contains(status.PendingSteps, "preview") {
		t.Fatalf("unsupported catalog or already-configured preview offered: %+v", status)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("status modified preferences", err)
	}
}

func TestSetupStatusLostSelectedModelAndCorruptPreferences(t *testing.T) {
	t.Parallel()
	c := newLoadedContainer(t)
	api := New(c, nil)
	prefs := config.Prefs{OnboardingDone: true, ModelPath: filepath.Join(t.TempDir(), "missing.gguf")}
	if err := prefs.Save(c.Config().DataDir); err != nil {
		t.Fatal(err)
	}
	status, err := api.GetSetupStatus()
	if err != nil || !status.NeedsSetup || !reflect.DeepEqual(status.RepairSteps, []string{"model"}) || slices.Contains(status.PendingSteps, "catalog") {
		t.Fatalf("lost selected model: %+v %v", status, err)
	}
	if err := c.ClearModel(); err != nil {
		t.Fatal(err)
	}
	status, err = api.GetSetupStatus()
	if err != nil || status.NeedsSetup {
		t.Fatalf("explicit clear should dismiss repair: %+v %v", status, err)
	}
	path := filepath.Join(c.Config().DataDir, "prefs.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.GetSetupStatus(); err == nil {
		t.Fatal("corrupt preferences silently treated as a first launch")
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != "{broken" {
		t.Fatal("corrupt preferences overwritten", err)
	}
}
