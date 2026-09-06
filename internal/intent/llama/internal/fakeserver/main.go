// Command fakeserver stands in for llama-server in tests: it accepts the flags
// the manager passes, serves /health and /v1/chat/completions, and returns a
// canned intent object derived from the request so round-trips can be asserted.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

func main() {
	model := flag.String("model", "", "")
	host := flag.String("host", "127.0.0.1", "")
	port := flag.Int("port", 8080, "")
	_ = flag.Int("ctx-size", 4096, "")
	_ = flag.Int("threads", 0, "")
	_ = flag.Int("n-gpu-layers", 0, "")
	_ = flag.String("device", "", "")
	flag.Parse()

	if *model == "" {
		fmt.Fprintln(os.Stderr, "fakeserver: --model required")
		os.Exit(2)
	}
	// Simulate a brief load before becoming healthy.
	healthyAt := time.Now().Add(300 * time.Millisecond)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		if time.Now().Before(healthyAt) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var req struct {
			Messages []struct{ Role, Content string } `json:"messages"`
			Grammar  string                           `json:"grammar"`
		}
		_ = json.Unmarshal(body, &req)

		var prompt string
		for _, m := range req.Messages {
			if m.Role == "user" {
				prompt = m.Content
			}
		}

		seeds := "[]"
		if m := regexp.MustCompile(`(?i)like ([A-Za-z][\w ]*)`).FindStringSubmatch(prompt); m != nil {
			seeds = fmt.Sprintf("[%q]", strings.TrimSpace(m[1]))
		}
		intent := fmt.Sprintf(
			`{"seeds":%s,"mode":"similar","count":18,"creativity":0.5,"noise":0.1,"lookback":3,"exclude_artists":[],"no_repeat_artist":true,"notes":%q}`,
			seeds, "fake: "+truncate(prompt, 80))

		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": intent}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	addr := fmt.Sprintf("%s:%d", *host, *port)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "fakeserver:", err)
		os.Exit(1)
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) > n {
		return s[:n]
	}
	return s
}
