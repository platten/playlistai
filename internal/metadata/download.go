package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const Publisher = "https://data.discogs.com/"

type DumpSet struct {
	Date  string
	Files map[string]Input
}

// Latest selects the latest complete listed set, never inventing file dates.
func Latest(ctx context.Context, year int) (DumpSet, error) {
	return latest(ctx, year, smallGet)
}

func latest(ctx context.Context, year int, get func(context.Context, string) ([]byte, error)) (DumpSet, error) {
	if year == 0 {
		raw, err := get(ctx, Publisher+"?prefix=data%2F")
		if err != nil {
			return DumpSet{}, err
		}
		decoded, err := url.QueryUnescape(string(raw))
		if err != nil {
			return DumpSet{}, err
		}
		matches := regexp.MustCompile(`data/([0-9]{4})/`).FindAllStringSubmatch(decoded, -1)
		seen := map[int]bool{}
		var years []int
		for _, match := range matches {
			y, _ := strconv.Atoi(match[1])
			if y >= 2000 && y <= time.Now().Year() && !seen[y] {
				seen[y] = true
				years = append(years, y)
			}
		}
		sort.Sort(sort.Reverse(sort.IntSlice(years)))
		for _, y := range years {
			body, err := get(ctx, Publisher+"?prefix="+url.QueryEscape(fmt.Sprintf("data/%d/", y)))
			if err != nil {
				return DumpSet{}, err
			} // Don't conceal a listing outage as an older "latest".
			if set, err := ParseListing(string(body), y); err == nil {
				return set, nil
			}
		}
		return DumpSet{}, fmt.Errorf("no complete Discogs dump set in published directories")
	}
	if year < 2000 || year > time.Now().Year() {
		return DumpSet{}, fmt.Errorf("invalid dump year")
	}
	raw, err := get(ctx, Publisher+"?prefix="+url.QueryEscape(fmt.Sprintf("data/%d/", year)))
	if err != nil {
		return DumpSet{}, err
	}
	return ParseListing(string(raw), year)
}
func ParseListing(raw string, year int) (DumpSet, error) {
	re := regexp.MustCompile(`discogs_([0-9]{8})_(artists\.xml\.gz|labels\.xml\.gz|masters\.xml\.gz|releases\.xml\.gz|CHECKSUM\.txt)`)
	dates := map[string]map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(raw, -1) {
		if !strings.HasPrefix(m[1], strconv.Itoa(year)) {
			continue
		}
		if _, err := time.Parse("20060102", m[1]); err != nil {
			continue
		}
		if dates[m[1]] == nil {
			dates[m[1]] = map[string]bool{}
		}
		dates[m[1]][m[2]] = true
	}
	var complete []string
	for date, files := range dates {
		if len(files) == 5 {
			complete = append(complete, date)
		}
	}
	sort.Strings(complete)
	if len(complete) == 0 {
		return DumpSet{}, fmt.Errorf("no complete Discogs dump set listed for %d", year)
	}
	return DumpSet{Date: complete[len(complete)-1]}, nil
}
func smallGet(ctx context.Context, source string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discogs: dump metadata HTTP %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if len(raw) > 2<<20 {
		return nil, fmt.Errorf("discogs: dump metadata exceeds size limit")
	}
	return raw, err
}

// Download uses a few bulk HTTPS transfers, separate from rate-limited API
// traffic. Existing verified files are reused; corrupt files are not accepted.
func Download(ctx context.Context, set DumpSet, dir string) (DumpSet, error) {
	return download(ctx, set, dir, Publisher)
}

func download(ctx context.Context, set DumpSet, dir, publisher string) (DumpSet, error) {
	if _, err := time.Parse("20060102", set.Date); err != nil {
		return set, fmt.Errorf("invalid snapshot date")
	}
	prefix := "data/" + set.Date[:4] + "/discogs_" + set.Date + "_"
	raw, err := smallGet(ctx, publisher+"?download="+url.QueryEscape(prefix+"CHECKSUM.txt"))
	if err != nil {
		return set, err
	}
	checks := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && len(fields[0]) == 64 {
			if _, err := hex.DecodeString(fields[0]); err == nil {
				checks[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
			}
		}
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return set, err
	}
	kinds := []string{"releases", "masters"}
	for _, kind := range kinds {
		if checks["discogs_"+set.Date+"_"+kind+".xml.gz"] == "" {
			return set, fmt.Errorf("publisher checksum missing for %s", kind)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	inputs := make([]Input, len(kinds))
	var wg sync.WaitGroup
	errors := make(chan error, len(kinds))
	for i, kind := range kinds {
		wg.Go(func() {
			if err := func() error {
				name := "discogs_" + set.Date + "_" + kind + ".xml.gz"
				sum := checks[name]
				dest := filepath.Join(dir, name)
				if ok := verifiedFile(ctx, dest, sum); !ok {
					if _, err := os.Stat(dest); err == nil {
						return fmt.Errorf("existing dump failed verification: %s", dest)
					}
					if err := downloadFile(ctx, publisher+"?download="+url.QueryEscape(prefix+kind+".xml.gz"), dest, sum); err != nil {
						return err
					}
				}
				inputs[i] = Input{Path: dest, SHA256: sum}
				return nil
			}(); err != nil {
				errors <- err
				cancel()
			}
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		return set, err
	}
	set.Files = map[string]Input{}
	for i, kind := range kinds {
		set.Files[kind] = inputs[i]
	}
	return set, nil
}
func verifiedFile(ctx context.Context, path, sum string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, contextReader{ctx, f})
	return err == nil && hex.EncodeToString(h.Sum(nil)) == sum
}
func downloadFile(ctx context.Context, source, dest, sum string) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("dump download HTTP %d", res.StatusCode)
	}
	f, err := os.CreateTemp(filepath.Dir(dest), ".discogs-download-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, (16<<30)+1))
	if err != nil {
		return err
	}
	if n > 16<<30 || hex.EncodeToString(h.Sum(nil)) != sum {
		return fmt.Errorf("download size/checksum invalid")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), dest)
}
