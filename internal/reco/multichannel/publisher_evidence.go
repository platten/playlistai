package multichannel

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
)

// Apple uses this single publisher category for hip hop. Interpret the exact
// label only with the same recording/link proof required for decisive evidence;
// slash-separated tags from other sources are not a genre taxonomy.
func publisherGenreLabelMatches(track core.EnrichedTrack, claim core.RecordingClaim, clause core.AudioClause) bool {
	return claim.Value == "Hip-Hop/Rap" && musicconcepts.Canonical("genre", clause.Text) == "hip hop" &&
		claim.Method == "publisher_field" && attributedRecordingClaim(claim, track) &&
		decisiveClaimMethod(track, claim, clause)
}

// A publisher classification needs the separately frozen recording-to-song
// link. Neither an imported tag nor a publisher's artist/album page provides it.
func linkedPublisherGenre(track core.EnrichedTrack, claim core.RecordingClaim) bool {
	if claim.Kind != "genre" || claim.State != core.EvidenceMatch || claim.Scope != "recording" ||
		claim.Source.Provider != "apple" || claim.Locator != "results[0].primaryGenreName" || claim.ExtractorVersion == "" {
		return false
	}
	source, err := url.Parse(claim.Source.URL)
	if err != nil || source.Scheme != "https" || source.Host != "itunes.apple.com" || source.Path != "/lookup" || source.RawPath != "" || source.User != nil || source.Fragment != "" {
		return false
	}
	query, err := url.ParseQuery(source.RawQuery)
	if err != nil || len(query) != 2 || len(query["country"]) != 1 || len(query["id"]) != 1 || query.Encode() != source.RawQuery {
		return false
	}
	country, id := query.Get("country"), query.Get("id")
	number, err := strconv.ParseInt(id, 10, 64)
	if err != nil || number <= 0 || strconv.FormatInt(number, 10) != id || len(country) != 2 || country[0] < 'a' || country[0] > 'z' || country[1] < 'a' || country[1] > 'z' {
		return false
	}
	songURL := "https://music.apple.com/" + country + "/song/" + id
	for _, link := range track.Claims {
		relation, linkedURL, found := strings.Cut(link.Locator, "].url.resource: ")
		index := strings.TrimPrefix(relation, "relations[")
		position, err := strconv.Atoi(index)
		validLocator := found && strings.HasPrefix(relation, "relations[") && err == nil && position >= 0 && strconv.Itoa(position) == index && linkedURL == songURL
		if link.Method == "publisher_song_link" && link.Kind == "recording_identity" && link.State == core.EvidenceMatch &&
			link.Scope == "recording" && link.Coverage == nil && link.Source.Provider == "musicbrainz" &&
			link.ExtractorVersion == claim.ExtractorVersion && link.Value == claim.Source.URL &&
			validLocator &&
			attributedRecordingClaim(link, track) {
			return true
		}
	}
	return false
}
