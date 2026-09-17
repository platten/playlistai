package localaudio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type probeDisposition struct {
	Default int `json:"default"`
}

type probeStream struct {
	Index            int               `json:"index"`
	CodecName        string            `json:"codec_name"`
	Profile          string            `json:"profile"`
	CodecType        string            `json:"codec_type"`
	CodecTag         string            `json:"codec_tag_string"`
	SampleFormat     string            `json:"sample_fmt"`
	SampleRate       string            `json:"sample_rate"`
	Channels         int               `json:"channels"`
	ChannelLayout    string            `json:"channel_layout"`
	BitsPerSample    int               `json:"bits_per_sample"`
	BitsPerRawSample string            `json:"bits_per_raw_sample"`
	Duration         string            `json:"duration"`
	BitRate          string            `json:"bit_rate"`
	Disposition      probeDisposition  `json:"disposition"`
	Tags             map[string]string `json:"tags"`
}

type probeFormat struct {
	Name     string            `json:"format_name"`
	LongName string            `json:"format_long_name"`
	Duration string            `json:"duration"`
	Size     string            `json:"size"`
	BitRate  string            `json:"bit_rate"`
	Tags     map[string]string `json:"tags"`
}

type probeDocument struct {
	Streams []probeStream `json:"streams"`
	Format  probeFormat   `json:"format"`
}

func parsePositiveInt(value string) int {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func parsePositiveInt64(value string) int64 {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func parseDuration(value, provenance string, reliable bool) Duration {
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return Duration{}
	}
	return Duration{Seconds: seconds, Provenance: provenance, Reliable: reliable}
}

func durationFor(stream probeStream, format probeFormat) Duration {
	if result := parseDuration(stream.Duration, "stream", true); result.Seconds > 0 {
		return result
	}
	// ADTS/raw AAC duration is commonly estimated from bitrate. Keep the value
	// useful for scheduling while recording that it is not a precise seek basis.
	reliable := format.Name != "aac"
	return parseDuration(format.Duration, "container", reliable)
}

func selectedAudioStream(streams []probeStream) (probeStream, error) {
	selected := -1
	for i := range streams {
		stream := streams[i]
		if stream.CodecType != "audio" {
			continue
		}
		if selected < 0 || stream.Disposition.Default > streams[selected].Disposition.Default ||
			stream.Disposition.Default == streams[selected].Disposition.Default && stream.Index < streams[selected].Index {
			selected = i
		}
	}
	if selected < 0 {
		return probeStream{}, fmt.Errorf("%w: no audio stream", ErrUnsupported)
	}
	return streams[selected], nil
}

func validateAudioStream(stream probeStream, maxSampleRate, maxChannels int) error {
	if strings.EqualFold(stream.CodecTag, "enca") || strings.Contains(strings.ToLower(stream.CodecName), "drm") {
		return fmt.Errorf("%w: encrypted/DRM audio", ErrUnsupported)
	}
	if stream.CodecName != "flac" && stream.CodecName != "mp3" && stream.CodecName != "aac" && !strings.HasPrefix(stream.CodecName, "pcm_f32") {
		return fmt.Errorf("%w: codec %q", ErrUnsupported, stream.CodecName)
	}
	rate := parsePositiveInt(stream.SampleRate)
	if rate < 8000 || rate > maxSampleRate || stream.Channels < 1 || stream.Channels > maxChannels {
		return fmt.Errorf("%w: sample rate or channel count", ErrUnsupported)
	}
	return nil
}

// Probe reads bounded stream/container metadata without packets, artwork, or
// payload data. The runtime build and protocol whitelist both disable network
// access. Source revision is checked again after the child exits.
func (r *Runtime) Probe(ctx context.Context, path string) (ProbeResult, error) {
	var result ProbeResult
	before, err := sourceRevision(path)
	if err != nil {
		return result, err
	}
	args := []string{
		"-v", "error", "-protocol_whitelist", "file,pipe", "-max_alloc", "67108864",
		"-probesize", "33554432", "-analyzeduration", "30000000",
		"-show_entries", "stream=index,codec_name,profile,codec_type,codec_tag_string,sample_fmt,sample_rate,channels,channel_layout,bits_per_sample,bits_per_raw_sample,duration,bit_rate:stream_disposition=default:stream_tags:format=format_name,format_long_name,duration,size,bit_rate:format_tags",
		"-of", "json", path,
	}
	raw, _, err := runBounded(ctx, r.limits.ProbeTimeout, r.ffprobe, args, r.limits.MaxProbeBytes, r.limits.MaxStderrBytes)
	if err != nil {
		return result, err
	}
	after, err := sourceRevision(path)
	if err != nil || after != before {
		return result, ErrSourceChanged
	}
	var document probeDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return result, fmt.Errorf("localaudio: invalid ffprobe JSON: %w", err)
	}
	stream, err := selectedAudioStream(document.Streams)
	if err != nil {
		return result, err
	}
	rate := parsePositiveInt(stream.SampleRate)
	bits := stream.BitsPerSample
	if rawBits := parsePositiveInt(stream.BitsPerRawSample); rawBits > bits {
		bits = rawBits
	}
	metadata, err := parseMetadata(document.Format.Tags, stream.Tags, r.limits.MaxMetadataBytes)
	if err != nil {
		return result, err
	}
	duration := durationFor(stream, document.Format)
	streamDuration := durationFor(stream, document.Format)
	result = ProbeResult{
		Path: path, Revision: before, Container: document.Format.Name, ContainerLong: document.Format.LongName,
		FileSize: before.Size, BitRate: parsePositiveInt64(document.Format.BitRate), Duration: duration,
		SelectedStream: AudioStream{
			Index: stream.Index, Codec: stream.CodecName, Profile: stream.Profile, CodecTag: stream.CodecTag,
			SampleFormat: stream.SampleFormat, SampleRate: rate, Channels: stream.Channels, ChannelLayout: stream.ChannelLayout,
			BitsPerSample: bits, BitRate: parsePositiveInt64(stream.BitRate), Default: stream.Disposition.Default != 0,
			Duration: streamDuration, RawTags: cloneTags(stream.Tags),
		},
		Metadata: metadata, ProbeRuntimeID: r.ID(),
	}
	return result, validateAudioStream(stream, r.limits.MaxSampleRate, r.limits.MaxChannels)
}
