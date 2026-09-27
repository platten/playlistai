package llama

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAudioComparisonCorrection(t *testing.T) {
	in := AudioComparisonInput{Prompt: "soft piano", Criteria: []string{"soft piano"}, Coverage: "30–40 seconds", ModelFingerprint: "test-model", CLAPCosine: map[string]float64{"soft piano": .3}}
	valid := `{"phrases":[{"phrase":"soft piano","overlap":"suggested","explanation":"CLAP suggests overlap; DSP unavailable and no calibration."}]}`
	for _, tc := range []struct {
		name, first, second, status string
		calls                       int
		repaired                    bool
	}{
		{"valid", valid, "", "completed", 1, false},
		{"malformed", `{"phrase":"soft piano"},`, valid, "completed", 2, true},
		{"lost adjective", strings.Replace(valid, "soft piano", "piano", 1), valid, "completed", 2, true},
		{"unknown field", `{"phrases":[],"verified":true}`, valid, "completed", 2, true},
		{"trailing", valid + `{}`, valid, "completed", 2, true},
		{"still invalid", "not JSON", "not JSON", "unknown", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			result, err := compareAudio(context.Background(), in, func(ctx context.Context, system, user string, n int, grammar string) (string, error) {
				calls++
				if grammar != audioComparisonGrammar || !strings.Contains(system, "uncalibrated") {
					t.Fatal("missing constraints")
				}
				if calls == 1 {
					return tc.first, nil
				}
				if !strings.Contains(user, "invalid_output") {
					t.Fatal("missing repair context")
				}
				return tc.second, nil
			})
			if err != nil || calls != tc.calls || result.Status != tc.status || result.Repaired != tc.repaired || !result.DiagnosticOnly || len(result.Phrases) != 1 {
				t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
			}
			if result.Status == "unknown" && result.Phrases[0].Overlap != "unclear" {
				t.Fatal("invalid output admitted")
			}
		})
	}
}
func TestAudioComparisonMissingAndCancellation(t *testing.T) {
	in := AudioComparisonInput{Prompt: "soft piano", Criteria: []string{"soft piano"}}
	calls := 0
	complete := func(context.Context, string, string, int, string) (string, error) {
		calls++
		return "", errors.New("provider failure")
	}
	result, err := compareAudio(context.Background(), in, complete)
	if err != nil || calls != 0 || result.Status != "unknown" || result.Phrases[0].Overlap != "unclear" {
		t.Fatalf("%+v %v", result, err)
	}
	in.Coverage = "10 seconds"
	in.ModelFingerprint = "test"
	in.CLAPCosine = map[string]float64{"soft piano": .3}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = compareAudio(ctx, in, complete); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("%v calls=%d", err, calls)
	}
	if _, err = compareAudio(context.Background(), in, complete); err == nil || calls != 1 {
		t.Fatal("provider failure retried or hidden")
	}
	in.ModelFingerprint = ""
	if _, err = compareAudio(context.Background(), in, complete); err == nil || calls != 1 {
		t.Fatal("missing provenance admitted")
	}
}

func TestAudioComparisonCancellationDuringCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := AudioComparisonInput{Prompt: "soft piano", Criteria: []string{"soft piano"}, Coverage: "10 seconds", ModelFingerprint: "test", CLAPCosine: map[string]float64{"soft piano": .3}}
	calls := 0
	_, err := compareAudio(ctx, in, func(context.Context, string, string, int, string) (string, error) {
		calls++
		cancel()
		return `{"phrases":[{"phrase":"soft piano","overlap":"suggested","explanation":"unvalidated"}]}`, nil
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("stale result accepted: %v (%d calls)", err, calls)
	}
}
