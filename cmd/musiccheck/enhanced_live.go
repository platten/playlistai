package main

import (
	"context"
	"math"
	"sync"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/preview/deezer"
)

type liveEnhancedOptions struct {
	CLAPBundle     string
	MERTBundle     string
	AnalysisDir    string
	CatalogVersion string
	CacheOnly      bool
	Recordings     ports.CachedRecordingReader
}

type liveEnhanced struct {
	mu             sync.Mutex
	store          *audio.Store
	preview        *audio.Service
	mert           *audio.MERTService
	clapPool       *audio.WorkerPool
	mertPool       *audio.MERTWorkerPool
	catalogVersion string
	model          core.AudioRepresentationIdentity
}

func openLiveEnhanced(ctx context.Context, options liveEnhancedOptions) (*liveEnhanced, error) {
	store, err := audio.OpenStore(options.AnalysisDir)
	if err != nil {
		return nil, err
	}
	runtime := &liveEnhanced{store: store, catalogVersion: options.CatalogVersion}
	ok := false
	defer func() {
		if !ok {
			_ = runtime.Close()
		}
	}()
	resolver := ports.AudioPreviewResolver(deezer.New(deezer.Config{}))
	if options.CacheOnly {
		resolver = cachedPreviewsOnly{}
	}
	runtime.preview = &audio.Service{
		Resolver: resolver, Store: store, Recordings: options.Recordings,
		DSPStore: store.DSP(), Authorized: true,
	}
	if options.CLAPBundle != "" {
		manifest, readErr := audio.ReadRuntimeBundle(options.CLAPBundle)
		if readErr != nil {
			return nil, readErr
		}
		worker := &audio.Worker{Executable: manifest.File(options.CLAPBundle, "worker"), BundleDir: options.CLAPBundle, Model: manifest.Model}
		if healthErr := worker.Health(ctx); healthErr != nil {
			_ = worker.Close()
			return nil, healthErr
		}
		runtime.clapPool = audio.NewWorkerPool(worker, audio.AnalysisParallelism())
		runtime.preview.Analyzer = runtime.clapPool
		runtime.preview.Policy = manifest.Policy
		runtime.preview.ParityValidated = manifest.Parity.Valid()
	}
	if options.MERTBundle != "" {
		manifest, readErr := audio.ReadMERTBundleContext(ctx, options.MERTBundle)
		if readErr != nil {
			return nil, readErr
		}
		worker := &audio.MERTWorker{BundleDir: options.MERTBundle, Model: manifest.Model}
		if healthErr := worker.Health(ctx); healthErr != nil {
			_ = worker.Close()
			return nil, healthErr
		}
		runtime.mertPool = audio.NewMERTWorkerPool(worker, audio.AnalysisParallelism())
		runtime.model = manifest.Model
		runtime.mert = &audio.MERTService{Preview: runtime.preview, Analyzer: runtime.mertPool, Store: store.Representations(), ParityValidated: manifest.Parity.ValidForBackend(manifest.Backend())}
		runtime.preview.MERT = runtime.mert
	}
	ok = true
	return runtime, nil
}

func (r *liveEnhanced) Close() error {
	if r == nil {
		return nil
	}
	var err error
	if r.mertPool != nil {
		err = r.mertPool.Close()
	}
	if r.clapPool != nil {
		if closeErr := r.clapPool.Close(); err == nil {
			err = closeErr
		}
	}
	if r.store != nil {
		if closeErr := r.store.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

func (r *liveEnhanced) Preview() *audio.Service { return r.preview }

func (r *liveEnhanced) Prepare(ctx context.Context, _ core.MusicIntent, _ core.TasteProfile, refs []core.TrackRef) (*core.EnhancedAudioSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return audio.PrepareEnhancedEvidence(ctx, r.preview, r.mert, r.catalogVersion, r.model, refs, true, nil)
}

func (r *liveEnhanced) Refresh(ctx context.Context, _ core.MusicIntent, _ core.TasteProfile, refs []core.TrackRef, previous *core.EnhancedAudioSnapshot) (*core.EnhancedAudioSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return audio.PrepareEnhancedEvidence(ctx, r.preview, r.mert, r.catalogVersion, r.model, refs, false, previous)
}

func (r *liveEnhanced) SearchMERT(ctx context.Context, intent core.MusicIntent, profile core.TasteProfile, queries []core.MERTSimilarityQuery, exclude map[string]struct{}, limit int) (*core.MERTSimilaritySearch, error) {
	if r.mert == nil || limit <= 0 || intent.Controls.RecommendationMode != core.EnhancedHybrid {
		return nil, nil
	}
	tracks := make([]core.TrackRef, 0, len(queries))
	for _, query := range queries {
		if query.Track.ID != "" {
			tracks = append(tracks, query.Track)
		}
	}
	snapshot, err := r.Prepare(ctx, intent, profile, tracks)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if snapshot == nil {
		return nil, err
	}
	input := snapshot.Input()
	search := &core.MERTSimilaritySearch{
		Recorded: true, CatalogVersion: input.CatalogVersion, Model: input.Model,
		Queries: append([]core.MERTSimilarityQuery(nil), queries...),
	}
	if input.Model.Dimension <= 0 {
		return search, err
	}
	validQueries := 0
	for index := range search.Queries {
		if representation, ok := input.Representations[search.Queries[index].Track.ID]; ok {
			search.Queries[index].RepresentationID = representation.ID
			validQueries++
		}
	}
	searcher := r.store.Representations()
	if validQueries == 0 {
		results, searchErr := searcher.SearchBatch(ctx, []ports.AudioRepresentationQuery{{CatalogVersion: input.CatalogVersion, Model: input.Model}})
		if searchErr != nil {
			return search, searchErr
		}
		if len(results) > 0 {
			search.SearchableTracks, search.ViewFingerprint = results[0].SearchableTracks, results[0].Fingerprint
		}
		return search, err
	}
	perQuery := max(1, int(math.Ceil(float64(limit)/float64(validQueries))))
	perQuery = min(perQuery, limit)
	baseExclude := make(map[string]struct{}, len(exclude)+len(queries))
	for id := range exclude {
		baseExclude[id] = struct{}{}
	}
	for _, query := range queries {
		baseExclude[query.Track.ID] = struct{}{}
	}
	batch := make([]ports.AudioRepresentationQuery, 0, validQueries)
	queryIndexes := make([]int, 0, validQueries)
	for index, query := range search.Queries {
		representation, ok := input.Representations[query.Track.ID]
		if !ok || representation.ID != query.RepresentationID {
			continue
		}
		queryIndexes = append(queryIndexes, index)
		batch = append(batch, ports.AudioRepresentationQuery{CatalogVersion: input.CatalogVersion, Model: input.Model, Vector: representation.Pooled, Limit: perQuery, Exclude: baseExclude})
	}
	results, searchErr := searcher.SearchBatch(ctx, batch)
	if searchErr != nil {
		return search, searchErr
	}
	for resultIndex, result := range results {
		query := search.Queries[queryIndexes[resultIndex]]
		search.SearchableTracks, search.ViewFingerprint = result.SearchableTracks, result.Fingerprint
		for rank, match := range result.Matches {
			search.Hits = append(search.Hits, core.MERTSimilarityHit{
				GroupID: query.GroupID, QueryTrackID: query.Track.ID, TrackID: match.Representation.TrackID,
				Rank: rank + 1, Score: match.Score, QueryWeight: query.Weight, Representation: match.Representation,
			})
		}
	}
	return search, err
}
