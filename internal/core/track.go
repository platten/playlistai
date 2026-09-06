// Package core holds the domain types shared across Playlist AI. It imports no
// framework code (no Wails, no SQLite, no HTTP) so every other package can depend
// on it freely.
package core

import "strings"

// TrackRef identifies a catalog track.
//
// ID is the Spotify base-62 track id (also the key of the Deej-AI datasets).
// Artist/Title are split from the catalog's single "Artist - Title" string, so
// Artist is the first artist only and Title may itself contain " - ".
type TrackRef struct {
	ID     string `json:"id"`
	Artist string `json:"artist"`
	Title  string `json:"title"`
}

// Display renders the track the way the upstream dataset stores it.
func (t TrackRef) Display() string {
	if t.Artist == "" {
		return t.Title
	}
	return t.Artist + " - " + t.Title
}

// ProvisionalRecordingKey is the catalog-independent recording identity used
// until canonical recording IDs are available.
func ProvisionalRecordingKey(t TrackRef) string {
	return NormalizeIdentityPart(t.Artist) + "\x00" + NormalizeIdentityPart(t.Title)
}

func NormalizeIdentityPart(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

// SpotifyURI is the spotify:track:<id> form.
func (t TrackRef) SpotifyURI() string {
	if t.ID == "" {
		return ""
	}
	return "spotify:track:" + t.ID
}

// SpotifyURL is the open.spotify.com web link.
func (t TrackRef) SpotifyURL() string {
	if t.ID == "" {
		return ""
	}
	return "https://open.spotify.com/track/" + t.ID
}

// ParseDisplay splits an "Artist - Title" string the same way deej-ai.online-app
// does: on the first " - ". A string with no separator is treated as a bare
// title.
func ParseDisplay(id, display string) TrackRef {
	if i := strings.Index(display, " - "); i >= 0 {
		return TrackRef{ID: id, Artist: display[:i], Title: display[i+3:]}
	}
	return TrackRef{ID: id, Title: display}
}

// TrackMeta is the per-track catalog metadata. The Deej-AI datasets carry only
// the display string and an often-empty 30s preview URL; everything else (album,
// year, ISRC, all artists) is filled later by an Enricher.
type TrackMeta struct {
	Ref           TrackRef `json:"ref"`
	PreviewURL    string   `json:"previewUrl"` // bundled Spotify CDN preview; frequently ""
	Album         string   `json:"album"`
	AlbumReliable bool     `json:"albumReliable"` // false means unknown, including a non-empty unverified value
}
