package localcatalog

import (
	"context"
	"errors"
	"strings"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/mbindex"
)

// RecognitionProvider distinguishes pack identities from MusicBrainz identifiers.
func (*Catalog) RecognitionProvider() string { return "paipack" }

func (c *Catalog) SnapshotIdentity() mbindex.SnapshotIdentity {
	return mbindex.SnapshotIdentity{IndexVersion: "paipack-recognition/v1", Snapshot: c.catalogVersion()}
}

// RecognitionGenres exposes only the learned genre vocabulary, never paths,
// arbitrary tags, or private track lists. Unknown terms remain exact categories.
func (c *Catalog) RecognitionGenres(ctx context.Context) ([]string, error) {
	_, done, err := c.withGeneration()
	if err != nil {
		return nil, err
	}
	defer done()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.metadata == nil {
		return nil, nil
	}
	terms := c.metadata.Vocabulary
	return append([]string(nil), terms[:min(len(terms), 10000)]...), nil
}

// LookupArtistAlbums grounds explicit album syntax without treating a composer
// or work name as a performing artist. Results are bounded by the same lookup
// cap as artist-scoped recording recognition.
func (c *Catalog) LookupArtistAlbums(ctx context.Context, artist, title string) ([]core.IdentityCandidate, bool, error) {
	tracks, err := c.artistRecordings(ctx, artist, 513)
	if err != nil {
		return nil, false, err
	}
	var out []core.IdentityCandidate
	seen := map[string]bool{}
	for _, ref := range tracks[:min(512, len(tracks))] {
		track, ok, err := c.Lookup(ctx, ref.ID)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			continue
		}
		if core.NormalizeIdentityPart(track.Album) != core.NormalizeIdentityPart(title) {
			continue
		}
		id := "paipack-album:" + normalizeUnicode(artist) + ":" + normalizeUnicode(title)
		if !seen[id] {
			seen[id] = true
			out = append(out, core.IdentityCandidate{Kind: core.ReferenceAlbum, ID: id, Name: track.Artist, Title: track.Album})
		}
	}
	return out, len(tracks) > 512, nil
}

// LookupArtistNames uses exact indexed names, never an unbounded catalog scan.
func (c *Catalog) LookupArtistNames(ctx context.Context, names []string) ([]mbindex.ArtistNameLookup, error) {
	if len(names) > mbindex.MaxLookupKeys {
		return nil, errors.New("too many recognition keys")
	}
	out := make([]mbindex.ArtistNameLookup, 0, len(names))
	for _, name := range names {
		tracks, err := c.artistRecordings(ctx, name, 1)
		if err != nil {
			return nil, err
		}
		result := mbindex.ArtistNameLookup{Name: name, NameKey: core.NormalizeIdentityPart(name)}
		if len(tracks) > 0 {
			result.Candidates = []mbindex.ArtistIdentity{{MBID: "paipack-artist:" + normalizeUnicode(name), Name: tracks[0].Artist, MatchedName: name, MatchType: mbindex.ArtistMatchCanonical}}
		}
		out = append(out, result)
	}
	return out, nil
}

func (c *Catalog) LookupArtistRecordings(ctx context.Context, queries []mbindex.ArtistRecordingQuery) ([]mbindex.ArtistRecordingLookup, error) {
	if len(queries) > mbindex.MaxLookupKeys {
		return nil, errors.New("too many recording recognition keys")
	}
	out := make([]mbindex.ArtistRecordingLookup, 0, len(queries))
	for _, query := range queries {
		result := mbindex.ArtistRecordingLookup{ArtistMBID: query.ArtistMBID, Title: query.Title, TitleKey: core.NormalizeIdentityPart(query.Title)}
		if artist, ok := strings.CutPrefix(query.ArtistMBID, "paipack-artist:"); ok {
			// Bound title recognition independently of an artist's discography.
			tracks, err := c.artistRecordings(ctx, artist, 513)
			if err != nil {
				return nil, err
			}
			result.Truncated = len(tracks) > 512
			for _, track := range tracks[:min(len(tracks), 512)] {
				if core.NormalizeIdentityPart(track.Title) != result.TitleKey {
					continue
				}
				if len(result.Candidates) == mbindex.MaxLookupCandidates {
					result.Truncated = true
					break
				}
				result.Candidates = append(result.Candidates, mbindex.RecordingIdentity{MBID: track.ID, Title: track.Title, ArtistCredit: track.Artist})
			}
		}
		out = append(out, result)
	}
	return out, nil
}
