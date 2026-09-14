package musicbrainz

import (
	"context"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/fakes"
)

func TestDiscoveryInputKeySeparatesReplayFromNewSampling(t *testing.T) {
	cat := fakes.NewCatalog(2, fakes.CatalogTrack{ID: "one", Display: "Artist - One"})
	c := newClient(t, "http://localhost", time.Nanosecond)
	intent := core.MusicIntent{Version: core.CurrentIntentVersion, Seed: "42", Preferences: core.SemanticPreferences{Genres: []core.IntentPreference{{Value: "ambient"}}}}
	s := c.OpenCandidates(intent, cat, cat).(*candidateStream)
	s.snapshot.Discovery = []core.TrackRef{{ID: "one"}}
	s.recordEvidence("one", "musicbrainz_recording", "https://musicbrainz.org/recording/7")
	intent.Knowledge = s.Snapshot()
	if !c.OpenCandidates(intent, cat, cat).(*candidateStream).replay {
		t.Fatal("same inputs lost offline replay")
	}
	for _, change := range []func(*core.MusicIntent){
		func(m *core.MusicIntent) { m.Seed = "43" },
		func(m *core.MusicIntent) { m.Controls.Discovery = .9 },
		func(m *core.MusicIntent) { m.Controls.ArtistDiversity = .8 },
		func(m *core.MusicIntent) { m.VerificationPolicy = core.VerifiedOnly },
		func(m *core.MusicIntent) { m.Preferences.Genres = []core.IntentPreference{{Value: "rock"}} },
	} {
		m := intent
		change(&m)
		changed := c.OpenCandidates(m, cat, cat).(*candidateStream)
		if changed.replay || len(changed.snapshot.Discovery) != 0 {
			t.Fatal("changed inputs reused sampling")
		}
	}
	replay := c.OpenCandidates(intent, cat, cat).(*candidateStream)
	if _, err := replay.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if evidence := replay.Evidence("one"); len(evidence) != 1 || evidence[0].Channel != "musicbrainz_recording" {
		t.Fatalf("lost provider provenance: %+v", evidence)
	}
}
