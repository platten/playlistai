package multichannel

import (
	"context"
	"math"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
	"github.com/platten/playlistai/internal/ports"
)

const ChannelMusicContext = "music_context"

func referenceContextScope(intent core.MusicIntent, reference core.IntentReference) string {
	key := referenceKey(reference)
	if intent.Start != nil && referenceKey(*intent.Start) == key {
		return "journey_start"
	}
	if intent.Destination != nil && referenceKey(*intent.Destination) == key {
		return "journey_end"
	}
	for _, waypoint := range intent.Journey.Waypoints {
		if referenceKey(waypoint) == key {
			return "journey_via"
		}
	}
	return "playlist"
}

func matchesContextReference(plan core.ContextSeedPlan, reference core.IntentReference) bool {
	if plan.ReferenceKind != reference.Kind || reference.Influence == core.InfluenceNegative {
		return false
	}
	if plan.EntityKey != "" {
		return reference.Resolution != nil && reference.Resolution.Status == core.ResolutionResolved &&
			reference.Resolution.Selected != nil && reference.Resolution.Selected.EntityID == plan.EntityKey
	}
	return plan.Query != "" && core.NormalizeIdentityPart(plan.Query) == core.NormalizeIdentityPart(reference.Query)
}

// Context may select different representatives of an explicit artist or album.
// It must never replace an explicit recording, alter required endpoints, or
// cross over into the other recommendation policies. Missing/old context keeps
// the ordinary catalog representatives, including for old saved histories.
func contextualReferenceVectors(cat ports.Catalog, intent core.MusicIntent, reference core.IntentReference) []weightedVectors {
	if intent.Controls.RecommendationMode != core.EnhancedHybrid || intent.Knowledge == nil ||
		(reference.Kind != core.ReferenceArtist && reference.Kind != core.ReferenceAlbum) {
		return nil
	}
	for _, plan := range intent.Knowledge.ContextPlans {
		if plan.Profile.ExtractorVersion != core.ContextProfileVersion ||
			plan.Scope != referenceContextScope(intent, reference) || !matchesContextReference(plan, reference) {
			continue
		}
		var tracks []core.WeightedTrack
		seen := map[string]bool{}
		var total float64
		for _, seed := range plan.Seeds {
			meta, ok := cat.Meta(seed.TrackID)
			if !ok || math.IsNaN(seed.Weight) || math.IsInf(seed.Weight, 0) || seed.Weight <= 0 {
				continue
			}
			key := core.ProvisionalRecordingKey(meta.Ref)
			if seen[key] {
				continue
			}
			if vectors, ok := cat.Vectors(seed.TrackID); !ok || !vectorAvailable(vectors.Audio) && !vectorAvailable(vectors.Track) {
				continue
			}
			seen[key] = true
			tracks = append(tracks, seed)
			total += seed.Weight
			if len(tracks) == 4 {
				break
			}
		}
		if total > 0 {
			for i := range tracks {
				tracks[i].Weight /= total
			}
			return trackVectors(cat, tracks)
		}
	}
	return nil
}

func activeContextPlan(intent core.MusicIntent, plan core.ContextSeedPlan) bool {
	if plan.Profile.ExtractorVersion != core.ContextProfileVersion || plan.Scope == "journey_end" {
		return false // Destination context must not redefine a starting category.
	}
	if plan.Profile.Kind == "genre" {
		for _, group := range [][]core.IntentPreference{intent.Preferences.Genres, intent.Preferences.Styles} {
			for _, preference := range group {
				if preference.Influence != core.InfluenceNegative && contextScopeApplies(preference.Scope, plan.Scope) && strings.EqualFold(musicconcepts.Canonical("genre", preference.Value), musicconcepts.Canonical("genre", plan.Query)) {
					return true
				}
			}
		}
		for _, criterion := range intent.EssentialCriteria {
			if (criterion.Kind == "genre" || criterion.Kind == "style") && criterion.Scope == plan.Scope &&
				strings.EqualFold(musicconcepts.Canonical("genre", criterion.Value), musicconcepts.Canonical("genre", plan.Query)) {
				return true
			}
		}
		return false
	}
	references := append(append([]core.IntentReference(nil), intent.References...), intent.Journey.Waypoints...)
	if intent.Start != nil {
		references = append(references, *intent.Start)
	}
	for _, reference := range references {
		if matchesContextReference(plan, reference) && plan.Scope == referenceContextScope(intent, reference) {
			return true
		}
	}
	return false
}

func contextScopeApplies(requirement, plan string) bool {
	return requirement == "" || requirement == "playlist" || requirement == plan
}

func excludedContextGenre(intent core.MusicIntent, genre, scope string) bool {
	graph := core.GenreGraph{}
	if intent.Knowledge != nil {
		graph = intent.Knowledge.Graph
	}
	for _, group := range [][]core.IntentPreference{intent.Preferences.Genres, intent.Preferences.Styles} {
		for _, preference := range group {
			if preference.Influence == core.InfluenceNegative && contextScopeApplies(preference.Scope, scope) && enhancedCategoryMatches(preference.Value, genre, graph) {
				return true
			}
		}
	}
	for _, constraint := range intent.HardConstraints {
		if (constraint.Kind == "exclude_genre" || constraint.Kind == "exclude_style") && enhancedCategoryMatches(constraint.Value, genre, graph) {
			return true
		}
	}
	return false
}

func excludedContextCharacteristic(intent core.MusicIntent, characteristic, scope string) bool {
	for kind, group := range map[string][]core.IntentPreference{
		"mood": intent.Preferences.Moods, "instrumentation": intent.Preferences.Instrumentation,
		"texture": intent.Preferences.TextureDescriptions, "vocal": intent.Preferences.VocalPreferences,
	} {
		for _, preference := range group {
			if preference.Influence == core.InfluenceNegative && contextScopeApplies(preference.Scope, scope) && strings.EqualFold(musicconcepts.Canonical(kind, preference.Value), musicconcepts.Canonical(kind, characteristic)) {
				return true
			}
		}
	}
	for _, constraint := range intent.HardConstraints {
		if strings.HasPrefix(constraint.Kind, "exclude_") && strings.EqualFold(strings.TrimSpace(constraint.Value), characteristic) {
			return true
		}
	}
	return false
}

type contextQuery struct {
	text  string
	genre bool
}

func contextQueries(profile core.ContextProfile) []contextQuery {
	// Interleave facets so several broad genres cannot crowd out the source's
	// sound descriptors before the shared four-query cap.
	var queries []contextQuery
	for index := 0; index < max(len(profile.Genres), len(profile.Characteristics)); index++ {
		if index < len(profile.Genres) {
			queries = append(queries, contextQuery{musicconcepts.Canonical("genre", profile.Genres[index]), true})
		}
		if index < len(profile.Characteristics) {
			queries = append(queries, contextQuery{strings.TrimSpace(profile.Characteristics[index]), false})
		}
	}
	return queries
}

// The optional semantic index broadens the candidate union using short,
// attributed source hints. These queries never enter semanticQueryText,
// essential criteria, audio verification clauses or recording genre tags.
func (r *Retriever) retrieveMusicContext(ctx context.Context, intent core.MusicIntent, byID map[string]*core.Candidate, exclude map[string]struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.semantic == nil || intent.Knowledge == nil || intent.Controls.RecommendationMode != core.EnhancedHybrid {
		return nil
	}
	seen := map[string]bool{}
	for _, plan := range intent.Knowledge.ContextPlans {
		if !activeContextPlan(intent, plan) {
			continue
		}
		for _, hint := range contextQueries(plan.Profile) {
			query := hint.text
			key := strings.ToLower(query)
			if query == "" || len(query) > 120 || seen[key] || hint.genre && excludedContextGenre(intent, query, plan.Scope) || !hint.genre && excludedContextCharacteristic(intent, query, plan.Scope) {
				continue
			}
			seen[key] = true
			hits, err := r.semantic.Search(ctx, query, maxInt(1, r.cfg.SemanticBudget/4), exclude)
			if err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			if err == nil {
				for index, hit := range hits {
					r.addSource(byID, ports.Match{ID: hit.TrackID, Score: float32(hit.Score)}, core.RetrievalEvidence{
						Channel: ChannelMusicContext, QueryID: plan.Profile.ID + ":" + query, Rank: index + 1, Score: hit.Score, QueryWeight: .5,
					})
				}
			}
			if len(seen) == 4 {
				return nil
			}
		}
	}
	return ctx.Err()
}
