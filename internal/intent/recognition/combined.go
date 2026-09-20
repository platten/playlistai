package recognition

import (
	"context"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/mbindex"
)

type combinedLookup []IdentityLookup

type genreLookup interface {
	RecognitionGenres(context.Context) ([]string, error)
}

type albumLookup interface {
	LookupArtistAlbums(context.Context, string, string) ([]core.IdentityCandidate, bool, error)
}

func (c combinedLookup) LookupArtistAlbums(ctx context.Context, artist, title string) ([]core.IdentityCandidate, bool, error) {
	var out []core.IdentityCandidate
	truncated := false
	seen := map[string]bool{}
	for _, source := range c {
		if albums, ok := source.(albumLookup); ok {
			matches, partial, err := albums.LookupArtistAlbums(ctx, artist, title)
			if err != nil {
				return nil, false, err
			}
			truncated = truncated || partial
			for _, match := range matches {
				if !seen[match.ID] {
					seen[match.ID] = true
					out = append(out, match)
				}
			}
		}
	}
	return out, truncated, nil
}

func (c combinedLookup) RecognitionGenres(ctx context.Context) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, source := range c {
		if genres, ok := source.(genreLookup); ok {
			terms, err := genres.RecognitionGenres(ctx)
			if err != nil {
				return nil, err
			}
			for _, term := range terms {
				if !seen[term] && len(out) < 10000 {
					seen[term] = true
					out = append(out, term)
				}
			}
		}
	}
	return out, nil
}

// Combine searches all enabled offline identity sources without preferring a
// library identity over an outside identity. Callers own and pin the sources.
func Combine(sources ...IdentityLookup) IdentityLookup {
	var enabled combinedLookup
	for _, source := range sources {
		if source != nil {
			enabled = append(enabled, source)
		}
	}
	if len(enabled) == 0 {
		return nil
	}
	if len(enabled) == 1 {
		return enabled[0]
	}
	return enabled
}

func (c combinedLookup) RecognitionProvider() string {
	var names []string
	for _, source := range c {
		names = append(names, provider(source))
	}
	return strings.Join(names, "+")
}

func (c combinedLookup) SnapshotIdentity() mbindex.SnapshotIdentity {
	var snapshots []string
	for _, source := range c {
		s := source.SnapshotIdentity()
		snapshots = append(snapshots, s.IndexVersion+":"+s.Snapshot)
	}
	return mbindex.SnapshotIdentity{IndexVersion: "combined/v1", Snapshot: strings.Join(snapshots, "|")}
}

func (c combinedLookup) LookupArtistNames(ctx context.Context, names []string) ([]mbindex.ArtistNameLookup, error) {
	out := make([]mbindex.ArtistNameLookup, len(names))
	positions := map[string]int{}
	for i, name := range names {
		out[i] = mbindex.ArtistNameLookup{Name: name, NameKey: core.NormalizeIdentityPart(name)}
		positions[out[i].NameKey] = i
	}
	for _, source := range c {
		results, err := source.LookupArtistNames(ctx, names)
		if err != nil {
			return nil, err
		}
		for _, result := range results {
			i, ok := positions[result.NameKey]
			if !ok {
				continue
			}
			out[i].Truncated = out[i].Truncated || result.Truncated
			for _, candidate := range result.Candidates {
				duplicate := false
				for _, prior := range out[i].Candidates {
					// A pack has an artist credit, not a disambiguated artist MBID.
					// Corroborating an existing canonical spelling adds no new
					// identity alternative (nor a recommendation ranking bonus).
					duplicate = duplicate || prior.MBID == candidate.MBID || strings.HasPrefix(candidate.MBID, "paipack-artist:") && core.NormalizeIdentityPart(prior.Name) == core.NormalizeIdentityPart(candidate.Name)
				}
				if duplicate {
					continue
				}
				if len(out[i].Candidates) >= mbindex.MaxLookupCandidates {
					out[i].Truncated = true
					continue
				}
				out[i].Candidates = append(out[i].Candidates, candidate)
			}
		}
	}
	return out, nil
}

func (c combinedLookup) LookupArtistRecordings(ctx context.Context, queries []mbindex.ArtistRecordingQuery) ([]mbindex.ArtistRecordingLookup, error) {
	var out []mbindex.ArtistRecordingLookup
	for _, source := range c {
		results, err := source.LookupArtistRecordings(ctx, queries)
		if err != nil {
			return nil, err
		}
		out = append(out, results...)
	}
	return out, nil
}
