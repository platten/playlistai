package app

import (
	"testing"

	"github.com/platten/playlistai/internal/config"
	"github.com/platten/playlistai/internal/intent/nlu"
	"github.com/platten/playlistai/internal/intent/rules"
)

func TestIntentAssistSettingsPreserveOtherPreferencesAndInvalidateParsing(t *testing.T) {
	dir := t.TempDir()
	p := config.Prefs{DebugLogging: true, ModelID: "chosen-llm", OnboardingDone: true}
	if err := p.Save(dir); err != nil {
		t.Fatal(err)
	}
	c := &Container{cfg: config.Config{DataDir: dir}, parser: rules.New()}
	before := c.ParserIdentity()
	if err := c.SetIntentAssistEnabled(true); err == nil {
		t.Fatal("enabled missing model")
	}
	c.intentAssist.extractor = &nlu.Worker{Config: nlu.WorkerConfig{Kind: nlu.DistilBERT}}
	if err := c.SetIntentAssistEnabled(true); err != nil {
		t.Fatal(err)
	}
	if c.ParserIdentity() == before {
		t.Fatal("identity did not change")
	}
	p = config.LoadPrefs(dir)
	if p.IntentExtractorEnabled == nil || !*p.IntentExtractorEnabled || p.IntentAssistEnabled || !p.DebugLogging || !p.OnboardingDone || p.ModelID != "chosen-llm" {
		t.Fatal(p)
	}
	enabled := c.ParserIdentity()
	if err := c.SetIntentAssistEnabled(false); err != nil {
		t.Fatal(err)
	}
	if c.ParserIdentity() == enabled {
		t.Fatal("disable reused enabled interpretation cache")
	}
}

func TestExtractorPreferenceMigrationDoesNotTransferDictionaryOnlyOptIn(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name        string
		prefs       config.Prefs
		valid, want bool
	}{
		{"legacy dictionary only", config.Prefs{IntentAssistEnabled: true}, false, false},
		{"legacy invalid extractor", config.Prefs{IntentAssistEnabled: true, IntentExtractorDir: "missing"}, false, false},
		{"legacy reviewed extractor", config.Prefs{IntentAssistEnabled: true, IntentExtractorDir: "reviewed"}, true, true},
		{"legacy opt out", config.Prefs{IntentExtractorDir: "reviewed"}, true, false},
		{"explicit opt out", config.Prefs{IntentAssistEnabled: true, IntentExtractorEnabled: &no}, true, false},
		{"explicit temporarily missing", config.Prefs{IntentExtractorEnabled: &yes}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := migratedExtractorEnabled(tc.prefs, tc.valid); got != tc.want {
				t.Fatalf("enabled=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestBaseAssetsCannotEnableExtractor(t *testing.T) {
	c := &Container{cfg: config.Config{DataDir: t.TempDir()}}
	c.intentAssist.installed = true
	if err := c.SetIntentAssistEnabled(true); err == nil {
		t.Fatal("base encoder became an extractor")
	}
}
