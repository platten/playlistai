package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestReleaseNotesReachOfferWithAndWithoutAutomaticPackage(t *testing.T) {
	const notes = "## What's new\n\n- Preserve Christian Löffler references.\n- Reuse existing setup.\n\n[Full changelog](https://github.com/platten/playlistai)"
	i := installation{Kind: "linux", Arch: "amd64"}
	for _, automatic := range []bool{false, true} {
		r := release{Tag: "v0.9.0", Body: notes}
		if automatic {
			r.Assets = []asset{{Name: i.assetName(), URL: Repository + "/releases/download/v0.9.0/" + i.assetName(), Size: 20, Digest: "sha256:" + strings.Repeat("a", 64)}}
		}
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Transport: fixtureTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
		})}
		offer, _, err := check(context.Background(), "0.8.0", i, client, latestURL)
		if err != nil || !offer.Available || offer.CanInstall != automatic || offer.Notes != notes || offer.URL != Repository+"/releases/tag/v0.9.0" {
			t.Fatalf("release notes/package availability lost: %+v, %v", offer, err)
		}
	}
}
