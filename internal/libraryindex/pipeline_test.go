package libraryindex

import (
	"testing"

	"github.com/platten/playlistai/internal/localaudio"
)

func TestFingerprintGenerationOnlyWhenIdentityTagsAreMissing(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata localaudio.Metadata
		generate bool
		status   string
	}{
		{name: "embedded fingerprint", metadata: localaudio.Metadata{AcoustIDFingerprint: &localaudio.TagValue{Value: "AQADtNQYhYkYnGhw7Xtagged"}}, status: "available"},
		{name: "embedded AcoustID", metadata: localaudio.Metadata{AcoustID: &localaudio.TagValue{Value: "11111111-2222-3333-4444-555555555555"}}, status: "not_generated_embedded_acoustid"},
		{name: "missing", generate: true, status: "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			record, generate, err := fingerprintFromEmbeddedTags(test.metadata)
			if err != nil || generate != test.generate || record.Status != test.status {
				t.Fatalf("record=%+v generate=%v err=%v", record, generate, err)
			}
		})
	}
}
