package localaudio

import (
	"context"
	"errors"
	"fmt"
)

const IntegrityValidationVersion = "ffmpeg-full-decode-flac-mp3/v1"

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
