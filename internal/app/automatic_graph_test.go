package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicgraph"
	"github.com/platten/playlistai/internal/ports"
)

type automaticGraphCatalog struct {
	ports.Catalog
	ports.LibraryMetadataCatalog
	metadata      map[string]core.EnrichedTrack
	metadataError map[string]error
	metadataReads []string
	indexReads    []string
}

func (c *automaticGraphCatalog) LibraryRecordingMetadata(ctx context.Context, id string) (core.EnrichedTrack, bool, error) {
	c.metadataReads = append(c.metadataReads, id)
	if err := ctx.Err(); err != nil {
		return core.EnrichedTrack{}, false, err
	}
	v, ok := c.metadata[id]
	return v, ok, c.metadataError[id]
}

func (c *automaticGraphCatalog) RecordingsByMBID(ctx context.Context, id string, limit int) ([]core.TrackRef, error) {
	c.indexReads = append(c.indexReads, id)
	if limit != 1 {
		return nil, fmt.Errorf("unexpected recording lookup limit %d", limit)
	}
	return []core.TrackRef{{ID: "pack:" + id, RecordingIdentity: "musicbrainz:" + id}}, ctx.Err()
}

func automaticGraphFixture(t *testing.T) (*musicgraph.Reader, []string) {
	t.Helper()
	stamp := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	source := func(url string) musicgraph.Source {
		return musicgraph.Source{Provider: "listenbrainz", URL: url, RetrievedAt: stamp, ResponseSHA256: strings.Repeat("a", 64), License: "CC0-1.0"}
	}
	const api = "https://api.listenbrainz.org"
	snapshot := musicgraph.Snapshot{Version: musicgraph.Version, PreparedAt: stamp}
	var artists, recordings []string
	for i := 1; i <= 10; i++ {
		artist := fmt.Sprintf("%08x-1111-4111-8111-111111111111", i+10)
		recording := fmt.Sprintf("%08x-2222-4222-8222-222222222222", i)
		artists, recordings = append(artists, artist), append(recordings, recording)
		top := source(api + "/1/popularity/top-recordings-for-artist/" + artist)
		snapshot.Artists = append(snapshot.Artists, musicgraph.Artist{MBID: artist, Source: source(api + "/1/popularity/artist")})
		snapshot.Recordings = append(snapshot.Recordings, musicgraph.Recording{MBID: recording, ArtistMBIDs: []string{artist}, Source: top})
		snapshot.TopRecordings = append(snapshot.TopRecordings, musicgraph.ArtistRecordings{ArtistMBID: artist, RecordingMBIDs: []string{recording}, Source: top})
	}
	radio := source(api + "/1/lb-radio/artist/" + artists[0] + "?max_recordings_per_artist=10&max_similar_artists=20&mode=easy&pop_begin=0&pop_end=100")
	snapshot.Neighbors = []musicgraph.Neighbor{{SeedArtistMBID: artists[0], ArtistMBID: artists[1], RecordingMBIDs: []string{recordings[1]}, Source: radio}}
	path := filepath.Join(t.TempDir(), "graph.json")
	hash, err := musicgraph.Write(context.Background(), path, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := musicgraph.Open(context.Background(), path, hash)
	if err != nil {
		t.Fatal(err)
	}
	return reader, artists
}

func TestAutomaticGraphExactTrackReference(t *testing.T) {
	graph, artists := automaticGraphFixture(t)
	for _, test := range []struct {
		name string
		want int
	}{
		{"exact track credits", 2},
		{"duplicate uppercase credits", 2},
		{"wrong recording", 0},
		{"unmatched metadata", 0},
		{"ambiguous metadata", 0},
		{"missing metadata", 0},
		{"malformed artist IDs", 0},
		{"artist names only", 0},
		{"grounding artist ID only", 0},
		{"negative reference", 0},
		{"missing catalog track ID", 0},
		{"inferred only", 0},
		{"required only", 0},
		{"endpoint and waypoint", 2},
		{"duplicate explicit references", 2},
		{"existing artist reference", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			ref := core.IntentReference{Kind: core.ReferenceTrack, Query: "Same Name - Song", TrackID: "seed", Influence: core.InfluencePositive}
			recording := core.EnrichedTrack{Ref: core.TrackRef{ID: "seed", Artist: "Same Name", Title: "Song"}, Matched: true, IdentityStatus: core.ResolutionResolved, ArtistIDs: []string{artists[0]}}
			intent := core.MusicIntent{References: []core.IntentReference{ref}, PreparedMusicSnapshot: graph.SnapshotIdentity()}
			cat := &automaticGraphCatalog{metadata: map[string]core.EnrichedTrack{}}
			switch test.name {
			case "duplicate uppercase credits":
				recording.ArtistIDs = []string{artists[0], strings.ToUpper(artists[0]), artists[0]}
			case "wrong recording":
				recording.Ref.ID = "different-recording"
			case "unmatched metadata":
				recording.Matched = false
			case "ambiguous metadata":
				recording.IdentityStatus = core.ResolutionAmbiguous
			case "malformed artist IDs":
				recording.ArtistIDs = []string{"Same Name", "artist:" + artists[0], artists[0] + ";" + artists[1]}
			case "artist names only", "grounding artist ID only":
				recording.ArtistIDs = nil
				recording.AllArtists = []string{artists[0], "Same Name"}
				if test.name == "grounding artist ID only" {
					intent.References[0].Grounding = &core.IdentityGrounding{Candidates: []core.IdentityCandidate{{Kind: core.ReferenceTrack, ID: "recording", ArtistID: artists[0], Name: "Same Name"}}}
				}
			case "negative reference":
				intent.References[0].Influence = core.InfluenceNegative
			case "missing catalog track ID":
				intent.References[0].TrackID = ""
			case "inferred only":
				intent.References = nil
				intent.InferredAnchors = []core.InferredAnchor{{Reference: ref}}
			case "required only":
				intent.References = nil
				intent.RequiredTracks = []core.IntentReference{ref}
			case "endpoint and waypoint":
				intent.References = nil
				intent.Start, intent.Destination = &ref, &ref
				intent.Journey.Waypoints = []core.IntentReference{ref}
			case "duplicate explicit references":
				intent.References = append(intent.References, ref)
			case "existing artist reference":
				intent.References[0].Kind = core.ReferenceArtist
				intent.References[0].Grounding = &core.IdentityGrounding{Candidates: []core.IdentityCandidate{{Kind: core.ReferenceArtist, ID: artists[0], Name: "Same Name"}}}
			}
			if test.name != "missing metadata" {
				cat.metadata["seed"] = recording
			}
			before, _ := json.Marshal(intent)
			got, err := (preparedGraphRetriever{graph: graph, catalog: cat}).Retrieve(context.Background(), ports.RetrievalRequest{Intent: intent})
			if err != nil || len(got) != test.want {
				t.Fatalf("candidates=%+v want=%d err=%v", got, test.want, err)
			}
			if test.want > 0 {
				if len(cat.indexReads) != 2 || got[0].Sources[0].Channel != "musicgraph_artist" || got[0].Sources[0].QueryID != artists[0] || got[1].Sources[0].Channel != "musicgraph_neighbor" || got[1].Sources[0].QueryID != artists[0]+":"+artists[1] {
					t.Fatalf("wrong graph provenance or duplicate expansion: %+v reads=%v", got, cat.indexReads)
				}
			}
			after, _ := json.Marshal(intent)
			if string(before) != string(after) {
				t.Fatal("retrieval mutated the reference intent")
			}
			if (test.name == "inferred only" || test.name == "required only" || test.name == "negative reference" || test.name == "existing artist reference") && len(cat.metadataReads) != 0 {
				t.Fatalf("unexpected recording metadata read: %v", cat.metadataReads)
			}
		})
	}
}

func TestAutomaticGraphTrackCreditsBoundAndCancellation(t *testing.T) {
	graph, artists := automaticGraphFixture(t)
	credits := slices.Clone(artists)
	slices.Reverse(credits)
	credits = append(credits, artists[0])
	cat := &automaticGraphCatalog{metadata: map[string]core.EnrichedTrack{"seed": {Ref: core.TrackRef{ID: "seed"}, Matched: true, IdentityStatus: core.ResolutionResolved, ArtistIDs: credits}}}
	ref := core.IntentReference{Kind: core.ReferenceTrack, TrackID: "seed", Influence: core.InfluencePositive}
	intent := core.MusicIntent{References: []core.IntentReference{ref}}
	retriever := preparedGraphRetriever{graph: graph, catalog: cat}
	got, err := retriever.Retrieve(context.Background(), ports.RetrievalRequest{Intent: intent})
	var expanded []string
	for _, candidate := range got {
		if candidate.Sources[0].Channel == "musicgraph_artist" {
			expanded = append(expanded, candidate.Sources[0].QueryID)
		}
	}
	if err != nil || !reflect.DeepEqual(expanded, artists[:8]) || len(got) != 9 || !reflect.DeepEqual(cat.metadata["seed"].ArtistIDs, credits) {
		t.Fatalf("unbounded or mutable credits: artists=%v count=%d err=%v", expanded, len(got), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cat.metadataReads, cat.indexReads = nil, nil
	got, err = retriever.Retrieve(ctx, ports.RetrievalRequest{Intent: intent})
	if !errors.Is(err, context.Canceled) || len(got) != 0 || len(cat.metadataReads) != 0 || len(cat.indexReads) != 0 {
		t.Fatal("canceled retrieval performed reads", got, err, cat.metadataReads, cat.indexReads)
	}
	// A later failed metadata read preserves completed graph candidates and
	// propagates the error; it never converts a failed read into an empty success.
	recording := cat.metadata["seed"]
	recording.ArtistIDs = artists[:1]
	cat.metadata["seed"] = recording
	cat.metadataError = map[string]error{"failed": context.DeadlineExceeded}
	intent.References = append(intent.References, core.IntentReference{Kind: core.ReferenceTrack, TrackID: "failed", Influence: core.InfluencePositive})
	got, err = retriever.Retrieve(context.Background(), ports.RetrievalRequest{Intent: intent})
	if !errors.Is(err, context.DeadlineExceeded) || len(got) != 2 {
		t.Fatalf("partial graph candidates or error lost: count=%d err=%v", len(got), err)
	}
}

type automaticRelatedCatalog struct {
	*automaticGraphCatalog
	relatedArtist string
}

func (c *automaticRelatedCatalog) ArtistRecordingsByMBID(ctx context.Context, artist string, limit int) ([]core.TrackRef, error) {
	if artist != c.relatedArtist {
		return nil, ctx.Err()
	}
	if limit != 32 {
		return nil, fmt.Errorf("unexpected artist lookup %s/%d", artist, limit)
	}
	return []core.TrackRef{{ID: "pack:outside-radio-nominations", Artist: "Compound & Related Artist", RecordingIdentity: "musicbrainz:ffffffff-2222-4222-8222-222222222222"}}, ctx.Err()
}

func TestAutomaticGraphRetrievesRelatedArtistBeyondNominatedRecordings(t *testing.T) {
	graph, artists := automaticGraphFixture(t)
	cat := &automaticRelatedCatalog{automaticGraphCatalog: &automaticGraphCatalog{}, relatedArtist: artists[1]}
	ref := core.IntentReference{Kind: core.ReferenceArtist, Influence: core.InfluencePositive,
		Grounding: &core.IdentityGrounding{Candidates: []core.IdentityCandidate{{Kind: core.ReferenceArtist, ID: artists[0], Name: "Seed"}}}}
	candidates, err := (preparedGraphRetriever{graph: graph, catalog: cat}).Retrieve(context.Background(), ports.RetrievalRequest{Intent: core.MusicIntent{References: []core.IntentReference{ref}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if candidate.Track.ID == "pack:outside-radio-nominations" {
			source := candidate.Sources[0]
			if source.Channel != "musicgraph_neighbor" || source.QueryID != artists[0]+":"+artists[1] {
				t.Fatalf("lost artist relationship: %+v", source)
			}
			return
		}
	}
	t.Fatal("related artist recordings outside provider nomination list were lost")
}

type fairGraphCatalog struct {
	*automaticGraphCatalog
	slowArtist  string
	artistCalls []string
	overfull    bool
}

func (c *fairGraphCatalog) ArtistRecordingsByMBID(ctx context.Context, artist string, limit int) ([]core.TrackRef, error) {
	c.artistCalls = append(c.artistCalls, artist)
	if artist == c.slowArtist {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	count := min(limit, 2)
	if c.overfull {
		count = 1024
	}
	out := make([]core.TrackRef, count)
	for i := range out {
		out[i] = core.TrackRef{ID: fmt.Sprintf("pack:direct:%s:%d", artist, i), Artist: artist}
	}
	return out, ctx.Err()
}
func graphArtistReference(id string) core.IntentReference {
	return core.IntentReference{Kind: core.ReferenceArtist, Influence: core.InfluencePositive, Grounding: &core.IdentityGrounding{Candidates: []core.IdentityCandidate{{Kind: core.ReferenceArtist, ID: id, Name: id}}}}
}

func TestAutomaticGraphReservesDirectOpportunityAfterSlowFirstArtist(t *testing.T) {
	graph, artists := automaticGraphFixture(t)
	cat := &fairGraphCatalog{automaticGraphCatalog: &automaticGraphCatalog{}, slowArtist: artists[0]}
	intent := core.MusicIntent{References: []core.IntentReference{graphArtistReference(artists[0]), graphArtistReference(artists[1])}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	candidates, err := (preparedGraphRetriever{graph: graph, catalog: cat}).Retrieve(ctx, ports.RetrievalRequest{Intent: intent})
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("per-anchor timeout exhausted parent: %v parent=%v", err, ctx.Err())
	}
	if len(cat.artistCalls) < 2 || !reflect.DeepEqual(cat.artistCalls[:2], artists[:2]) {
		t.Fatal("second artist opportunity lost", cat.artistCalls)
	}
	found := false
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate.Track.ID, "pack:direct:"+artists[1]) && candidate.Sources[0].Channel == "musicgraph_artist" {
			found = true
			if candidate.Sources[0].QueryID != artists[1] || candidate.Sources[0].Channel != "musicgraph_artist" {
				t.Fatal("direct identity provenance lost")
			}
		}
	}
	if !found {
		t.Fatal("second artist starved by first timeout")
	}
}
func TestAutomaticGraphVisitsEveryAnchorBeforeRepeatingGraphJoins(t *testing.T) {
	graph, artists := automaticGraphFixture(t)
	cat := &fairGraphCatalog{automaticGraphCatalog: &automaticGraphCatalog{}}
	intent := core.MusicIntent{References: []core.IntentReference{graphArtistReference(artists[0]), graphArtistReference(artists[1])}}
	candidates, err := (preparedGraphRetriever{graph: graph, catalog: cat}).Retrieve(context.Background(), ports.RetrievalRequest{Intent: intent})
	if err != nil || len(candidates) < 4 {
		t.Fatal(candidates, err)
	}
	for i := 0; i < 4; i++ {
		if !strings.HasPrefix(candidates[i].Track.ID, "pack:direct:") {
			t.Fatal("graph join preceded direct artist opportunities")
		}
	}
	if len(cat.indexReads) < 2 || cat.indexReads[0] == cat.indexReads[1] {
		t.Fatal("first anchor drained before second", cat.indexReads)
	}
	cat.overfull = true
	candidates, err = (preparedGraphRetriever{graph: graph, catalog: cat}).Retrieve(context.Background(), ports.RetrievalRequest{Intent: intent})
	if err != nil || len(candidates) != 512 {
		t.Fatalf("global cap=%d err=%v", len(candidates), err)
	}
	counts := map[string]int{}
	for _, candidate := range candidates {
		counts[candidate.Sources[0].QueryID]++
	}
	if counts[artists[0]] != 256 || counts[artists[1]] != 256 {
		t.Fatal("per-anchor cap lost", counts)
	}
}
