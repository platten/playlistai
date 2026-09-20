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
	"unicode"
	"unicode/utf8"

	"github.com/platten/playlistai/internal/core"
)

const (
	Format              = "playlist-ai-library-pack"
	FormatVersion       = 7
	PooledCLAPVersion   = 6
	LegacyFormatVersion = 5
	ManifestName        = "manifest.json"
	MetadataName        = "metadata.sqlite"
	MERTVectorsName     = "mert.f32"
	CLAPVectorsName     = "clap.f32"
	vectorFormatVersion = 1
)

var (
	ErrMutationInProgress = errors.New("librarypack: another library mutation is in progress")
	ErrNoActiveGeneration = errors.New("librarypack: no active library generation")
	ErrManagerClosed      = errors.New("librarypack: manager is closed")
	identifierPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
	isrcPattern           = regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{3}[0-9]{7}$`)
	mbidPattern           = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	acoustIDPattern       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
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
		MaxMembers:       4,
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
	CLAP        int `json:"clap"`
	DSP         int `json:"dsp"`
	Failed      int `json:"failed"`
	Unsupported int `json:"unsupported"`
}

type Manifest struct {
	Format               string                   `json:"format"`
	Version              int                      `json:"version"`
	PackID               string                   `json:"packId"`
	CreatedAt            string                   `json:"createdAt,omitempty"`
	CorpusGeneration     string                   `json:"corpusGeneration"`
	MetadataGeneration   string                   `json:"metadataGeneration"`
	MERTGeneration       string                   `json:"mertGeneration,omitempty"`
	CLAPGeneration       string                   `json:"clapGeneration,omitempty"`
	ClusterGeneration    string                   `json:"clusterGeneration,omitempty"`
	StatisticsGeneration string                   `json:"statisticsGeneration,omitempty"`
	Coverage             Coverage                 `json:"coverage"`
	MERT                 VectorSpace              `json:"mert"`
	CLAP                 VectorSpace              `json:"clap"`
	CLAPModel            *core.AudioModelIdentity `json:"clapModel,omitempty"`
	RootAliases          []string                 `json:"rootAliases"`
	Files                []File                   `json:"files"`
}

type legacyCoverageV5 struct {
	Tracks      int `json:"tracks"`
	Metadata    int `json:"metadata"`
	MERT        int `json:"mert"`
	DSP         int `json:"dsp"`
	Failed      int `json:"failed"`
	Unsupported int `json:"unsupported"`
}
type legacyManifestV5 struct {
	Format               string           `json:"format"`
	Version              int              `json:"version"`
	PackID               string           `json:"packId"`
	CreatedAt            string           `json:"createdAt,omitempty"`
	CorpusGeneration     string           `json:"corpusGeneration"`
	MetadataGeneration   string           `json:"metadataGeneration"`
	MERTGeneration       string           `json:"mertGeneration,omitempty"`
	ClusterGeneration    string           `json:"clusterGeneration,omitempty"`
	StatisticsGeneration string           `json:"statisticsGeneration,omitempty"`
	Coverage             legacyCoverageV5 `json:"coverage"`
	MERT                 VectorSpace      `json:"mert"`
	RootAliases          []string         `json:"rootAliases"`
	Files                []File           `json:"files"`
}

// Track is the portable metadata row. RelativePath is optional and meaningful
// only with RootAlias; a pack never contains an absolute source path.
type Track struct {
	ID                   string
	Artist               string
	Title                string
	NormalizedArtist     string
	NormalizedTitle      string
	SourceIdentity       string
	RecordingIdentity    string
	ISRC                 string
	MusicBrainzRecording string
	AcoustID             string
	AudioFingerprint     *AudioFingerprint
	DurationMilliseconds int64
	DurationProvenance   string
	DurationReliable     bool
	AlbumArtist          string
	Album                string
	RootAlias            string
	RelativePath         string
	Capabilities         []string
	RawTags              json.RawMessage
	DSP                  json.RawMessage
	Missingness          json.RawMessage
	Failure              string
	Unsupported          string
	MERT                 []float32
	CLAP                 []float32
	CLAPEvidence         *CLAPEvidence
	Cluster              *int
	ClusterScore         float64
	Alternative          *int
	AltScore             float64
}

// AudioFingerprint is an interoperable AcoustID Chromaprint value. The SHA-256
// is a lookup accelerator for exact equality and is always verified against
// Fingerprint while writing a pack.
type AudioFingerprint struct {
	Contract          string
	Format            string
	Algorithm         int
	Fingerprint       string
	FingerprintSHA256 string
	Scope             string
	DecoderRuntimeID  string
}

// SameRecording reports whether two pack rows carry high-confidence evidence
// for the same recording. Valid ISRC, recording MBID, and AcoustID values are
// authoritative. An exact compatible AcoustID/Chromaprint fingerprint is
// accepted only when artist and title metadata also match or are very similar.
func SameRecording(left, right Track) bool {
	if leftISRC, rightISRC := CanonicalISRC(left.ISRC), CanonicalISRC(right.ISRC); leftISRC != "" && leftISRC == rightISRC {
		return true
	}
	if leftMBID, rightMBID := CanonicalMusicBrainzRecordingID(left.MusicBrainzRecording), CanonicalMusicBrainzRecordingID(right.MusicBrainzRecording); leftMBID != "" && leftMBID == rightMBID {
		return true
	}
	if leftAcoustID, rightAcoustID := CanonicalAcoustID(left.AcoustID), CanonicalAcoustID(right.AcoustID); leftAcoustID != "" && leftAcoustID == rightAcoustID {
		return true
	}
	if !sameFingerprint(left.AudioFingerprint, right.AudioFingerprint) || !similarRecordingMetadata(left, right) {
		return false
	}
	if left.DurationReliable && right.DurationReliable && left.DurationMilliseconds > 0 && right.DurationMilliseconds > 0 {
		difference := left.DurationMilliseconds - right.DurationMilliseconds
		if difference < 0 {
			difference = -difference
		}
		tolerance := max(int64(3000), max(left.DurationMilliseconds, right.DurationMilliseconds)/50)
		if difference > tolerance {
			return false
		}
	}
	return true
}

// CanonicalISRC returns the compact uppercase representation of a valid ISRC.
// Invalid or vendor-specific values return an empty string and are never safe
// recording-identity evidence.
func CanonicalISRC(value string) string {
	value = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(value)))
	if !isrcPattern.MatchString(value) {
		return ""
	}
	return value
}

// CanonicalMusicBrainzRecordingID returns a lowercase valid recording UUID.
func CanonicalMusicBrainzRecordingID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !mbidPattern.MatchString(value) {
		return ""
	}
	return value
}

// CanonicalMBID is the concise alias for CanonicalMusicBrainzRecordingID.
func CanonicalMBID(value string) string { return CanonicalMusicBrainzRecordingID(value) }

// CanonicalAcoustID returns a lowercase valid AcoustID UUID.
func CanonicalAcoustID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !acoustIDPattern.MatchString(value) {
		return ""
	}
	return value
}

// RecordingIdentity derives the current portable recording identity. Tagged
// identifiers take precedence over a corroborated exact fingerprint. The
// result is an identity label, not an additional duplicate signal: callers
// must still use SameRecording when comparing two rows.
func RecordingIdentity(track Track) string {
	if value := CanonicalMusicBrainzRecordingID(track.MusicBrainzRecording); value != "" {
		return "musicbrainz:" + value
	}
	if value := CanonicalISRC(track.ISRC); value != "" {
		return "isrc:" + value
	}
	if value := CanonicalAcoustID(track.AcoustID); value != "" {
		return "acoustid-id:" + value
	}
	if track.AudioFingerprint == nil || track.AudioFingerprint.Contract == "" || track.AudioFingerprint.FingerprintSHA256 == "" {
		return ""
	}
	metadata := comparableMetadata(track.Artist) + "\x00" + comparableMetadata(track.Title)
	if metadata == "\x00" {
		return ""
	}
	digest := sha256.Sum256([]byte(metadata))
	return "acoustid:" + track.AudioFingerprint.Contract + ":" + track.AudioFingerprint.FingerprintSHA256 + ":metadata:" + hex.EncodeToString(digest[:12])
}

// Internal aliases keep pack validation and writing on the same exported
// canonicalization path used by the indexer and standalone tools.
func canonicalISRC(value string) string     { return CanonicalISRC(value) }
func canonicalMBID(value string) string     { return CanonicalMBID(value) }
func canonicalAcoustID(value string) string { return CanonicalAcoustID(value) }

func sameFingerprint(left, right *AudioFingerprint) bool {
	return left != nil && right != nil &&
		left.Contract != "" && left.Contract == right.Contract &&
		left.Format == "acoustid-chromaprint-base64" && left.Format == right.Format &&
		left.Algorithm == 1 && left.Algorithm == right.Algorithm &&
		left.Fingerprint != "" && left.Fingerprint == right.Fingerprint
}

func validAudioFingerprint(fingerprint AudioFingerprint) bool {
	if fingerprint.Contract == "" || fingerprint.Format != "acoustid-chromaprint-base64" || fingerprint.Algorithm != 1 ||
		fingerprint.Fingerprint == "" || fingerprint.DecoderRuntimeID == "" {
		return false
	}
	switch fingerprint.Scope {
	case "full_selected_stream":
		// Locally generated fingerprints include the decoder and Chromaprint
		// runtime in the contract, so only like-for-like values compare.
	case "embedded_tag":
		if fingerprint.Contract != "acoustid-chromaprint-tag/v1;algorithm=1" || fingerprint.DecoderRuntimeID != "embedded_tag" {
			return false
		}
	default:
		return false
	}
	digest := sha256.Sum256([]byte(fingerprint.Fingerprint))
	return strings.EqualFold(hex.EncodeToString(digest[:]), fingerprint.FingerprintSHA256)
}

func similarRecordingMetadata(left, right Track) bool {
	leftArtist, rightArtist := comparableMetadata(left.Artist), comparableMetadata(right.Artist)
	leftTitle, rightTitle := comparableMetadata(left.Title), comparableMetadata(right.Title)
	return verySimilarText(leftArtist, rightArtist) && verySimilarText(leftTitle, rightTitle)
}

func comparableMetadata(raw string) string {
	var out strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if space && out.Len() > 0 {
				out.WriteByte(' ')
			}
			out.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	return out.String()
}

func verySimilarText(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	leftRunes, rightRunes := []rune(left), []rune(right)
	longest := max(len(leftRunes), len(rightRunes))
	if longest < 8 {
		return false
	}
	if longest > 512 {
		return false
	}
	maximumDistance := max(1, longest/20)
	if difference := len(leftRunes) - len(rightRunes); difference > maximumDistance || difference < -maximumDistance {
		return false
	}
	previous := make([]int, len(rightRunes)+1)
	current := make([]int, len(rightRunes)+1)
	for index := range previous {
		previous[index] = index
	}
	for i, leftRune := range leftRunes {
		current[0] = i + 1
		rowMinimum := current[0]
		for j, rightRune := range rightRunes {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			current[j+1] = min(previous[j+1]+1, current[j]+1, previous[j]+cost)
			rowMinimum = min(rowMinimum, current[j+1])
		}
		if rowMinimum > maximumDistance {
			return false
		}
		previous, current = current, previous
	}
	return previous[len(rightRunes)] <= maximumDistance
}

func trackCapabilities(track Track) []string {
	capabilities := []string{"metadata"}
	if len(track.MERT) > 0 {
		capabilities = append(capabilities, "mert")
	}
	if len(track.CLAP) > 0 {
		capabilities = append(capabilities, "clap")
	}
	if len(track.DSP) > 0 && string(track.DSP) != "{}" {
		capabilities = append(capabilities, "dsp")
	}
	if track.RelativePath != "" {
		capabilities = append(capabilities, "local_path")
	}
	if track.AudioFingerprint != nil {
		capabilities = append(capabilities, "audio_fingerprint")
	}
	if canonicalAcoustID(track.AcoustID) != "" {
		capabilities = append(capabilities, "acoustid")
	}
	return capabilities
}

type Pack struct {
	CreatedAt            time.Time
	CorpusGeneration     string
	MetadataGeneration   string
	MERTGeneration       string
	CLAPGeneration       string
	ClusterGeneration    string
	StatisticsGeneration string
	MERT                 VectorSpace
	CLAP                 VectorSpace
	CLAPModel            *core.AudioModelIdentity
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
	if m.Format != Format || (m.Version != FormatVersion && m.Version != PooledCLAPVersion && m.Version != LegacyFormatVersion) {
		return fmt.Errorf("librarypack: unsupported format %q version %d", m.Format, m.Version)
	}
	if !validIdentifier(m.CorpusGeneration) || !validIdentifier(m.MetadataGeneration) {
		return errors.New("librarypack: invalid corpus or metadata generation")
	}
	for _, generation := range []string{m.MERTGeneration, m.CLAPGeneration, m.ClusterGeneration, m.StatisticsGeneration} {
		if generation != "" && !validIdentifier(generation) {
			return errors.New("librarypack: invalid optional generation identity")
		}
	}
	if m.Coverage.Tracks < 0 || m.Coverage.Tracks > limits.MaxTracks || m.Coverage.Metadata < 0 || m.Coverage.Metadata > m.Coverage.Tracks || m.Coverage.MERT < 0 || m.Coverage.MERT > m.Coverage.Tracks || m.Coverage.CLAP < 0 || m.Coverage.CLAP > m.Coverage.Tracks || m.Coverage.DSP < 0 || m.Coverage.DSP > m.Coverage.Tracks || m.Coverage.Failed < 0 || m.Coverage.Failed > m.Coverage.Tracks || m.Coverage.Unsupported < 0 || m.Coverage.Unsupported > m.Coverage.Tracks {
		return errors.New("librarypack: invalid coverage counts")
	}
	if m.CreatedAt != "" {
		if _, err := time.Parse(time.RFC3339, m.CreatedAt); err != nil {
			return errors.New("librarypack: invalid creation time")
		}
	}
	if err := validateVectorSpace(m.MERT, m.Coverage.MERT, limits, "library_mert"); err != nil {
		return err
	}
	if m.Coverage.MERT > 0 && m.MERTGeneration == "" {
		return errors.New("librarypack: MERT coverage requires a generation identity")
	}
	if err := validateVectorSpace(m.CLAP, m.Coverage.CLAP, limits, "library_clap"); err != nil {
		return err
	}
	if m.Version == LegacyFormatVersion && (m.Coverage.CLAP != 0 || m.CLAPGeneration != "" || m.CLAP != (VectorSpace{})) {
		return errors.New("librarypack: version 5 cannot contain CLAP data")
	}
	if m.Coverage.CLAP > 0 && m.CLAPGeneration == "" {
		return errors.New("librarypack: CLAP coverage requires a generation identity")
	}
	if m.CLAPModel != nil {
		if m.Version < FormatVersion || m.Coverage.CLAP == 0 || !validCLAPModel(*m.CLAPModel, m.CLAP) {
			return errors.New("librarypack: invalid paired CLAP model identity")
		}
	}
	wantFiles := 2
	if m.Coverage.CLAP > 0 {
		wantFiles++
	}
	if len(m.Files) != wantFiles || len(m.RootAliases) > 1024 {
		return errors.New("librarypack: manifest has an invalid vector file set")
	}
	want := map[string]string{MetadataName: "metadata_sqlite", MERTVectorsName: "mert_float32"}
	if m.Coverage.CLAP > 0 {
		want[CLAPVectorsName] = "clap_float32"
	}
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

func validateVectorSpace(v VectorSpace, count int, limits Limits, name string) error {
	if count == 0 && v.Dimension == 0 {
		return nil
	}
	if v.Name != name || v.Dimension <= 0 || v.Dimension > limits.MaxVectorDim || v.DType != "float32" || v.ByteOrder != "little" || !v.Normalized || strings.TrimSpace(v.Model) == "" || strings.TrimSpace(v.ModelRevision) == "" || !validHash(v.GraphSHA256) || strings.TrimSpace(v.Preprocessing) == "" || strings.TrimSpace(v.Sampling) == "" || strings.TrimSpace(v.Pooling) == "" || strings.TrimSpace(v.Scope) == "" || strings.TrimSpace(v.Missingness) == "" {
		return fmt.Errorf("librarypack: invalid %s representation contract", name)
	}
	return nil
}

func semanticID(m Manifest) string {
	if m.Version == LegacyFormatVersion {
		m.Files = append([]File(nil), m.Files...)
		sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Name < m.Files[j].Name })
		m.RootAliases = append([]string(nil), m.RootAliases...)
		sort.Strings(m.RootAliases)
		legacy := legacyManifestV5{Format: m.Format, Version: m.Version, PackID: "", CreatedAt: m.CreatedAt, CorpusGeneration: m.CorpusGeneration, MetadataGeneration: m.MetadataGeneration, MERTGeneration: m.MERTGeneration, ClusterGeneration: m.ClusterGeneration, StatisticsGeneration: m.StatisticsGeneration, MERT: m.MERT, RootAliases: m.RootAliases, Files: m.Files}
		legacy.Coverage = legacyCoverageV5{m.Coverage.Tracks, m.Coverage.Metadata, m.Coverage.MERT, m.Coverage.DSP, m.Coverage.Failed, m.Coverage.Unsupported}
		raw, _ := json.Marshal(legacy)
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
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
