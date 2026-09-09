package deezer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type analysisTrack struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Version string `json:"title_version"`
	ISRC    string `json:"isrc"`
	Preview string `json:"preview"`
	Artist  struct {
		Name string `json:"name"`
	} `json:"artist"`
}

// ResolveAudioPreview deliberately does not use PreviewURL: playback's
// best-effort search/fallback is insufficient evidence for audio analysis.
func (p *Provider) ResolveAudioPreview(ctx context.Context, ref core.TrackRef, cached core.EnrichedTrack) (core.ResolvedAudioPreview, error) {
	out := core.ResolvedAudioPreview{Identity: core.PreviewIdentity{Status: core.ResolutionUnresolved, Provider: "deezer"}}
	if ref.Artist == "" || ref.Title == "" {
		return out, nil
	}
	var candidates []analysisTrack
	wantISRC := ""
	if cached.Matched && cached.IdentityStatus == core.ResolutionResolved && core.ProvisionalRecordingKey(cached.Ref) == core.ProvisionalRecordingKey(ref) {
		wantISRC = normalizeISRC(cached.ISRC)
	}
	if wantISRC != "" {
		var candidate analysisTrack
		if err := p.analysisJSON(ctx, "/track/isrc:"+url.PathEscape(wantISRC), &candidate); err != nil {
			return out, err
		}
		if normalizeISRC(candidate.ISRC) == wantISRC {
			candidates = append(candidates, candidate)
		}
	} else {
		var response struct {
			Data []analysisTrack `json:"data"`
		}
		query := strings.TrimSpace(ref.Artist + " " + ref.Title)
		if err := p.analysisJSON(ctx, "/search?q="+url.QueryEscape(query)+"&limit=5", &response); err != nil {
			return out, err
		}
		// Plain search avoids observed empty advanced-search responses for
		// known recordings. Try the alternative syntax once on an empty page;
		// full artist/title/version corroboration below is unchanged.
		if len(response.Data) == 0 {
			if err := p.analysisJSON(ctx, "/search?q="+url.QueryEscape(deezerQuery(ref))+"&limit=5", &response); err != nil {
				return out, err
			}
		}
		candidates = response.Data
	}
	var matches []analysisTrack
	seen := map[int64]bool{}
	for _, candidate := range candidates {
		if candidate.ID <= 0 || seen[candidate.ID] {
			continue
		}
		seen[candidate.ID] = true
		out.Identity.Alternatives = append(out.Identity.Alternatives, strconv.FormatInt(candidate.ID, 10))
		title := candidate.Title
		if candidate.Version != "" && !strings.Contains(core.NormalizeIdentityPart(title), core.NormalizeIdentityPart(candidate.Version)) {
			title += " " + candidate.Version
		}
		// Full title comparison retains live/remix/remaster/version suffixes and
		// Unicode. Unknown aliases abstain rather than weakening identity checks.
		if core.NormalizeIdentityPart(candidate.Artist.Name) != core.NormalizeIdentityPart(ref.Artist) || core.NormalizeIdentityPart(title) != core.NormalizeIdentityPart(ref.Title) {
			continue
		}
		if wantISRC != "" && normalizeISRC(candidate.ISRC) != wantISRC {
			continue
		}
		matches = append(matches, candidate)
	}
	if len(matches) > 1 {
		out.Identity.Status = core.ResolutionAmbiguous
		return out, nil
	}
	if len(matches) == 0 {
		return out, nil
	}
	m := matches[0]
	out.Identity = core.PreviewIdentity{Status: core.ResolutionResolved, Provider: "deezer", ProviderID: strconv.FormatInt(m.ID, 10), ISRC: normalizeISRC(m.ISRC), Artist: m.Artist.Name, Title: m.Title, Method: "corroborated_artist_title_version"}
	if wantISRC != "" {
		out.Identity.Method = "isrc_and_artist_title_version"
		out.Identity.RecordingID = cached.RecordingID
	}
	out.URL = m.Preview
	return out, nil
}

func normalizeISRC(value string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
}

func (p *Provider) analysisJSON(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Cache-Control", "no-store")
	resp, err := p.hc.Do(req)
	if err != nil {
		return fmt.Errorf("deezer analysis: metadata request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deezer analysis: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return fmt.Errorf("deezer analysis: invalid metadata response")
	}
	return json.Unmarshal(raw, target)
}

var _ ports.AudioPreviewResolver = (*Provider)(nil)
