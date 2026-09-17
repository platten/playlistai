package localcatalog

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
)

type RecommendationMode string

const (
	ModeCombined    RecommendationMode = "combined"
	ModeLibraryOnly RecommendationMode = "library_only"
)

// PinProvider returns a catalog whose Close releases exactly one generation
// pin. Implementations may atomically replace their active generation while an
// older returned catalog remains live.
type PinProvider interface {
	PinLocalCatalog() (*Catalog, error)
}

// RecommendationOverlay owns one request-scoped local generation.
type RecommendationOverlay struct {
	Catalog   ports.Catalog
	Resolver  ports.ReferenceResolver
	Retriever ports.CandidateRetriever
	local     *Catalog
}

func (o RecommendationOverlay) Close() {
	if o.local != nil {
		_ = o.local.Close()
	}
}

func PinRecommendationOverlay(ctx context.Context, provider PinProvider, base ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever, mode RecommendationMode, workers int) (RecommendationOverlay, error) {
	if provider == nil || base == nil || resolver == nil || retriever == nil {
		return RecommendationOverlay{}, errors.New("localcatalog: complete base services and provider are required")
	}
	if mode != ModeCombined && mode != ModeLibraryOnly {
		return RecommendationOverlay{}, fmt.Errorf("localcatalog: invalid recommendation mode %q", mode)
	}
	local, err := provider.PinLocalCatalog()
	if err != nil {
		return RecommendationOverlay{}, err
	}
	return NewRecommendationOverlay(ctx, local, base, resolver, retriever, mode, workers)
}

// NewRecommendationOverlay consumes an already pinned catalog. This lets the
// application capture the generation lease, mode, and root mappings in one
// atomic request snapshot instead of reading mutable settings separately.
func NewRecommendationOverlay(ctx context.Context, local *Catalog, base ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever, mode RecommendationMode, workers int) (RecommendationOverlay, error) {
	if local == nil || base == nil || resolver == nil || retriever == nil {
		if local != nil {
			_ = local.Close()
		}
		return RecommendationOverlay{}, errors.New("localcatalog: complete base services and pinned local catalog are required")
	}
	composite := &CompositeCatalog{base: base, local: local, mode: mode}
	combinedResolver := &CompositeResolver{base: resolver, local: local, mode: mode}
	combinedRetriever, err := NewCombinedRetriever(retriever, local, mode, workers)
	if err != nil {
		_ = local.Close()
		return RecommendationOverlay{}, err
	}
	return RecommendationOverlay{Catalog: composite, Resolver: combinedResolver, Retriever: combinedRetriever, local: local}, nil
}

type CompositeCatalog struct {
	base  ports.Catalog
	local *Catalog
	mode  RecommendationMode
}

func (c *CompositeCatalog) Len() int {
	// Len is the number of dense Deej-AI vector rows, not total composite
	// membership. Local tracks are reachable through indexed retrieval and Meta.
	if c.mode == ModeLibraryOnly {
		return 0
	}
	return c.base.Len()
}
func (c *CompositeCatalog) Dim() int { return c.base.Dim() }
func (c *CompositeCatalog) ID(row int) string {
	if c.mode == ModeLibraryOnly {
		return ""
	}
	return c.base.ID(row)
}
func (c *CompositeCatalog) RowOf(id string) (int, bool) {
	if strings.HasPrefix(id, "local:") || c.mode == ModeLibraryOnly {
		return 0, false
	}
	return c.base.RowOf(id)
}
func (c *CompositeCatalog) Meta(id string) (core.TrackMeta, bool) {
	if strings.HasPrefix(id, "local:") {
		track, ok, err := c.local.Lookup(context.Background(), id)
		if err != nil || !ok {
			return core.TrackMeta{}, false
		}
		meta := core.TrackMeta{
			Ref:   core.TrackRef{ID: track.ID, Artist: track.Artist, Title: track.Title, RecordingIdentity: track.RecordingIdentity},
			Album: track.Album, AlbumReliable: track.Album != "", SourceIdentity: track.SourceIdentity,
			ISRC: track.ISRC, MusicBrainzRecording: track.MusicBrainzRecording,
		}
		if track.AudioFingerprint != nil {
			fingerprint := track.AudioFingerprint
			meta.AudioFingerprint = &core.AudioFingerprint{
				Contract: fingerprint.Contract, Format: fingerprint.Format, Algorithm: fingerprint.Algorithm,
				Fingerprint: fingerprint.Fingerprint, FingerprintSHA256: fingerprint.FingerprintSHA256,
				Scope: fingerprint.Scope, DecoderRuntimeID: fingerprint.DecoderRuntimeID,
			}
		}
		if track.DurationReliable && track.DurationMilliseconds > 0 {
			recordingID := track.RecordingIdentity
			if recordingID == "" {
				recordingID = track.SourceIdentity
			}
			meta.FullRecordingDuration = &core.RecordingDuration{Milliseconds: track.DurationMilliseconds, Source: "local:" + track.DurationProvenance, RecordingID: recordingID}
		}
		return meta, true
	}
	if c.mode == ModeLibraryOnly {
		return core.TrackMeta{}, false
	}
	return c.base.Meta(id)
}

// ArtistRecordings combines the two catalogs through their explicit indexes;
// it never assumes IDs are dense integers across heterogeneous spaces.
func (c *CompositeCatalog) ArtistRecordings(ctx context.Context, artist string) ([]core.TrackRef, error) {
	byKey := map[string]core.TrackRef{}
	if c.mode != ModeLibraryOnly {
		if indexed, ok := c.base.(ports.ArtistRecordingCatalog); ok {
			tracks, err := indexed.ArtistRecordings(ctx, artist)
			if err != nil {
				return nil, err
			}
			for _, track := range tracks {
				byKey[core.ProvisionalRecordingKey(track)] = track
			}
		}
	}
	tracks, err := c.local.ArtistRecordings(ctx, artist)
	if err != nil {
		return nil, err
	}
	for _, track := range tracks {
		key := core.ProvisionalRecordingKey(track)
		if _, exists := byKey[key]; !exists {
			byKey[key] = track
		}
	}
	out := make([]core.TrackRef, 0, len(byKey))
	for _, track := range byKey {
		out = append(out, track)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (c *CompositeCatalog) VectorsByRow(row int) (ports.Vectors, bool) {
	if c.mode == ModeLibraryOnly {
		return ports.Vectors{}, false
	}
	return c.base.VectorsByRow(row)
}
func (c *CompositeCatalog) Vectors(id string) (ports.Vectors, bool) {
	if strings.HasPrefix(id, "local:") || c.mode == ModeLibraryOnly {
		return ports.Vectors{}, false
	}
	return c.base.Vectors(id)
}
func (c *CompositeCatalog) RawRow(row int) ([]int8, []int8, bool) {
	if c.mode == ModeLibraryOnly {
		return nil, nil, false
	}
	return c.base.RawRow(row)
}
func (c *CompositeCatalog) Resolve(query string, limit int) []core.TrackRef {
	var out []core.TrackRef
	if c.mode != ModeLibraryOnly {
		out = append(out, c.base.Resolve(query, limit)...)
	}
	if len(out) < limit {
		hits, _ := c.local.Search(context.Background(), MetadataQuery{Text: query, Limit: limit - len(out)})
		for _, hit := range hits {
			out = append(out, core.TrackRef{ID: hit.Track.ID, Artist: hit.Track.Artist, Title: hit.Track.Title})
		}
	}
	return out
}

func (c *CompositeCatalog) SupportsCriterion(criterion core.MusicalCriterion) bool {
	return criterion.Kind == "genre"
}

func (c *CompositeCatalog) CriterionEvidence(ctx context.Context, id string, criterion core.MusicalCriterion) core.EvidenceState {
	if !strings.HasPrefix(id, "local:") {
		return core.EvidenceUnknown
	}
	return c.local.CriterionEvidence(ctx, id, criterion)
}

type CompositeResolver struct {
	base  ports.ReferenceResolver
	local *Catalog
	mode  RecommendationMode
}

func (r *CompositeResolver) CatalogVersion() string {
	if r.mode == ModeLibraryOnly {
		return "local-library:" + r.local.Provenance().PackID
	}
	return r.base.CatalogVersion() + "+local-library:" + r.local.Provenance().PackID
}

func (r *CompositeResolver) ResolveReference(ref core.IntentReference) core.ReferenceResolution {
	if ref.TrackID != "" && strings.HasPrefix(ref.TrackID, "local:") {
		if track, ok, _ := r.local.Lookup(context.Background(), ref.TrackID); ok {
			candidate := localResolutionCandidate(ref.Kind, []Track{track})
			return core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: r.CatalogVersion(), Selected: &candidate}
		}
	}
	if r.mode != ModeLibraryOnly {
		resolved := r.base.ResolveReference(ref)
		if resolved.Status == core.ResolutionResolved {
			return resolved
		}
	}
	if ref.Kind == core.ReferenceArtist {
		tracks, err := r.local.ArtistRecordings(context.Background(), ref.Query)
		if err == nil && len(tracks) > 0 {
			localTracks := make([]Track, 0, len(tracks))
			for _, track := range tracks {
				if local, ok, _ := r.local.Lookup(context.Background(), track.ID); ok {
					localTracks = append(localTracks, local)
				}
			}
			if len(localTracks) > 0 {
				candidate := localResolutionCandidate(ref.Kind, localTracks)
				return core.ReferenceResolution{Status: core.ResolutionResolved, CatalogVersion: r.CatalogVersion(), Selected: &candidate}
			}
		}
	}
	hits, _ := r.local.Search(context.Background(), MetadataQuery{Text: ref.Query, Limit: 20})
	groups := localResolutionGroups(ref.Kind, hits)
	result := core.ReferenceResolution{Status: core.ResolutionUnresolved, CatalogVersion: r.CatalogVersion()}
	if len(groups) == 1 {
		candidate := localResolutionCandidate(ref.Kind, groups[0])
		result.Status, result.Selected = core.ResolutionResolved, &candidate
	} else if len(groups) > 1 {
		result.Status = core.ResolutionAmbiguous
		for _, group := range groups {
			result.Alternatives = append(result.Alternatives, localResolutionCandidate(ref.Kind, group))
		}
	}
	return result
}

func localResolutionGroups(kind core.ReferenceKind, hits []Hit) [][]Track {
	byKey := map[string][]Track{}
	for _, hit := range hits {
		key := hit.Track.ID
		switch kind {
		case core.ReferenceArtist:
			key = normalizeUnicode(hit.Track.Artist)
		case core.ReferenceAlbum:
			key = normalizeUnicode(hit.Track.AlbumArtist + "\x00" + hit.Track.Album)
		}
		if key != "" {
			byKey[key] = append(byKey[key], hit.Track)
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	groups := make([][]Track, 0, len(keys))
	for _, key := range keys {
		groups = append(groups, byKey[key])
	}
	return groups
}

func localResolutionCandidate(kind core.ReferenceKind, tracks []Track) core.ResolutionCandidate {
	c := core.ResolutionCandidate{Kind: kind, Confidence: 1, Evidence: []core.ResolutionEvidence{{Match: "local_metadata", MatchedText: tracks[0].Artist + " - " + tracks[0].Title}}}
	switch kind {
	case core.ReferenceArtist:
		c.EntityID, c.Artist = "local-artist:"+normalizeUnicode(tracks[0].Artist), tracks[0].Artist
	case core.ReferenceAlbum:
		c.EntityID, c.Artist, c.Title = "local-album:"+normalizeUnicode(tracks[0].AlbumArtist+"\x00"+tracks[0].Album), tracks[0].AlbumArtist, tracks[0].Album
	default:
		c.EntityID, c.Artist, c.Title = tracks[0].ID, tracks[0].Artist, tracks[0].Title
	}
	limit := min(5, len(tracks))
	weight := 1 / float64(limit)
	for _, track := range tracks[:limit] {
		c.Representatives = append(c.Representatives, core.WeightedTrack{TrackID: track.ID, Weight: weight})
	}
	return c
}

type CombinedRetriever struct {
	base    ports.CandidateRetriever
	local   *Catalog
	mode    RecommendationMode
	workers int
}

func (*CombinedRetriever) SupportsIntentMetadata() bool { return true }

func NewCombinedRetriever(base ports.CandidateRetriever, local *Catalog, mode RecommendationMode, workers int) (*CombinedRetriever, error) {
	if base == nil || local == nil || workers <= 0 {
		return nil, errors.New("localcatalog: invalid combined retriever")
	}
	return &CombinedRetriever{base: base, local: local, mode: mode, workers: workers}, nil
}

func (r *CombinedRetriever) Retrieve(ctx context.Context, request ports.RetrievalRequest) ([]core.Candidate, error) {
	var baseCandidates []core.Candidate
	var baseErr error
	if r.mode != ModeLibraryOnly {
		baseCandidates, baseErr = r.base.Retrieve(ctx, request)
	}
	if baseErr != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	executor, err := NewExecutor(r.local, r.workers)
	if err != nil {
		return nil, err
	}
	queries := recommendationQueries(request)
	localByID := map[string]*Candidate{}
	for _, query := range queries {
		result, queryErr := executor.Query(ctx, query)
		if queryErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		for _, candidate := range result.Candidates {
			if _, excluded := request.AttemptedIDs[candidate.Track.ID]; excluded {
				continue
			}
			merged := localByID[candidate.Track.ID]
			if merged == nil {
				merged = &Candidate{Track: candidate.Track}
				localByID[candidate.Track.ID] = merged
			}
			merged.Evidence = append(merged.Evidence, candidate.Evidence...)
			if candidate.Track.Cluster != nil && !hasEvidenceChannel(merged.Evidence, ClusterChannel) {
				rank := 1
				if len(candidate.Evidence) > 0 {
					rank = max(1, candidate.Evidence[0].Rank)
				}
				merged.Evidence = append(merged.Evidence, Evidence{Channel: ClusterChannel, QueryID: fmt.Sprintf("cluster:%d", *candidate.Track.Cluster), Rank: rank, Score: candidate.Track.ClusterScore, Provenance: candidate.Track.Provenance})
			}
		}
	}
	for _, required := range request.Intent.RequiredTracks {
		if strings.HasPrefix(required.TrackID, "local:") {
			if track, ok, _ := r.local.Lookup(ctx, required.TrackID); ok {
				localByID[track.ID] = &Candidate{Track: track, Evidence: []Evidence{{Channel: "required_local", QueryID: track.ID, Rank: 1, Score: 1, Provenance: track.Provenance}}}
			}
		}
	}
	localCandidates := make([]Candidate, 0, len(localByID))
	for _, candidate := range localByID {
		sort.SliceStable(candidate.Evidence, func(i, j int) bool { return evidenceLess(candidate.Evidence[i], candidate.Evidence[j]) })
		localCandidates = append(localCandidates, *candidate)
	}
	sort.Slice(localCandidates, func(i, j int) bool { return candidateLess(localCandidates[i], localCandidates[j]) })
	localCandidates = deduplicateCandidates(localCandidates)
	all := append([]core.Candidate(nil), baseCandidates...)
	for _, candidate := range localCandidates {
		duplicate := false
		for _, base := range baseCandidates {
			if coreIdentityMatchesLocal(base.Track, candidate.Track) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		converted := core.Candidate{Track: core.TrackRef{ID: candidate.Track.ID, Artist: candidate.Track.Artist, Title: candidate.Track.Title, RecordingIdentity: candidate.Track.RecordingIdentity}}
		for _, evidence := range candidate.Evidence {
			weight := 1.0
			if evidence.Channel == ClusterChannel {
				weight = .25
			}
			converted.Sources = append(converted.Sources, core.RetrievalEvidence{Channel: evidence.Channel, QueryID: evidence.QueryID, Rank: evidence.Rank, Score: evidence.Score, QueryWeight: weight})
		}
		if score, ok := r.local.DSPPreferenceScore(ctx, candidate.Track.ID, request.Intent); ok {
			converted.Sources = append(converted.Sources, core.RetrievalEvidence{Channel: DSPChannel, QueryID: "reviewed-dsp-percentiles", Rank: percentileRank(score), Score: score, QueryWeight: .25})
		}
		all = append(all, converted)
	}
	clusterPopulation := map[string]int{}
	for _, candidate := range all {
		for _, source := range candidate.Sources {
			if source.Channel == ClusterChannel {
				clusterPopulation[source.QueryID]++
			}
		}
	}
	var maximum float64
	for i := range all {
		var fusion float64
		for _, source := range all[i].Sources {
			weight := source.QueryWeight
			if source.Channel == ClusterChannel && clusterPopulation[source.QueryID] > 1 {
				weight /= math.Sqrt(float64(clusterPopulation[source.QueryID]))
			}
			fusion += weight / float64(60+max(1, source.Rank))
		}
		if fusion > 0 {
			all[i].Scores.RetrievalFusion, all[i].Available.RetrievalFusion = fusion, true
		}
		maximum = max(maximum, fusion)
	}
	if maximum > 0 {
		for i := range all {
			if all[i].Available.RetrievalFusion {
				all[i].Scores.RetrievalFusion /= maximum
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Track.ID < all[j].Track.ID })
	return all, baseErr
}

func hasEvidenceChannel(evidence []Evidence, channel string) bool {
	for _, item := range evidence {
		if item.Channel == channel {
			return true
		}
	}
	return false
}

func coreIdentityMatchesLocal(ref core.TrackRef, track Track) bool {
	identity := strings.ToLower(strings.TrimSpace(ref.RecordingIdentity))
	if identity == "" {
		return false
	}
	local := packTrack(track)
	localIdentity := strings.ToLower(strings.TrimSpace(track.RecordingIdentity))
	if value, ok := strings.CutPrefix(identity, "isrc:"); ok {
		if local.ISRC == "" {
			local.ISRC, _ = strings.CutPrefix(localIdentity, "isrc:")
		}
		return librarypack.SameRecording(local, librarypack.Track{ISRC: value})
	}
	if value, ok := strings.CutPrefix(identity, "musicbrainz:"); ok {
		if local.MusicBrainzRecording == "" {
			local.MusicBrainzRecording, _ = strings.CutPrefix(localIdentity, "musicbrainz:")
		}
		return librarypack.SameRecording(local, librarypack.Track{MusicBrainzRecording: value})
	}
	return identity == localIdentity
}

func percentileRank(score float64) int {
	// Rank fusion consumes positive ordinals; retain the signed native score in
	// evidence while mapping stronger matches to smaller deterministic ranks.
	score = max(-1.0, min(1.0, score))
	return 1 + int((1-score)*49.5)
}

func recommendationQueries(request ports.RetrievalRequest) []Query {
	var queries []Query
	seenText := map[string]bool{}
	exclude := make(map[string]struct{}, len(request.AttemptedIDs)+len(request.RecentSelections))
	for id := range request.AttemptedIDs {
		exclude[id] = struct{}{}
	}
	for _, recent := range request.RecentSelections {
		exclude[recent.ID] = struct{}{}
	}
	for _, ref := range append(append([]core.IntentReference(nil), request.Intent.References...), request.Intent.RequiredTracks...) {
		if ref.TrackID != "" {
			exclude[ref.TrackID] = struct{}{}
		}
		if ref.Resolution != nil && ref.Resolution.Selected != nil {
			for _, representative := range ref.Resolution.Selected.Representatives {
				exclude[representative.TrackID] = struct{}{}
			}
		}
	}
	for _, ref := range append(append([]core.IntentReference(nil), request.Intent.References...), request.Intent.RequiredTracks...) {
		if ref.Influence == core.InfluenceNegative {
			continue
		}
		if text := strings.TrimSpace(ref.Query); text != "" && !seenText[text] {
			queries = append(queries, Query{Metadata: &MetadataQuery{Text: text, Limit: 100, ExcludeIDs: exclude}})
			seenText[text] = true
		}
		ids := []string{ref.TrackID}
		if ref.Resolution != nil && ref.Resolution.Selected != nil {
			for _, representative := range ref.Resolution.Selected.Representatives {
				ids = append(ids, representative.TrackID)
			}
		}
		for _, id := range ids {
			if strings.HasPrefix(id, "local:") {
				queries = append(queries, Query{MERT: &NeighborQuery{SeedID: id, Limit: 100, ExcludeIDs: exclude}})
			}
		}
	}
	for _, criterion := range request.Intent.EssentialCriteria {
		if criterion.Kind == "genre" || criterion.Kind == "style" {
			if text := strings.TrimSpace(criterion.Value); text != "" && !seenText[text] {
				queries = append(queries, Query{Metadata: &MetadataQuery{Text: text, Limit: 100, ExcludeIDs: exclude}})
				seenText[text] = true
			}
		}
	}
	return queries
}
