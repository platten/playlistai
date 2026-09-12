package app

import (
	"testing"

	"github.com/platten/playlistai/internal/config"
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
	c.intentAssist.installed = true
	if err := c.SetIntentAssistEnabled(true); err != nil {
		t.Fatal(err)
	}
	if c.ParserIdentity() == before {
		t.Fatal("identity did not change")
	}
	p = config.LoadPrefs(dir)
	if !p.IntentAssistEnabled || !p.DebugLogging || !p.OnboardingDone || p.ModelID != "chosen-llm" {
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
