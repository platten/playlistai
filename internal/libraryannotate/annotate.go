// Package libraryannotate acquires web-source facts for exact local recordings.
// These facts are not independent human listening labels or audio predictions.
package libraryannotate

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
)

const Version = "library-web-annotations/v1"

type Provider interface {
	ports.Enricher
	ports.RecordingVerifier
}

// Row embeds existing evidence contracts; it is a reviewable sidecar, not a new
// pack format or a calibration label file. Missing facts remain unknown.
type Row struct {
	Version       string                     `json:"version"`
	EvidenceClass string                     `json:"evidenceClass"`
	PackID        string                     `json:"packId"`
	PackSHA256    string                     `json:"packSha256"`
	Track         core.EnrichedTrack         `json:"track"`
	Assessments   []core.CriterionAssessment `json:"assessments"`
	Detail        string                     `json:"detail,omitempty"`
}

func Annotate(ctx context.Context, p Provider, track librarypack.Track, criteria []core.MusicalCriterion) (Row, error) {
	row := Row{Version: Version, EvidenceClass: "web_source", Track: core.EnrichedTrack{Ref: core.TrackRef{ID: track.ID, Artist: track.Artist, Title: track.Title, RecordingIdentity: track.RecordingIdentity}, Album: track.Album, ISRC: track.ISRC, ArtistIDs: librarypack.ArtistMBIDs(track.RawTags)}}
	if err := ctx.Err(); err != nil {
		return row, err
	}
	id := librarypack.CanonicalMBID(track.MusicBrainzRecording)
	if alternate := strings.TrimPrefix(track.RecordingIdentity, "musicbrainz:"); alternate != track.RecordingIdentity {
		other := librarypack.CanonicalMBID(alternate)
		if id != "" && other != "" && id != other {
			row.Detail = "Conflicting embedded recording identities; web acquisition skipped."
			row.assess(criteria)
			return row, nil
		}
		if id == "" {
			id = other
		}
	}
	if id == "" && p != nil {
		found, err := p.Enrich(ctx, []core.TrackRef{row.Track.Ref}, nil)
		if ctx.Err() != nil {
			return row, ctx.Err()
		}
		if err == nil && len(found) == 1 {
			row.Track.Alternatives = found[0].Alternatives
			// Search text alone cannot join this local file. Require embedded ISRC.
			isrc := librarypack.CanonicalISRC(track.ISRC)
			if isrc != "" && found[0].Matched && found[0].Ref.ID == track.ID && found[0].IdentityStatus == core.ResolutionResolved && hasISRC(found[0], isrc) && compatibleCredits(row.Track.ArtistIDs, found[0].ArtistIDs) && core.NormalizeIdentityPart(found[0].Ref.Artist) == core.NormalizeIdentityPart(track.Artist) && core.NormalizeIdentityPart(found[0].Ref.Title) == core.NormalizeIdentityPart(track.Title) {
				row.Track = found[0]
				id = librarypack.CanonicalMBID(found[0].RecordingID)
			}
		}
	}
	row.Track.RecordingID = id
	row.Track.ReleaseID = exactReleaseTag(track.RawTags, "musicbrainz_albumid")
	row.Track.ReleaseTrackID = exactReleaseTag(track.RawTags, "musicbrainz_releasetrackid")
	// Only reliable whole-file duration is forwarded; preview/segment coverage is unrelated.
	row.Track.FullRecordingDuration = nil
	if track.DurationReliable && strings.TrimSpace(track.DurationProvenance) != "" && !strings.Contains(strings.ToLower(track.DurationProvenance), "preview") {
		duration := &core.RecordingDuration{Milliseconds: track.DurationMilliseconds, Source: "local:" + track.DurationProvenance, RecordingID: "musicbrainz:" + id}
		if id != "" && duration.Valid() {
			row.Track.FullRecordingDuration = duration
		}
	}
	if p == nil {
		row.Detail = "Online acquisition disabled; source evidence remains unknown."
		row.assess(criteria)
		return row, nil
	}
	if id == "" {
		row.Detail = "Recording identity is unavailable or ambiguous; musical evidence remains unknown."
		row.assess(criteria)
		return row, nil
	}
	row.Track.RecordingID = id
	row.Track.IdentityStatus = core.ResolutionResolved
	row.Track.Matched = true
	verified, err := p.VerifyRecording(ctx, row.Track, criteria)
	if err != nil {
		if ctx.Err() != nil {
			return row, ctx.Err()
		}
		row.Track.IdentityStatus = core.ResolutionUnresolved
		row.Track.Matched = false
		row.Track.Claims = nil
		row.Detail = "Identity-linked sources were unavailable or conflicting; musical evidence remains unknown."
	} else if verified.RecordingID == id && verified.Ref.ID == track.ID && verified.IdentityStatus == core.ResolutionResolved && verified.Matched && compatibleCredits(row.Track.ArtistIDs, verified.ArtistIDs) {
		row.Track = verified
		row.Track.Claims = validClaims(id, verified.Claims)
	} else {
		row.Track.IdentityStatus = core.ResolutionUnresolved
		if verified.RecordingID == id && verified.Ref.ID == track.ID && verified.IdentityStatus == core.ResolutionAmbiguous {
			row.Track.IdentityStatus = core.ResolutionAmbiguous
		}
		row.Track.Matched = false
		row.Track.Claims = nil
		row.Detail = "Source returned a conflicting recording identity."
	}
	row.assess(criteria)
	return row, nil
}
func compatibleCredits(existing, proposed []string) bool {
	for _, id := range existing {
		if !slices.Contains(proposed, id) {
			return false
		}
	}
	return true
}
func hasISRC(t core.EnrichedTrack, want string) bool {
	for _, v := range append([]string{t.ISRC}, t.AllISRCs...) {
		if librarypack.CanonicalISRC(v) == want {
			return true
		}
	}
	return false
}
func validClaims(recording string, claims []core.RecordingClaim) []core.RecordingClaim {
	var out []core.RecordingClaim
	for _, c := range claims {
		u, err := url.Parse(c.Source.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || c.Source.Provider == "" || c.Source.Revision == "" || c.Locator == "" {
			continue
		}
		if c.RecordingID != recording || c.Scope != "recording" || c.EntityID != recording {
			continue
		}
		if c.State != core.EvidenceMatch && c.State != core.EvidenceMismatch {
			continue
		}
		out = append(out, c)
	}
	return out
}
func (r *Row) assess(criteria []core.MusicalCriterion) {
	for _, criterion := range criteria {
		a := core.CriterionAssessment{Clause: core.AudioClause{Kind: criterion.Kind, Text: criterion.Value}, State: core.EvidenceUnknown, Detail: "No explicit recording-scoped source statement establishes this criterion."}
		for _, claim := range r.Track.Claims {
			if claim.Kind != criterion.Kind || core.NormalizeIdentityPart(claim.Value) != core.NormalizeIdentityPart(criterion.Value) {
				continue
			}
			a.Claims = append(a.Claims, claim)
			if claim.Method == "quoted_statement" {
				continue
			}
			if a.State != core.EvidenceUnknown && a.State != claim.State {
				a.Conflict = true
			}
			a.State = claim.State
		}
		if a.Conflict {
			a.State = core.EvidenceUnknown
			a.Detail = "Sources disagree; retained as unknown."
		} else if a.State == core.EvidenceUnknown && len(a.Claims) > 0 {
			a.Detail = "Quoted source text is retained as an unverified extraction hint; it does not establish verified musical fit."
		} else if len(a.Claims) > 0 {
			a.Detail = "Explicit web-source statement; not an independent listening label."
		}
		r.Assessments = append(r.Assessments, a)
	}
}

// Exact release identities use only supported embedded fields, never album names.
func exactReleaseTag(raw json.RawMessage, field string) string {
	var tags map[string]json.RawMessage
	if json.Unmarshal(raw, &tags) != nil {
		return ""
	}
	ids := map[string]bool{}
	for key, value := range tags {
		if strings.ToLower(strings.TrimSpace(key)) != field {
			continue
		}
		var values []string
		var scalar string
		if json.Unmarshal(value, &scalar) == nil {
			values = []string{scalar}
		} else if json.Unmarshal(value, &values) != nil {
			return ""
		}
		for _, value := range values {
			id := librarypack.CanonicalMBID(value)
			if id == "" {
				return ""
			}
			ids[id] = true
		}
	}
	if len(ids) != 1 {
		return ""
	}
	for id := range ids {
		return id
	}
	return ""
}
