package localaudio

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = value
	}
	return out
}

func combinedTags(format, stream map[string]string, maximum int64) (map[string]string, error) {
	tags := cloneTags(format)
	if tags == nil {
		tags = map[string]string{}
	}
	var size int64
	for key, value := range tags {
		size += int64(len(key) + len(value))
	}
	for key, value := range stream {
		if _, exists := tags[key]; !exists {
			tags[key] = value
			size += int64(len(key) + len(value))
		}
	}
	if size > maximum {
		return nil, fmt.Errorf("localaudio: metadata exceeds %d bytes", maximum)
	}
	for key, value := range tags {
		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			return nil, fmt.Errorf("localaudio: metadata is not valid UTF-8")
		}
	}
	return tags, nil
}

func lookupTag(tags map[string]string, aliases ...string) (string, string, bool) {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, alias := range aliases {
		for _, key := range keys {
			value := tags[key]
			if strings.EqualFold(strings.TrimSpace(key), alias) && strings.TrimSpace(value) != "" {
				return key, value, true
			}
		}
	}
	return "", "", false
}

func tagValue(tags map[string]string, aliases ...string) *TagValue {
	key, value, ok := lookupTag(tags, aliases...)
	if !ok {
		return nil
	}
	return &TagValue{Value: value, SourceKey: key, Provenance: "embedded_tag"}
}

func tagValues(tags map[string]string, aliases ...string) []TagValue {
	value := tagValue(tags, aliases...)
	if value == nil {
		return nil
	}
	// Never split '/' or '&': AC/DC and R&B are single values. ffprobe exposes
	// one dictionary value per key; delimiters remain raw and uninterpreted.
	return []TagValue{*value}
}

func parseNumberPair(value string) TrackDiscNumber {
	parts := strings.SplitN(strings.TrimSpace(value), "/", 2)
	result := TrackDiscNumber{}
	result.Number, _ = strconv.Atoi(strings.TrimSpace(parts[0]))
	if len(parts) == 2 {
		result.Total, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
	}
	if result.Number < 0 {
		result.Number = 0
	}
	if result.Total < 0 {
		result.Total = 0
	}
	return result
}

func parseMetadata(format, stream map[string]string, maximum int64) (Metadata, error) {
	tags, err := combinedTags(format, stream, maximum)
	if err != nil {
		return Metadata{}, err
	}
	metadata := Metadata{
		RawTags: tags,
		Title:   tagValue(tags, "title"), ArtistCredits: tagValues(tags, "artist"),
		AlbumArtists: tagValues(tags, "album_artist", "albumartist"), Album: tagValue(tags, "album"),
		Genres: tagValues(tags, "genre"), Date: tagValue(tags, "date", "year"),
		ISRC:           tagValue(tags, "isrc", "tsrc", "wm/isrc", "com.apple.itunes:isrc", "----:com.apple.itunes:isrc"),
		MusicBrainzIDs: map[string]string{}, ReplayGain: map[string]string{},
	}
	if _, value, ok := lookupTag(tags, "track", "tracknumber"); ok {
		metadata.Track = parseNumberPair(value)
	}
	if _, value, ok := lookupTag(tags, "disc", "discnumber"); ok {
		metadata.Disc = parseNumberPair(value)
	}
	for key, value := range tags {
		normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), " ", "_"))
		if strings.HasPrefix(normalized, "musicbrainz_") || strings.HasPrefix(normalized, "musicbrainz-") {
			metadata.MusicBrainzIDs[key] = value
		}
		if strings.HasPrefix(normalized, "replaygain_") || normalized == "itunesnorm" {
			metadata.ReplayGain[key] = value
		}
	}
	if len(metadata.MusicBrainzIDs) == 0 {
		metadata.MusicBrainzIDs = nil
	}
	if len(metadata.ReplayGain) == 0 {
		metadata.ReplayGain = nil
	}
	return metadata, nil
}
