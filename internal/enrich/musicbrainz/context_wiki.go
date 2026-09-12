package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/core"
)

var contextQID = regexp.MustCompile(`^Q[1-9][0-9]*$`)

func validContextMetadata(path, namespace string, raw []byte) bool {
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	switch namespace {
	case "entity-context-v1:":
		var entity contextEntity
		parts := strings.Split(u.Path, "/")
		return len(parts) == 5 && json.Unmarshal(raw, &entity) == nil && strings.EqualFold(entity.ID, parts[4]) && (entity.Name != "" || entity.Title != "")
	case "wikidata-context-v1:":
		qid := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/wiki/Special:EntityData/"), ".json")
		var data struct {
			Entities map[string]wikidataEntity `json:"entities"`
		}
		if json.Unmarshal(raw, &data) != nil {
			return false
		}
		item, ok := data.Entities[qid]
		return ok && item.ID == qid && item.LastRevision > 0
	case "wikipedia-context-v1:":
		var data struct {
			Query struct {
				Pages []json.RawMessage `json:"pages"`
			} `json:"query"`
		}
		return json.Unmarshal(raw, &data) == nil && len(data.Query.Pages) == 1
	}
	return false
}

func (c *Client) configureContext(cfg Config) error {
	var err error
	c.wikidataBase, err = contextBase(cfg.WikidataURL, "https://www.wikidata.org")
	if err != nil {
		return err
	}
	c.wikipediaBase, err = contextBase(cfg.WikipediaURL, "https://en.wikipedia.org")
	if err != nil {
		return err
	}
	c.contextClient = &http.Client{Timeout: contextBudget, Transport: c.hc.Transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
			return fmt.Errorf("context redirect changed provider")
		}
		return nil
	}}
	return nil
}

func contextBase(override, canonical string) (string, error) {
	if override == "" {
		return canonical, nil
	}
	u, err := url.Parse(override)
	if err != nil {
		return "", err
	}
	ip := net.ParseIP(u.Hostname())
	if (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("context endpoint override must use a loopback test server")
	}
	return strings.TrimRight(override, "/"), nil
}

type wikidataEntity struct {
	ID           string `json:"id"`
	LastRevision int64  `json:"lastrevid"`
	Claims       map[string][]struct {
		Rank     string `json:"rank"`
		MainSnak struct {
			SnakType  string `json:"snaktype"`
			DataValue struct {
				Value json.RawMessage `json:"value"`
			} `json:"datavalue"`
		} `json:"mainsnak"`
	} `json:"claims"`
	Sitelinks map[string]struct {
		Title string `json:"title"`
	} `json:"sitelinks"`
}

func linkedWikidata(entity contextEntity) string {
	qid := ""
	for _, relation := range entity.Relations {
		if relation.Type != "wikidata" || relation.Ended {
			continue
		}
		u, err := url.Parse(relation.URL.Resource)
		if err != nil || u.Scheme != "https" || u.Host != "www.wikidata.org" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			continue
		}
		candidate, ok := strings.CutPrefix(u.Path, "/wiki/")
		if !ok || !contextQID.MatchString(candidate) {
			continue
		}
		if qid != "" && qid != candidate {
			return ""
		}
		qid = candidate
	}
	return qid
}

func (c *Client) linkedContext(ctx context.Context, mbid, kind string, entity contextEntity, profile *core.ContextProfile) {
	qid := linkedWikidata(entity)
	if qid == "" || ctx.Err() != nil {
		return
	}
	path := "/wiki/Special:EntityData/" + qid + ".json"
	raw, err := c.metadataGet(ctx, c.wikidataBase, path, "wikidata-context-v1:", c.contextClient, false)
	if err != nil {
		return
	}
	var data struct {
		Entities map[string]wikidataEntity `json:"entities"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return
	}
	item, ok := data.Entities[qid]
	if !ok || item.ID != qid || item.LastRevision <= 0 {
		return
	}
	property := "P434"
	if kind == "release-group" {
		property = "P436"
	}
	matched := false
	for _, claim := range item.Claims[property] {
		if claim.Rank == "deprecated" || claim.MainSnak.SnakType != "value" {
			continue
		}
		var value string
		if json.Unmarshal(claim.MainSnak.DataValue.Value, &value) != nil {
			continue
		}
		if !strings.EqualFold(value, mbid) {
			return
		} // conflicting identity never reaches Wikipedia
		matched = true
	}
	if !matched {
		return
	}
	revision := strconv.FormatInt(item.LastRevision, 10)
	profile.Sources = append(profile.Sources, core.ContextSource{Provider: "wikidata", URL: "https://www.wikidata.org/wiki/Special:EntityData/" + qid + ".json?revision=" + revision, Revision: revision, License: "CC0-1.0"})
	title := strings.TrimSpace(item.Sitelinks["enwiki"].Title)
	if title == "" || len(title) > 300 {
		return
	}
	path = "/w/api.php?" + url.Values{"action": {"query"}, "format": {"json"}, "formatversion": {"2"}, "prop": {"extracts|info|pageprops"}, "ppprop": {"wikibase_item|disambiguation"}, "explaintext": {"1"}, "exintro": {"1"}, "exchars": {"1200"}, "inprop": {"url"}, "titles": {title}, "maxlag": {"5"}}.Encode()
	raw, err = c.metadataGet(ctx, c.wikipediaBase, path, "wikipedia-context-v1:", c.contextClient, false)
	if err != nil {
		return
	}
	var page struct {
		Query struct {
			Pages []struct {
				PageID       int64  `json:"pageid"`
				NS           int    `json:"ns"`
				Title        string `json:"title"`
				LastRevision int64  `json:"lastrevid"`
				Extract      string `json:"extract"`
				Missing      bool   `json:"missing"`
				PageProps    struct {
					Item           string  `json:"wikibase_item"`
					Disambiguation *string `json:"disambiguation"`
				} `json:"pageprops"`
			} `json:"pages"`
		} `json:"query"`
	}
	if json.Unmarshal(raw, &page) != nil || len(page.Query.Pages) != 1 {
		return
	}
	p := page.Query.Pages[0]
	if p.Missing || p.PageID <= 0 || p.NS != 0 || p.LastRevision <= 0 || p.PageProps.Item != qid || p.PageProps.Disambiguation != nil || p.Title != title {
		return
	}
	text := []rune(strings.TrimSpace(p.Extract))
	if len(text) > 2400 {
		text = text[:2400]
	}
	if len(text) == 0 {
		return
	}
	profile.Description = string(text)
	revision = strconv.FormatInt(p.LastRevision, 10)
	profile.Sources = append(profile.Sources, core.ContextSource{Provider: "wikipedia", URL: "https://en.wikipedia.org/w/index.php?" + url.Values{"title": {p.Title}, "oldid": {revision}}.Encode(), Revision: revision, License: "CC-BY-SA-4.0"})
}
