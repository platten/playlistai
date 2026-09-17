package librarylearn

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"strings"
)

const SamplingVersion = "keyed-artist-round-robin/v1"

type SampleItem struct {
	ID       string
	ArtistID string
	GroupID  string
}

// DiverseSample returns at most limit stable IDs. Probable-recording groups are
// represented once, artists are traversed in keyed rather than lexical order,
// and successive rounds admit at most one item per artist. It is a deterministic
// priority sample, not a claim of statistical or musical optimality.
func DiverseSample(items []SampleItem, limit int, seed uint64) []string {
	if limit <= 0 {
		return nil
	}
	canonical := append([]SampleItem(nil), items...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].ID < canonical[j].ID })
	// Choose one stable representative per probable-recording group.
	groups := map[string]SampleItem{}
	for _, item := range canonical {
		item.ID, item.ArtistID, item.GroupID = strings.TrimSpace(item.ID), strings.TrimSpace(item.ArtistID), strings.TrimSpace(item.GroupID)
		if item.ID == "" {
			continue
		}
		if item.ArtistID == "" {
			item.ArtistID = "unknown-artist:" + item.ID
		}
		if item.GroupID == "" {
			item.GroupID = "recording:" + item.ID
		}
		current, exists := groups[item.GroupID]
		if !exists || keyedLess(seed, "group", item.ID, current.ID) {
			groups[item.GroupID] = item
		}
	}
	byArtist := map[string][]SampleItem{}
	for _, item := range groups {
		byArtist[item.ArtistID] = append(byArtist[item.ArtistID], item)
	}
	artists := make([]string, 0, len(byArtist))
	for artist, artistItems := range byArtist {
		artists = append(artists, artist)
		sort.Slice(artistItems, func(i, j int) bool { return keyedLess(seed, "item", artistItems[i].ID, artistItems[j].ID) })
		byArtist[artist] = artistItems
	}
	sort.Slice(artists, func(i, j int) bool { return keyedLess(seed, "artist", artists[i], artists[j]) })
	if limit > len(groups) {
		limit = len(groups)
	}
	out := make([]string, 0, limit)
	for round := 0; len(out) < limit; round++ {
		added := false
		for _, artist := range artists {
			if round < len(byArtist[artist]) {
				out = append(out, byArtist[artist][round].ID)
				added = true
				if len(out) == limit {
					break
				}
			}
		}
		if !added {
			break
		}
	}
	return out
}

func keyedLess(seed uint64, domain, left, right string) bool {
	a, b := keyedPriority(seed, domain, left), keyedPriority(seed, domain, right)
	if a != b {
		return bytes.Compare(a[:], b[:]) < 0
	}
	return left < right
}

func keyedPriority(seed uint64, domain, id string) [32]byte {
	h := sha256.New()
	var raw [8]byte
	binary.LittleEndian.PutUint64(raw[:], seed)
	_, _ = h.Write([]byte(SamplingVersion))
	_, _ = h.Write(raw[:])
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(id))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
