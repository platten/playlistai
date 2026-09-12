package core

import "strings"

const DefaultDurationToleranceSeconds = 60

// RecordingDuration describes the entire identified recording. Providers must
// not populate it from a preview, analyzed audio segment, album or artist.
// Milliseconds avoids cumulative rounding across a long playlist.
type RecordingDuration struct {
	Milliseconds int64  `json:"milliseconds"`
	Source       string `json:"source"`
	RecordingID  string `json:"recordingId"`
}

func (d *RecordingDuration) Valid() bool {
	return d != nil && d.Milliseconds > 0 && d.Milliseconds <= 24*60*60*1000 &&
		strings.TrimSpace(d.Source) != "" && strings.TrimSpace(d.RecordingID) != ""
}

type TrackDurationEvidence struct {
	TrackID string `json:"trackId"`
	RecordingDuration
}

// PlaylistDurationAssessment is saved with the actual result so replay can
// explain its duration without re-fetching changing provider metadata.
type PlaylistDurationAssessment struct {
	TargetSeconds     int                     `json:"targetSeconds"`
	ToleranceSeconds  int                     `json:"toleranceSeconds"`
	KnownMilliseconds int64                   `json:"knownMilliseconds"`
	UnknownTrackIDs   []string                `json:"unknownTrackIds"`
	Evidence          []TrackDurationEvidence `json:"evidence"`
	State             EvidenceState           `json:"state"`
}

// Verifies checks the persisted arithmetic and evidence completeness at the
// output/history boundary. A state label or rounded total alone is not proof.
func (d *PlaylistDurationAssessment) Verifies(intent MusicIntent, trackCount int) bool {
	if d == nil || trackCount <= 0 || d.State != EvidenceMatch || d.TargetSeconds != intent.DurationSeconds || d.ToleranceSeconds != intent.DurationTolerance() || len(d.UnknownTrackIDs) != 0 || len(d.Evidence) != trackCount {
		return false
	}
	var total int64
	seenTracks, seenRecordings := map[string]bool{}, map[string]bool{}
	for _, evidence := range d.Evidence {
		if strings.TrimSpace(evidence.TrackID) == "" || seenTracks[evidence.TrackID] || seenRecordings[evidence.RecordingID] || !evidence.Valid() {
			return false
		}
		seenTracks[evidence.TrackID], seenRecordings[evidence.RecordingID] = true, true
		total += evidence.Milliseconds
	}
	if total != d.KnownMilliseconds {
		return false
	}
	delta := total - int64(intent.DurationSeconds)*1000
	if delta < 0 {
		delta = -delta
	}
	return intent.DurationSeconds > 0 && delta <= int64(intent.DurationTolerance())*1000
}

func (m MusicIntent) DurationTolerance() int {
	if m.DurationSeconds <= 0 {
		return 0
	}
	if m.DurationToleranceSeconds > 0 {
		return m.DurationToleranceSeconds
	}
	return DefaultDurationToleranceSeconds
}

// HasExplicitTrackCount trusts protected source atoms, not a model's numeric
// default. Old saved requests with no source snapshot retain their stored count
// because its origin cannot safely be reconstructed during history replay.
func (m MusicIntent) HasExplicitTrackCount() bool {
	if m.TrackCountExplicit || m.Translation == nil {
		return true
	}
	for _, atom := range m.Translation.Atoms {
		if atom.Kind == "count" && atom.Polarity == "positive" && atom.Strength == "required" {
			return true
		}
	}
	return false
}
