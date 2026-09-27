package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
)

const (
	publisherBase      = "https://itunes.apple.com"
	publisherNamespace = "apple-song-v1:"
	publisherTerms     = "https://www.apple.com/legal/internet-services/itunes/terms.html"
)

// The archived Search API documentation specifies approximately 20 calls per
// minute. All clients share this three-second dispatch interval, including
// retries. There is no runtime endpoint override or redirect destination.
func newPublisherClient() *http.Client {
	limiter, _ := applicationLimiters.LoadOrStore("itunes.apple.com", &requestLimiter{gate: make(chan struct{}, 1)})
	throttle := &Client{interval: 3 * time.Second, limiter: limiter.(*requestLimiter)}
	return &http.Client{
		Timeout:   8 * time.Second,
		Transport: &limitedTransport{client: throttle, base: http.DefaultTransport},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type publisherSong struct {
	WrapperType string `json:"wrapperType"`
	Kind        string `json:"kind"`
	TrackID     int64  `json:"trackId"`
	Artist      string `json:"artistName"`
	Title       string `json:"trackName"`
	Duration    int64  `json:"trackTimeMillis"`
	Genre       string `json:"primaryGenreName"`
	ISRC        string `json:"isrc"`
}

type publisherResponse struct {
	Count   int             `json:"resultCount"`
	Results []publisherSong `json:"results"`
}

// Only canonical recording song links participate in this pilot. Album links
// with a selected track, slugs, search results and legacy URLs remain unknown.
func publisherLookupPath(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "music.apple.com" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return "", false
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 4 || parts[0] != "" || len(parts[1]) != 2 || parts[2] != "song" {
		return "", false
	}
	for _, ch := range parts[1] {
		if ch < 'a' || ch > 'z' {
			return "", false
		}
	}
	id, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != parts[3] {
		return "", false
	}
	return "/lookup?" + url.Values{"country": {parts[1]}, "id": {parts[3]}}.Encode(), true
}

func validPublisherResponse(path string, raw []byte) bool {
	var response struct {
		Count   *int            `json:"resultCount"`
		Results []publisherSong `json:"results"`
		Error   json.RawMessage `json:"error"`
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Count == nil || response.Results == nil || *response.Count != len(response.Results) || *response.Count > 1 {
		return false
	}
	for _, value := range []json.RawMessage{response.Error, response.Message} {
		if len(value) > 0 && string(value) != "null" {
			return false
		}
	}
	if *response.Count == 0 {
		return true // A successful empty lookup is a cacheable absence, not proof.
	}
	u, err := url.Parse(path)
	if err != nil || u.Path != "/lookup" {
		return false
	}
	song := response.Results[0]
	return song.TrackID > 0 && strconv.FormatInt(song.TrackID, 10) == u.Query().Get("id") && song.WrapperType == "track" && song.Kind == "song"
}

func (c *Client) publisherRecordingClaims(ctx context.Context, track *core.EnrichedTrack, doc recordingDocument, source core.ContextSource, fetched string) {
	if c.publisherClient == nil || ctx.Err() != nil {
		return
	}
	for i, relation := range doc.Relations {
		if relation.Ended || !recordingPageRelation(relation.Type) || relation.Direction != "" && relation.Direction != "forward" || relation.TargetType != "" && relation.TargetType != "url" {
			continue
		}
		path, ok := publisherLookupPath(relation.URL.Resource)
		if !ok {
			continue
		}
		// One eligible relationship, one dispatch including retries. A mismatch
		// or outage does not fan out across editions/countries. Existing cache
		// reuse retains historical response hashes, not a freshness guarantee.
		if budget, ok := ctx.Value(knowledgeBudgetKey{}).(*knowledgeBudget); ok {
			budget.mu.Lock()
			limit := budget.requests + 1
			budget.mu.Unlock()
			if inherited, ok := ctx.Value(contextRequestLimitKey{}).(int); ok {
				limit = min(limit, inherited)
			}
			ctx = context.WithValue(ctx, contextRequestLimitKey{}, limit)
		}
		raw, err := c.metadataGet(ctx, publisherBase, path, publisherNamespace, c.publisherClient, false)
		var response publisherResponse
		if err != nil || json.Unmarshal(raw, &response) != nil || len(response.Results) != 1 || !publisherSongMatches(*track, doc, response.Results[0]) || ctx.Err() != nil {
			return
		}
		lookupURL := publisherBase + path
		identity := recordingClaim(*track, "recording_identity", lookupURL, "recording", doc.ID, "publisher_song_link", fmt.Sprintf("relations[%d].url.resource: %s", i, relation.URL.Resource), source, fetched)
		songSource := core.ContextSource{Provider: "apple", URL: lookupURL, Revision: knowledgeHash(json.RawMessage(raw)), License: publisherTerms}
		genre := recordingClaim(*track, "genre", strings.TrimSpace(response.Results[0].Genre), "recording", doc.ID, "publisher_field", "results[0].primaryGenreName", songSource, c.claimRetrievedAt(ctx, publisherBase, path, publisherNamespace))
		track.Claims = append(track.Claims, identity, genre)
		return
	}
}

func publisherSongMatches(track core.EnrichedTrack, doc recordingDocument, song publisherSong) bool {
	// The narrow pilot requires one exact billed performer; it does not split
	// collaboration strings or infer that a composer is the performer.
	if len(doc.ArtistCredit) != 1 || !contextMBID.MatchString(doc.ArtistCredit[0].Artist.ID) {
		return false
	}
	credit := doc.ArtistCredit[0]
	artist := publisherName(song.Artist)
	if artist == "" || artist != publisherName(credit.Name) && artist != publisherName(credit.Artist.Name) {
		return false
	}
	if publisherTitle(song.Title) == "" || publisherTitle(song.Title) != publisherTitle(doc.Title) || strings.TrimSpace(song.Genre) == "" || len(song.Genre) > 120 {
		return false
	}
	// A version sometimes lives only in MB's disambiguation text. Its absence
	// as an explicit publisher title qualifier is unknown, even when a natural
	// title word agrees (for example, the song title "Live Forever").
	for _, version := range []string{"live", "remix", "edit", "acoustic", "demo", "instrumental", "karaoke"} {
		if containsPublisherWord(doc.Disambiguation, version) && !publisherVersionQualified(song.Title, version) {
			return false
		}
	}
	if doc.Length == nil || !publisherDurationMatches(*doc.Length, song.Duration) {
		return false
	}
	if track.FullRecordingDuration.Valid() && !publisherDurationMatches(track.FullRecordingDuration.Milliseconds, song.Duration) {
		return false
	}
	return !conflictingRecordingISRCs(track, []string{song.ISRC}) && !conflictingRecordingISRCs(core.EnrichedTrack{AllISRCs: doc.ISRCs}, []string{song.ISRC})
}

func publisherDurationMatches(recording, song int64) bool {
	if recording <= 0 || song <= 0 || recording > 24*60*60*1000 || song > 24*60*60*1000 {
		return false
	}
	delta := recording - song
	if delta < 0 {
		delta = -delta
	}
	return delta <= max(2000, min(5000, recording/50))
}

var publisherRemaster = regexp.MustCompile(`(?i)(?:\s*\((?:[12][0-9]{3} )?remaster(?:ed)?(?: [12][0-9]{3})?\)|\s*\[(?:[12][0-9]{3} )?remaster(?:ed)?(?: [12][0-9]{3})?\]| - (?:[12][0-9]{3} )?remaster(?:ed)?(?: [12][0-9]{3})?)$`)

func publisherTitle(value string) string {
	// Remasters may share a recording MBID. This is recording-level genre
	// evidence, never proof that two release masters have identical audio.
	return publisherName(publisherRemaster.ReplaceAllString(strings.TrimSpace(value), ""))
}

func publisherName(value string) string {
	return core.NormalizeIdentityPart(strings.NewReplacer("’", "'", "‘", "'", "‐", "-", "–", "-").Replace(value))
}

func publisherVersionQualified(value, version string) bool {
	// Narrow suffix forms are enough for the pilot. Named mixes, locations,
	// dates and other ambiguous version prose remain unknown.
	title := publisherTitle(value)
	return strings.HasSuffix(title, " ("+version+")") || strings.HasSuffix(title, " ["+version+"]") || strings.HasSuffix(title, " - "+version)
}

func containsPublisherWord(value, word string) bool {
	for _, token := range strings.FieldsFunc(strings.ToLower(value), func(ch rune) bool { return ch < 'a' || ch > 'z' }) {
		if token == word {
			return true
		}
	}
	return false
}
