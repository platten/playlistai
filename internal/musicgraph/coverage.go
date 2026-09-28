package musicgraph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	CatalogInventoryVersion           = "prepared-catalog-inventory/v1"
	MaxAdditionalInstalledBytes int64 = 10_000_000_000
	MaxInventoryBytes           int64 = 256 << 20
)

// CatalogInventory is an identity denominator, never an evaluation prompt set.
// Genres are source tags used only to balance preparation work, not verified
// recording attributes. License is supplied by the producer, never inferred.
type CatalogInventory struct {
	Version       string            `json:"version"`
	CatalogSHA256 string            `json:"catalogSha256"`
	Source        string            `json:"source"`
	License       string            `json:"license"`
	Tracks        []CatalogIdentity `json:"tracks"`
}

type CatalogIdentity struct {
	ID            string   `json:"id"`
	RecordingMBID string   `json:"recordingMbid,omitempty"`
	ArtistMBIDs   []string `json:"artistMbids,omitempty"`
	Genres        []string `json:"genres,omitempty"`
	CLAP          bool     `json:"clap,omitempty"`
	MERT          bool     `json:"mert,omitempty"`
	Classifier    bool     `json:"classifier,omitempty"`
}

func (c CatalogInventory) Validate() error {
	if c.Version != CatalogInventoryVersion || !validHash(c.CatalogSHA256) || strings.TrimSpace(c.Source) == "" || strings.TrimSpace(c.License) == "" || len(c.Tracks) > 5_000_000 {
		return errors.New("invalid catalog inventory identity, source terms or size")
	}
	seen := make(map[string]bool, len(c.Tracks))
	for _, row := range c.Tracks {
		if strings.TrimSpace(row.ID) == "" || len(row.ID) > 1024 || seen[row.ID] || row.RecordingMBID != "" && !validID(row.RecordingMBID) || !validIDs(row.ArtistMBIDs, 100) || len(row.Genres) > 100 {
			return errors.New("invalid or duplicate catalog identity")
		}
		seen[row.ID] = true
		for _, genre := range row.Genres {
			if strings.TrimSpace(genre) == "" || len(genre) > 256 {
				return errors.New("invalid catalog genre stratum")
			}
		}
	}
	return nil
}

func DecodeInventory(r io.Reader) (CatalogInventory, error) {
	var out CatalogInventory
	b, err := io.ReadAll(io.LimitReader(r, MaxInventoryBytes+1))
	if err != nil {
		return out, err
	}
	if int64(len(b)) > MaxInventoryBytes {
		return out, errors.New("catalog inventory exceeds size limit")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return out, err
	}
	if d.Decode(new(any)) != io.EOF {
		return out, errors.New("trailing catalog inventory data")
	}
	return out, out.Validate()
}

func WriteInventory(ctx context.Context, path string, inventory CatalogInventory) error {
	if err := inventory.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(inventory)
	if err != nil {
		return err
	}
	if int64(len(b)) > MaxInventoryBytes {
		return errors.New("catalog inventory exceeds size limit")
	}
	return writeNewBytes(ctx, path, b)
}

// PreparationSeeds visits every artist once, round-robin across the least
// populated source-genre strata first. Ordering is independent of input rows,
// listening counts and development prompts. Missing genre remains a stratum.
func (c CatalogInventory) PreparationSeeds() ([]string, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	groups := map[string]map[string]bool{}
	for _, row := range c.Tracks {
		genres := row.Genres
		if len(genres) == 0 {
			genres = []string{""}
		}
		for _, genre := range genres {
			key := strings.ToLower(strings.TrimSpace(genre))
			if groups[key] == nil {
				groups[key] = map[string]bool{}
			}
			for _, artist := range row.ArtistMBIDs {
				groups[key][artist] = true
			}
		}
	}
	keys := make([]string, 0, len(groups))
	ordered := map[string][]string{}
	for key, group := range groups {
		keys = append(keys, key)
		for artist := range group {
			ordered[key] = append(ordered[key], artist)
		}
		slices.Sort(ordered[key])
	}
	slices.SortFunc(keys, func(a, b string) int {
		if len(groups[a]) != len(groups[b]) {
			return len(groups[a]) - len(groups[b])
		}
		return strings.Compare(a, b)
	})
	var out []string
	seen := map[string]bool{}
	for round := 0; ; round++ {
		remaining := false
		for _, key := range keys {
			if round >= len(ordered[key]) {
				continue
			}
			remaining = true
			artist := ordered[key][round]
			if !seen[artist] {
				out = append(out, artist)
				seen[artist] = true
			}
		}
		if !remaining {
			break
		}
	}
	return out, nil
}

type IdentityCoverage struct {
	CatalogSHA256            string          `json:"catalogSha256"`
	SnapshotSHA256           string          `json:"snapshotSha256"`
	CatalogTracks            int             `json:"catalogTracks"`
	MissingRecordingIdentity int             `json:"missingRecordingIdentity"`
	UniqueRecordings         int             `json:"uniqueRecordings"`
	JoinedRecordings         int             `json:"joinedRecordings"`
	JoinedTracks             int             `json:"joinedTracks"`
	ConflictingTracks        int             `json:"conflictingTracks"`
	CatalogArtists           int             `json:"catalogArtists"`
	PreparedArtists          int             `json:"preparedArtists"`
	CLAPTracks               int             `json:"clapTracks"`
	MERTTracks               int             `json:"mertTracks"`
	ClassifierTracks         int             `json:"classifierTracks"`
	Genres                   []GenreCoverage `json:"genres"`
}

type GenreCoverage struct {
	Genre        string `json:"genre"`
	Tracks       int    `json:"tracks"`
	JoinedTracks int    `json:"joinedTracks"`
}

// Coverage joins only canonical recording IDs. A conflicting known performer
// credit prevents the join; a matching title or artist name never creates one.
// Audio feature counts describe the input catalog, not graph musical accuracy.
func (r *Reader) Coverage(ctx context.Context, catalog CatalogInventory) (IdentityCoverage, error) {
	out := IdentityCoverage{CatalogSHA256: catalog.CatalogSHA256, SnapshotSHA256: r.SnapshotIdentity()}
	if err := catalog.Validate(); err != nil {
		return out, err
	}
	if r == nil {
		return out, errors.New("prepared graph required")
	}
	recordings, joined, artists := map[string]bool{}, map[string]bool{}, map[string]bool{}
	genres := map[string]GenreCoverage{}
	for _, row := range catalog.Tracks {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		out.CatalogTracks++
		if row.CLAP {
			out.CLAPTracks++
		}
		if row.MERT {
			out.MERTTracks++
		}
		if row.Classifier {
			out.ClassifierTracks++
		}
		for _, artist := range row.ArtistMBIDs {
			artists[artist] = true
		}
		if row.RecordingMBID == "" {
			out.MissingRecordingIdentity++
		} else {
			recordings[row.RecordingMBID] = true
		}
		recording, matched := r.recordings[row.RecordingMBID]
		if matched && len(row.ArtistMBIDs) > 0 {
			// A source may list one performer from a multi-artist recording;
			// require all graph credits to be in the catalog recording credits.
			for _, artist := range recording.ArtistMBIDs {
				if !slices.Contains(row.ArtistMBIDs, artist) {
					matched = false
					out.ConflictingTracks++
					break
				}
			}
		}
		if matched {
			out.JoinedTracks++
			joined[row.RecordingMBID] = true
		}
		values := row.Genres
		if len(values) == 0 {
			values = []string{""}
		}
		seenGenres := map[string]bool{}
		for _, genre := range values {
			key := strings.ToLower(strings.TrimSpace(genre))
			if seenGenres[key] {
				continue
			}
			seenGenres[key] = true
			entry := genres[key]
			entry.Genre = key
			entry.Tracks++
			if matched {
				entry.JoinedTracks++
			}
			genres[key] = entry
		}
	}
	out.UniqueRecordings, out.JoinedRecordings, out.CatalogArtists = len(recordings), len(joined), len(artists)
	for artist := range artists {
		if _, ok := r.artists[artist]; ok {
			out.PreparedArtists++
		}
	}
	for _, entry := range genres {
		out.Genres = append(out.Genres, entry)
	}
	slices.SortFunc(out.Genres, func(a, b GenreCoverage) int { return strings.Compare(a.Genre, b.Genre) })
	return out, ctx.Err()
}

// CheckAdditionalDataBudget is shared by offline preparation and installers.
// Callers count installed derived assets, including retained pinned generations;
// compressed download size cannot stand in for expanded installed size.
func CheckAdditionalDataBudget(existingBytes int64, additions ...int64) error {
	if existingBytes < 0 || existingBytes > MaxAdditionalInstalledBytes {
		return errors.New("invalid existing prepared-data size or 10 GB installed-data ceiling exceeded")
	}
	for _, size := range additions {
		if size < 0 || size > MaxAdditionalInstalledBytes-existingBytes {
			return fmt.Errorf("prepared data exceeds %d-byte additional installed-data ceiling", MaxAdditionalInstalledBytes)
		}
		existingBytes += size
	}
	return nil
}
