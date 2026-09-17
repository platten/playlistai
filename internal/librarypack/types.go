// Package librarypack reads, writes, and atomically publishes portable Playlist
// AI library packs. It has no desktop or Wails dependencies.
package librarypack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	Format              = "playlist-ai-library-pack"
	FormatVersion       = 2
	ManifestName        = "manifest.json"
	MetadataName        = "metadata.sqlite"
	MERTVectorsName     = "mert.f32"
	vectorFormatVersion = 1
)

var (
	ErrMutationInProgress = errors.New("librarypack: another library mutation is in progress")
	ErrNoActiveGeneration = errors.New("librarypack: no active library generation")
	ErrManagerClosed      = errors.New("librarypack: manager is closed")
	identifierPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
)

// Limits bounds untrusted archive expansion, SQLite records, and allocations.
// Zero fields are replaced by DefaultLimits values.
type Limits struct {
	MaxArchiveBytes  int64
	MaxExpandedBytes int64
	MaxMemberBytes   int64
	MaxManifestBytes int64
	MaxMembers       int
	MaxTracks        int
	MaxVectorDim     int
	MaxRecordBytes   int
	MaxJSONBytes     int
}

func DefaultLimits() Limits {
	return Limits{
		MaxArchiveBytes:  96 << 30,
		MaxExpandedBytes: 128 << 30,
		MaxMemberBytes:   96 << 30,
		MaxManifestBytes: 4 << 20,
		MaxMembers:       3,
		MaxTracks:        5_000_000,
		MaxVectorDim:     4096,
		MaxRecordBytes:   1 << 20,
		MaxJSONBytes:     512 << 10,
	}
}

func (l Limits) normalized() Limits {
	d := DefaultLimits()
	if l.MaxArchiveBytes <= 0 {
		l.MaxArchiveBytes = d.MaxArchiveBytes
	}
	if l.MaxExpandedBytes <= 0 {
		l.MaxExpandedBytes = d.MaxExpandedBytes
	}
	if l.MaxMemberBytes <= 0 {
		l.MaxMemberBytes = d.MaxMemberBytes
	}
	if l.MaxManifestBytes <= 0 {
		l.MaxManifestBytes = d.MaxManifestBytes
	}
	if l.MaxMembers <= 0 {
		l.MaxMembers = d.MaxMembers
	}
	if l.MaxTracks <= 0 {
		l.MaxTracks = d.MaxTracks
	}
	if l.MaxVectorDim <= 0 {
		l.MaxVectorDim = d.MaxVectorDim
	}
	if l.MaxRecordBytes <= 0 {
		l.MaxRecordBytes = d.MaxRecordBytes
	}
	if l.MaxJSONBytes <= 0 {
		l.MaxJSONBytes = d.MaxJSONBytes
	}
	return l
}

type File struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// VectorSpace identifies one compatible MERT representation. Cosines from
// different identities must never be combined as though they share a space.
type VectorSpace struct {
	Name          string `json:"name"`
	Dimension     int    `json:"dimension"`
	DType         string `json:"dtype"`
	ByteOrder     string `json:"byteOrder"`
	Normalized    bool   `json:"normalized"`
	Model         string `json:"model"`
	ModelRevision string `json:"modelRevision"`
	GraphSHA256   string `json:"graphSha256"`
	Decoder       string `json:"decoder"`
	Preprocessing string `json:"preprocessing"`
	Sampling      string `json:"sampling"`
	Pooling       string `json:"pooling"`
	Scope         string `json:"scope"`
	Missingness   string `json:"missingness"`
}

type Coverage struct {
	Tracks      int `json:"tracks"`
	Metadata    int `json:"metadata"`
	MERT        int `json:"mert"`
	DSP         int `json:"dsp"`
	Failed      int `json:"failed"`
	Unsupported int `json:"unsupported"`
}

type Manifest struct {
	Format               string      `json:"format"`
	Version              int         `json:"version"`
	PackID               string      `json:"packId"`
	CreatedAt            string      `json:"createdAt,omitempty"`
	CorpusGeneration     string      `json:"corpusGeneration"`
	MetadataGeneration   string      `json:"metadataGeneration"`
	MERTGeneration       string      `json:"mertGeneration,omitempty"`
	ClusterGeneration    string      `json:"clusterGeneration,omitempty"`
	StatisticsGeneration string      `json:"statisticsGeneration,omitempty"`
	Coverage             Coverage    `json:"coverage"`
	MERT                 VectorSpace `json:"mert"`
	RootAliases          []string    `json:"rootAliases"`
	Files                []File      `json:"files"`
}

// Track is the portable metadata row. RelativePath is optional and meaningful
// only with RootAlias; a pack never contains an absolute source path.
type Track struct {
	ID           string
	Artist       string
	Title        string
	AlbumArtist  string
	Album        string
	RootAlias    string
	RelativePath string
	Capabilities []string
	RawTags      json.RawMessage
	DSP          json.RawMessage
	Missingness  json.RawMessage
	Failure      string
	Unsupported  string
	MERT         []float32
	Cluster      *int
	ClusterScore float64
	Alternative  *int
	AltScore     float64
}

func trackCapabilities(track Track) []string {
	capabilities := []string{"metadata"}
	if len(track.MERT) > 0 {
		capabilities = append(capabilities, "mert")
	}
	if len(track.DSP) > 0 && string(track.DSP) != "{}" {
		capabilities = append(capabilities, "dsp")
	}
	if track.RelativePath != "" {
		capabilities = append(capabilities, "local_path")
	}
	return capabilities
}

type Pack struct {
	CreatedAt            time.Time
	CorpusGeneration     string
	MetadataGeneration   string
	MERTGeneration       string
	ClusterGeneration    string
	StatisticsGeneration string
	MERT                 VectorSpace
	Tracks               []Track
	// Learning is a canonical JSON payload containing the fitted metadata
	// model and optional spherical model/assignments. It is stored inside the
	// checksummed metadata SQLite member, not as an unbounded JSON vector file.
	Learning json.RawMessage
	// Statistics contains compatible-space library-relative DSP distributions.
	// It is separate from absolute per-track DSP measurements.
	Statistics json.RawMessage
}

func (m Manifest) Validate(limits Limits) error {
	limits = limits.normalized()
	if m.Format != Format || m.Version != FormatVersion {
		return fmt.Errorf("librarypack: unsupported format %q version %d", m.Format, m.Version)
	}
	if !validIdentifier(m.CorpusGeneration) || !validIdentifier(m.MetadataGeneration) {
		return errors.New("librarypack: invalid corpus or metadata generation")
	}
	for _, generation := range []string{m.MERTGeneration, m.ClusterGeneration, m.StatisticsGeneration} {
		if generation != "" && !validIdentifier(generation) {
			return errors.New("librarypack: invalid optional generation identity")
		}
	}
	if m.Coverage.Tracks < 0 || m.Coverage.Tracks > limits.MaxTracks || m.Coverage.Metadata < 0 || m.Coverage.Metadata > m.Coverage.Tracks || m.Coverage.MERT < 0 || m.Coverage.MERT > m.Coverage.Tracks || m.Coverage.DSP < 0 || m.Coverage.DSP > m.Coverage.Tracks || m.Coverage.Failed < 0 || m.Coverage.Failed > m.Coverage.Tracks || m.Coverage.Unsupported < 0 || m.Coverage.Unsupported > m.Coverage.Tracks {
		return errors.New("librarypack: invalid coverage counts")
	}
	if m.CreatedAt != "" {
		if _, err := time.Parse(time.RFC3339, m.CreatedAt); err != nil {
			return errors.New("librarypack: invalid creation time")
		}
	}
	if err := validateVectorSpace(m.MERT, m.Coverage.MERT, limits); err != nil {
		return err
	}
	if m.Coverage.MERT > 0 && m.MERTGeneration == "" {
		return errors.New("librarypack: MERT coverage requires a generation identity")
	}
	if len(m.Files) != 2 || len(m.RootAliases) > 1024 {
		return errors.New("librarypack: manifest must list exactly metadata and MERT vector files")
	}
	want := map[string]string{MetadataName: "metadata_sqlite", MERTVectorsName: "mert_float32"}
	seen := map[string]bool{}
	var total int64
	for _, f := range m.Files {
		if want[f.Name] != f.Kind || seen[f.Name] || f.Size < 0 || f.Size > limits.MaxMemberBytes || !validHash(f.SHA256) {
			return fmt.Errorf("librarypack: invalid file entry %q", f.Name)
		}
		seen[f.Name] = true
		if f.Size > math.MaxInt64-total {
			return errors.New("librarypack: expanded size overflow")
		}
		total += f.Size
	}
	if len(seen) != len(want) || total > limits.MaxExpandedBytes {
		return errors.New("librarypack: manifest exceeds expansion limits")
	}
	aliases := append([]string(nil), m.RootAliases...)
	sort.Strings(aliases)
	for i, alias := range aliases {
		if !validIdentifier(alias) || i > 0 && alias == aliases[i-1] {
			return errors.New("librarypack: invalid or duplicate root alias")
		}
	}
	if !validHash(m.PackID) || semanticID(m) != m.PackID {
		return errors.New("librarypack: invalid semantic pack identity")
	}
	return nil
}

func validateVectorSpace(v VectorSpace, count int, limits Limits) error {
	if count == 0 && v.Dimension == 0 {
		return nil
	}
	if v.Name != "library_mert" || v.Dimension <= 0 || v.Dimension > limits.MaxVectorDim || v.DType != "float32" || v.ByteOrder != "little" || !v.Normalized || strings.TrimSpace(v.Model) == "" || strings.TrimSpace(v.ModelRevision) == "" || !validHash(v.GraphSHA256) || strings.TrimSpace(v.Preprocessing) == "" || strings.TrimSpace(v.Sampling) == "" || strings.TrimSpace(v.Pooling) == "" || strings.TrimSpace(v.Scope) == "" || strings.TrimSpace(v.Missingness) == "" {
		return errors.New("librarypack: invalid MERT representation contract")
	}
	return nil
}

func semanticID(m Manifest) string {
	m.PackID = ""
	m.Files = append([]File(nil), m.Files...)
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Name < m.Files[j].Name })
	m.RootAliases = append([]string(nil), m.RootAliases...)
	sort.Strings(m.RootAliases)
	raw, _ := json.Marshal(m)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validIdentifier(s string) bool { return identifierPattern.MatchString(strings.TrimSpace(s)) }
func validHash(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && s == strings.ToLower(s)
}

func validateRelativePath(name string) bool {
	if name == "" || strings.ContainsRune(name, 0) || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") || path.IsAbs(name) || path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return false
	}
	return utf8.ValidString(name)
}

func canonicalJSON(raw json.RawMessage, max int) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	if len(raw) > max || !json.Valid(raw) {
		return "", errors.New("librarypack: invalid or oversized JSON field")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(canonical) > max {
		return "", errors.New("librarypack: oversized canonical JSON field")
	}
	return string(canonical), nil
}
