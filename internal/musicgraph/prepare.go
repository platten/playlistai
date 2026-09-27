package musicgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// FetchTopRecordings prefers authenticated aggregate top recordings, falling
// back to public radio suggestions when disconnected or authentication fails.
func (c *Client) FetchTopRecordings(ctx context.Context, artist string) ([]Recording, Source, error) {
	if c.token != nil && c.token() != "" {
		if rows, source, err := c.fetchAuthenticatedTop(ctx, artist); err == nil {
			return rows, source, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, Source{}, err
		}
	}
	batch, err := c.fetchRadio(ctx, artist)
	if err != nil {
		return nil, Source{}, err
	}
	var out []Recording
	for _, r := range batch.recordings {
		if slices.Contains(batch.top.RecordingMBIDs, r.MBID) {
			out = append(out, r)
		}
	}
	sortRecordings(out)
	return out, batch.top.Source, ctx.Err()
}
func (c *Client) FetchNeighbors(ctx context.Context, seed string) ([]Neighbor, []Recording, error) {
	batch, err := c.fetchRadio(ctx, seed)
	if err != nil {
		return nil, nil, err
	}
	var out []Recording
	for _, r := range batch.recordings {
		if !slices.Contains(batch.top.RecordingMBIDs, r.MBID) {
			out = append(out, r)
		}
	}
	return batch.neighbors, out, ctx.Err()
}

type radioBatch struct {
	top        ArtistRecordings
	neighbors  []Neighbor
	recordings []Recording
}

func (c *Client) fetchRadio(ctx context.Context, seed string) (radioBatch, error) {
	out := radioBatch{}
	if !validID(seed) {
		return out, errors.New("invalid seed artist MBID")
	}
	b, source, err := c.requestAttempts(ctx, radioURL(seed), nil, 1)
	if err != nil {
		return out, err
	}
	out.top = ArtistRecordings{ArtistMBID: seed, Source: source}
	var groups map[string][]struct {
		ID      string `json:"recording_mbid"`
		Artist  string `json:"similar_artist_mbid"`
		Listens *int64 `json:"total_listen_count"`
	}
	if err = json.Unmarshal(b, &groups); err != nil {
		return out, errors.New("invalid artist radio JSON")
	}
	if len(groups) > MaxNeighbors+1 {
		return out, errors.New("too many radio artists")
	}
	artists := map[string]bool{}
	seenRecordings := map[string]bool{}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, key := range keys {
		rows := groups[key]
		if len(rows) == 0 {
			continue
		}
		if len(rows) > 10 {
			return out, errors.New("too many radio recordings")
		}
		artist := rows[0].Artist
		if !validID(artist) || artists[artist] || key != artist {
			return out, errors.New("invalid radio artist identity")
		}
		artists[artist] = true
		neighbor := Neighbor{SeedArtistMBID: seed, ArtistMBID: artist, Source: source}
		for _, v := range rows {
			counts := Counts{ListenCount: v.Listens}
			if !validID(v.ID) || v.Artist != artist || !counts.valid() {
				return out, fmt.Errorf("invalid radio recording identity or count in artist group %s", artist)
			}
			if seenRecordings[v.ID] {
				return out, fmt.Errorf("duplicate radio recording %s in artist group %s", v.ID, artist)
			}
			seenRecordings[v.ID] = true
			if artist == seed {
				out.top.RecordingMBIDs = append(out.top.RecordingMBIDs, v.ID)
			} else {
				neighbor.RecordingMBIDs = append(neighbor.RecordingMBIDs, v.ID)
			}
			out.recordings = append(out.recordings, Recording{MBID: v.ID, ArtistMBIDs: []string{artist}, Counts: counts, Source: source})
		}
		if artist != seed {
			slices.Sort(neighbor.RecordingMBIDs)
			out.neighbors = append(out.neighbors, neighbor)
		}
	}
	slices.Sort(out.top.RecordingMBIDs)
	slices.SortFunc(out.neighbors, func(a, b Neighbor) int { return strings.Compare(a.ArtistMBID, b.ArtistMBID) })
	return out, ctx.Err()
}

func sortRecordings(rows []Recording) {
	slices.SortFunc(rows, func(a, b Recording) int {
		if c := compareCounts(a.Counts, b.Counts); c != 0 {
			return c
		}
		return strings.Compare(a.MBID, b.MBID)
	})
}

// Prepare uses one public radio lookup per seed, then public artist and recording
// aggregate batches. Its seed group supplies bounded candidate suggestions;
// aggregate counts order only those returned suggestions. No authentication-only
// endpoint is tried. Invalid/unavailable radio batches are explicitly omitted;
// aggregate failures or parent cancellation prevent publication.
func (c *Client) Prepare(ctx context.Context, seeds []string) (Snapshot, error) {
	s := Snapshot{Version: Version}
	if len(seeds) == 0 || !validIDs(seeds, MaxBatch) {
		return s, errors.New("invalid preparation seeds")
	}
	seeds = slices.Clone(seeds)
	slices.Sort(seeds)
	artists := map[string]bool{}
	recordings := map[string]Recording{}
	for _, id := range seeds {
		if err := ctx.Err(); err != nil {
			return s, err
		}
		artists[id] = true
		batch, err := c.fetchRadio(ctx, id)
		if ctx.Err() != nil {
			return s, ctx.Err()
		}
		if err != nil {
			omission := DiscoveryOmission{ArtistMBID: id, Reason: "unavailable"}
			if batch.top.Source.valid() {
				omission.Reason = "invalid_response"
				source := batch.top.Source
				omission.Source = &source
			}
			s.OmittedDiscovery = append(s.OmittedDiscovery, omission)
			continue
		}
		conflict := false
		for _, v := range batch.recordings {
			if old, ok := recordings[v.MBID]; ok && !slices.Equal(old.ArtistMBIDs, v.ArtistMBIDs) {
				conflict = true
				break
			}
		}
		if conflict {
			source := batch.top.Source
			s.OmittedDiscovery = append(s.OmittedDiscovery, DiscoveryOmission{ArtistMBID: id, Reason: "conflicting_recording_artist", Source: &source})
			continue
		}
		s.TopRecordings = append(s.TopRecordings, batch.top)
		s.Neighbors = append(s.Neighbors, batch.neighbors...)
		for _, v := range batch.recordings {
			for _, artist := range v.ArtistMBIDs {
				artists[artist] = true
			}
			if _, ok := recordings[v.MBID]; !ok {
				recordings[v.MBID] = v
			}
		}
	}
	artistIDs := make([]string, 0, len(artists))
	for id := range artists {
		artistIDs = append(artistIDs, id)
	}
	slices.Sort(artistIDs)
	for start := 0; start < len(artistIDs); start += MaxBatch {
		rows, err := c.FetchArtists(ctx, artistIDs[start:min(start+MaxBatch, len(artistIDs))])
		if err != nil {
			return s, err
		}
		s.Artists = append(s.Artists, rows...)
	}
	recordingIDs := make([]string, 0, len(recordings))
	for id := range recordings {
		recordingIDs = append(recordingIDs, id)
	}
	slices.Sort(recordingIDs)
	for start := 0; start < len(recordingIDs); start += MaxBatch {
		rows, source, err := c.FetchRecordingCounts(ctx, recordingIDs[start:min(start+MaxBatch, len(recordingIDs))])
		if err != nil {
			return s, err
		}
		for id, counts := range rows {
			r := recordings[id]
			r.Counts = counts
			r.CountsSource = &source
			recordings[id] = r
		}
	}
	for _, id := range recordingIDs {
		s.Recordings = append(s.Recordings, recordings[id])
	}
	s.PreparedAt = time.Now().UTC()
	return s, s.Validate()
}
