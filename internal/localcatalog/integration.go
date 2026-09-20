package localcatalog

import (
	"context"
	"encoding/json"
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
	packs     []*Catalog
}

func (o RecommendationOverlay) Close() {
	for _, pack := range o.packs {
		_ = pack.Close()
	}
	if o.local != nil {
		_ = o.local.Close()
	}
}

// NewDiscoveryOverlay consumes every pin, including on failure. Each layer
// keeps its own namespace and forwards other namespaces to the next layer.
func NewDiscoveryOverlay(ctx context.Context, packs []*Catalog, base ports.Catalog, resolver ports.ReferenceResolver, retriever ports.CandidateRetriever, workers int) (RecommendationOverlay, error) {
	result := RecommendationOverlay{Catalog: base, Resolver: resolver, Retriever: retriever, packs: packs}
	if base == nil || resolver == nil || retriever == nil || workers <= 0 {
		result.Close()
		return RecommendationOverlay{}, errors.New("localcatalog: complete discovery services are required")
	}
	namespaces := map[string]bool{}
	for _, pack := range packs {
		if pack == nil || namespaces[pack.prefix] {
			result.Close()
			return RecommendationOverlay{}, errors.New("localcatalog: discovery packs require distinct namespaces")
		}
		namespaces[pack.prefix] = true
	}
	for _, pack := range packs {
		next, err := NewRecommendationOverlay(ctx, pack, result.Catalog, result.Resolver, result.Retriever, ModeCombined, workers)
		if err != nil {
			result.Close()
			return RecommendationOverlay{}, err
		}
		result.Catalog, result.Resolver, result.Retriever = next.Catalog, next.Resolver, next.Retriever
	}
	for layer := result.Retriever; layer != nil; {
		combined, ok := layer.(*CombinedRetriever)
		if !ok {
			break
		}
		combined.catalog = result.Catalog
		layer = combined.base
	}
	return result, nil
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
	composite := &CompositeCatalog{base: base, local: local, mode: mode, baseVersion: resolver.CatalogVersion()}
	combinedResolver := &CompositeResolver{base: resolver, local: local, mode: mode}
	combinedRetriever, err := NewCombinedRetriever(retriever, local, mode, workers)
	if err != nil {
		_ = local.Close()
		return RecommendationOverlay{}, err
	}
	combinedRetriever.catalog = composite
	var catalog ports.Catalog = composite
	if registrar, ok := base.(ports.DynamicTrackCatalog); ok && mode == ModeCombined {
		catalog = &dynamicCompositeCatalog{CompositeCatalog: composite, registrar: registrar}
	}
	return RecommendationOverlay{Catalog: catalog, Resolver: combinedResolver, Retriever: combinedRetriever, local: local}, nil
}

// Only combined views over a writable base expose registration. A library-only
// view must not accidentally enable external discovery through a type assertion.
type dynamicCompositeCatalog struct {
	*CompositeCatalog
	registrar ports.DynamicTrackCatalog
}

func (c *dynamicCompositeCatalog) RegisterDynamicTrack(track core.TrackMeta) error {
	return c.registrar.RegisterDynamicTrack(track)
}

type CompositeCatalog struct {
	semanticModel      core.AudioModelIdentity
	semanticQueries    []core.AudioClauseVector
	recordingKnowledge map[string]core.EnrichedTrack
	baseVersion        string
	base               ports.Catalog
	local              *Catalog
	mode               RecommendationMode
}

// NewEvidenceCatalog borrows a pinned local catalog for feedback and other
// read-only consumers. Output-source restrictions do not invalidate feedback
// on a previously displayed recording.
func NewEvidenceCatalog(base ports.Catalog, local *Catalog, baseVersion string) ports.Catalog {
	return &CompositeCatalog{base: base, local: local, mode: ModeCombined, baseVersion: baseVersion}
}

func (c *CompositeCatalog) CatalogVersion() string {
	if c.mode == ModeLibraryOnly {
		return c.local.catalogVersion()
	}
	return c.baseVersion + "+" + c.local.catalogVersion()
}

func (c *Catalog) catalogVersion() string {
	p := c.Provenance()
	if p.Source == "shared_pack" {
		return "shared-discovery-v1:" + p.SourceID + ":" + p.PackID + ":" + p.PackSHA256 + ":" + p.ProfileGeneration
	}
	return "local-library:" + p.PackID
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
	if c.local.owns(id) || c.mode == ModeLibraryOnly {
		return 0, false
	}
	return c.base.RowOf(id)
}
func (c *CompositeCatalog) Meta(id string) (core.TrackMeta, bool) {
	if c.local.owns(id) {
		track, ok, err := c.local.Lookup(context.Background(), id)
		if err != nil || !ok {
			return core.TrackMeta{}, false
		}
		meta := core.TrackMeta{
			Annotations: c.local.Annotations(context.Background(), id),
			Ref:         core.TrackRef{ID: track.ID, Artist: track.Artist, Title: track.Title, RecordingIdentity: track.RecordingIdentity},
			Album:       track.Album, AlbumReliable: track.Album != "", SourceIdentity: track.SourceIdentity,
			ISRC: track.ISRC, MusicBrainzRecording: track.MusicBrainzRecording, AcoustID: track.AcoustID,
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
	meta, ok := c.baseMetadata(id)
	if ok {
		if match, found := c.matchBase(context.Background(), meta); found {
			meta.Annotations = append(append([]core.MetadataAnnotation(nil), meta.Annotations...), c.local.Annotations(context.Background(), match.ID)...)
		}
	}
	return meta, ok
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
	if c.local.owns(id) || c.mode == ModeLibraryOnly {
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
	return supportsAnnotationCriterion(criterion)
}

func (c *CompositeCatalog) CriterionEvidence(ctx context.Context, id string, criterion core.MusicalCriterion) core.EvidenceState {
	if c.local.owns(id) {
		return c.local.CriterionEvidence(ctx, id, criterion)
	}
	if c.mode == ModeLibraryOnly {
		return core.EvidenceUnknown
	}
	if meta, ok := c.baseMetadata(id); ok {
		if match, found := c.matchBase(ctx, meta); found {
			if state := c.local.CriterionEvidence(ctx, match.ID, criterion); state != core.EvidenceUnknown {
				return state
			}
		}
	}
	if evidence, ok := c.base.(interface {
		CriterionEvidence(context.Context, string, core.MusicalCriterion) core.EvidenceState
	}); ok {
		return evidence.CriterionEvidence(ctx, id, criterion)
	}
	return core.EvidenceUnknown
}

type CompositeResolver struct {
	base  ports.ReferenceResolver
	local *Catalog
	mode  RecommendationMode
}

func (r *CompositeResolver) CatalogVersion() string {
	if r.mode == ModeLibraryOnly {
		return r.local.catalogVersion()
	}
	return r.base.CatalogVersion() + "+" + r.local.catalogVersion()
}

func (r *CompositeResolver) ResolveReference(ref core.IntentReference) core.ReferenceResolution {
	if r.local.owns(ref.TrackID) {
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
	catalog ports.Catalog
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
	if r.catalog != nil {
		if source, ok := r.catalog.(ports.LibraryAudioCatalog); ok {
			seen := map[string]bool{}
			for _, ref := range core.RetrievalReferences(request.Intent) {
				if ref.Influence == core.InfluenceNegative {
					continue
				}
				ids := []string{ref.TrackID}
				if ref.Resolution != nil && ref.Resolution.Selected != nil {
					for _, rep := range ref.Resolution.Selected.Representatives {
						ids = append(ids, rep.TrackID)
					}
				}
				for _, id := range ids {
					if id == "" || seen[id] || r.local.owns(id) {
						continue
					}
					seen[id] = true
					vector, found, e := source.LibraryVector(ctx, id)
					if e != nil && ctx.Err() != nil {
						return nil, ctx.Err()
					}
					if found && vector.Source.SpaceID == r.local.EvidenceSource().SpaceID {
						space := r.local.VectorSpace()
						queries = append(queries, Query{MERT: &NeighborQuery{Vector: vector.Values, Space: &space, Limit: 100, ExcludeIDs: request.AttemptedIDs}})
					}
				}
			}
		}
	}
	if provider, ok := r.catalog.(interface {
		libraryQueries(string) []core.AudioClauseVector
	}); ok &&
		(request.Intent.Controls.RecommendationMode == core.EnhancedHybrid || request.Intent.Controls.RecommendationMode == core.CLAPFirst) {
		space := r.local.CLAPVectorSpace()
		seen := map[string]bool{}
		for _, query := range provider.libraryQueries(r.local.manifest.PackID) {
			// Negative descriptions penalize ranking, never seed the positive pool.
			key := query.Clause.Scope + ":" + query.Clause.Kind + ":" + query.Clause.Text
			if query.Clause.Negative || seen[key] {
				continue
			}
			seen[key] = true
			if vector := normalizedTextVector(query.Values); len(vector) > 0 {
				queries = append(queries, Query{CLAP: &NeighborQuery{Vector: vector, Space: &space, CLAPModel: r.local.manifest.CLAPModel, Limit: 100, ExcludeIDs: request.AttemptedIDs}})
			}
		}
	}
	if request.Intent.Controls.RecommendationMode == core.EnhancedHybrid {
		space := r.local.VectorSpace()
		for _, taste := range request.Profile.Library {
			if taste.Source.SpaceID != r.local.EvidenceSource().SpaceID {
				continue
			}
			vectors := [][]float32{taste.RequestPositive, taste.Positive}
			vectors = append(vectors, taste.Clusters[:min(len(taste.Clusters), 4)]...)
			for _, vector := range vectors {
				if validQueryVector(vector, space.Dimension) {
					queries = append(queries, Query{MERT: &NeighborQuery{Vector: vector, Space: &space, Limit: 24, ExcludeIDs: request.AttemptedIDs}})
				}
			}
		}
		for _, recent := range request.RecentSelections[max(0, len(request.RecentSelections)-3):] {
			if r.local.owns(recent.ID) {
				queries = append(queries, Query{MERT: &NeighborQuery{SeedID: recent.ID, Limit: 16, ExcludeIDs: request.AttemptedIDs}})
			}
		}
	}
	var profiles []core.DiscoveryProfile
	if request.Intent.Controls.RecommendationMode == core.EnhancedHybrid {
		if request.Intent.Knowledge != nil {
			profiles = request.Intent.Knowledge.PackProfiles
		} else {
			var profileErr error
			profiles, profileErr = r.local.DiscoveryProfiles(ctx, request.Intent, 12)
			if profileErr != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
	}
	for _, profile := range profiles {
		if profile.Album != "" {
			continue
		}
		queries = append(queries, Query{Metadata: &MetadataQuery{Text: profile.Artist, Limit: 30, ExcludeIDs: request.AttemptedIDs}})
	}
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
		}
	}
	for _, required := range request.Intent.RequiredTracks {
		if r.local.owns(required.TrackID) {
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
		duplicateIndex := -1
		for i, base := range baseCandidates {
			if coreIdentityMatchesLocal(base.Track, candidate.Track) {
				duplicateIndex = i
				break
			}
		}
		converted := core.Candidate{Track: core.TrackRef{ID: candidate.Track.ID, Artist: candidate.Track.Artist, Title: candidate.Track.Title, RecordingIdentity: candidate.Track.RecordingIdentity}}
		for _, evidence := range candidate.Evidence {
			weight := 1.0
			if evidence.Channel == ClusterChannel {
				weight = .25
			}
			source := r.local.EvidenceSource()
			if evidence.Channel == CLAPChannel {
				source = r.local.CLAPEvidenceSource()
				if request.Intent.Controls.RecommendationMode == core.CLAPFirst {
					weight = 1.5
				}
			} else if evidence.Channel != MERTChannel {
				source.SpaceID = ""
				source.Generation = evidence.Provenance.MetadataGeneration
				source.Scope = "embedded_tags"
			}
			converted.Sources = append(converted.Sources, core.RetrievalEvidence{Channel: evidence.Channel, QueryID: evidence.QueryID, Rank: evidence.Rank, Score: evidence.Score, QueryWeight: weight, LibrarySource: &source})
		}
		if duplicateIndex >= 0 {
			all[duplicateIndex].Sources = append(all[duplicateIndex].Sources, converted.Sources...)
		} else {
			all = append(all, converted)
		}
	}
	clusterPopulation := map[string]int{}
	for i := range all {
		// A base-catalog alias and a pack file may report the very same query
		// observation. Merge once more after alias resolution, before RRF.
		all[i].Sources = uniqueRetrievalEvidence(all[i].Sources)
		candidate := all[i]
		for _, source := range candidate.Sources {
			if source.Channel == ClusterChannel {
				clusterPopulation[source.QueryID]++
			}
		}
	}
	var maximum float64
	for i := range all {
		var fusion float64
		// Preserve all generation provenance above, but do not let copies of
		// one representation/query buy additional ranking votes.
		votes := map[string]float64{}
		for _, source := range all[i].Sources {
			weight := source.QueryWeight
			if request.Intent.Controls.RecommendationMode == core.CLAPFirst && source.Channel == MERTChannel {
				weight *= .65
			}
			if source.Channel == ClusterChannel && clusterPopulation[source.QueryID] > 1 {
				weight /= math.Sqrt(float64(clusterPopulation[source.QueryID]))
			}
			space, scope := "", ""
			if source.LibrarySource != nil {
				space, scope = source.LibrarySource.SpaceID, source.LibrarySource.Scope
			}
			key, _ := json.Marshal([]string{source.Channel, source.QueryID, space, scope})
			votes[string(key)] = max(votes[string(key)], weight/float64(60+max(1, source.Rank)))
		}
		keys := make([]string, 0, len(votes))
		for key := range votes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fusion += votes[key]
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

func uniqueRetrievalEvidence(input []core.RetrievalEvidence) []core.RetrievalEvidence {
	best := make(map[string]core.RetrievalEvidence, len(input))
	for _, evidence := range input {
		space, generation, scope := "", "", ""
		if evidence.LibrarySource != nil {
			space = evidence.LibrarySource.SpaceID
			generation = evidence.LibrarySource.Generation
			scope = evidence.LibrarySource.Scope
		}
		key, _ := json.Marshal(struct {
			Channel, Query, Space, Generation, Scope string
		}{evidence.Channel, evidence.QueryID, space, generation, scope})
		previous, exists := best[string(key)]
		if !exists || max(1, evidence.Rank) < max(1, previous.Rank) ||
			(max(1, evidence.Rank) == max(1, previous.Rank) && (evidence.Score > previous.Score ||
				(evidence.Score == previous.Score && evidence.QueryWeight > previous.QueryWeight))) {
			best[string(key)] = evidence
		}
	}
	keys := make([]string, 0, len(best))
	for key := range best {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]core.RetrievalEvidence, 0, len(keys))
	for _, key := range keys {
		out = append(out, best[key])
	}
	return out
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
	if value, ok := strings.CutPrefix(identity, "acoustid-id:"); ok {
		if local.AcoustID == "" {
			local.AcoustID, _ = strings.CutPrefix(localIdentity, "acoustid-id:")
		}
		return librarypack.SameRecording(local, librarypack.Track{AcoustID: value})
	}
	return identity == localIdentity
}

func recommendationQueries(request ports.RetrievalRequest) []Query {
	var queries []Query
	seenText := map[string]bool{}
	seenID := map[string]bool{}
	exclude := make(map[string]struct{}, len(request.AttemptedIDs)+len(request.RecentSelections))
	for id := range request.AttemptedIDs {
		exclude[id] = struct{}{}
	}
	for _, recent := range request.RecentSelections {
		exclude[recent.ID] = struct{}{}
	}
	for _, ref := range core.RetrievalReferences(request.Intent) {
		if ref.TrackID != "" {
			exclude[ref.TrackID] = struct{}{}
		}
		if ref.Resolution != nil && ref.Resolution.Selected != nil {
			for _, representative := range ref.Resolution.Selected.Representatives {
				exclude[representative.TrackID] = struct{}{}
			}
		}
	}
	for _, ref := range core.RetrievalReferences(request.Intent) {
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
			if (strings.HasPrefix(id, "local:") || strings.HasPrefix(id, "pack:")) && !seenID[id] {
				seenID[id] = true
				queries = append(queries, Query{MERT: &NeighborQuery{SeedID: id, Limit: 100, ExcludeIDs: exclude}})
				if request.Intent.Controls.RecommendationMode == core.CLAPFirst || request.Intent.Controls.RecommendationMode == core.EnhancedHybrid {
					queries = append(queries, Query{CLAP: &NeighborQuery{SeedID: id, Limit: 100, ExcludeIDs: exclude}})
				}
			}
		}
	}
	criteria := append([]core.MusicalCriterion(nil), request.Intent.EssentialCriteria...)
	for kind, preferences := range map[string][]core.IntentPreference{"genre": request.Intent.Preferences.Genres, "style": request.Intent.Preferences.Styles, "mood": request.Intent.Preferences.Moods, "instrumentation": request.Intent.Preferences.Instrumentation, "texture": request.Intent.Preferences.TextureDescriptions} {
		for _, preference := range preferences {
			if preference.Influence != core.InfluenceNegative {
				criteria = append(criteria, core.MusicalCriterion{Kind: kind, Value: preference.Value, Scope: preference.Scope, Strength: preference.Strength, Group: preference.Group, ConceptID: preference.ConceptID})
			}
		}
	}
	sort.SliceStable(criteria, func(i, j int) bool { return criteria[i].Kind+criteria[i].Value < criteria[j].Kind+criteria[j].Value })
	for _, criterion := range criteria {
		if supportsAnnotationCriterion(criterion) {
			if text := strings.TrimSpace(criterion.Value); text != "" && !seenText[criterion.Kind+":"+text] {
				queries = append(queries, Query{Metadata: &MetadataQuery{Text: text, Criterion: &criterion, Limit: 100, ExcludeIDs: exclude}})
				seenText[criterion.Kind+":"+text] = true
			}
		}
	}
	return queries
}
