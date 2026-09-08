package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/url"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

// Discovery is bounded to 100 artists and one recording page per artist. The
// existing metadata client supplies caching, identity checks and rate limiting.
const discoveryArtists = 100

type candidateStream struct {
	client              *Client
	cat                 ports.Catalog
	resolver            ports.ReferenceResolver
	intent              core.MusicIntent
	snapshot            core.KnowledgeSnapshot
	rng                 *rand.Rand
	genres              []string
	artists             []core.GenreArtist
	pending             [][]core.TrackRef
	seen                map[string]bool
	initialized, replay bool
	position            int
	failures            int
	lastError           error
	fallback            *discogsCandidates
}

func (c *Client) OpenCandidates(intent core.MusicIntent, cat ports.Catalog, resolver ports.ReferenceResolver) ports.MusicCandidateStream {
	var genres []string
	seen := map[string]bool{}
	add := func(value string) {
		key := core.NormalizeIdentityPart(value)
		if key != "" && !seen[key] {
			genres = append(genres, value)
			seen[key] = true
		}
	}
	for _, p := range intent.Preferences.Genres {
		if p.Influence != core.InfluenceNegative {
			add(p.Value)
		}
	}
	for _, p := range intent.EssentialCriteria {
		if p.Kind == "genre" || p.Kind == "style" {
			add(p.Value)
		}
	}
	if len(genres) == 0 || cat == nil || resolver == nil {
		return nil
	}
	seed, _ := intent.Seed.Int64() // normalized and validated by the orchestrator
	s := &candidateStream{client: c, cat: cat, resolver: resolver, intent: intent, genres: genres, rng: rand.New(rand.NewSource(seed)), seen: map[string]bool{}}
	if intent.Knowledge != nil {
		raw, _ := json.Marshal(intent.Knowledge)
		_ = json.Unmarshal(raw, &s.snapshot) // independent request-owned slices
	}
	s.replay = s.snapshot.DiscoveryRecorded
	s.snapshot.DiscoveryRecorded = true
	return s
}

func (s *candidateStream) Snapshot() *core.KnowledgeSnapshot {
	s.snapshot.ID = ""
	s.snapshot.ID = knowledgeHash(s.snapshot)
	return &s.snapshot
}

func (s *candidateStream) Next(ctx context.Context) (core.TrackRef, error) {
	if s.fallback != nil {
		return s.nextDiscogs(ctx)
	}
	track, err := s.nextMusicBrainz(ctx)
	// A legitimate empty result and caller cancellation are not outages.
	// Cached (including stale offline) MusicBrainz data has already won here.
	if err == nil || err == io.EOF || ctx.Err() != nil || s.replay {
		return track, err
	}
	if !s.client.MetadataStatus().DiscogsConfigured {
		return track, fmt.Errorf("%w; configure a Discogs personal API token in Settings for fallback discovery", err)
	}
	s.fallback = &discogsCandidates{}
	s.snapshot.Notices = append(s.snapshot.Notices, "MusicBrainz discovery is unavailable. Trying Discogs release tracklists; every catalog candidate still requires the same musical checks.")
	return s.nextDiscogs(ctx)
}

func (s *candidateStream) nextMusicBrainz(ctx context.Context) (core.TrackRef, error) {
	if err := ctx.Err(); err != nil {
		return core.TrackRef{}, err
	}
	if s.replay {
		if s.position >= len(s.snapshot.Discovery) {
			return core.TrackRef{}, io.EOF
		}
		track := s.snapshot.Discovery[s.position]
		s.position++
		return track, nil
	}
	if !s.initialized {
		s.initialized = true
		var pools [][]core.GenreArtist
		for _, genre := range s.genres {
			var pool core.GenreArtistPool
			for _, cached := range s.snapshot.ArtistPools {
				if core.NormalizeIdentityPart(cached.Genre) == core.NormalizeIdentityPart(genre) {
					pool = cached
					break
				}
			}
			if len(pool.Artists) == 0 {
				pool = s.client.genreArtists(ctx, genre)
				s.snapshot.ArtistPools = append(s.snapshot.ArtistPools, pool)
			}
			if len(pool.Sources) == 0 {
				return core.TrackRef{}, fmt.Errorf("artist lookup unavailable for %q", genre)
			}
			artists := make([]core.GenreArtist, 0, len(pool.Artists))
			for _, i := range s.rng.Perm(len(pool.Artists)) {
				artists = append(artists, pool.Artists[i])
			}
			pools = append(pools, artists)
		}
		// Interleave genres so a journey's first stage cannot consume the pool.
		used := map[string]bool{}
		for round := 0; round < genreArtistTarget && len(s.artists) < discoveryArtists; round++ {
			for _, pool := range pools {
				if round >= len(pool) || len(s.artists) >= discoveryArtists {
					continue
				}
				a := pool[round]
				if !used[a.ID] && !excludedArtist(a.Name, s.intent.Constraints.ArtistsExclude) {
					s.artists = append(s.artists, a)
					used[a.ID] = true
				}
			}
		}
	}
	for len(s.artists) > 0 || len(s.pending) > 0 {
		if err := ctx.Err(); err != nil {
			return core.TrackRef{}, err
		}
		var tracks []core.TrackRef
		if len(s.artists) > 0 {
			artist := s.artists[0]
			s.artists = s.artists[1:]
			// Avoid online recording pages for artists absent from the local
			// catalog. Ambiguous names may still resolve at recording granularity.
			resolution := s.resolver.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: artist.Name})
			if resolution.Status == core.ResolutionUnresolved {
				continue
			}
			var exact map[string]string
			if catalog, ok := s.cat.(ports.ArtistRecordingCatalog); ok && resolution.Selected != nil {
				entries, err := catalog.ArtistRecordings(ctx, resolution.Selected.Artist)
				if err != nil {
					return core.TrackRef{}, err
				}
				exact = map[string]string{}
				for _, track := range entries {
					key := core.ProvisionalRecordingKey(track)
					if exact[key] == "" {
						exact[key] = track.ID
					}
				}
			}
			path := "/ws/2/recording?" + url.Values{"query": {"arid:" + artist.ID}, "fmt": {"json"}, "limit": {"100"}}.Encode()
			raw, err := s.client.knowledgeGet(ctx, path, false)
			if err != nil {
				s.failures++
				s.lastError = err
				if ctx.Err() != nil || s.failures >= 3 {
					return core.TrackRef{}, err
				}
				s.snapshot.Notices = append(s.snapshot.Notices, "An artist's recording page was unavailable; trying another artist.")
				continue
			}
			s.failures = 0
			var page struct {
				Recordings []mbRecording `json:"recordings"`
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				return core.TrackRef{}, err
			}
			s.snapshot.Sources = append(s.snapshot.Sources, s.client.base+path)
			for _, index := range s.rng.Perm(len(page.Recordings)) {
				r := page.Recordings[index]
				credited, blocked := false, false
				for _, credit := range r.ArtistCredit {
					credited = credited || credit.Artist.ID == artist.ID
					blocked = blocked || excludedArtist(credit.Name, s.intent.Constraints.ArtistsExclude)
				}
				if !credited || blocked || r.ID == "" {
					continue
				}
				// Use a temporary snapshot because an already enriched recording
				// can still be a new discovery-stream candidate.
				var matched core.KnowledgeSnapshot
				if exact != nil {
					key := core.ProvisionalRecordingKey(core.TrackRef{Artist: r.ArtistCredit[0].Name, Title: r.Title})
					id := exact[key]
					if id == "" {
						continue
					}
					s.client.addKnowledgeRecording(r, s.cat, s.resolver, &matched, id)
				} else {
					s.client.addKnowledgeRecording(r, s.cat, s.resolver, &matched)
				}
				for _, track := range matched.Candidates {
					// Merge recording-ID conflicts using the same provenance rules
					// as ordinary enrichment; never trust the first returned edition.
					s.client.addKnowledgeRecording(r, s.cat, s.resolver, &s.snapshot, track.ID)
					key := core.ProvisionalRecordingKey(track)
					if !s.seen[key] {
						tracks = append(tracks, track)
						s.seen[key] = true
					}
				}
			}
		} else {
			tracks = s.pending[0]
			s.pending = s.pending[1:]
		}
		if len(tracks) == 0 {
			continue
		}
		if len(tracks) > 1 {
			s.pending = append(s.pending, tracks[1:])
		}
		s.snapshot.Discovery = append(s.snapshot.Discovery, tracks[0])
		return tracks[0], nil
	}
	if s.lastError != nil {
		return core.TrackRef{}, s.lastError
	}
	return core.TrackRef{}, io.EOF
}
