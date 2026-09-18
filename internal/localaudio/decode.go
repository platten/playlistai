package localaudio

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"time"
)

const decodeFrameOverhead = int64(4096)

// DecodedPCMReservationBytes returns the bounded decoder output allowance for
// a requested window. FFmpeg may emit a small codec/filter tail beyond the
// nominal duration, so callers reserving PCM must include the same allowance
// enforced by DecodeWindow.
func DecodedPCMReservationBytes(window Window, sampleRate, channels int) (int64, error) {
	if window.Duration <= 0 || sampleRate <= 0 || channels <= 0 || int64(channels) > math.MaxInt64/4 {
		return 0, fmt.Errorf("localaudio: invalid PCM reservation inputs")
	}
	frameBytes := int64(channels) * 4
	maximumFrames := math.MaxInt64/frameBytes - decodeFrameOverhead
	frames := math.Ceil(window.Duration.Seconds() * float64(sampleRate))
	if frames <= 0 || frames > float64(maximumFrames) {
		return 0, fmt.Errorf("localaudio: decoded PCM reservation overflow")
	}
	return (int64(frames) + decodeFrameOverhead) * frameBytes, nil
}

func secondsArgument(value time.Duration) string {
	return strconv.FormatFloat(value.Seconds(), 'f', 9, 64)
}

func (r *Runtime) DecodeWindow(ctx context.Context, probe ProbeResult, window Window) (PCMWindow, error) {
	var result PCMWindow
	if probe.ProbeRuntimeID != r.ID() || probe.SelectedStream.SampleRate < 8000 ||
		probe.SelectedStream.SampleRate > r.limits.MaxSampleRate || probe.SelectedStream.Channels < 1 ||
		probe.SelectedStream.Channels > r.limits.MaxChannels {
		return result, fmt.Errorf("localaudio: incompatible probe/runtime or stream")
	}
	if window.Index < 0 || window.Start < 0 || window.Duration <= 0 || window.Duration > r.limits.MaxWindowDuration {
		return result, fmt.Errorf("localaudio: invalid decode window")
	}
	before, err := sourceRevision(probe.Path)
	if err != nil || before != probe.Revision {
		return result, ErrSourceChanged
	}
	rate, channels := probe.SelectedStream.SampleRate, probe.SelectedStream.Channels
	reservation, reservationErr := DecodedPCMReservationBytes(window, rate, channels)
	if reservationErr != nil || reservation > r.limits.MaxPCMBytes {
		return result, fmt.Errorf("localaudio: decoded PCM reservation exceeds limit")
	}
	args := []string{
		"-v", "error", "-nostdin", "-protocol_whitelist", "file,pipe", "-threads", "1",
		"-ss", secondsArgument(window.Start), "-i", probe.Path,
		"-map", fmt.Sprintf("0:%d", probe.SelectedStream.Index), "-t", secondsArgument(window.Duration),
		"-vn", "-sn", "-dn", "-map_metadata", "-1", "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1",
	}
	raw, _, err := runBounded(ctx, r.limits.DecodeTimeout, r.ffmpeg, args, reservation, r.limits.MaxStderrBytes)
	if err != nil {
		return result, err
	}
	after, err := sourceRevision(probe.Path)
	if err != nil || after != before {
		clear(raw)
		return result, ErrSourceChanged
	}
	frameSize := channels * 4
	if len(raw) == 0 || len(raw)%frameSize != 0 {
		clear(raw)
		return result, fmt.Errorf("localaudio: decoder returned incomplete float32 frames")
	}
	samples := make([]float32, len(raw)/4)
	for i := range samples {
		value := math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			clear(raw)
			clear(samples)
			return result, fmt.Errorf("localaudio: decoder returned nonfinite PCM")
		}
		samples[i] = value
	}
	clear(raw)
	frames := len(samples) / channels
	return PCMWindow{
		Index: window.Index, RequestedStart: window.Start, RequestedDuration: window.Duration,
		ObservedDuration: time.Duration(float64(time.Second) * float64(frames) / float64(rate)),
		Samples:          samples, SampleRate: rate, Channels: channels, ChannelLayout: probe.SelectedStream.ChannelLayout,
		StreamIndex: probe.SelectedStream.Index, DecoderRuntimeID: r.ID(), SourceRevision: before,
	}, nil
}

// DecodeWindows visits windows sequentially through one owned source operation.
// Each PCM buffer is valid only during visit and is cleared immediately after it
// returns. This prevents a sampling profile from opening several decoders at once.
func (r *Runtime) DecodeWindows(ctx context.Context, probe ProbeResult, windows []Window, visit func(PCMWindow) error) error {
	if len(windows) == 0 || visit == nil {
		return fmt.Errorf("localaudio: decode windows and visitor are required")
	}
	for _, window := range windows {
		if err := ctx.Err(); err != nil {
			return err
		}
		pcm, err := r.DecodeWindow(ctx, probe, window)
		if err != nil {
			return err
		}
		err = func() error {
			defer clear(pcm.Samples)
			return visit(pcm)
		}()
		if err != nil {
			return err
		}
	}
	after, err := sourceRevision(probe.Path)
	if err != nil || after != probe.Revision {
		return ErrSourceChanged
	}
	return nil
}
