package llama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/schema"
	"github.com/platten/playlistai/internal/ports"
)

func TestClientRetriesReuseSuppliedSourceFacts(t *testing.T) {
	prompt := "12 songs like Nine Inch Nails; include Hurt by Nine Inch Nails exactly once."
	source := lexicon.Extract(prompt)
	source.Version = "request-snapshot/v1"
	before, _ := json.Marshal(source)
	var requests []chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requests = append(requests, request)
		w.Header().Set("Content-Type", "application/json")
		content := "invalid interpretation"
		if len(requests) == 2 {
			raw, _ := json.Marshal(schema.Wire{Genres: []schema.WirePreference{}, Mode: "similar", TotalCount: 30})
			content = string(raw)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	m, err := NewClient(server.URL).Parse(context.Background(), ports.IntentInput{Prompt: prompt, SourceFacts: &source})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || m.Translation.Version != source.Version || m.Count != 12 || len(m.RequiredTracks) != 1 {
		t.Fatalf("snapshot not preserved across retry: calls=%d intent=%+v", len(requests), m)
	}
	first := requests[0].Messages[len(requests[0].Messages)-1].Content
	second := requests[1].Messages[len(requests[1].Messages)-1].Content
	if !strings.HasPrefix(second, first+"\n\nValidation feedback") {
		t.Fatal("retry changed prompt or source facts")
	}
	after, _ := json.Marshal(source)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("retry mutated source snapshot")
	}
}
