package musicgraph

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
)

// Reader is immutable and safe for concurrent requests. Every lookup is local;
// network refresh is an explicit, separate Client operation.
type Reader struct {
	hash           string
	prepared       Snapshot
	artists        map[string]Artist
	recordings     map[string]Recording
	top            map[string][]string
	neighbors      map[string][]Neighbor
	listenerCounts []int64
}

func Open(ctx context.Context, path, expectedSHA256 string) (*Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validHash(expectedSHA256) {
		return nil, errors.New("expected music graph SHA256 required")
	}
	if info, err := os.Lstat(path); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() || info.Size() > MaxSnapshotBytes {
		return nil, errors.New("music graph must be a bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxSnapshotBytes {
		return nil, errors.New("music graph must be a bounded regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxSnapshotBytes+1))
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if len(b) > MaxSnapshotBytes || digest(b) != expectedSHA256 {
		return nil, errors.New("music graph hash or size mismatch")
	}
	s, err := Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	r := &Reader{hash: expectedSHA256, prepared: s, artists: map[string]Artist{}, recordings: map[string]Recording{}, top: map[string][]string{}, neighbors: map[string][]Neighbor{}}
	for _, a := range s.Artists {
		r.artists[a.MBID] = a
	}
	for _, v := range s.Recordings {
		r.recordings[v.MBID] = v
		if v.UniqueListeners != nil {
			r.listenerCounts = append(r.listenerCounts, *v.UniqueListeners)
		}
	}
	slices.Sort(r.listenerCounts)
	for _, v := range s.TopRecordings {
		r.top[v.ArtistMBID] = v.RecordingMBIDs
	}
	for _, v := range s.Neighbors {
		r.neighbors[v.SeedArtistMBID] = append(r.neighbors[v.SeedArtistMBID], v)
	}
	for id := range r.neighbors {
		slices.SortFunc(r.neighbors[id], func(a, b Neighbor) int {
			x, y := r.artists[a.ArtistMBID], r.artists[b.ArtistMBID]
			if c := compareCounts(x.Counts, y.Counts); c != 0 {
				return c
			}
			return strings.Compare(a.ArtistMBID, b.ArtistMBID)
		})
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Reader) SnapshotIdentity() string {
	if r == nil {
		return ""
	}
	return r.hash
}

type Info struct {
	Version          string    `json:"version"`
	PreparedAt       time.Time `json:"preparedAt"`
	Artists          int       `json:"artists"`
	Recordings       int       `json:"recordings"`
	OmittedDiscovery int       `json:"omittedDiscovery"`
}

func (r *Reader) Info() Info {
	if r == nil {
		return Info{}
	}
	return Info{Version: r.prepared.Version, PreparedAt: r.prepared.PreparedAt, Artists: len(r.prepared.Artists), Recordings: len(r.prepared.Recordings), OmittedDiscovery: len(r.prepared.OmittedDiscovery)}
}

// Manifest returns a deep copy of the public projection for inspection/export.
func (r *Reader) Manifest() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	s := r.prepared
	s.Artists = make([]Artist, len(r.prepared.Artists))
	for i, a := range r.prepared.Artists {
		a.Counts = cloneCounts(a.Counts)
		s.Artists[i] = a
	}
	s.Recordings = make([]Recording, len(r.prepared.Recordings))
	for i, v := range r.prepared.Recordings {
		s.Recordings[i] = cloneRecording(v)
	}
	s.TopRecordings = make([]ArtistRecordings, len(r.prepared.TopRecordings))
	for i, v := range r.prepared.TopRecordings {
		v.RecordingMBIDs = slices.Clone(v.RecordingMBIDs)
		s.TopRecordings[i] = v
	}
	s.Neighbors = make([]Neighbor, len(r.prepared.Neighbors))
	for i, v := range r.prepared.Neighbors {
		v.RecordingMBIDs = slices.Clone(v.RecordingMBIDs)
		s.Neighbors[i] = v
	}
	s.OmittedDiscovery = slices.Clone(r.prepared.OmittedDiscovery)
	for i, v := range s.OmittedDiscovery {
		if v.Source != nil {
			source := *v.Source
			s.OmittedDiscovery[i].Source = &source
		}
	}
	return s
}

func (r *Reader) LookupArtistPopularity(ctx context.Context, ids []string) (map[string]core.ArtistPopularity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validIDs(ids, MaxBatch) {
		return nil, errors.New("invalid artist popularity batch")
	}
	out := map[string]core.ArtistPopularity{}
	if r == nil {
		return out, nil
	}
	for _, id := range ids {
		if a, ok := r.artists[id]; ok {
			c := cloneCounts(a.Counts)
			out[id] = core.ArtistPopularity{Snapshot: r.hash, UniqueListeners: c.UniqueListeners, Listens: c.ListenCount}
		}
	}
	return out, ctx.Err()
}

func (r *Reader) Artist(ctx context.Context, id string) (Artist, bool) {
	if r == nil || ctx.Err() != nil {
		return Artist{}, false
	}
	v, ok := r.artists[id]
	v.Counts = cloneCounts(v.Counts)
	return v, ok
}
func (r *Reader) Recording(ctx context.Context, id string) (Recording, bool) {
	if r == nil || ctx.Err() != nil {
		return Recording{}, false
	}
	v, ok := r.recordings[id]
	return cloneRecording(v), ok
}

func (r *Reader) TopRecordings(ctx context.Context, artist string, limit int) []Recording {
	if r == nil || ctx.Err() != nil || limit <= 0 {
		return nil
	}
	ids := r.top[artist]
	out := make([]Recording, 0, min(len(ids), limit))
	for _, id := range ids {
		out = append(out, cloneRecording(r.recordings[id]))
	}
	slices.SortFunc(out, func(a, b Recording) int {
		if c := compareCounts(a.Counts, b.Counts); c != 0 {
			return c
		}
		return strings.Compare(a.MBID, b.MBID)
	})
	return out[:min(len(out), limit)]
}

func (r *Reader) SimilarArtists(ctx context.Context, artist string, limit int) []Neighbor {
	if r == nil || ctx.Err() != nil || limit <= 0 {
		return nil
	}
	v := r.neighbors[artist]
	out := slices.Clone(v[:min(len(v), limit)])
	for i := range out {
		out[i].RecordingMBIDs = slices.Clone(out[i].RecordingMBIDs)
	}
	return out
}

// PopularityRank is an ordinal within this prepared snapshot, not a probability
// or a global popularity score. Ties share a rank; absent listener counts abstain.
func (r *Reader) PopularityRank(ctx context.Context, id string) (float64, bool) {
	v, ok := r.Recording(ctx, id)
	if !ok || v.UniqueListeners == nil || len(r.listenerCounts) < 2 {
		return 0, false
	}
	index, _ := slices.BinarySearch(r.listenerCounts, *v.UniqueListeners)
	return float64(index) / float64(len(r.listenerCounts)-1), true
}

func cloneCounts(c Counts) Counts {
	if c.UniqueListeners != nil {
		n := *c.UniqueListeners
		c.UniqueListeners = &n
	}
	if c.ListenCount != nil {
		n := *c.ListenCount
		c.ListenCount = &n
	}
	return c
}
func cloneRecording(v Recording) Recording {
	v.Counts = cloneCounts(v.Counts)
	v.ArtistMBIDs = slices.Clone(v.ArtistMBIDs)
	if v.CountsSource != nil {
		source := *v.CountsSource
		v.CountsSource = &source
	}
	return v
}
func compareCounts(a, b Counts) int {
	for i, x := range []*int64{a.UniqueListeners, a.ListenCount} {
		y := []*int64{b.UniqueListeners, b.ListenCount}[i]
		if x == nil && y != nil {
			return 1
		}
		if x != nil && y == nil {
			return -1
		}
		if x != nil && y != nil {
			if *x > *y {
				return -1
			}
			if *x < *y {
				return 1
			}
		}
	}
	return 0
}
