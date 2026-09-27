package musicbrainz

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

type recordingExtractorFunc func(context.Context, ports.RecordingSource) ([]core.RecordingClaim, error)

func (f recordingExtractorFunc) ExtractRecordingClaims(ctx context.Context, in ports.RecordingSource) ([]core.RecordingClaim, error) {
	return f(ctx, in)
}

func TestOfficialRecordingClaimsRequireSourcePassageAndIdentity(t *testing.T) {
	const passage = "Song is an instrumental piano recording by Artist."
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, `<html><script>invented lyrics</script><nav>related tracks</nav><article>`+passage+`</article></html>`)
	}))
	defer server.Close()
	client := newClient(t, server.URL, time.Nanosecond)
	client.WithRecordingSourceExtractor(recordingExtractorFunc(func(ctx context.Context, in ports.RecordingSource) ([]core.RecordingClaim, error) {
		if in.Text != passage {
			t.Errorf("unsafe source text=%q", in.Text)
		}
		// Even an extractor that asks for stronger authority remains a hint.
		good := core.RecordingClaim{Kind: "vocal", Value: "instrumental", State: core.EvidenceMatch, Scope: "recording", EntityID: acousticTestID, RecordingID: acousticTestID, Locator: passage, Method: "linked_statement"}
		invented, wrongTrack, artistScope := good, good, good
		invented.Locator = "This claim is absent."
		wrongTrack.RecordingID = contextAlbumID
		artistScope.Scope = "artist"
		return []core.RecordingClaim{good, invented, wrongTrack, artistScope}, nil
	}))
	relation := recordingRelation{Type: "official homepage"}
	relation.URL.Resource = server.URL + "/song"
	track := verificationTrack()
	client.officialRecordingClaims(context.Background(), &track, []recordingRelation{relation, relation}, nil)
	if len(track.Claims) != 1 || track.Claims[0].Method != "quoted_statement" || track.Claims[0].Source.URL != relation.URL.Resource || requests.Load() != 1 {
		t.Fatalf("claims=%+v requests=%d", track.Claims, requests.Load())
	}
	client.officialRecordingClaims(context.Background(), &track, []recordingRelation{relation}, nil)
	if requests.Load() != 1 {
		t.Fatal("official response cache missed")
	}
}

func TestOfficialRecordingRejectsUnsafeURLsAndRedirects(t *testing.T) {
	client := &Client{base: "https://musicbrainz.org"}
	for _, raw := range []string{"http://label.example/song", "https://127.0.0.1/song", "https://169.254.169.254/metadata", "https://100.100.100.200/metadata", "https://[::1]/song", "https://label.example:444/song", "https://user@label.example/song", "https://label.example/song#part", "file:///tmp/song"} {
		u, _ := url.Parse(raw)
		if client.allowedOfficialURL(u) {
			t.Errorf("allowed unsafe URL %s", raw)
		}
	}
	for _, raw := range []string{"10.1.2.3", "172.16.0.1", "192.168.1.1", "100.64.0.1", "::ffff:127.0.0.1", "fc00::1"} {
		if publicSourceIP(net.ParseIP(raw)) {
			t.Errorf("allowed private address %s", raw)
		}
	}
	var escaped atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { escaped.Add(1) }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }))
	defer server.Close()
	client = newClient(t, server.URL, time.Nanosecond)
	u, _ := url.Parse(server.URL)
	httpClient, closeIdle := client.officialRecordingClient(u)
	defer closeIdle()
	if _, err := httpClient.Get(server.URL); err == nil || escaped.Load() != 0 {
		t.Fatalf("redirect escaped: err=%v calls=%d", err, escaped.Load())
	}
}

func TestOfficialRecordingFetchChargesBudgetAndRespectsCancellation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, "<html>Artist Song</html>")
	}))
	defer server.Close()
	client := newClient(t, server.URL, time.Nanosecond)
	u, _ := url.Parse(server.URL)
	httpClient, closeIdle := client.officialRecordingClient(u)
	defer closeIdle()
	budget := &knowledgeBudget{}
	ctx := context.WithValue(context.Background(), knowledgeBudgetKey{}, budget)
	ctx = context.WithValue(ctx, contextRequestLimitKey{}, 1)
	if _, err := client.metadataGet(ctx, server.URL, "/first", "official-recording-v1:", httpClient, false); err != nil {
		t.Fatal(err)
	}
	if _, err := client.metadataGet(ctx, server.URL, "/second", "official-recording-v1:", httpClient, false); err == nil || requests.Load() != 1 || budget.requests != 1 {
		t.Fatalf("budget escaped: err=%v requests=%d budget=%d", err, requests.Load(), budget.requests)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.metadataGet(canceled, server.URL, "/third", "official-recording-v1:", httpClient, false); err != context.Canceled || requests.Load() != 1 {
		t.Fatal("canceled source read made request", err)
	}
}

func TestOfficialRecordingTextIsBounded(t *testing.T) {
	text := officialRecordingText([]byte("<article>" + strings.Repeat("word ", 10000) + "</article><script>secret</script>"))
	if len([]rune(text)) > 16000 || strings.Contains(text, "secret") {
		t.Fatalf("unsafe text length=%d", len(text))
	}
}

func TestRecordingStreamingPageRequiresCreditedArtistsOfficialHost(t *testing.T) {
	for _, linked := range []bool{true, false} {
		t.Run(fmt.Sprint(linked), func(t *testing.T) {
			var pageRequests, extractions atomic.Int32
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/ws/2/artist/"+contextArtistID {
					homepage := "https://unrelated.example/"
					if linked {
						homepage = server.URL + "/artist"
					}
					_, _ = fmt.Fprintf(w, `{"id":%q,"name":"Artist","relations":[{"type":"official homepage","url":{"resource":%q}}]}`, contextArtistID, homepage)
					return
				}
				pageRequests.Add(1)
				_, _ = fmt.Fprint(w, "<article>Artist Song is instrumental.</article>")
			}))
			defer server.Close()
			client := newClient(t, server.URL, time.Nanosecond)
			client.WithRecordingSourceExtractor(recordingExtractorFunc(func(ctx context.Context, source ports.RecordingSource) ([]core.RecordingClaim, error) {
				extractions.Add(1)
				return nil, nil
			}))
			relation := recordingRelation{Type: "free streaming"}
			relation.URL.Resource = server.URL + "/song"
			credit := mbArtistCredit{Name: "Artist"}
			credit.Artist.ID = contextArtistID
			track := verificationTrack()
			client.officialRecordingClaims(context.Background(), &track, []recordingRelation{relation}, []mbArtistCredit{credit})
			want := int32(0)
			if linked {
				want = 1
			}
			if pageRequests.Load() != want || extractions.Load() != want {
				t.Fatalf("recording page accepted without official artist identity: requests=%d extractions=%d", pageRequests.Load(), extractions.Load())
			}
		})
	}
}
