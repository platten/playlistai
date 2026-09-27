package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/reco/multichannel"
)

// AutomaticEngineOptions pins the producer and policy independently of the
// feature artifact. This adapter runs prepared inputs, not the desktop parser,
// downloader, provider caches, or a production retrieval index.
type AutomaticEngineOptions struct {
	ProducerSourceSHA256 string
	PolicySHA256         string
	PolicyFrozen         bool
	Seed                 core.RNGSeed
}

// AutomaticFeatureRequests is a fixed published-taxonomy task bank. It never
// inspects corpus labels or selects tasks based on model scores. Coarse grouped
// genre labels are alternatives, not a claim that each recording blends them.
func AutomaticFeatureRequests() []AutomaticFeatureRequest {
	tasks := []struct {
		facet, value, kind string
		words              []string
	}{
		{"voice_instrumental", "instrumental", "vocal", []string{"instrumental"}},
		{"voice_instrumental", "voice", "vocal", []string{"vocals"}},
		{"mood_acoustic", "acoustic", "texture", []string{"acoustic"}},
		{"mood_electronic", "electronic", "texture", []string{"electronic"}},
		{"mood_relaxed", "relaxed", "mood", []string{"relaxed"}},
		{"danceability", "danceable", "texture", []string{"danceable"}},
		{"genre_dortmund", "alternative", "genre", []string{"alternative"}},
		{"genre_dortmund", "blues", "genre", []string{"blues"}},
		{"genre_dortmund", "electronic", "genre", []string{"electronic"}},
		{"genre_dortmund", "folk_country", "genre", []string{"folk", "country"}},
		{"genre_dortmund", "funk_soul_rnb", "genre", []string{"funk", "soul", "r&b"}},
		{"genre_dortmund", "jazz", "genre", []string{"jazz"}},
		{"genre_dortmund", "pop", "genre", []string{"pop"}},
		{"genre_dortmund", "rap_hiphop", "genre", []string{"rap", "hip hop"}},
		{"genre_dortmund", "rock", "genre", []string{"rock"}},
	}
	out := make([]AutomaticFeatureRequest, 0, len(tasks))
	for _, task := range tasks {
		count := 10
		if task.facet == "voice_instrumental" && task.value == "instrumental" {
			// Distinct outputs, rather than repeated seeds, must support the
			// one-sided vocal-leakage confidence bound when evidence permits.
			count = 60
		}
		intent := core.MusicIntent{Version: core.CurrentIntentVersion, Seed: core.NewRNGSeed(1), Controls: core.IntentControls{RecommendationMode: core.Automatic, TotalTrackCount: count}}
		for _, word := range task.words {
			criterion := core.MusicalCriterion{Kind: task.kind, Value: word, Scope: "playlist", Strength: "essential"}
			if len(task.words) > 1 {
				criterion.Group = task.value
			}
			intent.EssentialCriteria = append(intent.EssentialCriteria, criterion)
		}
		out = append(out, AutomaticFeatureRequest{Facet: task.facet, Value: task.value, Intent: intent.Normalized()})
	}
	return out
}

// RunAutomaticFeatures uses the production engine for all ablations. The
// fixed 300-recording pool deliberately measures fit/assembly over prepared
// inputs; its candidate order is not a simulated production index. Target
// labels stay in the scorer and are never copied into this catalog.
func RunAutomaticFeatures(ctx context.Context, corpus AutomaticCorpus, features AutomaticFeatures, options AutomaticEngineOptions) (AutomaticMeasurements, error) {
	measurements := AutomaticMeasurements{Version: AutomaticEvaluationVersion, CorpusSHA256: AutomaticCorpusSHA256(corpus), ProducerSourceSHA256: options.ProducerSourceSHA256, PolicySHA256: options.PolicySHA256, PolicyFrozen: options.PolicyFrozen}
	if err := validateAutomaticFeatures(corpus, features, options); err != nil {
		return measurements, err
	}
	raw, _ := json.Marshal(features)
	measurements.FeatureSnapshotSHA256 = automaticSHA(raw)
	var ids []string
	for _, track := range features.Tracks {
		ids = append(ids, track.ID)
	}
	sort.Strings(ids)
	for taskIndex, query := range features.Queries {
		for _, variant := range []string{"metadata", "audio", "combined"} {
			if err := ctx.Err(); err != nil {
				return measurements, err
			}
			intent := query.Request.Intent.Normalized()
			intent.Controls.RecommendationMode, intent.Seed = core.Automatic, options.Seed
			catalog := newAutomaticFeatureCatalog(features, query, variant, measurements.FeatureSnapshotSHA256)
			engine := multichannel.NewAutomatic(catalog, nil, catalog, multichannel.DefaultConfig())
			started := time.Now()
			playlist, err := engine.Build(ctx, intent)
			milliseconds := max(int64(1), (time.Since(started).Nanoseconds()+int64(time.Millisecond)-1)/int64(time.Millisecond))
			run := AutomaticRun{TaskID: fmt.Sprintf("fixed-taxonomy-%02d:%s:%s", taskIndex, query.Request.Facet, query.Request.Value), Split: features.Split, Variant: variant, CacheCondition: "warm_installed", Seed: string(options.Seed), CandidateIDs: append([]string(nil), ids...), CandidateSHA256: AutomaticCandidateSHA256(ids), Facet: query.Request.Facet, Value: query.Request.Value, Requested: intent.Count, Milliseconds: &milliseconds, TimingScope: "prepared_engine", RetrievalScope: "fixed_pool_order"}
			if err != nil {
				run.Error = err.Error()
			}
			if playlist.Search != nil {
				for _, candidate := range playlist.Search.Candidates {
					run.Retrieved = append(run.Retrieved, candidate.Track.ID)
				}
				for _, candidate := range playlist.Search.EligibleCandidates {
					if candidate.FitAssessment != nil && candidate.FitAssessment.State == core.AutomaticStrong {
						run.StrongAdmissions = append(run.StrongAdmissions, candidate.Track.ID)
					}
				}
			}
			violations := 0
			seen := map[string]bool{}
			for _, track := range playlist.Tracks {
				meta, ok := catalog.Meta(track.ID)
				if !ok || meta.Ref.Artist != track.Artist || seen[track.ID] {
					violations++
				}
				seen[track.ID] = true
				run.Output = append(run.Output, AutomaticOutput{ID: track.ID, ArtistID: track.Artist})
			}
			if len(playlist.Tracks) > intent.Count {
				violations += len(playlist.Tracks) - intent.Count
			}
			run.FactualViolations, run.FactualChecker = &violations, "fixed-corpus-identity+duplicates+count/v1"
			measurements.Runs = append(measurements.Runs, run)
		}
	}
	return measurements, ctx.Err()
}

func validateAutomaticFeatures(corpus AutomaticCorpus, features AutomaticFeatures, options AutomaticEngineOptions) error {
	if err := corpus.Validate(); err != nil {
		return err
	}
	if _, err := options.Seed.Int64(); err != nil || options.Seed.IsZero() || !automaticValidSHA(options.PolicySHA256) || !automaticValidSHA(options.ProducerSourceSHA256) || features.Split == "heldout" && !options.PolicyFrozen {
		return errors.New("automatic engine evaluation: valid seed and frozen policy/source identities required")
	}
	if features.Split == "heldout" && features.FrozenPolicySHA256 != options.PolicySHA256 {
		return errors.New("automatic engine evaluation: heldout feature policy differs from frozen policy")
	}
	if features.Version != AutomaticFeaturesVersion || features.CorpusSHA256 != AutomaticCorpusSHA256(corpus) || features.Split != "development" && features.Split != "heldout" || len(features.Tracks) != 300 || len(features.Queries) == 0 || len(features.Queries) > 64 || features.DecoderID == "" || features.Sampling == "" || features.Model.Dimension <= 0 || features.Model.Dimension > 4096 || features.Model.Model == "" || features.Model.Revision == "" || features.Model.Runtime == "" || !automaticValidSHA(features.Model.Weights) {
		return errors.New("automatic engine evaluation: incomplete feature identity or pool")
	}
	known := map[string]AutomaticCorpusTrack{}
	for _, track := range corpus.Tracks {
		if track.Split == features.Split {
			// Labels are intentionally not passed to the feature catalog.
			known[track.ID] = track
		}
	}
	seen := map[string]bool{}
	for _, track := range features.Tracks {
		identity, ok := known[track.ID]
		if !ok || seen[track.ID] || track.ArtistID != identity.ArtistID || track.AudioSHA256 != identity.LowAudioSHA256 {
			return errors.New("automatic engine evaluation: feature identity differs from pinned corpus")
		}
		seen[track.ID] = true
		if len(track.Annotations) > 0 && (!automaticValidSHA(features.MetadataSourceSHA256) || features.MetadataSourceURL == "" || features.MetadataSourceSHA256 == corpus.AnnotationsSHA256) {
			return errors.New("automatic engine evaluation: metadata requires a separate declared source")
		}
		if track.Error != "" {
			if len(track.Pooled) > 0 || len(track.Segments) > 0 || track.CoveredSeconds != 0 {
				return errors.New("automatic engine evaluation: failed acquisition contains usable audio")
			}
			continue
		}
		if !automaticFeatureVector(track.Pooled, features.Model.Dimension, false) || len(track.Segments) == 0 || !finiteFeature(track.DurationSeconds) || track.DurationSeconds <= 0 {
			return errors.New("automatic engine evaluation: invalid audio vector or duration")
		}
		end, covered := 0.0, 0.0
		for _, segment := range track.Segments {
			if !finiteFeature(segment.StartSeconds) || !finiteFeature(segment.EndSeconds) || segment.StartSeconds < end || segment.EndSeconds <= segment.StartSeconds || segment.EndSeconds > track.DurationSeconds+.002 || !automaticFeatureVector(segment.Embedding, features.Model.Dimension, false) {
				return errors.New("automatic engine evaluation: invalid sampled audio intervals")
			}
			end, covered = segment.EndSeconds, covered+segment.EndSeconds-segment.StartSeconds
		}
		if !finiteFeature(track.CoveredSeconds) || math.Abs(covered-track.CoveredSeconds) > .002 {
			return errors.New("automatic engine evaluation: inconsistent measured coverage")
		}
	}
	for _, query := range features.Queries {
		intent := query.Request.Intent.Normalized()
		if query.Request.Facet == "" || query.Request.Value == "" || intent.Count < 1 || intent.Count > 100 || len(intent.References)+len(intent.RequiredTracks)+len(intent.Journey.Waypoints)+len(intent.HardConstraints)+len(intent.Temporal) > 0 || intent.Start != nil || intent.Destination != nil {
			return errors.New("automatic engine evaluation: unsupported factual task; use the independent factual suite")
		}
		intent.Controls.RecommendationMode = core.EnhancedHybrid
		clauses := audio.Clauses(intent)
		if len(clauses) == 0 || len(query.Queries) != len(clauses) {
			return errors.New("automatic engine evaluation: incomplete encoded task clauses")
		}
		for i, q := range query.Queries {
			if !reflect.DeepEqual(q.Clause, clauses[i]) || !automaticFeatureVector(q.Values, features.Model.Dimension, true) {
				return errors.New("automatic engine evaluation: query identity/vector differs from frozen task")
			}
		}
	}
	return nil
}

func finiteFeature(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func automaticFeatureVector(vector []float32, dimension int, mean bool) bool {
	if len(vector) != dimension {
		return false
	}
	var norm float64
	for _, value := range vector {
		if !finiteFeature(float64(value)) {
			return false
		}
		norm += float64(value) * float64(value)
	}
	return norm > 0 && norm <= 1.0002 && (mean || math.Abs(norm-1) <= .0002)
}

type automaticFeatureCatalog struct {
	ids         []string
	meta        map[string]core.TrackMeta
	assessments map[string]core.AudioAssessment
}

func newAutomaticFeatureCatalog(features AutomaticFeatures, query AutomaticFeatureQuery, variant, identity string) *automaticFeatureCatalog {
	catalog := &automaticFeatureCatalog{meta: map[string]core.TrackMeta{}, assessments: map[string]core.AudioAssessment{}}
	for _, track := range features.Tracks {
		ref := core.TrackRef{ID: track.ID, Artist: track.ArtistID, Title: track.ID, RecordingIdentity: "mtg-audio:" + track.AudioSHA256}
		meta := core.TrackMeta{Ref: ref}
		if variant != "audio" {
			meta.Annotations = append([]core.MetadataAnnotation(nil), track.Annotations...)
		}
		catalog.ids = append(catalog.ids, track.ID)
		catalog.meta[track.ID] = meta
		if variant == "metadata" || track.Error != "" {
			continue
		}
		assessment := core.AudioAssessment{ModelFingerprint: audio.Fingerprint(features.Model), TrackID: track.ID, AnalysisID: identity + ":" + track.ID, PolicyVersion: "prepared-evaluation/segments-v1", LibraryCoverage: &core.LibraryCLAPCoverage{CoveredSeconds: track.CoveredSeconds, Incomplete: track.CoveredSeconds < track.DurationSeconds}}
		if assessment.LibraryCoverage.Incomplete {
			assessment.LibraryCoverage.PartialReason = "Sampled recording intervals only."
		}
		for _, segment := range track.Segments {
			assessment.LibraryCoverage.Segments = append(assessment.LibraryCoverage.Segments, core.LibraryAudioInterval{StartSeconds: segment.StartSeconds, EndSeconds: segment.EndSeconds})
		}
		for _, vector := range query.Queries {
			var weighted, duration float64
			strongest := math.Inf(-1)
			for _, segment := range track.Segments {
				var score float64
				for i, v := range vector.Values {
					score += float64(v) * float64(segment.Embedding[i])
				}
				seconds := segment.EndSeconds - segment.StartSeconds
				weighted, duration = weighted+score*seconds, duration+seconds
				strongest = max(strongest, score)
			}
			score := weighted / duration
			if vector.Clause.Negative {
				score = strongest
			}
			assessment.Clauses = append(assessment.Clauses, core.AudioClauseAssessment{Clause: vector.Clause, Score: max(-1, min(1, score)), ScoreAvailable: true, State: core.EvidenceUnknown})
		}
		catalog.assessments[track.ID] = assessment
	}
	sort.Strings(catalog.ids)
	return catalog
}

func (c *automaticFeatureCatalog) Len() int { return len(c.ids) }
func (*automaticFeatureCatalog) Dim() int   { return 0 }
func (c *automaticFeatureCatalog) ID(row int) string {
	if row < 0 || row >= len(c.ids) {
		return ""
	}
	return c.ids[row]
}
func (c *automaticFeatureCatalog) RowOf(id string) (int, bool) { return slices.BinarySearch(c.ids, id) }
func (c *automaticFeatureCatalog) Meta(id string) (core.TrackMeta, bool) {
	meta, ok := c.meta[id]
	return meta, ok
}
func (*automaticFeatureCatalog) VectorsByRow(int) (ports.Vectors, bool) {
	return ports.Vectors{}, false
}
func (*automaticFeatureCatalog) Vectors(string) (ports.Vectors, bool) { return ports.Vectors{}, false }
func (*automaticFeatureCatalog) RawRow(int) ([]int8, []int8, bool)    { return nil, nil, false }
func (*automaticFeatureCatalog) Resolve(string, int) []core.TrackRef  { return nil }
func (c *automaticFeatureCatalog) Retrieve(ctx context.Context, _ ports.RetrievalRequest) ([]core.Candidate, error) {
	var candidates []core.Candidate
	for _, id := range c.ids {
		if err := ctx.Err(); err != nil {
			return candidates, err
		}
		candidates = append(candidates, core.Candidate{Track: c.meta[id].Ref, Sources: []core.RetrievalEvidence{{Channel: "fixed_evaluation_pool", QueryID: "all300", Rank: 1}}})
	}
	return candidates, nil
}
func (*automaticFeatureCatalog) BindLibraryQueries(core.AudioModelIdentity, []core.AudioClauseVector) {
}
func (*automaticFeatureCatalog) LibraryCLAPVector(ctx context.Context, _ string) (core.LibraryVector, bool, error) {
	return core.LibraryVector{}, false, ctx.Err()
}
func (c *automaticFeatureCatalog) LibraryAssessment(ctx context.Context, id string) (core.AudioAssessment, bool, error) {
	assessment, ok := c.assessments[id]
	return assessment, ok, ctx.Err()
}
