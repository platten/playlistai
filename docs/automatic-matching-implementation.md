# Automatic musical matching upgrade

Automatic v8 / fit v5 separates estimated musical character from strict
requirements. This implementation extends the existing prepared catalog,
retrieval, evidence ledger and native desktop boundaries. It does not establish
that the historical 40-prompt quality gates now pass.

## Behavior and compatibility

Ordinary mood, instrumentation and texture criteria with explicit `essential`
or `preferred` strength can use estimated evidence. Complete phrases such as
“soft piano” and “spacious reverberation” remain complete model queries. `required`
criteria, strict exclusions, defining genres, dates, artist-only restrictions
and required recordings keep their admission rules. Historical essential criteria
with no strength keep conservative semantics. Other recommendation modes retain
their separate policies.

Fresh ordinary references preserve `preferred` strength from source extraction
through schema v13, core intent and generated bridge bindings. “Like Radiohead”
can discover catalog recordings through pinned artist relationships or compatible
seed audio; “Radiohead only” still requires the artist identity. Missing historical
reference strengths retain the previous graph-plus-calibrated-audio gate. Frozen
history replay continues to use the original result and evidence versions.

Numeric CLAP and specialist outputs are retained as uncalibrated observations.
Their ranks are compared only within the same model and complete clause. Reference
channels also combine ranks instead of the maximum score across incompatible
spaces. Estimated scores never change a strict evidence state. Prepared model
fingerprints and sampled coverage travel with the saved evidence ledger.

Journey scopes receive separate bounded retrieval opportunities. Candidates use
stage-specific scores and must fit their best estimated stage; the existing
sequencer still enforces ordered stages, required waypoints and deduplication.
The Generate summary separates musical character from strict requirements, and
Playlist shows “Best estimates — some qualities are unconfirmed” for a full
estimated result. Partial counts stay visible. Settings explains these rules.

## Preparation and data

Every prepared row's cached identity conflict is checked before a sufficiency
probe or optional inference. The probe uses the actual selector and sequencer
without emitting suggestions. It stops acquisition when the prepared pool meets
count/duration, required recordings, strict constraints, diversity and journey
obligations. Required recordings lead the acquisition order. Missing strict
subjective evidence cannot trigger fresh audio unless a valid literal calibration
could apply to the installed model. Existing provider budgets, cancellation,
stop-and-keep, the outer two-minute deadline and final immutable assembly boundary
remain in force. Existing opt-in phase timings remain separate from deterministic
saved search fingerprints; saved stopping reasons distinguish obligations met,
evidence exhausted and interrupted preparation.

[Prepared jobs](prepared-music-jobs.md) derive a broad artist plan from catalog
identities and genre strata, resume immutable batches, retain provider provenance
and measure exact recording joins independently of the development prompts.
Preparation v2 quarantines conflicting recording identities across batches,
removes their graph references and reports the original observations. Explicit
reuse into a new v2 job preserves the earlier raw checkpoints and their hashes.
Optional classifier evidence is stored in the checksummed pack metadata and is
preserved through pack combination. Older packs simply have no such evidence.
The added installed-data limit is 10,000,000,000 bytes; the job documentation
states the runtime and preparation accounting scope.

[Discogs-EffNet preparation](discogs-effnet.md) runs the official encoder once per
sample and preserves all class scores from the instrument, vocal, relaxed-mood
and style heads. All heads sharing that encoder count as one evidence family.
The desktop can download the pinned original ONNX encoder and heads and analyze
observed previews locally. The standalone playlist-indexer can run the same
native model over sampled local audio as a resumable `effnet` stage and export
its provenance and coverage in a paipack. Linux synthetic-fixture parity has
been checked; Windows and macOS native execution remains untested. Original
model notices and permitted source/derived terms remain explicit. CLAP and
optional MERT stay available.

## Validation and measured limits

Before editing, the current dirty worktree was archived outside the repository.
The existing reco, intent, core, audio and bridge baseline tests passed. A matched
[synthetic receipt](data/automatic-matching-policy-2026-09-27.json) uses identical
four-recording catalog identities, clause scores, strengths, seed 42 and empty
starting stores in the archived v7 source and v8 source. Ordinary “soft piano”
changed from 0/2 to 2/2 estimated results; both `required` and ambiguous historical
strength controls remain 0/2. The ordinary v8 run stopped before the fake blocking
provider; the receipt records its measured timing. This verifies admission and
scheduling, not musical quality or application latency.

The [independent-label baseline](automatic-matching-evaluation.md) actually ran
installed CLAP on eight checksum-verified MTG development recordings. The new
frozen comparison harness preserves complete Song Describer human captions,
known MTG labels and CCMSim rated intervals, audits overlap, and requires a positive
paired 95% confidence interval on a declared primary metric before replacement
review. Unlabelled facets remain unknown. Estimated-result coverage does not
substitute for strong-match precision.

The [classifier smoke measurements](discogs-effnet.md) run all four official heads
on eight seconds of synthetic PCM and distinguish fresh inference, repeated
inference and cached preparation. They verify the model path and resource cost,
not music classification quality. A 128-seed public preparation produced 21,747
recordings and 1,323 artists in 19.46 MB, with 2,245 exact recording identities
joining 3,005 catalog rows. Nine cross-batch identities were quarantined; eight
checkpoint batches resumed offline into identical bytes. This measures metadata
coverage, not new audio coverage, and is not a published broad music release.

The [native runtime receipt](data/automatic-matching-runtime-2026-09-27.json)
records separate cold-provider-cache and warm replay observations for two fixed
requests; host-specific model cache paths are redacted. The installed public
pack's legacy CLAP vectors are incompatible with
the active original-LAION model; they are correctly kept separate. The detailed
description cold run exhausted its eight-second local retrieval budget with no
candidates. Its warm replay returned 9/10 estimated tracks through other
retrieval and preview evidence, reaching the preparation deadline. This is a
remaining data/retrieval limit, not evidence of a broad speed improvement.
The Radiohead discovery request returned 10/10 tracks from ten other catalog
artists in both runs and stopped at `prepared_obligations_met`. Its 12.711-second
cold result included 10.824 seconds of intent preparation; the 1.054-second warm
run reused that frozen interpretation. The total difference therefore cannot be
attributed solely to provider caches. Neither run verifies musical resemblance.

No MuQ weights or validated native export were available through the verified
asset paths. No matched MuQ quality comparison, positive paired quality interval,
full 40-prompt rerun or native Windows/macOS run is claimed. CLAP remains enabled.
Broad compatible permitted audio preparation, release publication, held-out model
comparison and native head/export validation remain rollout work, with runnable
preparation/evaluation commands provided.

## Software checks

Focused regressions cover ordinary versus required and historical strengths,
full-phrase scores, genre/exclusion/artist-only gates, model compatibility,
reference discovery without artist UUIDs, independent journey stages, cached
identity conflicts before early stopping, calibration-aware acquisition,
classifier family deduplication, immutable history and callback behavior.

The complete `./scripts/test.sh` gate passed:
shell checks, binding generation, frontend typechecking, 276 frontend tests,
production build, Go vet, pure-Go compilation, race-enabled Go tests and
golangci-lint (zero issues). The evaluation helper passed 11 tests; the classifier
helper passed four offline tests with its explicit real-model test skipped in the
normal suite (the opt-in model run passed separately). The existing audio
calibration helper passed three tests. `git diff --check` passed.

Rendered checks passed using the existing mocked bridge scripts in dark/light themes at
390/1000 pixels for Generate and Settings and 390/1100 for Playlist. They exercise
submission timing, stop-and-keep, cancellation and stale response handling,
navigation, identity corrections, lossless seeds, evidence details and uncertainty
labels. Dark 390-pixel Playlist, dark 1000-pixel Generate and light 1000-pixel
Settings captures were visually inspected. These browser checks do not establish
native-host behavior. Captures are outside the repository under
`/tmp/playlist-ai-matching-ui-after/`; the gate log is
`/tmp/playlist-ai-matching-gate-final.log`.
