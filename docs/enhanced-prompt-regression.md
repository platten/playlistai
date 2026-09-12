# Four-mode offline prompt regression

`cmd/musiccheck` accepts the fourth `enhanced_hybrid` policy and an optional
`-enhanced-evidence` file containing `core.EnhancedAudioInput`. It freezes that
derived evidence, supplies it to ranking and sequencing, and records the file's
SHA-256 and snapshot fingerprint in each report. It does not acquire enhanced
audio, load MERT, modify analysis caches or train a model.

The evidence option requires Enhanced Hybrid and rejects `-online`. If a CLAP
bundle is supplied, `-cached-audio-only` is also required. Saved catalog or policy
mismatches and malformed evidence are rejected. Missing compatible evidence
remains unknown. Without an evidence file the Enhanced mode has its normal
no-enhanced-data fallback; accepting the mode does not invent availability.

`-rules-parser` explicitly chooses the existing deterministic rules parser.
The default language-model parser and application defaults are unchanged. This
option cannot be combined with replay or language-model options.

## Executed measurement

On 2026-09-12 UTC (September 11 local time), the existing
[`music-prompts-v8.json`](../internal/evaluation/testdata/music-prompts-v8.json)
12-prompt fixture was parsed with `rules/v11`, then replayed through all four
modes on the installed 956,917-track catalog, version `1:956917:1788613313`.
The seed was 42; no count override, lowered minimum, fixture modification or
application preference change was used. All commands ran on Windows amd64.

This is **new deterministic rules-parser replay, not historical LLM replay**.
The checked-in historical `docs/data/*-live.json` files are summary reports;
they do not contain the raw intent objects required by `-replay`. No compatible
raw historical report was found in the inspected local locations. The new
parse-only report records its actual rules-parser provenance and limitations.

No CLAP bundle or online metadata was enabled in these four runs. No additional
HTTP, preview download, model inference or dataset preparation was performed.
Enhanced Hybrid received the eight already-acquired DSP/MERT records from
`real-preview-20260911/enhanced-evidence.json`.

| Mode | Cases | Output tracks | Cases with output | Cases failing fixture checks | Selected MERT evidence |
| --- | ---: | ---: | ---: | ---: | ---: |
| AcousticBrainz-first | 12 | 40 | 2 | 11 | 0 |
| CLAP-first | 12 | 40 | 2 | 11 | 0 |
| Deej-AI-only | 12 | 40 | 2 | 11 | 0 |
| Enhanced Hybrid | 12 | 40 | 2 | 11 | 0 |

Every mode reported six `needs_clarification`, three `unsupported`, two
`fulfilled`, and one no-seeds error with no outcome. These engine labels do not
override fixture checks: the `dubstep` case produced 20 tracks but failed genre
preservation. `Marilyn Manson` produced 20 tracks and passed. Other failures
included missing exclusions, periods, destination, mood/vocal preferences and
empty output. The parse-only report itself had issues in 10 of 12 cases. Each
four-mode command correctly exited with status 1 for unmet expectations.
The same 11 cases failed in all four modes. Enhanced Hybrid had exactly the
same error lists as AcousticBrainz-first and CLAP-first. Deej-AI-only had
different generation-error details in two cases, while retaining the same
failing-case count. These are observed baseline limitations in this rules-only
offline configuration, not new failing cases introduced by Enhanced Hybrid.
This comparison does not establish historical released-engine behavior, and
the prompt suite did not pass.

CLAP-first and Enhanced Hybrid had identical ordered outputs to the
AcousticBrainz-first run. Deej-AI-only differed in both nonempty playlists.
The real eight-track evidence cohort did not overlap selected outputs: two
Enhanced generations saved its snapshot, but selected MERT coverage was zero.
This therefore checks replay, missing-evidence behavior and contracts; it does
**not** establish MERT benefit, prompt understanding quality, CLAP availability
or musical superiority. No failure was relabeled as passing.

The deterministic CLI regression in `cmd/musiccheck/enhanced_test.go` separately
supplies compatible synthetic embeddings for all fixture tracks, verifies
available `mert_audio_affinity` in selected-track explanations, and verifies
identical repeated replay. That test proves the score path is connected, not
that the synthetic vectors represent musical quality.

## Exact reproduction

From the repository root in PowerShell:

```powershell
$measure = 'C:\Users\pawel\Downloads\playlistai-enhanced-audio\evaluation\prompt-regression-20260912'
$catalog = 'C:\Users\pawel\AppData\Roaming\playlist-ai\catalog'
$evidence = 'C:\Users\pawel\Downloads\playlistai-enhanced-audio\evaluation\real-preview-20260911\enhanced-evidence.json'
New-Item -ItemType Directory -Force -Path $measure | Out-Null
go build -o "$measure\musiccheck.exe" ./cmd/musiccheck

& "$measure\musiccheck.exe" -catalog $catalog -prompts internal/evaluation/testdata/music-prompts-v8.json -rules-parser -parse-only -output "$measure\rules-parsed.json"

& "$measure\musiccheck.exe" -catalog $catalog -prompts internal/evaluation/testdata/music-prompts-v8.json -replay "$measure\rules-parsed.json" -replay-parsed -mode acousticbrainz_first -output "$measure\acousticbrainz_first.json"
& "$measure\musiccheck.exe" -catalog $catalog -prompts internal/evaluation/testdata/music-prompts-v8.json -replay "$measure\rules-parsed.json" -replay-parsed -mode clap_first -output "$measure\clap_first.json"
& "$measure\musiccheck.exe" -catalog $catalog -prompts internal/evaluation/testdata/music-prompts-v8.json -replay "$measure\rules-parsed.json" -replay-parsed -mode deejai_only -output "$measure\deejai_only.json"
& "$measure\musiccheck.exe" -catalog $catalog -prompts internal/evaluation/testdata/music-prompts-v8.json -replay "$measure\rules-parsed.json" -replay-parsed -mode enhanced_hybrid -enhanced-evidence $evidence -output "$measure\enhanced_hybrid.json"
```

Preserve the emitted reports even when the exit status is 1. The executed run
also saved a separate `.log` per command in the same Downloads directory.
These are local evaluation artifacts and are not committed or release assets.
Future runs can have different timings and binary hashes; compare outcomes and
ordered track IDs with the recorded catalog, intent, seed and evidence versions.

Recorded SHA-256 identities:

| Artifact | SHA-256 |
| --- | --- |
| Executed `musiccheck.exe` | `6b6e33cdf0f04349b3be748f9156ff7c1d06f83c34c0e03602440e311bcb6987` |
| `rules-parsed.json` | `c9e555c94c00483a17b2c1186242a418b2e61602e4fbf3ff8ffc906390416161` |
| `enhanced-evidence.json` | `5ed69dd2f4e26a8291e0f2a08df10d378e0415c46cc1d39d82134e4804d9109a` |
| `music-prompts-v8.json` fixture | `ac595dab9130998dc12f5c216c4c88cefcba636ee0a0d061a876013dda22e1d3` |

Automated checks executed for the CLI additions:

```sh
go test ./cmd/musiccheck
go vet ./cmd/musiccheck
```

The tests cover evidence reaching actual ranking, immutable repeated replay,
catalog/policy mismatch, malformed input, prohibited network flag combinations,
and explicit rules-parser provenance. The real prompt failures remain recorded
above, independently of those passing implementation tests.
