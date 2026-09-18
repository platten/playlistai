// Package core holds the domain types shared across Playlist AI. It imports no
// framework code (no Wails, no SQLite, no HTTP) so every other package can depend
// on it freely.
package core

import "strings"

// TrackRef identifies a catalog track.
//
// ID is the Spotify base-62 track id for Deej-AI rows. Preview-resolved tracks
// outside that dataset use a provider-scoped id such as "deezer:123".
// Artist/Title are split from the catalog's single "Artist - Title" string, so
// Artist is the first artist only and Title may itself contain " - ".
type TrackRef struct {
	ID                string `json:"id"`
	Artist            string `json:"artist"`
	Title             string `json:"title"`
	RecordingIdentity string `json:"recordingIdentity,omitempty"`
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
	if identity := strings.TrimSpace(t.RecordingIdentity); identity != "" {
		return "recording-id\x00" + strings.ToLower(identity)
	}
	// A local pack preserves editions/masters and has no authoritative link to
	// a bundled recording. Artist/title equality must not silently collapse it
	// across catalogs; a future exact linkage can supply a canonical identity.
	if strings.HasPrefix(t.ID, "local:") {
		return "local-id\x00" + t.ID
	}
	return NormalizeIdentityPart(t.Artist) + "\x00" + NormalizeIdentityPart(t.Title)
}

func NormalizeIdentityPart(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

// SpotifyURI is the spotify:track:<id> form.
func (t TrackRef) SpotifyURI() string {
	if t.ID == "" || strings.Contains(t.ID, ":") {
		return ""
	}
	return "spotify:track:" + t.ID
}

// SpotifyURL is the open.spotify.com web link.
func (t TrackRef) SpotifyURL() string {
	if t.ID == "" || strings.Contains(t.ID, ":") {
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
	Annotations           []MetadataAnnotation `json:"annotations,omitempty"`
	FullRecordingDuration *RecordingDuration   `json:"fullRecordingDuration,omitempty"`
	Ref                   TrackRef             `json:"ref"`
	PreviewURL            string               `json:"previewUrl"` // bundled Spotify CDN preview; frequently ""
	Album                 string               `json:"album"`
	AlbumReliable         bool                 `json:"albumReliable"` // false means unknown, including a non-empty unverified value
	SourceIdentity        string               `json:"sourceIdentity,omitempty"`
	ISRC                  string               `json:"isrc,omitempty"`
	MusicBrainzRecording  string               `json:"musicBrainzRecording,omitempty"`
	AcoustID              string               `json:"acoustId,omitempty"`
	AudioFingerprint      *AudioFingerprint    `json:"audioFingerprint,omitempty"`
}

// AudioFingerprint is tagged or locally computed AcoustID Chromaprint evidence.
// It is never proof of ownership or listener preference and is not sent to a
// remote identification service by Playlist AI.
type AudioFingerprint struct {
	Contract          string `json:"contract"`
	Format            string `json:"format"`
	Algorithm         int    `json:"algorithm"`
	Fingerprint       string `json:"fingerprint"`
	FingerprintSHA256 string `json:"fingerprintSha256"`
	Scope             string `json:"scope"`
	DecoderRuntimeID  string `json:"decoderRuntimeId"`
}
