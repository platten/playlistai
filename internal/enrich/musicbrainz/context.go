package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
	"github.com/platten/playlistai/internal/ports"
)

const contextBudget = 8 * time.Second
const contextRequests = 8
const contextReferences = 3
const contextSeeds = 4

type contextRequestLimitKey struct{}

var contextMBID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type contextEntity struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Title        string           `json:"title"`
	Genres       []mbTag          `json:"genres"`
	ArtistCredit []mbArtistCredit `json:"artist-credit"`
	FirstDate    string           `json:"first-release-date"`
	PrimaryType  string           `json:"primary-type"`
	Secondary    []string         `json:"secondary-types"`
	Aliases      []struct {
		Name string `json:"name"`
	} `json:"aliases"`
	Relations []struct {
		Type  string `json:"type"`
		Ended bool   `json:"ended"`
		URL   struct {
			Resource string `json:"resource"`
		} `json:"url"`
	} `json:"relations"`
}

// prepareContext never rewrites a reference, preference, or recording feature.
// The optional child deadline cannot cancel its parent generation. All provider
// calls are sequential and share the existing request counter and rate limiter.
func (c *Client) prepareContext(ctx context.Context, intent core.MusicIntent, cat ports.Catalog, resolver ports.ReferenceResolver, snapshot *core.KnowledgeSnapshot, p ports.Progress) {
	ctx, cancel := context.WithTimeout(ctx, contextBudget)
	defer cancel()
	limit := contextRequests
	if budget, ok := ctx.Value(knowledgeBudgetKey{}).(*knowledgeBudget); ok {
		limit += budget.requests
	} else {
		ctx = context.WithValue(ctx, knowledgeBudgetKey{}, &knowledgeBudget{})
	}
	ctx = context.WithValue(ctx, contextRequestLimitKey{}, limit)
	type request struct {
		ref   core.IntentReference
		scope string
	}
	var requests []request
	if intent.Start != nil {
		requests = append(requests, request{*intent.Start, "journey_start"})
	}
	if intent.Destination != nil {
		requests = append(requests, request{*intent.Destination, "journey_end"})
	}
	for _, ref := range intent.References {
		requests = append(requests, request{ref, "playlist"})
	}
	for _, ref := range intent.Journey.Waypoints {
		requests = append(requests, request{ref, "journey_via"})
	}
	seen := map[string]bool{}
	for _, request := range requests {
		ref := request.ref
		key := request.scope + "\x00" + string(ref.Kind) + "\x00" + core.NormalizeIdentityPart(ref.Query)
		if seen[key] || ref.Influence == core.InfluenceNegative || ref.Kind != core.ReferenceArtist && ref.Kind != core.ReferenceAlbum {
			continue
		}
		if len(seen) >= contextReferences || ctx.Err() != nil {
			break
		}
		seen[key] = true
		p.Report("generation", 0, 0, "Finding source context for music references")
		plan, ok := c.referenceContext(ctx, intent, ref, request.scope, cat, resolver)
		if ok {
			snapshot.ContextPlans = append(snapshot.ContextPlans, plan)
		}
	}
	if len(requests) == 0 && ctx.Err() == nil {
		c.genreContext(ctx, intent, snapshot)
	}
	if len(seen) > 0 && len(snapshot.ContextPlans) == 0 {
		snapshot.Notices = append(snapshot.Notices, "Optional artist or album context was unavailable; using the resolved references and existing musical evidence.")
	}
}

func (c *Client) referenceContext(ctx context.Context, intent core.MusicIntent, ref core.IntentReference, scope string, cat ports.Catalog, resolver ports.ReferenceResolver) (core.ContextSeedPlan, bool) {
	resolution := ref.Resolution
	if resolution == nil || resolution.CatalogVersion != resolver.CatalogVersion() || resolution.Status != core.ResolutionResolved {
		local := resolver.ResolveReference(ref)
		resolution = &local
	}
	// An album at Start may not have passed through the legacy album resolver.
	// Resolve a private copy so requested identity and required-output semantics
	// are not changed by this optional service.
	if ref.Kind == core.ReferenceAlbum && (resolution.Status != core.ResolutionResolved || resolution.Selected == nil) {
		copy := c.resolveAlbum(ctx, ref, cat, resolver, &core.KnowledgeSnapshot{})
		if copy.Resolution != nil {
			resolution = copy.Resolution
		}
	}
	if resolution.Status != core.ResolutionResolved || resolution.Selected == nil {
		return core.ContextSeedPlan{}, false
	}
	selected := resolution.Selected
	entityID, kind := selected.EntityID, "artist"
	var identitySources []core.ContextSource
	if ref.Kind == core.ReferenceAlbum {
		kind = "release-group"
	}
	if !contextMBID.MatchString(entityID) {
		if ref.Kind != core.ReferenceArtist || selected.Artist == "" {
			return core.ContextSeedPlan{}, false
		}
		artist, sources, ok := c.contextArtistIdentity(ctx, *selected, cat)
		if !ok || !contextMBID.MatchString(artist.ID) || !seedNameMatches(selected.Artist, artist.names()) {
			return core.ContextSeedPlan{}, false
		}
		entityID = artist.ID
		identitySources = sources
	}
	entity, raw, path, err := c.contextEntity(ctx, kind, entityID)
	if err != nil {
		return core.ContextSeedPlan{}, false
	}
	if ref.Kind == core.ReferenceArtist {
		names := []string{entity.Name}
		for _, alias := range entity.Aliases {
			names = append(names, alias.Name)
		}
		if !seedNameMatches(selected.Artist, names) {
			return core.ContextSeedPlan{}, false
		}
	} else if core.NormalizeIdentityPart(entity.Title) != core.NormalizeIdentityPart(selected.Title) || !contextArtistCredit(entity.ArtistCredit, "", selected.Artist) {
		return core.ContextSeedPlan{}, false
	}
	profile := core.ContextProfile{Kind: string(ref.Kind), EntityKey: "musicbrainz:" + kind + ":" + entityID, Query: ref.Query, Name: entity.Name, ExtractorVersion: core.ContextProfileVersion}
	if ref.Kind == core.ReferenceAlbum {
		profile.Name = entity.Title
		profile.ReleaseGroupIDs = []string{entityID}
		profile.FirstYear = yearOf(entity.FirstDate)
		profile.LastYear = profile.FirstYear
	}
	profile.Genres = contextGenres(entity.Genres)
	profile.Sources = []core.ContextSource{contextMBSource(c.base+path, raw)}
	profile.Sources = append(profile.Sources, identitySources...)
	plan := core.ContextSeedPlan{ReferenceKind: ref.Kind, Query: ref.Query, EntityKey: selected.EntityID, Scope: scope, Profile: profile}
	// A scoped seed search is optional. Failure never substitutes a broad
	// artist seed while claiming that the requested era was established.
	if ref.Kind == core.ReferenceAlbum {
		plan.Seeds = c.contextRecordingSeeds(ctx, "rgid:"+entityID, "", selected.Artist, cat, resolver, &plan.Profile)
		plan.Profile.ScopeNote = "Catalog recordings from the identified album; musical suitability remains to be assessed."
	} else {
		period, early := contextualPeriod(intent, ref, selected.Artist, scope)
		if period != nil || early {
			c.contextArtistAlbums(ctx, entityID, selected.Artist, period, early, cat, resolver, &plan)
		} else {
			plan.Seeds = contextCatalogSeeds(selected.Representatives, cat)
			plan.Profile.ScopeNote = "Existing artist representatives; source context does not verify their musical suitability."
		}
	}
	c.linkedContext(ctx, entityID, kind, entity, &plan.Profile)
	var sourceGenres []string
	for _, genre := range entity.Genres {
		sourceGenres = append(sourceGenres, genre.Name)
	}
	plan.Profile.Characteristics = contextCharacteristics(plan.Profile.Description, sourceGenres...)
	plan.Profile.ID = knowledgeHash(plan.Profile)
	return plan, true
}

func (c *Client) contextEntity(ctx context.Context, kind, id string) (contextEntity, []byte, string, error) {
	path := "/ws/2/" + kind + "/" + id + "?" + url.Values{"fmt": {"json"}, "inc": {"genres+url-rels+artists"}}.Encode()
	if kind == "artist" {
		path = "/ws/2/artist/" + id + "?" + url.Values{"fmt": {"json"}, "inc": {"genres+url-rels+aliases"}}.Encode()
	}
	raw, err := c.metadataGet(ctx, c.base, path, "entity-context-v1:", c.hc, false)
	var entity contextEntity
	if err == nil {
		err = json.Unmarshal(raw, &entity)
	}
	if err == nil && !strings.EqualFold(entity.ID, id) {
		err = fmt.Errorf("context entity identity differs from the linked entity")
	}
	return entity, raw, path, err
}

func contextArtistCredit(credits []mbArtistCredit, id, name string) bool {
	for _, credit := range credits {
		creditedName := credit.Name
		if creditedName == "" {
			creditedName = credit.Artist.Name
		}
		if seedNameKey(creditedName) == seedNameKey(name) && (id == "" || credit.Artist.ID == id) {
			return true
		}
	}
	return false
}

func contextMBSource(source string, raw []byte) core.ContextSource {
	// Genre votes are supplementary MusicBrainz data. Retain the restrictive
	// license for this mixed entity context rather than claiming all fields CC0.
	return core.ContextSource{Provider: "musicbrainz", URL: source, Revision: knowledgeHash(json.RawMessage(raw)), License: "CC-BY-NC-SA-3.0"}
}

func contextGenres(tags []mbTag) []string {
	// The API may return alphabetically ordered tags. Rank positive vote counts
	// before limiting; exact aliases share their strongest count, not summed votes.
	counts := map[string]int{}
	for _, tag := range tags {
		name := musicconcepts.Canonical("genre", strings.TrimSpace(tag.Name))
		key := core.NormalizeIdentityPart(name)
		if tag.Count <= 0 || key == "" {
			continue
		}
		if tag.Count > counts[key] {
			counts[key] = tag.Count
		}
	}
	genres := make([]string, 0, len(counts))
	for name := range counts {
		genres = append(genres, name)
	}
	sort.Slice(genres, func(i, j int) bool {
		if counts[genres[i]] == counts[genres[j]] {
			return genres[i] < genres[j]
		}
		return counts[genres[i]] > counts[genres[j]]
	})
	return genres[:min(len(genres), 6)]
}

func contextCatalogSeeds(candidates []core.WeightedTrack, cat ports.Catalog) []core.WeightedTrack {
	var seeds []core.WeightedTrack
	seen := map[string]bool{}
	var total float64
	for _, candidate := range candidates {
		if _, ok := cat.Meta(candidate.TrackID); !ok || seen[candidate.TrackID] {
			continue
		}
		seen[candidate.TrackID] = true
		if candidate.Weight <= 0 {
			candidate.Weight = 1
		}
		seeds = append(seeds, candidate)
		total += candidate.Weight
		if len(seeds) == contextSeeds {
			break
		}
	}
	for i := range seeds {
		seeds[i].Weight /= total
	}
	return seeds
}

func contextualPeriod(intent core.MusicIntent, ref core.IntentReference, artist, scope string) (*core.TemporalRequirement, bool) {
	for _, period := range intent.Temporal {
		if period.Basis == "original_release" && period.StartYear > 0 && period.EndYear >= period.StartYear && (period.Scope == "" || period.Scope == "playlist" || period.Scope == scope) {
			return &period, false
		}
	}
	// This narrow phrase only proposes an early-discography retrieval scope.
	// It never adds Temporal or a required classifier to the user's intent.
	prompt := " " + contextWords(intent.OriginalDescription) + " "
	for _, name := range []string{ref.Query, artist} {
		if key := contextWords(name); key != "" && strings.Contains(prompt, " early "+key+" ") {
			negated := false
			for _, prefix := range []string{" not", " no", " avoid", " without", " unlike", " rather than", " instead of"} {
				negated = negated || strings.Contains(prompt, prefix+" early "+key+" ")
			}
			if !negated {
				return nil, true
			}
		}
	}
	return nil, false
}

func contextWords(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }), " ")
}

func contextCharacteristics(description string, sourceGenres ...string) []string {
	var result []string
	seen := map[string]bool{}
	// A genre phrase is not a free-standing texture claim. Heavy metal is
	// deliberately distinct from the registry's broader metal category.
	genrePhrases := append([]string{"heavy metal"}, sourceGenres...)
	concepts := musicconcepts.Concepts()
	for _, concept := range concepts {
		if concept.Kind == "genre" {
			genrePhrases = append(genrePhrases, concept.Value)
			genrePhrases = append(genrePhrases, concept.Aliases...)
		}
	}
	sort.Slice(genrePhrases, func(i, j int) bool { return len(genrePhrases[i]) > len(genrePhrases[j]) })
	for _, sentence := range strings.FieldsFunc(description, func(r rune) bool { return r == '.' || r == ';' || r == '\n' }) {
		words := " " + contextWords(sentence) + " "
		musical := false
		for _, marker := range []string{" music ", " sound ", " album ", " tracks ", " recordings ", " style "} {
			musical = musical || strings.Contains(words, marker)
		}
		if !musical {
			continue
		}
		negated := false
		for _, marker := range []string{" not ", " no ", " without ", " unlike ", " rather than ", " instead of "} {
			negated = negated || strings.Contains(words, marker)
		}
		if negated {
			continue
		}
		for _, genre := range genrePhrases {
			phrase := contextWords(genre)
			if strings.Contains(phrase, " ") {
				words = strings.ReplaceAll(words, " "+phrase+" ", " ")
			}
		}
		for _, concept := range concepts {
			if concept.Kind != "mood" && concept.Kind != "texture" && concept.Kind != "instrumentation" {
				continue
			}
			for _, term := range append([]string{concept.Value}, concept.Aliases...) {
				if strings.Contains(words, " "+contextWords(term)+" ") && !seen[concept.ID] {
					seen[concept.ID] = true
					result = append(result, concept.Value)
					if len(result) == 4 {
						return result
					}
					break
				}
			}
		}
	}
	return result
}

func contextualDiscoveryGenres(intent core.MusicIntent, plans []core.ContextSeedPlan) []string {
	genres := discoveryGenres(intent)
	if intent.Controls.RecommendationMode != core.EnhancedHybrid {
		return genres
	}
	// Explicit categories retain the entire metadata discovery allowance. Context
	// may support a reference-only request, but cannot broaden a counted genre.
	if len(genres) > 0 {
		return genres
	}
	seen := map[string]bool{}
	for _, genre := range genres {
		seen[core.NormalizeIdentityPart(genre)] = true
	}
	for _, plan := range plans {
		if plan.Scope != "playlist" && plan.Scope != "journey_start" || !contextPlanActive(intent, plan) {
			continue
		}
		for _, genre := range plan.Profile.Genres {
			key := core.NormalizeIdentityPart(genre)
			if key == "" || seen[key] || contextExcludedGenre(intent, genre, plan.Scope) {
				continue
			}
			seen[key] = true
			genres = append(genres, genre)
			if len(genres) >= 8 {
				return genres
			}
		}
	}
	return genres
}

func contextPlans(intent core.MusicIntent) []core.ContextSeedPlan {
	if intent.Knowledge == nil {
		return nil
	}
	return intent.Knowledge.ContextPlans
}

func contextPlanActive(intent core.MusicIntent, plan core.ContextSeedPlan) bool {
	if plan.Profile.ExtractorVersion != core.ContextProfileVersion {
		return false
	}
	var references []core.IntentReference
	if plan.Scope == "journey_start" {
		if intent.Start != nil {
			references = append(references, *intent.Start)
		}
	} else {
		references = intent.References
	}
	for _, ref := range references {
		if ref.Kind != plan.ReferenceKind || ref.Influence == core.InfluenceNegative {
			continue
		}
		if plan.EntityKey != "" {
			if ref.Resolution != nil && ref.Resolution.Status == core.ResolutionResolved && ref.Resolution.Selected != nil && ref.Resolution.Selected.EntityID == plan.EntityKey {
				return true
			}
			continue
		}
		if core.NormalizeIdentityPart(ref.Query) == core.NormalizeIdentityPart(plan.Query) {
			return true
		}
	}
	return false
}

func contextExcludedGenre(intent core.MusicIntent, genre, scope string) bool {
	for _, group := range [][]core.IntentPreference{intent.Preferences.Genres, intent.Preferences.Styles} {
		for _, pref := range group {
			if pref.Influence == core.InfluenceNegative && (pref.Scope == "" || pref.Scope == "playlist" || pref.Scope == scope) && core.NormalizeIdentityPart(musicconcepts.Canonical("genre", pref.Value)) == core.NormalizeIdentityPart(genre) {
				return true
			}
		}
	}
	for _, constraint := range intent.HardConstraints {
		if (constraint.Kind == "exclude_style" || constraint.Kind == "exclude_genre") && core.NormalizeIdentityPart(musicconcepts.Canonical("genre", constraint.Value)) == core.NormalizeIdentityPart(genre) {
			return true
		}
	}
	return false
}
