package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/musicgraph"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
)

// Prepared data is pinned by interpretation. Activating an update cannot alter
// the artist identities or priors halfway through a request.
func (c *Container) graphForIntent(ctx context.Context, intent core.MusicIntent) (*musicgraph.Reader, error) {
	if intent.PreparedMusicSnapshot == "" {
		return nil, nil
	}
	return c.PreparedMusicGraphSnapshot(ctx, intent.PreparedMusicSnapshot)
}

func (c *Container) prepareAutomaticFeatures(ctx context.Context, intent core.MusicIntent, cat ports.Catalog) error {
	local, ok := cat.(ports.LibrarySemanticCatalog)
	if !ok {
		return nil
	}
	service := c.AudioService()
	if service == nil || !service.ParityValidated || service.Analyzer == nil {
		return nil
	}
	queries, err := audio.EncodeClauseQueries(ctx, service.Analyzer, intent)
	if err != nil {
		return err
	}
	local.BindLibraryQueries(service.Analyzer.Identity(), queries)
	return ctx.Err()
}

func (c *Container) pinAutomaticRecommendationOverlay(ctx context.Context, intent core.MusicIntent, cat ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever) (multichannel.RequestOverlay, error) {
	overlay, err := c.pinDiscoveryRecommendationOverlay(ctx, intent, cat, resolver, retriever)
	if err != nil {
		return overlay, err
	}
	graph, err := c.graphForIntent(ctx, intent)
	if err != nil {
		if overlay.Release != nil {
			overlay.Release()
		}
		return multichannel.RequestOverlay{}, err
	}
	if graph != nil {
		overlay.PreparedRetriever = preparedGraphRetriever{graph: graph, catalog: overlay.Catalog}
		overlay.FamiliarityReader = func(ctx context.Context, ref core.TrackRef) (float64, bool) {
			meta, ok := ports.CatalogMeta(ctx, overlay.Catalog, ref.ID)
			if !ok {
				return 0, false
			}
			id := recordingMBID(meta)
			return graph.PopularityRank(ctx, id)
		}
	}
	return overlay, nil
}

func recordingMBID(meta core.TrackMeta) string {
	if id := librarypack.CanonicalMBID(meta.MusicBrainzRecording); id != "" {
		return id
	}
	id, _ := strings.CutPrefix(meta.Ref.RecordingIdentity, "musicbrainz:")
	return librarypack.CanonicalMBID(id)
}

type preparedGraphRetriever struct {
	graph   *musicgraph.Reader
	catalog ports.Catalog
}

func (r preparedGraphRetriever) Retrieve(ctx context.Context, request ports.RetrievalRequest) ([]core.Candidate, error) {
	indexed, hasRecordings := r.catalog.(ports.RecordingIdentityCatalog)
	artistsIndex, hasArtists := r.catalog.(ports.ArtistIdentityCatalog)
	if (!hasRecordings && !hasArtists) || r.graph == nil {
		return nil, ctx.Err()
	}
	refs := append([]core.IntentReference(nil), request.Intent.References...)
	if request.Intent.Start != nil {
		refs = append(refs, *request.Intent.Start)
	}
	if request.Intent.Destination != nil {
		refs = append(refs, *request.Intent.Destination)
	}
	refs = append(refs, request.Intent.Journey.Waypoints...)
	var anchors []string
	seen := map[string]bool{}
	var failures []error
	for i, ref := range refs {
		if ctx.Err() != nil {
			break
		}
		if ref.Influence == core.InfluenceNegative {
			continue
		}
		lookup, cancel := graphLookupContext(ctx, len(refs)-i)
		ids, err := r.graphArtistIDs(lookup, ref)
		cancel()
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				anchors = append(anchors, id)
			}
		}
	}
	if len(anchors) == 0 {
		return nil, errors.Join(append(failures, ctx.Err())...)
	}
	const limit = 512
	quota := max(1, limit/len(anchors))
	counts := make([]int, len(anchors))
	var candidates []core.Candidate
	add := func(owner int, tracks []core.TrackRef, channel, query string, rank int) {
		for _, track := range tracks {
			if len(candidates) >= limit || counts[owner] >= quota {
				return
			}
			candidates = append(candidates, core.Candidate{Track: track, Sources: []core.RetrievalEvidence{{Channel: channel, QueryID: query, Rank: rank, Score: 1 / float64(rank), QueryWeight: 1}}})
			counts[owner]++
		}
	}
	// Every requested identity receives a direct indexed page before optional
	// graph recording joins. One slow anchor cannot consume the whole deadline.
	if hasArtists {
		for i, artist := range anchors {
			if ctx.Err() != nil {
				break
			}
			lookup, cancel := graphLookupContext(ctx, len(anchors)-i)
			tracks, err := artistsIndex.ArtistRecordingsByMBID(lookup, artist, min(32, quota))
			cancel()
			if err != nil {
				failures = append(failures, err)
				continue
			}
			for rank, track := range tracks {
				add(i, []core.TrackRef{track}, "musicgraph_artist", artist, rank+1)
			}
		}
	}
	type graphJob struct {
		artist, recording, channel, query string
		rank                              int
	}
	jobs := make([][]graphJob, len(anchors))
	for i, artist := range anchors {
		if hasRecordings {
			for rank, recording := range r.graph.TopRecordings(ctx, artist, 20) {
				jobs[i] = append(jobs[i], graphJob{recording: recording.MBID, channel: "musicgraph_artist", query: artist, rank: rank + 1})
			}
		}
		for rank, neighbor := range r.graph.SimilarArtists(ctx, artist, 8) {
			query := artist + ":" + neighbor.ArtistMBID
			if hasArtists {
				jobs[i] = append(jobs[i], graphJob{artist: neighbor.ArtistMBID, channel: "musicgraph_neighbor", query: query, rank: rank + 1})
			}
			if hasRecordings {
				for j, recording := range neighbor.RecordingMBIDs[:min(len(neighbor.RecordingMBIDs), 8)] {
					jobs[i] = append(jobs[i], graphJob{recording: recording, channel: "musicgraph_neighbor", query: query, rank: j + 1})
				}
			}
		}
	}
	for round := 0; ctx.Err() == nil && len(candidates) < limit; round++ {
		added := false
		for owner := range anchors {
			if round >= len(jobs[owner]) || counts[owner] >= quota {
				continue
			}
			added = true
			job := jobs[owner][round]
			lookup, cancel := graphLookupContext(ctx, len(anchors))
			var tracks []core.TrackRef
			var err error
			if job.artist != "" {
				tracks, err = artistsIndex.ArtistRecordingsByMBID(lookup, job.artist, min(32, quota-counts[owner]))
			} else {
				tracks, err = indexed.RecordingsByMBID(lookup, job.recording, 1)
			}
			cancel()
			if err != nil {
				failures = append(failures, err)
			} else {
				add(owner, tracks, job.channel, job.query, job.rank)
			}
			if ctx.Err() != nil {
				break
			}
		}
		if !added {
			break
		}
	}
	return candidates, errors.Join(append(failures, ctx.Err())...)
}

func graphLookupContext(ctx context.Context, remaining int) (context.Context, context.CancelFunc) {
	budget := 500 * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		budget = min(budget, max(time.Duration(0), time.Until(deadline)/time.Duration(max(1, remaining)+1)))
	}
	return context.WithTimeout(ctx, budget)
}

// Track references keep their recording identity. Only their exact local
// recording credits can supply graph anchors; names and album artists cannot.
// These bounded reads happen during retrieval, before the evidence is frozen.
func (r preparedGraphRetriever) graphArtistIDs(ctx context.Context, ref core.IntentReference) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ref.Influence == core.InfluenceNegative {
		return nil, nil
	}
	if ref.Kind == core.ReferenceArtist {
		if artist, ok := automaticArtist(ref); ok {
			if id := librarypack.CanonicalMBID(artist.ID); id != "" {
				return []string{id}, nil
			}
		}
		return nil, nil
	}
	metadata, ok := r.catalog.(ports.LibraryMetadataCatalog)
	if !ok || ref.Kind != core.ReferenceTrack || ref.TrackID == "" {
		return nil, nil
	}
	value, found, err := metadata.LibraryRecordingMetadata(ctx, ref.TrackID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !found || value.Ref.ID != ref.TrackID || !value.Matched || value.IdentityStatus != core.ResolutionResolved {
		return nil, nil
	}
	ids := make([]string, 0, len(value.ArtistIDs))
	for _, raw := range value.ArtistIDs {
		if id := librarypack.CanonicalMBID(raw); id != "" {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	return ids[:min(len(ids), 8)], nil
}
