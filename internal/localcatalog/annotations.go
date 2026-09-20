package localcatalog

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
)

// Annotation aliases are explicit. Delimiters inside a tag value are not split:
// ffprobe's dictionary does not establish the original container multiplicity.
var annotationKinds = map[string]string{
	"genre": "genre", "style": "style", "mood": "mood",
	"bpm": "tempo", "tbpm": "tempo", "fbpm": "tempo",
	"key": "key", "tkey": "key", "initialkey": "key",
	"language": "language", "language_2_letter": "language",
	"date": "edition_date", "year": "edition_date",
	"originaldate": "original_release_date", "originalreleasedate": "original_release_date", "original_release_date": "original_release_date",
	"artist": "artist_credit", "artists": "artist_credit", "album_artist": "album_artist", "albumartist": "album_artist",
	"musicbrainz_artistid": "artist_mbid",
	"composer":             "composer", "work": "work", "musicbrainz_work": "work", "movement": "movement", "part": "movement",
	"track": "track_number", "tracknumber": "track_number", "disc": "disc_number", "discnumber": "disc_number",
	"releasetype": "release_type", "release_type": "release_type", "musicbrainz_album_type": "release_type",
	"replaygain_track_gain": "replaygain_track_gain", "replaygain_album_gain": "replaygain_album_gain",
	"replaygain_track_peak": "replaygain_track_peak", "replaygain_album_peak": "replaygain_album_peak",
}

func annotations(raw json.RawMessage) []core.MetadataAnnotation {
	var tags map[string]json.RawMessage
	if json.Unmarshal(raw, &tags) != nil {
		return nil
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var result []core.MetadataAnnotation
	for _, key := range keys {
		var scalar string
		var values []string
		if json.Unmarshal(tags[key], &scalar) == nil {
			values = []string{scalar}
		} else if json.Unmarshal(tags[key], &values) != nil {
			continue
		}
		normalized := strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(strings.TrimSpace(key)))
		kind := annotationKinds[normalized]
		scale := ""
		if strings.HasPrefix(normalized, "mood_") {
			kind, scale = "legacy_prediction:"+normalized, "unknown"
		}
		if kind == "" {
			continue
		}
		seen := map[string]bool{}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			result = append(result, core.MetadataAnnotation{Kind: kind, Value: value, SourceKey: key, Origin: "embedded_tag", Scale: scale})
		}
	}
	return result
}

func supportsAnnotationCriterion(criterion core.MusicalCriterion) bool {
	switch criterion.Kind {
	case "genre", "style", "mood", "language", "original_release_date", "edition_date":
		return true
	}
	return false
}

func (c *Catalog) Annotations(ctx context.Context, id string) []core.MetadataAnnotation {
	localID, err := c.localID(id)
	if err != nil {
		return nil
	}
	generation, done, err := c.withGeneration()
	if err != nil {
		return nil
	}
	defer done()
	track, ok, err := generation.Lookup(ctx, localID)
	if err != nil || !ok {
		return nil
	}
	return annotations(track.RawTags)
}

func annotationMatches(annotation core.MetadataAnnotation, criterion core.MusicalCriterion) bool {
	// Genre and style share the reviewed vocabulary. No inference is made
	// from an artist's genre, free text, or a related (non-parent) concept.
	annotationIsGenre := annotation.Kind == "genre" || annotation.Kind == "style"
	criterionIsGenre := criterion.Kind == "genre" || criterion.Kind == "style"
	if annotation.Kind != criterion.Kind && (!annotationIsGenre || !criterionIsGenre) {
		return false
	}
	if normalizeUnicode(musicconcepts.Canonical(criterion.Kind, annotation.Value)) == normalizeUnicode(musicconcepts.Canonical(criterion.Kind, criterion.Value)) {
		return true
	}
	child, childOK := musicconcepts.Find(annotation.Kind, annotation.Value)
	parent, parentOK := musicconcepts.Find(criterion.Kind, criterion.Value)
	if !childOK || !parentOK {
		return false
	}
	seen := map[string]bool{}
	var matches func(musicconcepts.Concept) bool
	matches = func(c musicconcepts.Concept) bool {
		if seen[c.ID] {
			return false
		}
		seen[c.ID] = true
		for _, id := range c.Parents {
			if id == parent.ID {
				return true
			}
			if next, ok := musicconcepts.FindID(id); ok && matches(next) {
				return true
			}
		}
		return false
	}
	return matches(child)
}
