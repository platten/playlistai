package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"sync"

	"github.com/platten/playlistai/internal/core"
)

const discoveryPrefetchPages = 4

type prefetchedPage struct {
	done chan struct{}
	raw  []byte
	err  error
}

type discoveryPrefetch struct {
	ctx     context.Context
	cancel  context.CancelFunc
	ready   chan struct{}
	plan    *candidateStream
	err     error
	mu      sync.Mutex
	stopped bool
	pages   map[string]*prefetchedPage
	tail    <-chan struct{}
	issued  int
	wg      sync.WaitGroup
}

func recordingPagePath(artist string, offset int) string {
	values := url.Values{"query": {"arid:" + artist}, "fmt": {"json"}, "limit": {"100"}}
	if offset > 0 {
		values.Set("offset", fmt.Sprint(offset))
	}
	return "/ws/2/recording?" + values.Encode()
}

func (s *candidateStream) StartPrefetch(ctx context.Context) {
	if s.prefetch != nil || s.initialized || s.replay || s.replayError != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.WithValue(ctx, speculativeRequestKey{}, true))
	p := &discoveryPrefetch{ctx: ctx, cancel: cancel, ready: make(chan struct{}), pages: make(map[string]*prefetchedPage)}
	// Planning owns a private snapshot and RNG. Only Next publishes the plan;
	// an unused speculative lookup cannot alter the saved discovery snapshot.
	plan := *s
	raw, _ := json.Marshal(s.snapshot)
	plan.snapshot = core.KnowledgeSnapshot{}
	_ = json.Unmarshal(raw, &plan.snapshot)
	seed, _ := s.intent.Seed.Int64()
	plan.rng = rand.New(rand.NewSource(seed))
	plan.prefetch = p
	s.prefetch = p
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.err = plan.initializeDiscovery(ctx)
		p.plan = &plan
		if p.err == nil {
			plan.schedulePrefetch()
		}
		close(p.ready)
	}()
}

func (s *candidateStream) StopPrefetch() {
	p := s.prefetch
	if p == nil {
		return
	}
	p.mu.Lock()
	p.stopped = true
	p.cancel()
	p.mu.Unlock()
	p.wg.Wait()
}

func (s *candidateStream) prepareDiscovery(ctx context.Context) error {
	if s.initialized {
		return nil
	}
	if p := s.prefetch; p != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.ready:
		}
		if p.err != nil {
			return p.err
		}
		s.artists, s.snapshot, s.rng = p.plan.artists, p.plan.snapshot, p.plan.rng
		s.initialized = true
		return nil
	}
	return s.initializeDiscovery(ctx)
}

// Only the coordinator inspects artist rotation and offsets. Workers receive
// immutable paths and publish raw responses, never identities or evidence.
func (s *candidateStream) schedulePrefetch() {
	p := s.prefetch
	if p == nil || s.client.localMusicBrainz() != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || p.ctx.Err() != nil {
		return
	}
	artists := s.artists
	if len(artists) == 0 {
		artists = s.deferredArtists
	}
	for _, artist := range artists {
		if len(p.pages) >= discoveryPrefetchPages || p.issued >= discoveryRecordingPages {
			break
		}
		if !s.dynamicDiscoveryEnabled() {
			resolution := s.resolver.ResolveReference(core.IntentReference{Kind: core.ReferenceArtist, Query: artist.Name})
			if resolution.Status == core.ResolutionUnresolved {
				continue
			}
		}
		path := recordingPagePath(artist.ID, s.recordingOffsets[artist.ID])
		if _, exists := p.pages[path]; exists {
			continue
		}
		page := &prefetchedPage{done: make(chan struct{})}
		p.pages[path] = page
		p.issued++
		previous := p.tail
		p.tail = page.done
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer close(page.done)
			if previous != nil {
				select {
				case <-previous:
				case <-p.ctx.Done():
					page.err = p.ctx.Err()
					return
				}
			}
			page.raw, page.err = s.client.knowledgeGet(context.WithValue(p.ctx, speculativeRequestKey{}, true), path, false)
		}()
	}
}

func (s *candidateStream) recordingPage(ctx context.Context, path string) ([]byte, error) {
	p := s.prefetch
	if p == nil {
		return s.client.knowledgeGet(ctx, path, false)
	}
	p.mu.Lock()
	page := p.pages[path]
	p.mu.Unlock()
	if page == nil {
		p.mu.Lock()
		if p.issued >= discoveryRecordingPages {
			p.mu.Unlock()
			return nil, fmt.Errorf("metadata recording page budget exhausted")
		}
		p.issued++
		p.mu.Unlock()
		return s.client.knowledgeGet(ctx, path, false)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-page.done:
	}
	p.mu.Lock()
	delete(p.pages, path)
	p.mu.Unlock()
	s.schedulePrefetch()
	return page.raw, page.err
}
