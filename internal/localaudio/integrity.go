package localaudio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const IntegrityValidationVersion = "ffmpeg-full-decode-flac-mp3/v1"
const AudioFingerprintVersion = "acoustid-chromaprint/v1;chromaprint=1.6.1;algorithm=1"
const EmbeddedAudioFingerprintVersion = "acoustid-chromaprint-tag/v1;algorithm=1"

// RequiresIntegrityValidation reports whether the selected stream has the
// explicit full-decode validation contract. Selection is based on the probed
// codec rather than the filename extension.
func RequiresIntegrityValidation(probe ProbeResult) bool {
	return probe.SelectedStream.Codec == "flac" || probe.SelectedStream.Codec == "mp3"
}

// ValidateIntegrity fully decodes a FLAC or MP3 stream through the pinned
// runtime and discards the resulting float PCM. It checks the source revision
// before and after the child process, so size and other stat identity fields
// must remain stable for the result to be accepted.
func (r *Runtime) ValidateIntegrity(ctx context.Context, probe ProbeResult) error {
	if !RequiresIntegrityValidation(probe) {
		return nil
	}
	if probe.ProbeRuntimeID != r.ID() || probe.SelectedStream.Index < 0 {
		return fmt.Errorf("localaudio: incompatible probe/runtime or stream")
	}
	before, err := sourceRevision(probe.Path)
	if err != nil || before != probe.Revision {
		return ErrSourceChanged
	}
	args := []string{
		"-v", "error", "-xerror", "-nostdin", "-protocol_whitelist", "file,pipe", "-threads", "1",
		"-err_detect", "explode", "-i", probe.Path,
		"-map", fmt.Sprintf("0:%d", probe.SelectedStream.Index), "-vn", "-sn", "-dn", "-map_metadata", "-1",
		"-c:a", "pcm_f32le", "-f", "f32le", "pipe:1",
	}
	_, validationErr := runDiscarding(ctx, r.limits.IntegrityTimeout, r.limits.IntegrityStall, r.ffmpeg, args, r.limits.MaxStderrBytes)
	after, statErr := sourceRevision(probe.Path)
	if statErr != nil || after != before {
		return ErrSourceChanged
	}
	if validationErr == nil {
		return nil
	}
	if errors.Is(validationErr, context.Canceled) || errors.Is(validationErr, context.DeadlineExceeded) || errors.Is(validationErr, ErrOutputLimit) || errors.Is(validationErr, ErrProcessStalled) {
		return validationErr
	}
	return fmt.Errorf("%w: %v", ErrCorrupt, validationErr)
}

// AudioFingerprint fully decodes the selected stream through the packaged,
// network-disabled FFmpeg/Chromaprint runtime and returns AcoustID's compressed
// base64 Chromaprint representation. The output is bounded; source audio is
// never accumulated in memory or sent to the AcoustID service.
func (r *Runtime) AudioFingerprint(ctx context.Context, probe ProbeResult) (AudioFingerprint, error) {
	var fingerprint AudioFingerprint
	if probe.ProbeRuntimeID != r.ID() || probe.SelectedStream.Index < 0 {
		return fingerprint, fmt.Errorf("localaudio: incompatible probe/runtime or stream")
	}
	before, err := sourceRevision(probe.Path)
	if err != nil || before != probe.Revision {
		return fingerprint, ErrSourceChanged
	}
	args := []string{
		"-v", "error", "-xerror", "-nostdin", "-protocol_whitelist", "file,pipe", "-threads", "1",
		"-err_detect", "explode", "-i", probe.Path,
		"-map", fmt.Sprintf("0:%d", probe.SelectedStream.Index), "-vn", "-sn", "-dn", "-map_metadata", "-1",
	}
	if probe.SelectedStream.Channels > 2 {
		args = append(args, "-ac", "2")
	}
	args = append(args, "-c:a", "pcm_s16le", "-f", "chromaprint", "-algorithm", "1", "-fp_format", "base64", "pipe:1")
	output := &boundedBuffer{limit: 8 << 20}
	_, fingerprintErr := runStreaming(ctx, r.limits.IntegrityTimeout, r.limits.IntegrityStall, r.ffmpeg, args, output, r.limits.MaxStderrBytes)
	after, statErr := sourceRevision(probe.Path)
	if statErr != nil || after != before {
		return fingerprint, ErrSourceChanged
	}
	if fingerprintErr != nil || output.over {
		if output.over {
			fingerprintErr = ErrOutputLimit
		}
		if errors.Is(fingerprintErr, context.Canceled) || errors.Is(fingerprintErr, context.DeadlineExceeded) || errors.Is(fingerprintErr, ErrOutputLimit) || errors.Is(fingerprintErr, ErrProcessStalled) {
			return fingerprint, fingerprintErr
		}
		return fingerprint, fmt.Errorf("%w: %v", ErrFingerprint, fingerprintErr)
	}
	value := strings.TrimSpace(output.String())
	if !validChromaprint(value) {
		return fingerprint, fmt.Errorf("%w: runtime returned an invalid or empty Chromaprint value", ErrFingerprint)
	}
	digest := sha256.Sum256([]byte(value))
	fingerprint = AudioFingerprint{
		Contract: AudioFingerprintVersion, Format: "acoustid-chromaprint-base64", Algorithm: 1,
		Fingerprint: value, FingerprintSHA256: hex.EncodeToString(digest[:]), Scope: "full_selected_stream",
		DecoderRuntimeID: r.ID(),
	}
	return fingerprint, nil
}

// EmbeddedAudioFingerprint returns a fingerprint already carried by the file's
// tags. present remains true for an invalid value so callers do not silently
// replace user-supplied identity metadata with a newly generated fingerprint.
func EmbeddedAudioFingerprint(metadata Metadata) (fingerprint AudioFingerprint, present bool, err error) {
	if metadata.AcoustIDFingerprint == nil {
		return fingerprint, false, nil
	}
	value := strings.TrimSpace(metadata.AcoustIDFingerprint.Value)
	if !validChromaprint(value) {
		return fingerprint, true, fmt.Errorf("%w: embedded ACOUSTID_FINGERPRINT is invalid", ErrFingerprint)
	}
	digest := sha256.Sum256([]byte(value))
	return AudioFingerprint{
		Contract: EmbeddedAudioFingerprintVersion, Format: "acoustid-chromaprint-base64", Algorithm: 1,
		Fingerprint: value, FingerprintSHA256: hex.EncodeToString(digest[:]), Scope: "embedded_tag",
		DecoderRuntimeID: "embedded_tag",
	}, true, nil
}

func validChromaprint(value string) bool {
	if len(value) < 8 || len(value) > 8<<20 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '+' || r == '/' || r == '=' {
			continue
		}
		return false
	}
	return true
}
