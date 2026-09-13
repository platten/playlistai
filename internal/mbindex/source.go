package mbindex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/platten/playlistai/internal/dataset"
)

const DefaultDumpBase = "https://ftp.musicbrainz.org/pub/musicbrainz/data/json-dumps/"

type DumpFile struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Path   string `json:"path,omitempty"`
}

type DumpSet struct {
	Snapshot string              `json:"snapshot"`
	Files    map[string]DumpFile `json:"files"`
}

func Latest(ctx context.Context, base string) (DumpSet, error) {
	baseURL, err := validateHTTPSBase(base)
	if err != nil {
		return DumpSet{}, err
	}
	client := sourceClient()
	snapshotRaw, err := getSmall(ctx, client, baseURL.ResolveReference(&url.URL{Path: "LATEST"}).String(), 128)
	if err != nil {
		return DumpSet{}, err
	}
	snapshot := strings.TrimSpace(string(snapshotRaw))
	if _, err := time.Parse("20060102-150405", snapshot); err != nil {
		return DumpSet{}, errors.New("MusicBrainz LATEST contained an invalid snapshot")
	}
	dir := baseURL.ResolveReference(&url.URL{Path: snapshot + "/"})
	sumsRaw, err := getSmall(ctx, client, dir.ResolveReference(&url.URL{Path: "SHA256SUMS"}).String(), 1<<20)
	if err != nil {
		return DumpSet{}, err
	}
	sums := parseSHA256Sums(string(sumsRaw))
	set := DumpSet{Snapshot: snapshot, Files: map[string]DumpFile{}}
	for _, name := range []string{"artist.tar.xz", "recording.tar.xz"} {
		sum := sums[name]
		if len(sum) != 64 {
			return DumpSet{}, fmt.Errorf("publisher checksum missing for %s", name)
		}
		remote := dir.ResolveReference(&url.URL{Path: name}).String()
		size, err := remoteSize(ctx, client, remote)
		if err != nil {
			return DumpSet{}, err
		}
		set.Files[name] = DumpFile{Name: name, URL: remote, Size: size, SHA256: sum}
	}
	return set, nil
}

func Download(ctx context.Context, set DumpSet, dir string, progress func(name string, done, total int64)) (DumpSet, error) {
	if _, err := time.Parse("20060102-150405", set.Snapshot); err != nil {
		return DumpSet{}, errors.New("invalid MusicBrainz dump set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return DumpSet{}, err
	}
	names := []string{"artist.tar.xz", "recording.tar.xz"}
	for _, name := range names {
		file, ok := set.Files[name]
		if !ok || file.Size <= 0 || len(file.SHA256) != 64 {
			return DumpSet{}, fmt.Errorf("invalid MusicBrainz dump file %s", name)
		}
	}
	downloadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	client := sourceClient()
	type result struct {
		name string
		file DumpFile
		err  error
	}
	results := make(chan result, len(names))
	for _, name := range names {
		name, file := name, set.Files[name]
		go func() {
			target := filepath.Join(dir, set.Snapshot+"-"+name)
			if err := dataset.VerifyFile(downloadCtx, target, file.Size, file.SHA256); err == nil {
				if progress != nil {
					progress(name, file.Size, file.Size)
				}
				file.Path = target
				results <- result{name: name, file: file}
				return
			}
			_, err := dataset.DownloadWithClient(downloadCtx, file.URL, target, file.Size, file.SHA256, func(done, total int64) {
				if progress != nil {
					progress(name, done, total)
				}
			}, client)
			file.Path = target
			results <- result{name, file, err}
		}()
	}
	var firstErr error
	for range names {
		result := <-results
		if result.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("download %s: %w", result.name, result.err)
			cancel()
		}
		if result.err == nil {
			set.Files[result.name] = result.file
		}
	}
	if firstErr != nil {
		return DumpSet{}, firstErr
	}
	return set, nil
}

func validateHTTPSBase(value string) (*url.URL, error) {
	if strings.TrimSpace(value) == "" {
		value = DefaultDumpBase
	}
	u, err := url.Parse(value)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("MusicBrainz dump source must be an HTTPS directory")
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u, nil
}

func sourceClient() *http.Client {
	return &http.Client{Timeout: 0, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" {
			return errors.New("MusicBrainz dump redirect rejected")
		}
		return nil
	}}
}

func getSmall(ctx context.Context, client *http.Client, remote string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remote, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, remote)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("MusicBrainz metadata response too large")
	}
	return raw, nil
}

func remoteSize(ctx context.Context, client *http.Client, remote string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, remote, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("HTTP %d checking %s", resp.StatusCode, remote)
	}
	value := resp.Header.Get("Content-Length")
	size, err := strconv.ParseInt(value, 10, 64)
	if err != nil || size <= 0 || size > 64<<30 {
		return 0, errors.New("MusicBrainz dump size is missing or unsafe")
	}
	return size, nil
}

func parseSHA256Sums(raw string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			out[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
		}
	}
	return out
}
