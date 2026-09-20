package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/musicconcepts"
	"github.com/platten/playlistai/internal/ports"
)

// Fetch full 100-record pages and expand only after buffered candidates run out.
// Artist rotation remains intact; continuation pages share the response cache.
const discoveryArtists = genreArtistTarget
const artistRecordingPages = 5
const discoveryRecordingPages = 100

type candidateStream struct {
	client               *Client
	cat                  ports.Catalog
	resolver             ports.ReferenceResolver
	intent               core.MusicIntent
	snapshot             core.KnowledgeSnapshot
	rng                  *rand.Rand
	genres               []string
	artists              []core.GenreArtist
	pending              [][]core.TrackRef
	seen                 map[string]bool
	initialized, replay  bool
	position             int
	failures             int
	lastError            error
	deferredArtists      []core.GenreArtist
	recordingOffsets     map[string]int
	recordingPages       map[string]int
	exactByArtist        map[string]map[string]string
	offlineRead          map[string]bool
	recordingReads       int
	windowReads          int
	dynamicRegistrations int
	dynamicBudgetNotice  bool
	acousticSpent        time.Duration
	replayError          error
}

// Keep small requests cheap; larger genre playlists can draw from up to twenty
// fresh artists/releases before reusing buffered tracks. Response caching and
// the existing overall page/detail budgets still bound network traffic.
const discoveryWindow = 4

func (s *candidateStream) discoveryWindowSize() int {
	return min(20, max(discoveryWindow, max(s.intent.Count, s.intent.Controls.TotalTrackCount)))
}

// Bound persistent metadata growth independently of actual preview analysis.
func (s *candidateStream) dynamicRegistrationLimit() int {
	return min(100, max(20, max(s.intent.Count, s.intent.Controls.TotalTrackCount)*8))
}

func (s *candidateStream) ReplayError() error { return s.replayError }

func (s *candidateStream) dynamicDiscoveryEnabled() bool {
	_, writable := s.cat.(ports.DynamicTrackCatalog)
	return writable && s.intent.Controls.RecommendationMode == core.EnhancedHybrid
}

func discoveryKey(intent core.MusicIntent, catalog string) string {
	return discoveryKeyVersion(intent, catalog, "discovery/v6")
}

func discoveryKeyVersion(intent core.MusicIntent, catalog, version string) string {
	intent = intent.Normalized()
	constraints := make([][2]string, 0, len(intent.HardConstraints))
	for _, c := range intent.HardConstraints {
		constraints = append(constraints, [2]string{c.Kind, c.Value})
	}
	key := knowledgeHash(struct {
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
		Start       *core.IntentReference
		Context     []core.ContextSeedPlan
		Policy      core.VerificationPolicy
	}{version + "+" + musicconcepts.Version + "+" + core.ContextProfileVersion, catalog, intent.Seed, intent.Controls, intent.OriginalDescription,
		intent.Preferences, intent.EssentialCriteria, constraints, intent.References, intent.RequiredTracks, intent.Mode,
		intent.Journey, intent.Temporal, intent.Destination, intent.Start, contextPlans(intent), intent.VerificationPolicy})
	if intent.Knowledge != nil && len(intent.Knowledge.PackProfiles) > 0 {
		key += "+pack-discovery/v1:" + knowledgeHash(intent.Knowledge.PackProfiles)
	}
	return key
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
	genres := contextualDiscoveryGenres(intent, contextPlans(intent))
	if len(genres) == 0 && core.WantsInstrumental(intent) {
		// MusicBrainz's instrumental tag is a discovery hint. Every proposed
		// recording still has to pass catalog identity and CLAP vocal screening.
		genres = []string{"instrumental"}
	}
	hasPackProfiles := intent.Controls.RecommendationMode == core.EnhancedHybrid && intent.Knowledge != nil && len(intent.Knowledge.PackProfiles) > 0
	if len(genres) == 0 && !hasPackProfiles || cat == nil || resolver == nil {
		return nil
	}
	seed, _ := intent.Seed.Int64() // normalized and validated by the orchestrator
	s := &candidateStream{client: c, cat: cat, resolver: resolver, intent: intent, genres: genres, rng: rand.New(rand.NewSource(seed)), seen: map[string]bool{}}
	if intent.Knowledge != nil {
		raw, _ := json.Marshal(intent.Knowledge)
		_ = json.Unmarshal(raw, &s.snapshot) // independent request-owned slices
	}
	key := discoveryKey(intent, resolver.CatalogVersion())
	requestKey := discoveryKey(intent, "")
	// Explicit v5 snapshots still replay their recorded identities offline;
	// only newly generated discovery adopts metadata-only registration.
	if s.snapshot.DiscoveryRecorded && s.snapshot.DiscoveryKey != "" &&
		(s.snapshot.DiscoveryKey == discoveryKeyVersion(intent, resolver.CatalogVersion(), "discovery/v5") ||
			s.snapshot.DiscoveryRequestKey == discoveryKeyVersion(intent, "", "discovery/v5")) {
		key = discoveryKeyVersion(intent, resolver.CatalogVersion(), "discovery/v5")
		requestKey = discoveryKeyVersion(intent, "", "discovery/v5")
	}
	// A saved generation cannot silently become a fresh online search when a
	// shared asset is replaced. Explicit edits clear DiscoveryRecorded upstream.
	if s.snapshot.DiscoveryRecorded && (len(s.snapshot.PackProfiles) > 0 || s.snapshot.DiscoveryCatalog != "") && s.snapshot.DiscoveryKey != "" && s.snapshot.DiscoveryKey != key {
		if s.snapshot.DiscoveryRequestKey == "" || s.snapshot.DiscoveryRequestKey == requestKey {
			s.replayError = fmt.Errorf("%w; start a new generation", ports.ErrDiscoveryGenerationMismatch)
		}
	}
	// Unkeyed legacy snapshots retain their original offline replay behavior.
	s.replay = s.snapshot.DiscoveryRecorded && (s.snapshot.DiscoveryKey == "" || s.snapshot.DiscoveryKey == key)
	if !s.replay {
		s.snapshot.Discovery = nil
		s.snapshot.DiscoveryEvidence = nil
	}
	s.snapshot.DiscoveryKey = key
	s.snapshot.DiscoveryCatalog = resolver.CatalogVersion()
	s.snapshot.DiscoveryRequestKey = requestKey
	s.snapshot.DiscoveryRecorded = true
	return s
}

func (s *candidateStream) Snapshot() *core.KnowledgeSnapshot {
	s.snapshot.ID = ""
	s.snapshot.ID = knowledgeHash(s.snapshot)
	return &s.snapshot
}

func (s *candidateStream) Next(ctx context.Context) (core.TrackRef, error) {
	return s.nextMusicBrainz(ctx)
}

func (s *candidateStream) nextMusicBrainz(ctx context.Context) (core.TrackRef, error) {
	if err := ctx.Err(); err != nil {
		return core.TrackRef{}, err
	}
	if s.replayError != nil {
		return core.TrackRef{}, s.replayError
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
		if s.intent.Controls.RecommendationMode == core.EnhancedHybrid && len(s.snapshot.PackProfiles) > 0 {
			pools = append(pools, s.packDiscoveryArtists(ctx))
		}
		unavailableGenre := ""
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
				if err := ctx.Err(); err != nil {
					return core.TrackRef{}, err
				}
				unavailableGenre = genre
				continue
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
		if unavailableGenre != "" {
			if len(s.artists) == 0 {
				return core.TrackRef{}, fmt.Errorf("artist lookup unavailable for %q", unavailableGenre)
			}
			s.snapshot.Notices = append(s.snapshot.Notices, "Some genre spelling lookups were unavailable; using retrieved candidates with the same musical checks.")
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
		if len(s.artists) > 0 && s.windowReads < s.discoveryWindowSize() {
			artist := s.artists[0]
			s.artists = s.artists[1:]
			// Enhanced discovery can register provider-identified recordings even
			// when preview audio is unavailable; fit is assessed independently.
			resolution := s.resolver.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: artist.Name})
			if resolution.Status == core.ResolutionUnresolved && !s.dynamicDiscoveryEnabled() {
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
				exact = indexKnownArtistRecordings(s.cat, entries)
			}
			s.exactByArtist[artist.ID] = exact
			if offline := s.client.localMusicBrainz(); offline != nil && !s.offlineRead[artist.ID] {
				if s.offlineRead == nil {
					s.offlineRead = make(map[string]bool)
				}
				s.offlineRead[artist.ID] = true
				rows, offlineErr := offline.ArtistRecordings(ctx, artist.ID, artistRecordingPages*100, 0)
				if offlineErr != nil {
					return core.TrackRef{}, offlineErr
				}
				if len(rows) > 0 {
					s.snapshot.Sources = append(s.snapshot.Sources, "musicbrainz-dump:"+offline.Info().Snapshot)
					for _, row := range rows {
						r := offlineRecording(row)
						credited, blocked := false, false
						for _, credit := range r.ArtistCredit {
							credited = credited || credit.Artist.ID == artist.ID
							blocked = blocked || excludedArtist(credit.Name, s.intent.Constraints.ArtistsExclude)
						}
						if !credited || blocked {
							continue
						}
						var matched core.KnowledgeSnapshot
						if exact != nil {
							id := matchKnownRecording(s.cat, exact, r)
							if id != "" {
								s.client.addKnowledgeRecording(r, s.cat, s.resolver, &matched, id)
							}
						} else {
							s.client.addKnowledgeRecording(r, s.cat, s.resolver, &matched)
						}
						if len(matched.Candidates) == 0 && s.dynamicDiscoveryEnabled() {
							if s.dynamicRegistrations < s.dynamicRegistrationLimit() {
								s.dynamicRegistrations++
								if err := s.client.addDynamicKnowledgeRecording(ctx, r, s.cat, &matched); err != nil {
									return core.TrackRef{}, err
								}
							} else if !s.dynamicBudgetNotice {
								s.dynamicBudgetNotice = true
								s.snapshot.Notices = append(s.snapshot.Notices, fmt.Sprintf("Metadata registration reached its %d-recording limit; additional MusicBrainz candidates were not registered.", s.dynamicRegistrationLimit()))
							}
						}
						for _, track := range matched.Candidates {
							s.client.addKnowledgeRecording(r, s.cat, s.resolver, &s.snapshot, track.ID)
							key := core.ProvisionalRecordingKey(track)
							if !s.seen[key] {
								tracks = append(tracks, track)
								s.recordEvidence(track.ID, "musicbrainz_dump", "musicbrainz-dump:"+offline.Info().Snapshot)
								s.seen[key] = true
							}
						}
					}
					s.enrichAcoustic(ctx)
					if len(tracks) == 0 {
						continue
					}
					if len(tracks) > 1 {
						s.pending = append(s.pending, tracks[1:])
					}
					s.snapshot.Discovery = append(s.snapshot.Discovery, tracks[0])
					return tracks[0], nil
				}
			}
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
					id := matchKnownRecording(s.cat, exact, r)
					if id != "" {
						s.client.addKnowledgeRecording(r, s.cat, s.resolver, &matched, id)
					}
				} else {
					s.client.addKnowledgeRecording(r, s.cat, s.resolver, &matched)
				}
				if len(matched.Candidates) == 0 && s.dynamicDiscoveryEnabled() && s.dynamicRegistrations < s.dynamicRegistrationLimit() {
					s.dynamicRegistrations++
					if err := s.client.addDynamicKnowledgeRecording(ctx, r, s.cat, &matched); err != nil {
						return core.TrackRef{}, err
					}
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
			s.enrichAcoustic(ctx)
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

// Optional metadata has one time budget across the whole stream, not another
// eight-second allowance for every artist page. Replay never enters this path.
func (s *candidateStream) enrichAcoustic(ctx context.Context) {
	remaining := 8*time.Second - s.acousticSpent
	if remaining <= 0 || s.client.acoustic == nil {
		return
	}
	started := time.Now()
	defer func() { s.acousticSpent += time.Since(started) }()
	ctx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	s.client.acousticTracks(ctx, s.snapshot.Tracks, 25)
}
