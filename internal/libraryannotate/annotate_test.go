package libraryannotate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/librarypack"
	"github.com/platten/playlistai/internal/ports"
)

const recording = "11111111-1111-4111-8111-111111111111"

type fixtureProvider struct {
	verified    int
	found       *core.EnrichedTrack
	rejectMatch bool
	claims      []core.RecordingClaim
}

func (p *fixtureProvider) Name() string { return "fixture" }
func (p *fixtureProvider) Enrich(_ context.Context, refs []core.TrackRef, _ ports.Progress) ([]core.EnrichedTrack, error) {
	if p.found != nil {
		return []core.EnrichedTrack{*p.found}, nil
	}
	return []core.EnrichedTrack{{Ref: refs[0], RecordingID: recording, IdentityStatus: core.ResolutionResolved}}, nil
}
func (p *fixtureProvider) VerifyRecording(_ context.Context, t core.EnrichedTrack, _ []core.MusicalCriterion) (core.EnrichedTrack, error) {
	p.verified++
	if p.rejectMatch {
		t.Matched = false
	}
	t.Claims = p.claims
	return t, nil
}
func claim(kind, value string, state core.EvidenceState) core.RecordingClaim {
	return core.RecordingClaim{Kind: kind, Value: value, State: state, Scope: "recording", EntityID: recording, RecordingID: recording, Locator: "relations[0]", Source: core.ContextSource{Provider: "musicbrainz", URL: "https://musicbrainz.org/recording/" + recording, Revision: "fixture"}}
}
func TestAnnotationsPreserveUnknownQualifiersConflictsAndSourceScope(t *testing.T) {
	p := &fixtureProvider{claims: []core.RecordingClaim{claim("instrumentation", "piano", core.EvidenceMatch), claim("vocal", "instrumental", core.EvidenceMatch), claim("vocal", "instrumental", core.EvidenceMismatch)}}
	album := claim("texture", "spacious reverberation", core.EvidenceMatch)
	album.Scope = "album"
	p.claims = append(p.claims, album)
	criteria := []core.MusicalCriterion{{Kind: "instrumentation", Value: "soft piano"}, {Kind: "texture", Value: "spacious reverberation"}, {Kind: "vocal", Value: "instrumental"}, {Kind: "instrumentation", Value: "piano"}}
	row, err := Annotate(context.Background(), p, librarypack.Track{ID: "local:test:one", Artist: "Artist", Title: "Song", MusicBrainzRecording: recording}, criteria)
	if err != nil {
		t.Fatal(err)
	}
	if row.EvidenceClass != "web_source" || p.verified != 1 {
		t.Fatal(row)
	}
	for i := range 3 {
		if row.Assessments[i].State != core.EvidenceUnknown {
			t.Fatal("unknown criterion promoted", row.Assessments[i])
		}
	}
	if !row.Assessments[2].Conflict || row.Assessments[3].State != core.EvidenceMatch {
		t.Fatal(row.Assessments)
	}
	if len(row.Track.Claims) != 3 {
		t.Fatal("album claim admitted")
	}
	raw, _ := json.Marshal(row)
	if len(raw) == 0 {
		t.Fatal("empty export")
	}
}
func TestNameSearchCannotAuthenticateLocalRecording(t *testing.T) {
	p := &fixtureProvider{}
	row, err := Annotate(context.Background(), p, librarypack.Track{ID: "local:test:one", Artist: "Artist", Title: "Song"}, []core.MusicalCriterion{{Kind: "genre", Value: "jazz"}})
	if err != nil || p.verified != 0 || row.Assessments[0].State != core.EvidenceUnknown {
		t.Fatal(row, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Annotate(ctx, p, librarypack.Track{}, nil); err == nil {
		t.Fatal("canceled acquisition proceeded")
	}
}
func TestLiteralExtractorRequiresExactSubjectAndQualifiedStatement(t *testing.T) {
	extractor := LiteralExtractor{Criteria: []core.MusicalCriterion{{Kind: "instrumentation", Value: "soft piano"}}}
	source := ports.RecordingSource{Track: core.EnrichedTrack{Ref: core.TrackRef{Artist: "Artist", Title: "Song"}, RecordingID: recording}}
	for _, text := range []string{"Artist uses soft piano on the album.", "Song by Artist features piano.", "Song by Artist might feature soft piano.", "Song by Artist features soft piano and loud drums."} {
		source.Text = text
		claims, err := extractor.ExtractRecordingClaims(context.Background(), source)
		if err != nil || len(claims) != 0 {
			t.Fatal(text, claims, err)
		}
	}
	source.Text = "Song by Artist features soft piano."
	claims, err := extractor.ExtractRecordingClaims(context.Background(), source)
	if err != nil || len(claims) != 1 || claims[0].State != core.EvidenceMatch {
		t.Fatal(claims, err)
	}
	source.Text = "Song by Artist does not feature soft piano."
	claims, err = extractor.ExtractRecordingClaims(context.Background(), source)
	if err != nil || len(claims) != 1 || claims[0].State != core.EvidenceMismatch {
		t.Fatal(claims, err)
	}
}
func TestSubsetBoundedCopiesAreDeterministicAndLeaveSourcesUntouched(t *testing.T) {
	root := t.TempDir()
	parent := t.TempDir()
	for _, name := range []string{"b.flac", "a.mp3", "c.flac"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, out := range []string{"one", "two"} {
		m, err := Subset(context.Background(), root, filepath.Join(parent, out), 2, 100)
		if err != nil || len(m.Files) != 2 || m.Files[0].Path != "a.mp3" || m.Files[1].Path != "b.flac" {
			t.Fatal(m, err)
		}
	}
	first, _ := os.ReadFile(filepath.Join(parent, "one", "subset-manifest.json"))
	second, _ := os.ReadFile(filepath.Join(parent, "two", "subset-manifest.json"))
	if string(first) != string(second) {
		t.Fatal("subset changed")
	}
	if _, err := Subset(context.Background(), root, filepath.Join(parent, "over-budget"), 2, 1); err == nil {
		t.Fatal("byte ceiling ignored")
	}
	if _, err := os.Stat(filepath.Join(parent, "over-budget")); !os.IsNotExist(err) {
		t.Fatal("partial subset published")
	}
	if _, err := Subset(context.Background(), root, filepath.Join(root, "nested"), 2, 100); err == nil {
		t.Fatal("recursive destination admitted")
	}
	raw, _ := os.ReadFile(filepath.Join(root, "a.mp3"))
	if string(raw) != "a.mp3" {
		t.Fatal("source changed")
	}
}

func TestISRCProposalCannotReplaceLocalTrackOrConflictingCredits(t *testing.T) {
	const artist = "22222222-2222-4222-8222-222222222222"
	track := librarypack.Track{ID: "local:test:one", Artist: "Artist", Title: "Song", ISRC: "USAAA0100001", RawTags: json.RawMessage(`{"MUSICBRAINZ_ARTISTID":"` + artist + `"}`)}
	for _, wrong := range []string{"ref", "artist", "unmatched"} {
		found := core.EnrichedTrack{Ref: core.TrackRef{ID: track.ID, Artist: track.Artist, Title: track.Title}, RecordingID: recording, ISRC: track.ISRC, ArtistIDs: []string{artist}, Matched: true, IdentityStatus: core.ResolutionResolved}
		switch wrong {
		case "ref":
			found.Ref.ID = "other"
		case "artist":
			found.ArtistIDs = []string{recording}
		case "unmatched":
			found.Matched = false
		}
		p := &fixtureProvider{found: &found}
		row, err := Annotate(context.Background(), p, track, nil)
		if err != nil || p.verified != 0 || row.Track.Matched {
			t.Fatal(wrong, row, err)
		}
	}
	p := &fixtureProvider{rejectMatch: true, claims: []core.RecordingClaim{claim("genre", "jazz", core.EvidenceMatch)}}
	track.MusicBrainzRecording = recording
	row, err := Annotate(context.Background(), p, track, []core.MusicalCriterion{{Kind: "genre", Value: "jazz"}})
	if err != nil || row.Track.Matched || len(row.Track.Claims) > 0 || row.Assessments[0].State != core.EvidenceUnknown {
		t.Fatal(row, err)
	}
}
func TestQuotedExtractionHintsDoNotBecomeVerifiedFit(t *testing.T) {
	c := claim("instrumentation", "soft piano", core.EvidenceMatch)
	c.Method = "quoted_statement"
	p := &fixtureProvider{claims: []core.RecordingClaim{c}}
	row, err := Annotate(context.Background(), p, librarypack.Track{ID: "local:test:one", Artist: "Artist", Title: "Song", MusicBrainzRecording: recording}, []core.MusicalCriterion{{Kind: "instrumentation", Value: "soft piano"}})
	if err != nil || row.Assessments[0].State != core.EvidenceUnknown || len(row.Assessments[0].Claims) != 1 {
		t.Fatal(row, err)
	}
}

func TestAnnotationForwardsWholeRecordingDuration(t *testing.T) {
	for _, tc := range []struct {
		name, provenance string
		reliable         bool
		duration         int64
		want             bool
	}{
		{"stream", "stream", true, 576093, true}, {"container", "container", true, 576093, true},
		{"preview", "preview", true, 30000, false}, {"mixed preview", "source:PREVIEW", true, 30000, false}, {"other reliable", "decoder-v2", true, 576093, true}, {"unreliable", "stream", false, 576093, false},
		{"invalid", "stream", true, -1, false}, {"missing", "", true, 576093, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, err := Annotate(context.Background(), &fixtureProvider{}, librarypack.Track{ID: "local", Artist: "Artist", Title: "Song", MusicBrainzRecording: recording, DurationMilliseconds: tc.duration, DurationReliable: tc.reliable, DurationProvenance: tc.provenance}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if row.Track.FullRecordingDuration.Valid() != tc.want {
				t.Fatalf("duration=%+v", row.Track.FullRecordingDuration)
			}
			if tc.want && row.Track.FullRecordingDuration.Milliseconds != tc.duration {
				t.Fatal("duration changed")
			}
		})
	}
}

func TestAnnotationPreservesUniqueDeclaredReleaseIdentities(t *testing.T) {
	const release = "22222222-2222-4222-8222-222222222222"
	const releaseTrack = "33333333-3333-4333-8333-333333333333"
	for _, tc := range []struct{ name, raw, wantRelease, wantTrack string }{
		{"scalar", `{"MUSICBRAINZ_ALBUMID":"` + release + `","MUSICBRAINZ_RELEASETRACKID":"` + releaseTrack + `"}`, release, releaseTrack},
		{"duplicate array", `{"musicbrainz_albumid":["` + release + `","` + release + `"],"musicbrainz_releasetrackid":["` + releaseTrack + `"]}`, release, releaseTrack},
		{"conflict", `{"MUSICBRAINZ_ALBUMID":["` + release + `","` + releaseTrack + `"],"MUSICBRAINZ_RELEASETRACKID":"bad"}`, "", ""},
		{"names are not IDs", `{"ALBUM":"` + release + `","TITLE":"` + releaseTrack + `"}`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, err := Annotate(context.Background(), &fixtureProvider{}, librarypack.Track{ID: "local", Artist: "Artist", Title: "Song", MusicBrainzRecording: recording, RawTags: json.RawMessage(tc.raw)}, nil)
			if err != nil || row.Track.ReleaseID != tc.wantRelease || row.Track.ReleaseTrackID != tc.wantTrack {
				t.Fatalf("track=%+v err=%v", row.Track, err)
			}
		})
	}
}
