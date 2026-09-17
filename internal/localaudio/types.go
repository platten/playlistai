// Package localaudio probes and decodes user-owned local audio through a
// verified, application-managed FFmpeg runtime. It never searches PATH and it
// never writes to source audio files.
package localaudio

import (
	"errors"
	"time"
)

var (
	ErrOutputLimit    = errors.New("local audio subprocess output exceeded its limit")
	ErrProcessStalled = errors.New("local audio subprocess stopped producing output")
	ErrSourceChanged  = errors.New("local audio source changed during processing")
	ErrUnsupported    = errors.New("local audio stream is unsupported")
	ErrCorrupt        = errors.New("local audio stream is corrupt")
	ErrFingerprint    = errors.New("local audio fingerprint is unavailable")
)

// SourceRevision is a stat-based change detector, not an audio identity or a
// complete-file hash. Device/inode fields are zero on platforms that do not
// expose them.
type SourceRevision struct {
	Size            int64  `json:"size"`
	ModTimeUnixNano int64  `json:"modTimeUnixNano"`
	ChangeUnixNano  int64  `json:"changeUnixNano,omitempty"`
	Device          uint64 `json:"device,omitempty"`
	Inode           uint64 `json:"inode,omitempty"`
}

type TagValue struct {
	Value      string `json:"value"`
	SourceKey  string `json:"sourceKey"`
	Provenance string `json:"provenance"`
}

type TrackDiscNumber struct {
	Number int `json:"number,omitempty"`
	Total  int `json:"total,omitempty"`
}

// Metadata retains the exact ffprobe tag dictionary and exposes common fields
// without guessing separators in artist or genre names.
type Metadata struct {
	RawTags             map[string]string `json:"rawTags,omitempty"`
	Title               *TagValue         `json:"title,omitempty"`
	ArtistCredits       []TagValue        `json:"artistCredits,omitempty"`
	AlbumArtists        []TagValue        `json:"albumArtists,omitempty"`
	Album               *TagValue         `json:"album,omitempty"`
	Genres              []TagValue        `json:"genres,omitempty"`
	Moods               []TagValue        `json:"moods,omitempty"`
	Styles              []TagValue        `json:"styles,omitempty"`
	Date                *TagValue         `json:"date,omitempty"`
	Track               TrackDiscNumber   `json:"track,omitempty"`
	Disc                TrackDiscNumber   `json:"disc,omitempty"`
	MusicBrainzIDs      map[string]string `json:"musicBrainzIds,omitempty"`
	ISRC                *TagValue         `json:"isrc,omitempty"`
	AcoustID            *TagValue         `json:"acoustId,omitempty"`
	AcoustIDFingerprint *TagValue         `json:"acoustIdFingerprint,omitempty"`
	ReplayGain          map[string]string `json:"replayGain,omitempty"`
}

type Duration struct {
	Seconds    float64 `json:"seconds"`
	Provenance string  `json:"provenance"`
	Reliable   bool    `json:"reliable"`
}

type AudioStream struct {
	Index         int               `json:"index"`
	Codec         string            `json:"codec"`
	Profile       string            `json:"profile,omitempty"`
	CodecTag      string            `json:"codecTag,omitempty"`
	SampleFormat  string            `json:"sampleFormat,omitempty"`
	SampleRate    int               `json:"sampleRate"`
	Channels      int               `json:"channels"`
	ChannelLayout string            `json:"channelLayout,omitempty"`
	BitsPerSample int               `json:"bitsPerSample,omitempty"`
	BitRate       int64             `json:"bitRate,omitempty"`
	Default       bool              `json:"default"`
	Duration      Duration          `json:"duration"`
	RawTags       map[string]string `json:"rawTags,omitempty"`
}

type ProbeResult struct {
	Path           string         `json:"path"`
	Revision       SourceRevision `json:"revision"`
	Container      string         `json:"container"`
	ContainerLong  string         `json:"containerLong,omitempty"`
	FileSize       int64          `json:"fileSize"`
	BitRate        int64          `json:"bitRate,omitempty"`
	Duration       Duration       `json:"duration"`
	SelectedStream AudioStream    `json:"selectedStream"`
	Metadata       Metadata       `json:"metadata"`
	ProbeRuntimeID string         `json:"probeRuntimeId"`
}

// AudioFingerprint is the compressed base64 Chromaprint value used by
// AcoustID. FingerprintSHA256 supports an indexed exact-equality check without
// replacing the interoperable Fingerprint value. Approximate matching requires
// decoding/comparing raw Chromaprint items; a matching ISRC alone is never
// treated as proof that two masters have identical audio.
type AudioFingerprint struct {
	Contract          string `json:"contract"`
	Format            string `json:"format"`
	Algorithm         int    `json:"algorithm"`
	Fingerprint       string `json:"fingerprint"`
	FingerprintSHA256 string `json:"fingerprintSha256"`
	Scope             string `json:"scope"`
	DecoderRuntimeID  string `json:"decoderRuntimeId"`
}

type Window struct {
	Index    int           `json:"index"`
	Start    time.Duration `json:"start"`
	Duration time.Duration `json:"duration"`
}

// PCMWindow owns interleaved little-endian-decoded float32 samples. Samples are
// not clipped or quantized. Clear Samples when the final consumer releases it.
type PCMWindow struct {
	Index             int            `json:"index"`
	RequestedStart    time.Duration  `json:"requestedStart"`
	RequestedDuration time.Duration  `json:"requestedDuration"`
	ObservedDuration  time.Duration  `json:"observedDuration"`
	Samples           []float32      `json:"-"`
	SampleRate        int            `json:"sampleRate"`
	Channels          int            `json:"channels"`
	ChannelLayout     string         `json:"channelLayout,omitempty"`
	StreamIndex       int            `json:"streamIndex"`
	DecoderRuntimeID  string         `json:"decoderRuntimeId"`
	SourceRevision    SourceRevision `json:"sourceRevision"`
}

type Limits struct {
	ProbeTimeout      time.Duration
	DecodeTimeout     time.Duration
	IntegrityTimeout  time.Duration
	IntegrityStall    time.Duration
	MaxProbeBytes     int64
	MaxStderrBytes    int64
	MaxMetadataBytes  int64
	MaxWindowDuration time.Duration
	MaxPCMBytes       int64
	MaxChannels       int
	MaxSampleRate     int
}

func DefaultLimits() Limits {
	return Limits{
		ProbeTimeout:      45 * time.Second,
		DecodeTimeout:     2 * time.Minute,
		IntegrityTimeout:  30 * time.Minute,
		IntegrityStall:    30 * time.Second,
		MaxProbeBytes:     4 << 20,
		MaxStderrBytes:    128 << 10,
		MaxMetadataBytes:  1 << 20,
		MaxWindowDuration: 30 * time.Second,
		MaxPCMBytes:       256 << 20,
		MaxChannels:       8,
		MaxSampleRate:     192000,
	}
}
