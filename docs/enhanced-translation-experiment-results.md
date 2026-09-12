# Enhanced Hybrid translation experiments

Executed on 2026-09-12 at `7e91fc199c57b1487aa315f6a9c382db2c4364e8`.
These are local interpretation and bounded offline replay experiments. They do
not measure listener preference, full-catalog audio retrieval or a production
implementation of the proposed dictionary.

The [remediation plan](enhanced-translation-remediation-plan.md) integrates these
results with the provider, prompt and ranking audits. The
[machine-readable summary](data/enhanced-translation-results-v1.json) records
per-case narrow errors, timings, input/report hashes and manual-review counts.

## Ten additional realistic prompts

Full prompts and existing CLI expectations are preserved in
[enhanced-translation-prompts-v1.json](data/enhanced-translation-prompts-v1.json).
They were generated for this investigation from the user's stated interests;
none replaced an existing regression case.

| # | Request | Material findings with the original prompt |
| --- | --- | --- |
| 1 | Relaxing classical for concentration; mostly piano/strings; no singing; 15 tracks | Piano and strings become mandatory texture criteria instead of soft instrumentation |
| 2 | Dramatic 20th-century classical; quiet-to-loud changes; no film scores; 12 tracks | Dramatic becomes a genre; composition period is absent; film-score exclusion gets the wrong type |
| 3 | Aerosmith-like workout music, excluding Aerosmith themselves; 15 tracks | Explicit contracted exclusion is rejected, or local connection fails during the attempt |
| 4 | Bluesy hard rock like early Aerosmith; raw band sound rather than polished pop; 12 tracks | One transport failure; accepted run loses early-career scope and negative polished-pop preference |
| 5 | Relaxing electronic like `christrian loeffler`; warm texture, gentle pulse, not too clubby; 20 tracks | Schema rejects negative entity reference; no usable normalized intent |
| 6 | Christian Löffler/Kiasmos; melancholic but comforting; mostly instrumental; 15 tracks | Moods become essential genres and mostly instrumental becomes a texture |
| 7 | Heavy/aggressive/energetic lifting music; steady beat; no slow ballads; 20 tracks | Heavy/aggressive become essential genres; ballad exclusion and rhythm meaning are incomplete |
| 8 | 30-minute electronic running arc; easy start, build, cool down; no harsh vocals | Duration becomes 30 tracks; negative vocal phrase becomes an affirmative essential texture; narrow check misses this |
| 9 | Nine Inch Nails to Marilyn Manson; smooth transitions, no adjacent artist repeats; 15 tracks | Transport/schema failures; actual artist endpoints still need explicit output roles |
| 10 | Dark industrial rock like NIN; less aggressive; no Manson/screaming; 15 tracks | Dark is fused into genre, reduced aggression becomes positive essential style, screaming exclusion disappears |

The [independent semantic audit](enhanced-translation-ten-prompt-audit.md)
defines a stricter rubric: all material facts must retain their correct type,
polarity, degree, identity, units and order. It does not require a single literal
canonical spelling or count unsupported downstream features as implemented.
Both original baseline runs had **0/10 fully faithful interpretations** under
this rubric. That is a semantic review result, not an audio-quality score.

## Original parser, repeated

The existing native Qwen2.5-3B-Instruct Q4_K_M and current `llama/v13` parser were
used with temperature zero, prompt caching, four 4,096-token runtime slots,
the current GBNF grammar, four current few-shot examples and unchanged repair
logic. Model calls were sequential. The root agent owned and stopped the local
server; no application process or preferences were changed.

| Run | Accepted normalized intents | Narrow CLI passes | Full-meaning review passes | Total parsing time |
| --- | ---: | ---: | ---: | ---: |
| Original direct 1 | 6/10 | 2/10 | 0/10 | 62.412s |
| Original direct 2 | 7/10 | 3/10 | 0/10 | 62.276s |

“Accepted” means schema parsing produced a core intent, not that the intent
faithfully represents the request. Narrow CLI checks examine only the fields
supported by the existing fixture structure. Cases 1 and 8 pass those checks
while still containing substantial mistakes. Case 4 also passes narrowly in
the second run. Six accepted normalized intents were exactly identical across
the two runs, exposing repeatable semantic problems rather than only noise.

The configured initial output allowance is 1,800 tokens; retry allowance is
2,400 plus correction feedback. Applying the actual chat template and tokenizer
to case 1 measured **2,071 input tokens**, 10,092 characters. The nominal retry
reservation exceeds a 4,096-token slot, but the observed server completions
reported no truncation. Context pressure is a design concern, not a proven
explanation for the recorded connection errors.

## Controlled shorter-prompt comparison

The candidate in [the prompt audit](enhanced-intent-prompt-audit.md) was written
to the current grammar before inspecting its ablation outputs. It reduced the
system instruction from 6,498 to 3,095 characters. A local reverse proxy changed
only the first message's exact system-prompt prefix. It preserved repair feedback,
the other nine messages, grammar, sampling, output budgets and streaming.

The same proxy path was used for both original and compact arms, with order
original-1, compact-1, compact-2, original-2 on one owned server. All **64 captured
requests** (including repairs) were programmatically checked: only the allowed
system prefix changed. The proxy's self-check covered prefix isolation, correction
suffix preservation, unexpected-prefix rejection and upstream status/streaming.
This is a small counterbalanced probe; cache/host variation still affects timing.

| Arm | Input tokens for case 1 | Accepted intents | Narrow passes | Full-meaning passes | Total parsing time |
| --- | ---: | ---: | ---: | ---: | ---: |
| Original 1 | 2,071 | 7/10 | 3/10 | 0/10 | 68.796s |
| Compact 1 | 1,448 | 5/10 | 1/10 | 0/10 | 59.092s |
| Compact 2 | 1,448 | 5/10 | 1/10 | 0/10 | 55.660s |
| Original 2 | 2,071 | 7/10 | 3/10 | 0/10 | 67.167s |

The compact candidate uses 30.1% fewer input tokens, but **must not ship on these
results**. Dramatic/melancholic/aggressive sometimes move from genre to mood,
yet remain unnecessarily essential. Vocal exclusions, degree and units still
fail. Classical instrumentation now fails grounding, and the formerly accepted
early-Aerosmith case fails. The running case invents 1990–2023 composition and
release restrictions, despite no requested period; these pass current validation.
It also turns “picks up the pace”/“cools down” into mood values `up`/`down`.

Both compact repeats produced identical normalized intents and error lists.
This experiment rejects a prompt-only shortcut and identifies a temporal
grounding gap. It does not prove that a future protected-atom prompt or a larger
model cannot improve interpretation. No pre-LLM dictionary implementation was
tested; its benefit remains a proposed, separately measurable change.

## Enhanced Hybrid replay and its limits

Every accepted original normalized intent was replayed unchanged, including
ones that failed semantic expectations: six in run 1, seven in run 2. The
remaining four/three requests had no valid intent and were not silently repaired
or excluded from the overall ten-case failure accounting.

Native CLAP ran against **copies** of the existing eight compatible derived
preview recordings and the frozen DSP/MERT evidence snapshot. The catalog
contains 956,917 tracks, version `1:956917:1788613313`. No online MusicBrainz or
AcousticBrainz lookups and no new previews were enabled. The `-cached-audio-only`
resolver explicitly rejects new preview acquisition; source audio was not read
or retained. MERT representations were reused, not recomputed.

Both replay runs returned **zero tracks across all replayable cases**, and both
commands correctly exited with failure. This documents a coverage/contract
bottleneck in the fixed sparse setup. It cannot establish that live Enhanced
generation would return zero tracks or attribute the result solely to the LLM,
dictionary, CLAP or MERT. A paired reviewed-intent replay plus an adequately
covered authorized preview pool is required to separate those causes.

The native bundle used was `clap-music-speech-fp32-v1-3c41596002da61be`, the
existing music-and-speech model. Its paired identity, catalog and record hashes
remain enforced. These results do not test the recommended music-only bundle
or resolve its separate embedding-discrimination concern.

## Reproduction and retained evidence

Artifacts remain in `bin/enhanced-translation-investigation/` (ignored by Git):

- `run-parser.ps1`, `native-parser/`: current binary build, two native parse
  reports, logs and owned-server lifecycle.
- `run-ablation.ps1`, `prompt-proxy.go`, `prompt-proxy.exe`, `original-system.txt`,
  `compact-system.txt`, `original-1/2`, `compact-1/2`: exact experimental inputs,
  forwarded requests, report JSON, tokenizer measurements and logs.
- `run-replay.ps1`, `replay-input-1/2.json`, `replay-prompts-1/2.json`,
  `enhanced-replay-1/2.json`, `replay-1/2-derived/`: accepted raw intent replays,
  isolated cache copies and failure evidence.
- `summarize.mjs`, `manifest.json`: request-mutation checks, summary generation,
  source/report hashes, model/runtime identity and native compiler environment.

From the repository root after normal native setup:

```powershell
go build -o bin/musiccheck.exe ./cmd/musiccheck
.\bin\musiccheck.exe -parse-only -server-url http://127.0.0.1:PORT `
  -catalog "$env:APPDATA/playlist-ai/catalog" `
  -prompts docs/data/enhanced-translation-prompts-v1.json `
  -output bin/new-translation-baseline.json

# Recorded owned-server experiments; retain nonzero exits and report JSON.
.\bin\enhanced-translation-investigation\run-parser.ps1
.\bin\enhanced-translation-investigation\run-ablation.ps1
.\bin\enhanced-translation-investigation\run-replay.ps1
node bin/enhanced-translation-investigation/summarize.mjs
```

The local scripts contain the actual installed paths and are an audit trail,
not newly shipped contributor commands. For reruns, choose fresh output
directories and update paths to installed compatible assets. Do not overwrite
the recorded comparison or copy a live SQLite cache without a safe snapshot.

Existing focused rules/schema, audio/provider and selector/spacing tests passed
during the code audit; the native musiccheck binaries and proxy compiled.
The six native ten-case runs and both replay runs **failed acceptance**, as
reported above. No full CI gate or hosted CI was run for these documentation
and research-only artifacts. No application production code was changed.
