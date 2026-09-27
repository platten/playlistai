package core

import (
	"encoding/json"
	"testing"
)

func TestPreviewIdentityPolicyLeavesLegacyJSONUnchanged(t *testing.T) {
	const saved = `{"status":"resolved","provider":"deezer","providerId":"123","recordingId":"","isrc":"","artist":"Artist","title":"Song","method":"corroborated_artist_title_version","alternatives":null}`
	var identity PreviewIdentity
	if err := json.Unmarshal([]byte(saved), &identity); err != nil {
		t.Fatal(err)
	}
	if identity.CurrentPolicy() {
		t.Fatal("legacy identity promoted to current proof")
	}
	raw, err := json.Marshal(identity)
	if err != nil || string(raw) != saved {
		t.Fatalf("historical JSON changed: %s err=%v", raw, err)
	}
	identity.PolicyVersion = PreviewIdentityPolicyVersion
	if !identity.CurrentPolicy() {
		t.Fatal("missing optional provider duration should remain unknown")
	}
	for _, duration := range []int64{-1, 86400001} {
		identity.FullRecordingMilliseconds = duration
		if identity.CurrentPolicy() {
			t.Fatal("invalid full duration accepted")
		}
	}
}
