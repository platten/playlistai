package libraryindex

import (
	"math"
	"strings"
	"testing"

	"github.com/platten/playlistai/internal/core"
)

func TestAudioSemanticKeyIncludesSamplingContract(t *testing.T) {
	if !strings.Contains(AudioSemanticKey("runtime", core.AudioRepresentationIdentity{}, ProfileBalanced), ";"+SamplingVersion+";") {
		t.Fatal("sampling changes would reuse stale audio")
	}
}

func TestMERTAccumulationRejectsWholeMalformedWindow(t *testing.T) {
	sums := []float64{2, 3}
	if err := accumulateMERT(sums, []float32{1, float32(math.NaN())}, 2); err == nil {
		t.Fatal("accepted nonfinite vector")
	}
	if sums[0] != 2 || sums[1] != 3 {
		t.Fatalf("failed window changed sums: %v", sums)
	}
	if err := accumulateMERT(sums, []float32{1, 2}, 2); err != nil {
		t.Fatal(err)
	}
	if sums[0] != 4 || sums[1] != 7 {
		t.Fatalf("wrong weighted sums: %v", sums)
	}
}

func TestRecordingTagAliasesDoNotUseReleaseTrackIdentity(t *testing.T) {
	id := "01234567-89ab-cdef-0123-456789abcdef"
	for _, key := range []string{"MUSICBRAINZ_TRACKID", "MusicBrainz Track Id", "musicbrainz_recording_id"} {
		if got := musicBrainzRecordingID(map[string]string{key: id}); got != id {
			t.Fatalf("%s: %q", key, got)
		}
	}
	if got := musicBrainzRecordingID(map[string]string{"musicbrainz_releasetrackid": id, "musicbrainz_trackid": "invalid"}); got != "" {
		t.Fatalf("invalid identity accepted: %q", got)
	}
}
