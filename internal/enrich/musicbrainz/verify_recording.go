package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/core"
)

const recordingExtractorVersion = "musicbrainz-recording-claims/v1"

type recordingRelation struct {
	Type       string   `json:"type"`
	Direction  string   `json:"direction"`
	TargetType string   `json:"target-type"`
	Ended      bool     `json:"ended"`
	Attributes []string `json:"attributes"`
	Artist     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
	Work struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"work"`
	URL struct {
		Resource string `json:"resource"`
	} `json:"url"`
}

type recordingDocument struct {
	mbRecording
	Disambiguation string              `json:"disambiguation"`
	Relations      []recordingRelation `json:"relations"`
}

// VerifyRecording acquires facts independently of ranking. Artist profiles,
// empty tags and absent credits never become proof about a recording.
func (c *Client) VerifyRecording(ctx context.Context, track core.EnrichedTrack, criteria []core.MusicalCriterion) (core.EnrichedTrack, error) {
	return c.verifyRecording(ctx, track, criteria, false)
}

// VerifyCachedRecording applies the same identity policy to cached raw provider
// evidence. It never fetches missing responses or invokes source extraction.
func (c *Client) VerifyCachedRecording(ctx context.Context, track core.EnrichedTrack) (core.EnrichedTrack, error) {
	return c.verifyRecording(context.WithValue(ctx, cacheOnlyKey{}, true), track, nil, true)
}

func (c *Client) verifyRecording(ctx context.Context, track core.EnrichedTrack, criteria []core.MusicalCriterion, identityOnly bool) (core.EnrichedTrack, error) {
	if err := ctx.Err(); err != nil {
		return track, err
	}
	if track.IdentityStatus != core.ResolutionResolved || !contextMBID.MatchString(track.RecordingID) {
		return track, nil
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, ok := ctx.Value(knowledgeBudgetKey{}).(*knowledgeBudget); !ok {
		ctx = context.WithValue(ctx, knowledgeBudgetKey{}, &knowledgeBudget{})
	}
	budget := ctx.Value(knowledgeBudgetKey{}).(*knowledgeBudget)
	budget.mu.Lock()
	limit := budget.requests + 12
	budget.mu.Unlock()
	if inherited, ok := ctx.Value(contextRequestLimitKey{}).(int); ok {
		limit = min(limit, inherited)
	}
	ctx = context.WithValue(ctx, contextRequestLimitKey{}, limit)
	path := "/ws/2/recording/" + track.RecordingID + "?" + url.Values{"fmt": {"json"}, "inc": {"artist-credits+genres+tags+isrcs+artist-rels+work-rels+url-rels"}}.Encode()
	raw, err := c.metadataGet(ctx, c.base, path, "entity-context-v1:", c.hc, false)
	if err != nil {
		return track, err
	}
	var doc recordingDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return track, err
	}
	if !strings.EqualFold(doc.ID, track.RecordingID) {
		return track, fmt.Errorf("recording evidence identity mismatch")
	}
	if conflictingRecordingISRCs(track, doc.ISRCs) {
		track.IdentityStatus, track.Matched, track.Claims = core.ResolutionAmbiguous, false, nil
		return track, nil
	}
	// A recording length is aggregate metadata. An explicitly declared release
	// track is more specific; unavailable edition evidence leaves duration unknown.
	var releaseEvidence *releaseTrackEvidence
	releaseChecked := false
	duration := track.FullRecordingDuration
	if duration.Valid() && !strings.Contains(strings.ToLower(duration.Source), "preview") && recordingDurationBelongsTo(track, duration.RecordingID) &&
		doc.Length != nil && *doc.Length > 0 && *doc.Length <= 24*60*60*1000 && !publisherDurationMatches(duration.Milliseconds, *doc.Length) {
		conflict := true
		if contextMBID.MatchString(track.ReleaseID) && contextMBID.MatchString(track.ReleaseTrackID) {
			releaseChecked = true
			releaseEvidence, err = c.corroborateReleaseTrack(ctx, track)
			if ctx.Err() != nil {
				return track, ctx.Err()
			}
			conflict = err == nil && releaseEvidence != nil && (releaseEvidence.identityConflict ||
				releaseEvidence.length != nil && *releaseEvidence.length > 0 && *releaseEvidence.length <= 24*60*60*1000 && !publisherDurationMatches(duration.Milliseconds, *releaseEvidence.length))
		}
		if conflict {
			track.IdentityStatus, track.Matched, track.Claims = core.ResolutionAmbiguous, false, nil
			return track, nil
		}
	}
	credited := false
	for _, credit := range doc.ArtistCredit {
		credited = credited || core.NormalizeIdentityPart(credit.Name) == core.NormalizeIdentityPart(track.Ref.Artist)
		for _, artistID := range track.ArtistIDs {
			credited = credited || artistID != "" && strings.EqualFold(artistID, credit.Artist.ID)
		}
	}
	if !credited {
		return track, fmt.Errorf("recording evidence artist mismatch")
	}
	var identityClaim *core.RecordingClaim
	if releaseEvidence != nil {
		identityClaim = releaseEvidence.claim
	}
	if core.NormalizeIdentityPart(doc.Title) != core.NormalizeIdentityPart(track.Ref.Title) {
		if !releaseChecked {
			releaseEvidence, err = c.corroborateReleaseTrack(ctx, track)
		}
		if err != nil {
			return track, err
		}
		if releaseEvidence != nil {
			if releaseEvidence.identityConflict {
				track.IdentityStatus, track.Matched, track.Claims = core.ResolutionAmbiguous, false, nil
				return track, nil
			}
			identityClaim = releaseEvidence.claim
		}
		if identityClaim == nil {
			return track, fmt.Errorf("recording title differs without exact release-track corroboration")
		}
	}
	if identityOnly {
		return track, ctx.Err()
	}
	claims := make([]core.RecordingClaim, 0, len(track.Claims))
	for _, claim := range track.Claims {
		if claim.ExtractorVersion != recordingExtractorVersion {
			claims = append(claims, claim)
		}
	}
	track.Claims = claims
	if identityClaim != nil {
		track.Claims = append(track.Claims, *identityClaim)
	}
	source := core.ContextSource{Provider: "musicbrainz", URL: c.base + path, Revision: knowledgeHash(json.RawMessage(raw)), License: "CC0-1.0"}
	fetched := c.claimRetrievedAt(ctx, c.base, path, "entity-context-v1:")
	add := func(kind, value, method, locator string) {
		claimSource := source
		if method == "community_tag" {
			claimSource.License = "CC-BY-NC-SA-3.0"
		}
		track.Claims = append(track.Claims, recordingClaim(track, kind, value, "recording", doc.ID, method, locator, claimSource, fetched))
	}
	if doc.FirstReleaseDate != "" {
		add("original_release_date", doc.FirstReleaseDate, "recording_metadata", "first-release-date")
	}
	for _, group := range []struct {
		kind, field string
		tags        []mbTag
	}{{"genre", "genres", doc.Genres}, {"tag", "tags", doc.Tags}} {
		for i, tag := range group.tags {
			if tag.Count > 0 && strings.TrimSpace(tag.Name) != "" {
				add(group.kind, tag.Name, "community_tag", fmt.Sprintf("%s[%d]", group.field, i))
			}
		}
	}
	for i, rel := range doc.Relations {
		// Ended performance credits describe completed recording sessions;
		// unlike expired URL links, they remain facts about the recording.
		if !contextMBID.MatchString(rel.Artist.ID) {
			continue
		}
		locator := fmt.Sprintf("relations[%d]", i)
		// Artist credits can include composers, while the display string can
		// contain untyped contributors. Only explicit relationship roles say
		// who performed or engineered this particular recording.
		if name := strings.TrimSpace(rel.Artist.Name); name != "" {
			role := ""
			switch rel.Type {
			case "performer", "instrument", "vocal", "performing orchestra", "conductor", "chorus master", "concertmaster":
				role = "performer"
			case "engineer", "audio", "sound", "mix", "recording", "field recordist", "balance":
				role = "engineer"
			}
			if role != "" {
				add(role, name, "recording_credit", locator)
				track.Claims[len(track.Claims)-1].ArtistID = rel.Artist.ID
			}
		}
		switch rel.Type {
		case "vocal":
			add("vocal", "vocal", "recording_credit", locator)
		case "instrument":
			for _, instrument := range rel.Attributes {
				switch strings.ToLower(strings.TrimSpace(instrument)) {
				case "guest", "additional", "solo", "unknown":
					continue // Relationship modifiers do not identify instruments.
				}
				if strings.TrimSpace(instrument) != "" {
					add("instrumentation", instrument, "recording_credit", locator)
				}
			}
		}
	}
	c.publisherRecordingClaims(ctx, &track, doc, source, fetched)
	c.linkedRecordingClaims(ctx, &track, doc.Relations, "recording", doc.ID, "P4404")
	wantsComposer := false
	for _, criterion := range criteria {
		wantsComposer = wantsComposer || criterion.Kind == "composer"
	}
	if wantsComposer {
		var works []string
		for _, rel := range doc.Relations {
			if rel.Type != "performance" || !contextMBID.MatchString(rel.Work.ID) {
				continue
			}
			works = append(works, rel.Work.ID)
		}
		// Medleys with several works cannot be credited to one inferred work.
		if len(works) == 1 {
			// Additional work evidence is optional; its outage or exhausted
			// child budget cannot erase already acquired recording facts.
			_ = c.workRecordingClaims(ctx, &track, works[0])
		}
	}
	c.officialRecordingClaims(ctx, &track, doc.Relations, doc.ArtistCredit)
	return track, parent.Err()
}

func recordingClaim(track core.EnrichedTrack, kind, value, scope, entityID, method, locator string, source core.ContextSource, fetched string) core.RecordingClaim {
	return core.RecordingClaim{Kind: kind, Value: value, State: core.EvidenceMatch, Scope: scope, EntityID: entityID, RecordingID: track.RecordingID, Source: source, Locator: locator, Method: method, RetrievedAt: fetched, ExtractorVersion: recordingExtractorVersion}
}

func (c *Client) claimRetrievedAt(ctx context.Context, base, path, namespace string) string {
	entry := c.readCache(ctx, metadataKey(base, path, namespace))
	if entry.fetched <= 0 {
		return ""
	}
	return time.Unix(entry.fetched, 0).UTC().Format(time.RFC3339)
}

func (c *Client) workRecordingClaims(ctx context.Context, track *core.EnrichedTrack, id string) error {
	path := "/ws/2/work/" + id + "?" + url.Values{"fmt": {"json"}, "inc": {"artist-rels+url-rels"}}.Encode()
	raw, err := c.metadataGet(ctx, c.base, path, "entity-context-v1:", c.hc, false)
	if err != nil {
		return err
	}
	var doc recordingDocument
	if json.Unmarshal(raw, &doc) != nil || !strings.EqualFold(doc.ID, id) {
		return fmt.Errorf("work evidence identity mismatch")
	}
	source := core.ContextSource{Provider: "musicbrainz", URL: c.base + path, Revision: knowledgeHash(json.RawMessage(raw)), License: "CC0-1.0"}
	fetched := c.claimRetrievedAt(ctx, c.base, path, "entity-context-v1:")
	for i, rel := range doc.Relations {
		if rel.Type == "composer" && contextMBID.MatchString(rel.Artist.ID) && strings.TrimSpace(rel.Artist.Name) != "" {
			track.Claims = append(track.Claims, recordingClaim(*track, "composer", rel.Artist.Name, "work", id, "recording_credit", fmt.Sprintf("relations[%d]", i), source, fetched))
		}
	}
	c.linkedRecordingClaims(ctx, track, doc.Relations, "work", id, "P435")
	return ctx.Err()
}

func (c *Client) linkedRecordingClaims(ctx context.Context, track *core.EnrichedTrack, relations []recordingRelation, scope, mbid, identityProperty string) {
	entity := contextEntity{Relations: relations}
	qid := linkedWikidata(entity)
	item, sourceURL, ok := c.discoveryWikidataEntity(ctx, qid)
	if !ok || !wikidataIdentity(item, identityProperty, mbid) {
		return
	}
	path := "/wiki/Special:EntityData/" + qid + ".json"
	fetched := c.claimRetrievedAt(ctx, c.wikidataBase, path, "wikidata-context-v1:")
	source := core.ContextSource{Provider: "wikidata", URL: sourceURL, Revision: jsonNumber(item.LastRevision), License: "CC0-1.0"}
	properties := []struct{ property, kind string }{{"P136", "genre"}, {"P1303", "instrumentation"}}
	if scope == "work" {
		properties = []struct{ property, kind string }{{"P86", "composer"}}
	}
	for _, property := range properties {
		for i, statement := range item.Claims[property.property] {
			if !sourcedStatement(statement) {
				continue
			}
			var value struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(statement.MainSnak.DataValue.Value, &value) != nil || !contextQID.MatchString(value.ID) {
				continue
			}
			label, _, found := c.discoveryWikidataEntity(ctx, value.ID)
			name := strings.TrimSpace(label.Labels["en"].Value)
			if !found || name == "" {
				continue
			}
			track.Claims = append(track.Claims, recordingClaim(*track, property.kind, name, scope, mbid, "linked_statement", fmt.Sprintf("claims.%s[%d]", property.property, i), source, fetched))
		}
	}
}

// Qualified statements may describe only one part or version. Until a qualifier
// is interpreted explicitly it cannot establish a full-recording assertion.
func sourcedStatement(statement wikidataStatement) bool {
	if statement.Rank != "normal" && statement.Rank != "preferred" || statement.MainSnak.SnakType != "value" || len(statement.Qualifiers) > 0 {
		return false
	}
	for _, reference := range statement.References {
		for _, snak := range reference.Snaks["P854"] {
			var value string
			if snak.SnakType != "value" || json.Unmarshal(snak.DataValue.Value, &value) != nil {
				continue
			}
			u, err := url.Parse(value)
			if err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil {
				return true
			}
		}
		for _, snak := range reference.Snaks["P248"] {
			var value struct {
				ID string `json:"id"`
			}
			if snak.SnakType == "value" && json.Unmarshal(snak.DataValue.Value, &value) == nil && contextQID.MatchString(value.ID) {
				return true
			}
		}
	}
	return false
}

// Match the existing preview-provider full-duration binding rules; this checks identity, not preview length.
func recordingDurationBelongsTo(track core.EnrichedTrack, recordingID string) bool {
	id := strings.ToLower(strings.TrimSpace(recordingID))
	for _, value := range []string{track.RecordingID, track.Ref.RecordingIdentity, track.Ref.ID} {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && (id == value || strings.TrimPrefix(id, "musicbrainz:") == strings.TrimPrefix(value, "musicbrainz:")) {
			return true
		}
	}
	return false
}
