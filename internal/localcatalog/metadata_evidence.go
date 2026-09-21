package localcatalog

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/audio"
	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
	"github.com/platten/playlistai/internal/ports"
)

func (c *CompositeCatalog) LibraryTrackFeatures(ctx context.Context, id string) (core.LibraryTrackFeatures, bool) {
	if m, _, found := c.mergedMusicalMetadata(ctx, id); found {
		return core.LibraryTrackFeatures{TempoBPM: m.BPM, Key: m.Key, OriginalYear: m.OriginalYear,
			CompositionYear: m.CompositionYear, Descriptors: m.Descriptors, Conflicts: m.Conflicts}, found
	}
	if base, ok := c.base.(ports.LibraryMetadataCatalog); ok && c.mode != ModeLibraryOnly {
		return base.LibraryTrackFeatures(ctx, id)
	}
	return core.LibraryTrackFeatures{}, false
}

func (c *CompositeCatalog) LibraryRecordingMetadata(ctx context.Context, id string) (core.EnrichedTrack, bool, error) {
	if err := ctx.Err(); err != nil {
		return core.EnrichedTrack{}, false, err
	}
	if m, meta, found := c.mergedMusicalMetadata(ctx, id); found {
		out := core.EnrichedTrack{Ref: meta.Ref,
			IdentityStatus: core.ResolutionResolved, Matched: true, RecordingID: meta.MusicBrainzRecording, ISRC: meta.ISRC, Album: meta.Album}
		if m.OriginalYear != nil {
			out.OriginalReleaseDate = strconv.Itoa(*m.OriginalYear)
		}
		if m.EditionYear != nil {
			out.Year, out.ReleaseEditionDate = *m.EditionYear, strconv.Itoa(*m.EditionYear)
		}
		if m.CompositionYear != nil {
			out.CompositionStartYear, out.CompositionEndYear = *m.CompositionYear, *m.CompositionYear
		}
		workIDs := map[string]bool{}
		for _, a := range m.Annotations {
			switch a.Kind {
			case "artist_credit":
				out.AllArtists = append(out.AllArtists, a.Value)
			case "genre", "style":
				out.GenreTags = append(out.GenreTags, core.AttributedGenreTag{Name: a.Value, Votes: 1, Source: a.Origin, EntityID: meta.Ref.RecordingIdentity, Facet: a.Kind})
			case "work_mbid":
				workIDs[strings.ToLower(strings.TrimSpace(a.Value))] = true
			}
		}
		if len(workIDs) == 1 {
			for id := range workIDs {
				out.WorkID = id
			}
		}
		return out, true, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return core.EnrichedTrack{}, false, err
	}
	if base, ok := c.base.(ports.LibraryMetadataCatalog); ok && c.mode != ModeLibraryOnly {
		return base.LibraryRecordingMetadata(ctx, id)
	}
	return core.EnrichedTrack{}, false, nil
}

// LibraryPreferenceScore exposes soft typed-tag agreement using the same scoped
// clause grouping as CLAP. Numeric descriptors never grant categorical fit.
func (c *CompositeCatalog) LibraryPreferenceScore(ctx context.Context, id string, intent core.MusicIntent, scope string) (float64, bool) {
	m, _, found := c.mergedMusicalMetadata(ctx, id)
	if !found {
		if base, available := c.base.(ports.LibraryMetadataCatalog); available && c.mode != ModeLibraryOnly {
			return base.LibraryPreferenceScore(ctx, id, intent, scope)
		}
		return 0, false
	}
	a := core.AudioAssessment{PolicyVersion: audio.QueryPolicyVersion}
	anyKnown := false
	for _, clause := range audio.Clauses(intent) {
		if clause.Scope != "" && clause.Scope != "playlist" && clause.Scope != scope {
			continue
		}
		entry := core.AudioClauseAssessment{Clause: clause, State: core.EvidenceUnknown}
		criterion := core.MusicalCriterion{Kind: clause.Kind, Value: clause.Text}
		for _, annotation := range m.Annotations {
			if annotationMatches(annotation, criterion) {
				entry.Score, entry.ScoreAvailable = 1, true
				break
			}
		}
		if !entry.ScoreAvailable && compoundGenreAnnotations(m.Annotations, criterion) {
			// Component tags corroborate the discovery phrase but do not
			// establish the complete compound category.
			entry.Score, entry.ScoreAvailable = .75, true
		}
		if score, ok := descriptorPreference(m, clause); ok {
			entry.Score, entry.ScoreAvailable = score, true
		}
		anyKnown = anyKnown || entry.ScoreAvailable
		// Include unavailable clauses on a fixed scale, rather than inflating
		// a track which happens to carry only one convenient tag.
		entry.ScoreAvailable = true
		a.Clauses = append(a.Clauses, entry)
	}
	if len(a.Clauses) == 0 || !anyKnown {
		return 0, false
	}
	var result core.Candidate
	audio.ApplyScores(&result, a)
	return result.Scores.SemanticMatch - result.Scores.SemanticNegativeMatch, true
}

// CompoundGenreSupport is separate from CriterionEvidence: two sourced
// component tags are partial support, not proof of the named subgenre.
func (c *CompositeCatalog) CompoundGenreSupport(ctx context.Context, id string, criterion core.MusicalCriterion) bool {
	m, _, found := c.mergedMusicalMetadata(ctx, id)
	return found && compoundGenreAnnotations(m.Annotations, criterion)
}

func compoundGenreAnnotations(annotations []core.MetadataAnnotation, criterion core.MusicalCriterion) bool {
	for _, pair := range musicconcepts.CompoundGenreLeads(criterion.Kind, criterion.Value) {
		first := core.MusicalCriterion{Kind: "genre", Value: pair[0]}
		second := core.MusicalCriterion{Kind: "genre", Value: pair[1]}
		if annotationCriterionEvidence(annotations, first) == core.EvidenceMatch &&
			annotationCriterionEvidence(annotations, second) == core.EvidenceMatch {
			return true
		}
	}
	return false
}

// Collect claims before normalization: a conflict must not become an empty
// value which a later overlay accidentally fills. Joins use recording identity,
// never artist/title resemblance, and work for any displayed source alias.
func (c *CompositeCatalog) mergedMusicalMetadata(ctx context.Context, id string) (MusicalMetadata, core.TrackMeta, bool) {
	if ctx.Err() != nil {
		return MusicalMetadata{}, core.TrackMeta{}, false
	}
	meta, ok := c.Meta(id)
	if !ok {
		return MusicalMetadata{}, core.TrackMeta{}, false
	}
	claims, found := c.recordingAnnotations(ctx, meta)
	if ctx.Err() != nil {
		return MusicalMetadata{}, core.TrackMeta{}, false
	}
	return NormalizeMusicalMetadata(claims), meta, found
}

func (c *CompositeCatalog) recordingAnnotations(ctx context.Context, meta core.TrackMeta) ([]core.MetadataAnnotation, bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	var claims []core.MetadataAnnotation
	found := false
	if c.local.owns(meta.Ref.ID) {
		claims, found = c.local.Annotations(ctx, meta.Ref.ID), true
	} else if match, ok := c.matchBase(ctx, meta); ok {
		claims, found = c.local.Annotations(ctx, match.ID), true
	}
	if c.mode != ModeLibraryOnly {
		if base, ok := c.base.(interface {
			recordingAnnotations(context.Context, core.TrackMeta) ([]core.MetadataAnnotation, bool)
		}); ok {
			more, matched := base.recordingAnnotations(ctx, meta)
			claims, found = append(claims, more...), found || matched
		} else if base, ok := c.base.Meta(meta.Ref.ID); ok {
			claims = append(claims, base.Annotations...)
			found = found || len(base.Annotations) > 0
		}
	}
	return claims, found
}

func descriptorPreference(m MusicalMetadata, clause core.AudioClause) (float64, bool) {
	concept, ok := musicconcepts.Find(clause.Kind, clause.Text)
	if !ok {
		return 0, false
	}
	var total float64
	count := 0
	models := make([]string, 0, len(concept.Providers.AcousticBrainz))
	for model := range concept.Providers.AcousticBrainz {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		class := concept.Providers.AcousticBrainz[model]
		key := model
		if model == "danceability" {
			key = "mood_danceability"
		}
		if !strings.HasPrefix(key, "mood_") {
			continue
		}
		value, found := m.Descriptors["acousticbrainz:"+key]
		if !found {
			continue
		}
		sign := 1.0
		if strings.HasPrefix(class, "not_") || class == "voice" {
			sign = -1
		}
		total += sign * value / 100
		count++
	}
	if count == 0 {
		return 0, false
	}
	return total / float64(count), true
}

var _ ports.LibraryMetadataCatalog = (*CompositeCatalog)(nil)
