package evaluation

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

const AutomaticCorpusVersion = "automatic-mtg-corpus/v1"

// AutomaticCorpus keeps independently collected labels out of model features.
// Audio checksums are published metadata; building a corpus fetches no audio.
type AutomaticCorpus struct {
	Version              string                 `json:"version"`
	Seed                 string                 `json:"seed"`
	AnnotationsSHA256    string                 `json:"annotationsSHA256"`
	AudioHashesSHA256    string                 `json:"audioHashesSHA256"`
	LowAudioHashesSHA256 string                 `json:"lowAudioHashesSHA256"`
	MetadataLicense      string                 `json:"metadataLicense"`
	SourceURL            string                 `json:"sourceURL"`
	SelectionPolicy      string                 `json:"selectionPolicy"`
	DuplicatePolicy      string                 `json:"duplicatePolicy"`
	Tracks               []AutomaticCorpusTrack `json:"tracks"`
}

type AutomaticCorpusTrack struct {
	ID             string            `json:"id"`
	ArtistID       string            `json:"artistId"`
	AlbumID        string            `json:"albumId"`
	AudioPath      string            `json:"audioPath"`
	AudioSHA256    string            `json:"audioSHA256"`
	LowAudioSHA256 string            `json:"lowAudioSHA256"`
	Duration       float64           `json:"duration"`
	Split          string            `json:"split"`
	Labels         map[string]string `json:"labels"`
}

func automaticSHA(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func automaticValidSHA(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func AutomaticCorpusSHA256(c AutomaticCorpus) string {
	raw, _ := json.Marshal(c)
	return automaticSHA(raw)
}

// AutomaticCandidateSHA256 is independent of the caller's map/row ordering.
func AutomaticCandidateSHA256(ids []string) string {
	ordered := append([]string(nil), ids...)
	sort.Strings(ordered)
	raw, _ := json.Marshal(ordered)
	return automaticSHA(raw)
}

// BuildAutomaticCorpus selects 150 voice and 150 instrumental recordings for
// each split, with one recording per artist across the entire 600-row corpus.
// Exactly three identical raw annotations are required; the cleaned upstream
// file excludes instrumental labels and must not be substituted.
func BuildAutomaticCorpus(raw, audioHashes, lowAudioHashes []byte, seed string) (AutomaticCorpus, error) {
	c := AutomaticCorpus{Version: AutomaticCorpusVersion, Seed: seed,
		AnnotationsSHA256: automaticSHA(raw), AudioHashesSHA256: automaticSHA(audioHashes), LowAudioHashesSHA256: automaticSHA(lowAudioHashes),
		MetadataLicense: "CC BY-NC-SA 4.0", SourceURL: "https://github.com/MTG/mtg-jamendo-dataset/tree/master/derived/music-classification-annotations",
		SelectionPolicy: "sha256(seed + NUL + track ID); voice then instrumental; one recording per artist globally; first 150 per class development, next 150 heldout",
		DuplicatePolicy: "distinct track IDs, paths, and published full/low encoded-audio SHA256s; does not establish perceptual deduplication across re-encodings"}
	if seed == "" {
		return c, errors.New("automatic corpus: seed required")
	}
	full, err := automaticAudioHashes(audioHashes, false)
	if err != nil {
		return c, err
	}
	low, err := automaticAudioHashes(lowAudioHashes, true)
	if err != nil {
		return c, err
	}
	r := csv.NewReader(bytes.NewReader(raw))
	r.Comma, r.FieldsPerRecord = '\t', -1
	header, err := r.Read()
	if err != nil || strings.Join(header, "\t") != "TRACK_ID\tARTIST_ID\tALBUM_ID\tPATH\tDURATION\tANNOTATIONS" {
		return c, errors.New("automatic corpus: invalid raw annotation header")
	}
	var eligible []AutomaticCorpusTrack
	seen := map[string]bool{}
	for {
		row, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil || len(row) < 6 || seen[row[0]] {
			return c, errors.New("automatic corpus: malformed or duplicate annotation row")
		}
		seen[row[0]] = true
		labels := map[string]string{}
		for _, field := range row[5:] {
			taxonomy, votes, ok := strings.Cut(field, "---")
			if !ok {
				return c, errors.New("automatic corpus: malformed annotation")
			}
			answers := strings.Split(votes, ",")
			if len(answers) == 3 && answers[0] != "" && answers[0] == answers[1] && answers[0] == answers[2] && answers[0] != "unmatched" && (answers[0] != "instrumental" || taxonomy == "voice_instrumental") {
				if _, duplicate := labels[taxonomy]; duplicate {
					return c, errors.New("automatic corpus: duplicate unanimous taxonomy")
				}
				labels[taxonomy] = answers[0]
			}
		}
		voice := labels["voice_instrumental"]
		if (voice != "voice" && voice != "instrumental") || full[row[3]] == "" || low[row[3]] == "" {
			continue
		}
		duration, err := strconv.ParseFloat(row[4], 64)
		if err != nil || math.IsNaN(duration) || duration <= 0 || duration > 86400 {
			return c, errors.New("automatic corpus: invalid duration")
		}
		eligible = append(eligible, AutomaticCorpusTrack{ID: row[0], ArtistID: row[1], AlbumID: row[2], AudioPath: row[3], AudioSHA256: full[row[3]], LowAudioSHA256: low[row[3]], Duration: duration, Labels: labels})
	}
	sort.Slice(eligible, func(i, j int) bool {
		return automaticSHA([]byte(seed+"\x00"+eligible[i].ID)) < automaticSHA([]byte(seed+"\x00"+eligible[j].ID))
	})
	artists, hashes := map[string]bool{}, map[string]bool{}
	for _, label := range []string{"voice", "instrumental"} {
		count := 0
		for _, track := range eligible {
			if count == 300 {
				break
			}
			if track.Labels["voice_instrumental"] != label || artists[track.ArtistID] || hashes[track.AudioSHA256] || hashes[track.LowAudioSHA256] {
				continue
			}
			track.Split = "development"
			if count >= 150 {
				track.Split = "heldout"
			}
			c.Tracks = append(c.Tracks, track)
			artists[track.ArtistID], hashes[track.AudioSHA256], hashes[track.LowAudioSHA256] = true, true, true
			count++
		}
		if count != 300 {
			return c, fmt.Errorf("automatic corpus: insufficient artist/duplicate-disjoint %s recordings (%d/300)", label, count)
		}
	}
	sort.Slice(c.Tracks, func(i, j int) bool { return c.Tracks[i].ID < c.Tracks[j].ID })
	return c, c.Validate()
}

func automaticAudioHashes(raw []byte, low bool) (map[string]string, error) {
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 || !automaticValidSHA(parts[0]) {
			return nil, errors.New("automatic corpus: invalid audio checksum metadata")
		}
		path := parts[1]
		if low {
			path = strings.TrimSuffix(path, ".low.mp3") + ".mp3"
		}
		if out[path] != "" {
			return nil, errors.New("automatic corpus: duplicate audio checksum path")
		}
		out[path] = parts[0]
	}
	return out, nil
}

func (c AutomaticCorpus) Validate() error {
	if c.Version != AutomaticCorpusVersion || c.Seed == "" || len(c.Tracks) != 600 || !automaticValidSHA(c.AnnotationsSHA256) || !automaticValidSHA(c.AudioHashesSHA256) || !automaticValidSHA(c.LowAudioHashesSHA256) {
		return errors.New("automatic corpus: requires a pinned 600-recording manifest")
	}
	seen, artists, paths, hashes, counts := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]int{}
	for _, t := range c.Tracks {
		id, validID := strings.CutPrefix(t.ID, "track_")
		artist, validArtist := strings.CutPrefix(t.ArtistID, "artist_")
		numericID, idErr := strconv.ParseUint(id, 10, 64)
		_, artistErr := strconv.ParseUint(artist, 10, 64)
		label := t.Labels["voice_instrumental"]
		if !validID || !validArtist || idErr != nil || artistErr != nil || t.AudioPath != fmt.Sprintf("%02d/%d.mp3", numericID%100, numericID) || (t.Split != "development" && t.Split != "heldout") || (label != "voice" && label != "instrumental") || !automaticValidSHA(t.AudioSHA256) || !automaticValidSHA(t.LowAudioSHA256) || math.IsNaN(t.Duration) || t.Duration <= 0 || t.Duration > 86400 {
			return errors.New("automatic corpus: invalid recording identity, split, label, or checksum")
		}
		if seen[t.ID] || artists[t.ArtistID] || paths[t.AudioPath] || hashes[t.AudioSHA256] || hashes[t.LowAudioSHA256] {
			return errors.New("automatic corpus: artist or duplicate identity leaks across corpus")
		}
		seen[t.ID], artists[t.ArtistID], paths[t.AudioPath], hashes[t.AudioSHA256], hashes[t.LowAudioSHA256] = true, true, true, true, true
		counts[t.Split+":"+label]++
	}
	for _, split := range []string{"development", "heldout"} {
		for _, label := range []string{"voice", "instrumental"} {
			if counts[split+":"+label] != 150 {
				return errors.New("automatic corpus: each split needs exactly 150 voice and 150 instrumental recordings")
			}
		}
	}
	return nil
}
