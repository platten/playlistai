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

// Fetch full 100-record pages and expand only after buffered candidates run out.
// Artist rotation remains intact; continuation pages share the response cache.
const discoveryArtists = genreArtistTarget
const artistRecordingPages = 5
const discoveryRecordingPages = 100

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
	deferredArtists     []core.GenreArtist
	recordingOffsets    map[string]int
	recordingPages      map[string]int
	exactByArtist       map[string]map[string]string
	recordingReads      int
	windowReads         int
}

// Rotate across at most four fresh artists/releases, then consume the buffered
// tracks round-robin before opening another network window.
const discoveryWindow = 4

func discoveryKey(intent core.MusicIntent, catalog string) string {
	intent = intent.Normalized()
	constraints := make([][2]string, 0, len(intent.HardConstraints))
	for _, c := range intent.HardConstraints {
		constraints = append(constraints, [2]string{c.Kind, c.Value})
	}
	return knowledgeHash(struct {
		Version     string
		Catalog     string
		Seed        core.RNGSeed
		Controls    core.IntentControls
		Description string
		Preferences core.SemanticPreferences
		Criteria    []core.MusicalCriterion
		Constraints [][2]string
		References  []core.IntentReference
		Required    []core.IntentReference
		Mode        core.Mode
		Journey     core.JourneyPlan
		Temporal    []core.TemporalRequirement
		Destination *core.IntentReference
		Policy      core.VerificationPolicy
	}{"discovery/v2", catalog, intent.Seed, intent.Controls, intent.OriginalDescription,
		intent.Preferences, intent.EssentialCriteria, constraints, intent.References, intent.RequiredTracks, intent.Mode,
		intent.Journey, intent.Temporal, intent.Destination, intent.VerificationPolicy})
}

func (s *candidateStream) Evidence(trackID string) []core.RetrievalEvidence {
	if evidence := s.snapshot.DiscoveryEvidence[trackID]; len(evidence) > 0 {
		return append([]core.RetrievalEvidence(nil), evidence...)
	}
	return []core.RetrievalEvidence{{Channel: "metadata_discovery", QueryWeight: 1}}
}

func (s *candidateStream) recordEvidence(trackID, channel, source string) {
	if s.snapshot.DiscoveryEvidence == nil {
		s.snapshot.DiscoveryEvidence = make(map[string][]core.RetrievalEvidence)
	}
	s.snapshot.DiscoveryEvidence[trackID] = []core.RetrievalEvidence{{Channel: channel, QueryID: source, QueryWeight: 1}}
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
	key := discoveryKey(intent, resolver.CatalogVersion())
	// Unkeyed legacy snapshots retain their original offline replay behavior.
	s.replay = s.snapshot.DiscoveryRecorded && (s.snapshot.DiscoveryKey == "" || s.snapshot.DiscoveryKey == key)
	if !s.replay {
		s.snapshot.Discovery = nil
		s.snapshot.DiscoveryEvidence = nil
	}
	s.snapshot.DiscoveryKey = key
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
	for len(s.artists) > 0 || len(s.pending) > 0 || len(s.deferredArtists) > 0 {
		if err := ctx.Err(); err != nil {
			return core.TrackRef{}, err
		}
		var tracks []core.TrackRef
		if s.recordingReads >= discoveryRecordingPages && (len(s.artists) > 0 || len(s.deferredArtists) > 0) {
			s.artists, s.deferredArtists = nil, nil
			s.snapshot.Notices = append(s.snapshot.Notices, "MusicBrainz discovery reached its 100-recording-page limit; remaining provider pages were not fetched.")
		}
		if len(s.artists) == 0 && len(s.pending) == 0 && len(s.deferredArtists) == 0 {
			break
		}
		if len(s.artists) == 0 && len(s.pending) == 0 {
			s.artists, s.deferredArtists = s.deferredArtists, nil
		}
		if len(s.pending) == 0 {
			s.windowReads = 0
		}
		if len(s.artists) > 0 && s.windowReads < discoveryWindow {
			artist := s.artists[0]
			s.artists = s.artists[1:]
			// Avoid online recording pages for artists absent from the local
			// catalog. Ambiguous names may still resolve at recording granularity.
			resolution := s.resolver.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: artist.Name})
			if resolution.Status == core.ResolutionUnresolved {
				continue
			}
			if s.recordingOffsets == nil {
				s.recordingOffsets = make(map[string]int)
				s.recordingPages = make(map[string]int)
				s.exactByArtist = make(map[string]map[string]string)
			}
			exact, cached := s.exactByArtist[artist.ID]
			if catalog, ok := s.cat.(ports.ArtistRecordingCatalog); ok && resolution.Selected != nil && !cached {
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
			s.exactByArtist[artist.ID] = exact
			values := url.Values{"query": {"arid:" + artist.ID}, "fmt": {"json"}, "limit": {"100"}}
			if offset := s.recordingOffsets[artist.ID]; offset > 0 {
				values.Set("offset", fmt.Sprint(offset))
			}
			path := "/ws/2/recording?" + values.Encode()
			s.recordingReads++
			s.windowReads++
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
				Count      int           `json:"count"`
				Recordings []mbRecording `json:"recordings"`
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				return core.TrackRef{}, err
			}
			s.snapshot.Sources = append(s.snapshot.Sources, s.client.base+path)
			s.recordingPages[artist.ID]++
			s.recordingOffsets[artist.ID] += len(page.Recordings)
			if len(page.Recordings) > 0 && s.recordingOffsets[artist.ID] < page.Count {
				if s.recordingPages[artist.ID] < artistRecordingPages {
					s.deferredArtists = append(s.deferredArtists, artist)
				} else {
					s.snapshot.Notices = append(s.snapshot.Notices, fmt.Sprintf("MusicBrainz recording lookup for %q reached its five-page limit; additional recordings were not fetched.", artist.Name))
				}
			}
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
						s.recordEvidence(track.ID, "musicbrainz_artist_sample", s.client.base+path)
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
