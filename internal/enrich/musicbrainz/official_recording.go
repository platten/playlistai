package musicbrainz

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/httpretry"
	"github.com/platten/playlistai/internal/ports"
)

// WithRecordingSourceExtractor configures the local extractor once at startup,
// before this client is used by generation goroutines. Nil disables page reads.
func (c *Client) WithRecordingSourceExtractor(extractor ports.RecordingSourceExtractor) {
	c.sourceExtractor = extractor
}

func (c *Client) officialRecordingClaims(ctx context.Context, track *core.EnrichedTrack, relations []recordingRelation, credits []mbArtistCredit) {
	if c.sourceExtractor == nil {
		return
	}
	var hosts map[string]bool
	for _, relation := range relations {
		if !relation.Ended && recordingPageRelation(relation.Type) && len(credits) > 0 {
			hosts = c.officialArtistHosts(ctx, credits)
			break
		}
	}
	seen := map[string]bool{}
	for _, relation := range relations {
		if len(seen) >= 2 || ctx.Err() != nil {
			return
		}
		if relation.Ended {
			continue
		}
		u, err := url.Parse(relation.URL.Resource)
		if err != nil || seen[u.String()] || !c.allowedOfficialURL(u) {
			continue
		}
		// Streaming/download links establish a recording page, but only the
		// credited artist's own official host can establish first-party context.
		if relation.Type != "official homepage" && relation.Type != "discography entry" && (!recordingPageRelation(relation.Type) || !hosts[strings.ToLower(u.Hostname())]) {
			continue
		}
		seen[u.String()] = true
		base, path := u.Scheme+"://"+u.Host, u.RequestURI()
		client, closeIdle := c.officialRecordingClient(u)
		raw, err := c.metadataGet(ctx, base, path, "official-recording-v1:", client, false)
		closeIdle()
		if err != nil {
			continue
		}
		text := officialRecordingText(raw)
		// A linked page must still identify this recording and its artist.
		normalized := core.NormalizeIdentityPart(text)
		if text == "" || !strings.Contains(normalized, core.NormalizeIdentityPart(track.Ref.Title)) || !strings.Contains(normalized, core.NormalizeIdentityPart(track.Ref.Artist)) {
			continue
		}
		source := core.ContextSource{Provider: "official", URL: u.String(), Revision: knowledgeHash(string(raw)), License: "source-specific"}
		claims, err := c.sourceExtractor.ExtractRecordingClaims(ctx, ports.RecordingSource{Track: *track, Source: source, Text: text})
		if err != nil {
			continue
		}
		for _, claim := range claims {
			if claim.Scope != "recording" || claim.EntityID != track.RecordingID || claim.RecordingID != track.RecordingID || claim.Value == "" || claim.Locator == "" || len(claim.Locator) > 1200 || !strings.Contains(text, claim.Locator) {
				continue
			}
			if claim.State != core.EvidenceMatch && claim.State != core.EvidenceMismatch {
				continue
			}
			switch claim.Kind {
			case "genre", "style", "vocal", "instrumentation", "composer", "original_release_date":
			default:
				continue
			}
			// A literal quote proves attribution, not semantic entailment. Keep
			// model extraction distinct from verified structured statements.
			claim.Source, claim.Method = source, "quoted_statement"
			claim.RetrievedAt = c.claimRetrievedAt(ctx, base, path, "official-recording-v1:")
			if claim.ExtractorVersion == "" {
				claim.ExtractorVersion = recordingExtractorVersion
			}
			track.Claims = append(track.Claims, claim)
		}
	}
}

func recordingPageRelation(kind string) bool {
	return kind == "free streaming" || kind == "streaming" || kind == "download for free" || kind == "purchase for download"
}

func (c *Client) officialArtistHosts(ctx context.Context, credits []mbArtistCredit) map[string]bool {
	hosts := map[string]bool{}
	for _, credit := range credits[:min(2, len(credits))] {
		if !contextMBID.MatchString(credit.Artist.ID) {
			continue
		}
		entity, _, _, err := c.contextEntity(ctx, "artist", credit.Artist.ID)
		if err != nil {
			continue
		}
		for _, relation := range entity.Relations {
			if relation.Ended || relation.Type != "official homepage" && relation.Type != "bandcamp" {
				continue
			}
			u, err := url.Parse(relation.URL.Resource)
			if err == nil && c.allowedOfficialURL(u) {
				hosts[strings.ToLower(u.Hostname())] = true
			}
		}
	}
	return hosts
}

func (c *Client) allowedOfficialURL(u *url.URL) bool {
	if u == nil || u.User != nil || u.Fragment != "" || len(u.String()) > 2048 || u.Hostname() == "" {
		return false
	}
	// Explicit loopback MusicBrainz mirrors are test fixtures, never linked
	// public sources. They allow offline HTTP tests without weakening real URLs.
	base, _ := url.Parse(c.base)
	if base != nil && u.Scheme == base.Scheme && u.Host == base.Host {
		ip := net.ParseIP(u.Hostname())
		if ip != nil && ip.IsLoopback() {
			return true
		}
	}
	if u.Scheme != "https" || u.Port() != "" && u.Port() != "443" || !strings.Contains(u.Hostname(), ".") {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return publicSourceIP(ip)
	}
	return true
}

func publicSourceIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	// Shared carrier addresses can expose host/cloud metadata services too.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return false
	}
	return true
}

func (c *Client) officialRecordingClient(origin *url.URL) (*http.Client, func()) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, candidate := range ips {
			probe := &url.URL{Scheme: origin.Scheme, Host: net.JoinHostPort(candidate.IP.String(), port)}
			localFixture := origin.Host == probe.Host && c.allowedOfficialURL(probe)
			if !publicSourceIP(candidate.IP) && !localFixture {
				return nil, httpretry.Permanent(fmt.Errorf("official source resolved to a private address"))
			}
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("official source has no address")
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != origin.Scheme || req.URL.Host != origin.Host || !c.allowedOfficialURL(req.URL) {
			return fmt.Errorf("official source redirect changed origin")
		}
		return nil
	}}
	return httpretry.Client(client), transport.CloseIdleConnections
}

func officialRecordingText(raw []byte) string {
	doc, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return ""
	}
	var text strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if text.Len() >= 16000 {
			return
		}
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "style", "nav", "header", "footer", "form", "noscript", "template":
				return
			}
		}
		if node.Type == html.TextNode {
			value := strings.Join(strings.Fields(node.Data), " ")
			if value != "" {
				text.WriteString(value)
				text.WriteByte(' ')
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	runes := []rune(strings.TrimSpace(text.String()))
	if len(runes) > 16000 {
		runes = runes[:16000]
	}
	return string(runes)
}
