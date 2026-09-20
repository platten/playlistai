// Package genrevocab prepares and reads the optional official MusicBrainz
// genre vocabulary used by the offline prompt recognizer.
package genrevocab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	Version         = "musicbrainz-genres/v1"
	DefaultEndpoint = "https://musicbrainz.org/ws/2/genre/all"
	MaxGenres       = 10000
	maxDocumentSize = 4 << 20
)

type Genre struct {
	MBID string `json:"mbid"`
	Name string `json:"name"`
}

type Vocabulary struct {
	Version       string  `json:"version"`
	Provider      string  `json:"provider"`
	Source        string  `json:"source"`
	RetrievedAt   string  `json:"retrievedAt"`
	License       string  `json:"license"`
	LicenseURL    string  `json:"licenseUrl"`
	ContentSHA256 string  `json:"contentSha256"`
	Genres        []Genre `json:"genres"`
}

func (v Vocabulary) Validate() error {
	if v.Version != Version || v.Provider != "MusicBrainz" || v.Source == "" || v.License != "CC0-1.0" || v.LicenseURL == "" {
		return errors.New("genre vocabulary metadata is incomplete")
	}
	if _, err := time.Parse(time.RFC3339, v.RetrievedAt); err != nil {
		return fmt.Errorf("genre vocabulary retrieval date: %w", err)
	}
	if len(v.Genres) == 0 || len(v.Genres) > MaxGenres {
		return errors.New("genre vocabulary has no usable genres")
	}
	seenID, seenName := map[string]bool{}, map[string]bool{}
	for _, genre := range v.Genres {
		name := strings.TrimSpace(genre.Name)
		id := strings.TrimSpace(genre.MBID)
		if name == "" || id == "" || seenID[id] || seenName[strings.ToLower(name)] {
			return errors.New("genre vocabulary contains an invalid or duplicate genre")
		}
		seenID[id], seenName[strings.ToLower(name)] = true, true
	}
	if !strings.EqualFold(v.ContentSHA256, contentHash(v.Genres)) {
		return errors.New("genre vocabulary content hash mismatch")
	}
	return nil
}

func Load(path string) (Vocabulary, error) {
	var v Vocabulary
	f, err := os.Open(path)
	if err != nil {
		return v, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, maxDocumentSize+1))
	if err = decoder.Decode(&v); err != nil {
		return v, err
	}
	if err = v.Validate(); err != nil {
		return v, err
	}
	return v, nil
}

func Write(path string, v Vocabulary) error {
	if err := v.Validate(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

// Fetch downloads the documented paginated MusicBrainz genre resource. It is
// intended for release preparation; runtime prompt parsing never calls it.
func Fetch(ctx context.Context, client *http.Client, endpoint, userAgent string, retrievedAt time.Time) (Vocabulary, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	base, err := url.Parse(endpoint)
	if err != nil {
		return Vocabulary{}, errors.New("genre vocabulary endpoint must use HTTPS")
	}
	loopback := base.Hostname() == "localhost" || base.Hostname() == "127.0.0.1" || base.Hostname() == "::1"
	if base.Scheme != "https" && (base.Scheme != "http" || !loopback) {
		return Vocabulary{}, errors.New("genre vocabulary endpoint must use HTTPS")
	}
	if strings.TrimSpace(userAgent) == "" {
		return Vocabulary{}, errors.New("MusicBrainz genre fetch requires a user agent")
	}
	var genres []Genre
	for offset := 0; ; offset += 100 {
		u := *base
		q := u.Query()
		q.Set("fmt", "json")
		q.Set("limit", "100")
		q.Set("offset", fmt.Sprint(offset))
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return Vocabulary{}, err
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := client.Do(req)
		if err != nil {
			return Vocabulary{}, err
		}
		var page struct {
			Count  int `json:"genre-count"`
			Genres []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"genres"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&page)
		closeErr := resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return Vocabulary{}, fmt.Errorf("MusicBrainz genre endpoint returned HTTP %d", resp.StatusCode)
		}
		if decodeErr != nil {
			return Vocabulary{}, decodeErr
		}
		if closeErr != nil {
			return Vocabulary{}, closeErr
		}
		for _, item := range page.Genres {
			genres = append(genres, Genre{MBID: strings.TrimSpace(item.ID), Name: strings.TrimSpace(item.Name)})
		}
		if len(genres) > MaxGenres {
			return Vocabulary{}, errors.New("MusicBrainz genre response exceeds limit")
		}
		if len(page.Genres) == 0 || len(genres) >= page.Count {
			break
		}
		// MusicBrainz requires clients to keep request rates modest. This path
		// is release tooling, so a one-second interval is preferable to retries.
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Vocabulary{}, ctx.Err()
		case <-timer.C:
		}
	}
	sort.Slice(genres, func(i, j int) bool {
		if strings.EqualFold(genres[i].Name, genres[j].Name) {
			return genres[i].MBID < genres[j].MBID
		}
		return strings.ToLower(genres[i].Name) < strings.ToLower(genres[j].Name)
	})
	v := Vocabulary{Version: Version, Provider: "MusicBrainz", Source: endpoint, RetrievedAt: retrievedAt.UTC().Format(time.RFC3339), License: "CC0-1.0", LicenseURL: "https://creativecommons.org/publicdomain/zero/1.0/", Genres: genres}
	v.ContentSHA256 = contentHash(v.Genres)
	return v, v.Validate()
}

func contentHash(genres []Genre) string {
	h := sha256.New()
	for _, genre := range genres {
		_, _ = io.WriteString(h, genre.MBID)
		_, _ = io.WriteString(h, "\x00")
		_, _ = io.WriteString(h, genre.Name)
		_, _ = io.WriteString(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}
