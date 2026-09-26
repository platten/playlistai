package llama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
)

func TestOptionalContextUsesOnlyMeasuredSpareSpace(t *testing.T) {
	for _, tc := range []struct {
		name                string
		context, tokens     int
		measured, wantHints bool
		wantOutput          int
	}{
		{"room", 8192, 1600, true, true, 1800}, {"tight", 4096, 2500, true, false, 1468}, {"no tokenizer", 16384, 0, false, false, 1800}, {"unknown window", 0, 0, false, false, 1800},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if !tc.measured {
					http.NotFound(w, r)
					return
				}
				switch r.URL.Path {
				case "/apply-template":
					_, _ = w.Write([]byte(`{"prompt":"mandatory"}`))
				case "/tokenize":
					_ = json.NewEncoder(w).Encode(map[string]any{"tokens": make([]int, tc.tokens)})
				default:
					t.Fatal("unexpected RPC")
				}
			}))
			defer srv.Close()
			c := NewClientWithContext(srv.URL, tc.context)
			source := lexicon.Extract("warm timbre")
			in := lexicon.PrepareParsingContext(ports.IntentInput{Prompt: source.OriginalText, SourceFacts: &source, EnrichParsingContext: true})
			messages := buildMessages(in)
			obs, err := c.measureBudget(context.Background(), messages, 1800)
			if err != nil {
				t.Fatal(err)
			}
			c.admitHints(messages, in.SourceFacts.ParsingContext, &obs)
			if obs.OutputAllowance != tc.wantOutput || (obs.OptionalByteBound > 0) != tc.wantHints || (obs.MandatoryTokens != nil) != tc.measured {
				t.Fatalf("budget: %+v", obs)
			}
			if tc.measured && calls != 2 {
				t.Fatal("extra tokenizer RPC", calls)
			}
			if tc.wantHints && obs.OptionalByteBound != len(lexicon.ContextHeader)+len(in.SourceFacts.ParsingContext.Hints[0].Text) {
				t.Fatal("formatting excluded from byte bound", obs)
			}
		})
	}
}

func TestRetryContextSnapshotAndSuccessfulSubset(t *testing.T) {
	for _, window := range []int{4096, 8192} {
		t.Run(strconv.Itoa(window), func(t *testing.T) {
			var requests []chatRequest
			var measured []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/apply-template":
					var req chatRequest
					_ = json.NewDecoder(r.Body).Decode(&req)
					text := req.Messages[len(req.Messages)-1].Content
					if strings.Contains(text, lexicon.ContextHeader) {
						t.Error("optional hints measured")
					}
					measured = append(measured, text)
					_, _ = w.Write([]byte(`{"prompt":"mandatory"}`))
				case "/tokenize":
					_ = json.NewEncoder(w).Encode(map[string]any{"tokens": make([]int, 1600+100*len(requests))})
				case "/v1/chat/completions":
					var req chatRequest
					_ = json.NewDecoder(r.Body).Decode(&req)
					requests = append(requests, req)
					raw := "invalid"
					if len(requests) == 2 {
						b, _ := json.Marshal(schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 12})
						raw = string(b)
					}
					_, _ = w.Write([]byte(completion(raw)))
				default:
					t.Error("unexpected RPC")
				}
			}))
			defer srv.Close()
			source := lexicon.Extract("12 tracks with warm timbre")
			var observations []ports.ParseAttemptObservation
			in := lexicon.PrepareParsingContext(ports.IntentInput{Prompt: source.OriginalText, SourceFacts: &source, EnrichParsingContext: true, ObserveParseAttempt: func(o ports.ParseAttemptObservation) { observations = append(observations, o) }})
			before, _ := json.Marshal(in.SourceFacts)
			got, err := NewClientWithContext(srv.URL, window).Parse(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(in.SourceFacts)
			if string(before) != string(after) || len(observations) != 2 || !strings.Contains(measured[1], "Validation feedback") || observations[0].Error == "" {
				t.Fatal("retry snapshot or observation lost", observations)
			}
			if !reflect.DeepEqual(got.Translation.ParsingContext.UsedHintIndexes, observations[1].UsedHintIndexes) {
				t.Fatal("successful subset not saved")
			}
			if requests[0].NPredict != 1800 || requests[1].NPredict != min(2400, window-1700-128) {
				t.Fatal("optional hints reduced output")
			}
			if observations[0].OptionalByteBound == 0 || (window == 4096 && observations[1].OptionalByteBound != 0) {
				t.Fatal("unexpected admission", observations)
			}
			got.Translation.ParsingContext.Hints[0].Text = "changed"
			if in.SourceFacts.ParsingContext.Hints[0].Text == "changed" {
				t.Fatal("returned translation aliases source")
			}
		})
	}
}

func TestAnchorPayloadExcludesParsingContext(t *testing.T) {
	var payloads []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		payloads = append(payloads, req.Messages[1].Content)
		_, _ = w.Write([]byte(completion(`[]`)))
	}))
	defer srv.Close()
	p := NewWithClient(NewClient(srv.URL))
	source := lexicon.Extract("warm timbre")
	intent := core.MusicIntent{OriginalDescription: source.OriginalText, Translation: &source}
	if _, err := p.ProposeAnchors(context.Background(), intent, nil); err != nil {
		t.Fatal(err)
	}
	prepared := lexicon.PrepareParsingContext(ports.IntentInput{SourceFacts: &source, EnrichParsingContext: true})
	intent.Translation = prepared.SourceFacts
	if _, err := p.ProposeAnchors(context.Background(), intent, nil); err != nil {
		t.Fatal(err)
	}
	if payloads[0] != payloads[1] || intent.Translation.ParsingContext == nil {
		t.Fatal("anchor payload changed or evidence mutated")
	}
}

func TestAttemptObservationsRetainTruncationAndUnknownMeasurement(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{}`}, "finish_reason": "length"}}})
	}))
	defer srv.Close()
	var observations []ports.ParseAttemptObservation
	_, err := NewClient(srv.URL).Parse(context.Background(), ports.IntentInput{Prompt: "warm timbre", EnrichParsingContext: true, ObserveParseAttempt: func(o ports.ParseAttemptObservation) { observations = append(observations, o) }})
	if err == nil || len(observations) != 2 {
		t.Fatal("bounded retry not observed", err, observations)
	}
	for i, o := range observations {
		if o.Attempt != i+1 || !o.Truncated || o.MandatoryTokens != nil || o.OptionalByteBound != 0 || o.OmittedHints == 0 {
			t.Fatal("unknown budget/truncation lost", o)
		}
	}
}
