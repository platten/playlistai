package audio

import (
	"fmt"
	"math"
	"math/rand/v2"

	"github.com/platten/playlistai/internal/core"
)

// PreviewSamplingVersion describes evidence selection, not the CLAP tensor
// preprocessing contract. Existing model bundles remain compatible.
const PreviewSamplingVersion = "centered-random-duration-22-47s/v1"

const (
	MinEvidenceSeconds = 22
	MaxEvidenceSeconds = 47
)

// evidenceBounds chooses a duration uniformly at PCM-frame precision, then
// centers the interval in the available audio. Short previews are used whole;
// padding for CLAP's ten-second input shape never adds observed evidence.
func evidenceBounds(frames int, choose func(int) int) (start, end int) {
	if frames <= MinEvidenceSeconds*SampleRate {
		return 0, max(0, frames)
	}
	maximum := min(frames, MaxEvidenceSeconds*SampleRate)
	length := MinEvidenceSeconds*SampleRate + choose(maximum-MinEvidenceSeconds*SampleRate+1)
	start = (frames - length) / 2
	return start, start + length
}

func randomEvidenceBounds(frames int) (int, int) {
	return evidenceBounds(frames, rand.IntN)
}

// Legacy rows have no selection metadata and retain their original identity.
// New rows must describe exactly the interval represented by their embeddings.
func validateSampling(a core.AudioAnalysis) error {
	coverage, available := a.Coverage, a.Sampling.AvailableSeconds
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	// Frame-aligned boundaries can differ by one frame when centered. Leave
	// room for floating-point subtraction after converting those frames to seconds.
	const tolerance = 1.0/SampleRate + 1e-9
	if a.Sampling.Policy == "" || !finite(available) || available <= 0 || available > MaxPreviewSeconds || !coverage.Available ||
		!finite(coverage.StartSeconds) || !finite(coverage.EndSeconds) || !finite(coverage.CoveredSeconds) ||
		coverage.StartSeconds < 0 || coverage.EndSeconds > available+tolerance || coverage.EndSeconds <= coverage.StartSeconds ||
		math.Abs(coverage.CoveredSeconds-(coverage.EndSeconds-coverage.StartSeconds)) > tolerance {
		return fmt.Errorf("audio: invalid evidence sample coverage")
	}
	if a.Sampling.Policy == PreviewSamplingVersion {
		minimum := min(float64(MinEvidenceSeconds), available)
		maximum := min(float64(MaxEvidenceSeconds), available)
		if coverage.CoveredSeconds < minimum-tolerance || coverage.CoveredSeconds > maximum+tolerance ||
			math.Abs(coverage.StartSeconds-(available-coverage.EndSeconds)) > tolerance {
			return fmt.Errorf("audio: evidence sample does not match its selection policy")
		}
	}
	end := coverage.StartSeconds
	for _, segment := range a.Segments {
		if math.Abs(segment.StartSeconds-end) > tolerance || segment.EndSeconds > coverage.EndSeconds+tolerance || segment.EndSeconds-segment.StartSeconds > 10+tolerance {
			return fmt.Errorf("audio: segment lies outside sampled coverage")
		}
		end = segment.EndSeconds
	}
	if math.Abs(end-coverage.EndSeconds) > tolerance {
		return fmt.Errorf("audio: sampled coverage exceeds analyzed segments")
	}
	return nil
}
