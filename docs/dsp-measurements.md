# Original-preview DSP measurements

Milestone 2 measures decoded preview PCM in compiled Go. It needs no Python,
model, training corpus, provider request, or additional dataset. Measurements
describe only observed preview audio. They do not establish whole-track
properties, emotional intensity, instrumentation, tempo, or musical suitability.

`audio.DSPVersion` identifies this exact algorithm. Changing any definition,
constant, framing rule, normalization, missingness rule, or aggregation requires
a version change and separate cache identity.

## Input and time-domain definitions

Input is borrowed interleaved float32 PCM at its original sample rate; channels
are not downmixed, resampled, peak-normalized, or loudness-normalized. Accepted
rates are 8–96 kHz, 1–8 channels, and at most 60 seconds. Empty, incomplete,
nonfinite, and out-of-range [-1,1] PCM is an error. Clips shorter than 400 ms
return unknown measurements with `insufficient_duration`. Cancellation is checked
before processing, every 16,384 input values, and before every spectral frame.
Caller PCM is never changed; allocated audio/spectral scratch is cleared.

* `rms_dbfs`: 10 log10 of mean squared sample value, averaged across all channels
  and all samples, including the final partial window.
* `sample_peak_dbfs`: 20 log10 of the largest absolute sample across channels.
  This is a sample peak, not an oversampled true peak.
* `crest_factor_db`: sample peak dBFS minus RMS dBFS.
* `short_window_rms_spread_db`: P95 minus P10 of complete, nonoverlapping 400 ms
  windows' channel-averaged RMS dBFS. Window length is floor(rate × 0.4).
  Discard only the incomplete final window. Quantiles linearly interpolate at
  index q × (window count − 1); fewer than two windows is unknown with
  `insufficient_rms_windows`. This is not standardized loudness range.

The dB power floor is 1e-16 (−160 dBFS), so JSON never contains infinity.
Mean power at or below 1e-12 (−120 dBFS) is treated as silence: RMS and sample
peak remain finite observations; all other measurements are unknown with
`silent_audio`. Silence does not fabricate zero band ratios or crest values.

## Spectrum and frequency coverage

FFT length N is the smallest power of two at least sampleRate / 10 (at least
100 ms). At 44.1 and 48 kHz this is 8192, with hop N/4 = 2048 samples. At
24 kHz it is 4096; at 96 kHz it is 16384. Each complete frame uses periodic
Hann w[i] = 0.5 − 0.5 cos(2πi/N), with no padding or centering. Ignore the
partial tail. Compute each channel's FFT separately using the existing Go
radix-two implementation and average channel powers, preventing anti-phase
cancellation.

Each one-sided power bin is |FFT|² / (N × sum(w²)), multiplied by two except
DC and Nyquist, then averaged across channels. Bin frequency is k × rate / N.
Use bins with centers in the fixed inclusive [20,12000] Hz band. Do not
interpolate boundary bins. Rates below 24 kHz mark all six spectral measures
unknown with `insufficient_spectral_bandwidth`; they never silently shrink
the denominator. If mean eligible frame power is at or below 1e-12, those
measures are unknown with `insufficient_band_energy`.

* `subbass_energy_ratio`: summed [20,80) Hz power / summed [20,12000] Hz power.
* `bass_energy_ratio`: summed [20,250) Hz power / the same denominator.
* `treble_energy_ratio`: summed [4000,12000] Hz power / the same denominator.
* `spectral_centroid_hz`: sum(bin frequency × power) / total eligible power.

These four quantities aggregate powers over all complete frames before dividing.
Subbass overlaps bass intentionally. Ratios are dimensionless. Frequency
resolution, bin boundaries, window leakage, and preview edits limit comparisons.

## Spectral change and onset activity

For successive complete frames, positive spectral flux is the sum over eligible
bins of max(0, current power − previous power). `positive_spectral_flux` is
the arithmetic mean across frame transitions (exclude the first frame). Its
units are normalized PCM power per transition. It responds to amplitude as
well as spectral changes; it is not gain-invariant and is not a probability.

For onset detection only, retain a zero first-frame flux sentinel. Compute the
median M of transition flux, then median absolute deviation D from M using
the same interpolated quantiles. Threshold is max(1e-6, M + 3D). Count interior
frame peaks strictly above threshold and their left neighbor, and at least as
high as their right neighbor. Accept the first peak of a plateau; keep accepted
peaks at least 100 ms apart (greedy earliest first). First/last frame peaks
cannot be confirmed and are excluded. `onset_rate_hz` is accepted peak count
divided by the entire observed PCM duration, including the discarded tail.
The 400 ms minimum guarantees enough complete frames for this calculation at
supported spectral rates. This deterministic activity proxy is not beat
tracking, a calibrated perceptual onset detector, or BPM estimation.

Synthetic tests cover tone levels and bands, silence, DC, anti-phase stereo,
amplitude envelopes, impulse activity, seeded noise, repeatability, invalid
inputs, short duration, low sample rates, and cancellation. They establish DSP
behavior, not held-out musical quality or native packaging on every OS.
