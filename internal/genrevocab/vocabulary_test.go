package genrevocab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchAndValidateOfficialVocabulary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "PlaylistAI/test" || r.URL.Query().Get("fmt") != "json" {
			t.Fatalf("missing MusicBrainz request contract: %s %+v", r.Header.Get("User-Agent"), r.URL.Query())
		}
		_, _ = w.Write([]byte(`{"genre-count":2,"genres":[{"id":"2","name":"Soul"},{"id":"1","name":"Liquid drum and bass"}]}`))
	}))
	defer server.Close()
	v, err := Fetch(context.Background(), server.Client(), server.URL, "PlaylistAI/test", time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Genres) != 2 || v.Genres[0].Name != "Liquid drum and bass" || v.ContentSHA256 == "" {
		t.Fatalf("unexpected vocabulary: %+v", v)
	}
	v.Genres[0].Name = "damaged"
	if v.Validate() == nil {
		t.Fatal("damaged vocabulary accepted")
	}
}
