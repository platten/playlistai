# Original 40-prompt quality audit

Status: all 40 baseline and all 40 frozen-treatment cases have completed web
audits, including every returned song. The frozen treatment did not improve
observed match quality or useful coverage. Its stricter evidence requirements
removed many unsupported recommendations, but also left most requested slots
empty. All 24 approved eight-prompt matched controls have completed native runs
and web audits. The later workspace fixes passed their repository gate. Four
preselected diagnostics on a separate build returned zero songs across forty
requested slots: three deadline errors and one empty partial result. A further
opt-in progress trace also returned no songs at the deadline and located the
last reported phase at final assembly. These diagnostics do not replace the
original benchmark or demonstrate improved quality.

| Measure | Baseline | Frozen treatment |
| --- | ---: | ---: |
| Requested slots | 400 | 400 |
| Returned songs | 219 | 61 |
| Good / partial / mismatch / unknown | 97 / 62 / 47 / 13 | 16 / 29 / 14 / 2 |
| Good matches per requested slot | 24.3% | 4.0% |
| Good matches per returned song | 44.3% | 26.2% |
| Unfilled slots | 181 | 339 |
| Operation failures | 7 | 7 |

These are AI web assessments, not human listening scores. The raw runs use
different seeds and five-minute versus ten-minute generation budgets, so they
do not isolate a causal implementation effect. The matched controls use the
same baseline-derived typed request and seed across their three arms.
The [forty-case results table](forty-prompt-results-2026-09-26.md) retains every
empty case alongside the returned-song counts and web grades.
The [searchable song audit](forty-prompt-song-audit-2026-09-26.html) shows all
280 returned occurrences, their exact web judgments, recording caveats and
712 source references. It works as a standalone local HTML file.

The first live treatment attempt stopped after two completed empty results
exposed an artist-identity handoff defect. Its reports are preserved. The
corrected frozen source passed its repository gate and independent review; the
fresh full run lasted from 2026-09-26 16:02 to 23:34 UTC.

The requested suite is
`internal/evaluation/testdata/varied-prompts-v1.json`: 40 original prompts,
ten requested songs each, Enhanced hybrid, installed local Qwen parser, and
free online sources. The separate held-out relevance-family fixture is not used
for tuning these fixes.

## Completed matched controls

All eight complete case triples use the same baseline-derived typed intent
and lossless seed in B5 (baseline, five minutes), T5 (frozen treatment, five
minutes) and T10 (frozen treatment, ten minutes).

| Case | B5 good / returned | T5 good / returned | T10 good / returned | Finding |
| --- | ---: | ---: | ---: | --- |
| 2: Electronic | 2 / 10 | 1 / 1 | 1 / 1 | Both treatment budgets return the same Röyksopp Lo’99 remix. Doubling the budget adds no supported song in this pair. Baseline has five partial matches and three mismatches, with Alice Cooper occupying five slots. |
| 10: Fela Kuti / Tony Allen | 0 / 0 | 0 / 0 | 0 / 0 | Every arm retains the baseline's unresolved, truncated Tony Allen candidate set and requests clarification. These controls do not measure the newer parser's identity recovery. |
| 14: Bossa nova / samba / Brazilian jazz | 3 / 10 | 0 / 0 | 0 / 0 | Baseline also has six partial matches and one unknown; complete genre coverage is unverified. Every arm retains the baseline's three separate essential genres rather than the newer playlist-coverage group. |
| 17: Spacious electronic | 0 / 10 | 0 / 0 | 0 / 0 | Baseline has four partial matches and six mismatches. A 94-minute continuous mix accounts for two-thirds of total playlist duration. Neither treatment budget finds an eligible recording. |
| 21: Ambient → downtempo → melodic house | 0 / 0 | 0 / 0 | 0 / 0 | All arms preserve the baseline's mistaken artist references and request clarification. This fixed-input control cannot measure the newer genre-journey parser. |
| 23: Brian Eno → Boards of Canada → Jon Hopkins | 0 / 0 | 0 / 0 | 0 / 0 | All arms request clarification. The fixed input retains mistaken Brian and Other references, alongside the separate Boards of Canada ambiguity. |
| 25: Instrumental / no vocals | 1 / 2 | 0 / 0 | 0 / 0 | Baseline returns one good and one partial match, leaving eight slots empty and the requested blend and arc incomplete. Treatment cannot corroborate all required genre and strict criteria. |
| 40: Radiohead only | 0 / 0 | 0 / 0 | 0 / 0 | All arms request clarification from the inherited Radiohead / On a Friday alternatives. Fresh parsing is outside this control. |

Across all 80 requested slots per arm, B5 returns 32 songs: six good, sixteen
partial, nine mismatches and one unknown. T5 and T10 each return the same one
good song, leaving 79 slots empty. The paired change is five fewer good matches
under T5 than B5, and no additional good match under T10 versus T5. The treatment's
one-of-one returned-song precision cannot establish general precision; supported
requested slots fall from 7.5% to 1.25%. No control has an operation failure.
Four cases clarify in every arm, while two generation cases preserve older
genre conjunctions. These are exploratory, fixed-input comparisons with live
providers, not measurements of the current parser or isolated performance gains.

Each cell has ten requested slots. Electronic generation took 285,286 ms in B5,
285,025 ms in T5 and 585,031 ms in T10; all three completed without an operation
error. The baseline's nonzero process exit reports adjacent-artist findings.
The extra 300,006 ms in T10 considered 84 more candidates but returned no
additional song. These are exploratory, fixed-order observations for one case,
not a general conclusion about search budgets. The
[portable controls](data/forty-prompt-audit-2026-09-26/controls.json)
retain all 80 requested slots per arm, with no pending or unaudited slots.

Case 14 took 285,252 / 285,014 / 585,018 ms in B5 / T5 / T10, with no operation
errors. Treatment considered 57 candidates at five minutes and 141 at ten
minutes, with none eligible. Its fixed input preserves the earlier per-track
conjunction, so these controls cannot measure the newer collective-genre parser
behavior. The web rubric still assesses the original playlist-wide request.

Case 17 took 285,330 / 285,023 / 585,032 ms in B5 / T5 / T10, without
operation errors. Treatment considered 56 / 148 candidates, with none eligible.
Case 21's completed generation timings are 44,491 / 55,590 / 52,737 ms;
its nonzero process exits report the journey assertion, not operation failures.
Its frozen request contains the earlier ambient/Downtempo/Melodic artist anchors.
Case 23 took 38,350 / 41,855 / 33,249 ms, without operation errors or assertion
findings. Brian Eno and Jon Hopkins resolve, but the inherited additional
references and two Boards of Canada candidates prevent generation.
Case 25 took 285,051 / 285,016 / 585,027 ms without operation errors.
Treatment considered 61 / 145 candidates, with none eligible at either budget.
Baseline's good match is Sofiane Pamart's “SOLITUDE”; Einaudi's “In Limine”
remains partial because not every requested property is independently confirmed.
All three fixed case-25 inputs retain separate essential modern, classical,
ambient and electronic genres, with no coverage group. Every treatment candidate
has unknown evidence for all four. The strict instrumental/no-vocals clauses
also have no matches: each has 45 unknown and 16 mismatches in T5, and 109 unknown
and 36 mismatches in T10. Either evidence deficit independently prevents
admission; the zero output does not isolate the vocal policy or measure the
newer collective-genre parser.
Case 40 took 7,435 / 11,389 / 15,335 ms, without operation errors, timeouts or
assertion findings. The original native queue closed successfully at
2026-09-27 01:45:40 UTC. Its listening packet is prepared with blank human grades;
no human listening comparison has been performed.

## Completed later-build diagnostics

Cases 6 (Daft Punk), 7 (Radiohead including other artists), 8 (Nina Simone and
Bill Withers) and 11 (Massive Attack's “Teardrop”) ran serially on a separate
build after its repository gate and independent review passed.
They retain their original prompts and web rubrics, with fresh stores. Actual
seeds are recorded when initialized; a failed run may not retain one.
All four cases are complete and audited: zero returned songs, forty missing
slots and three operation failures. These diagnostics examine the
combined later build and cannot isolate one repair or replace the original
forty-case comparison. The [separate supplementary package](data/forty-prompt-supplementary-2026-09-27/summary.json)
retains the exact child timings, seeds, errors and original rubrics. With no
returned songs, there are no song verdicts or returned-song precision estimate.

| Case | Returned / requested | Web result | Completed generation |
| --- | ---: | --- | ---: |
| 6: Daft Punk | 0 / 10 | No songs to grade; deadline error | 600,002 ms |
| 7: Radiohead including others | 0 / 10 | No songs to grade; deadline error | 600,016 ms |
| 8: Nina Simone / Bill Withers | 0 / 10 | No songs to grade; partial search, no operation error | 598,606 ms |
| 11: “Teardrop” | 0 / 10 | No songs to grade; deadline error | 600,000 ms |

Case 6's exact completed child and parent report agree. Parsing took 8,675 ms
and reference resolution 3,559 ms; the remaining 587,768 ms cannot be allocated
to a specific operation from the saved report. Its seed is explicitly
uninitialized and its reproducibility fields are empty. This run does not repeat
the earlier 41,739-ms observed overrun but still fails to produce a playlist; it
does not establish the cause of that earlier overrun or a general timing guarantee.

Case 7 resolves the canonical Radiohead identity and retains the request to
include other artists, but also returns no songs before the deadline. Parsing
took 8,703 ms and resolution 1,502 ms; the saved report cannot allocate the
remaining 589,811 ms. Read-only inspection of case 6's isolated cache confirms
that reference analysis and candidate retrieval did occur. The failure path
does not retain the candidate assessment snapshot, so blank output cannot
establish that generation never began. Bounded, opt-in phase/count diagnostics
are now implemented and tested. The separate one-case trace below retains
progress despite the failed result.

Case 8 returns an explicit partial outcome after the search budget is reached,
without an operation error or watchdog termination. It retains the actual seed
`2375438183872585540` and records 581,162 ms in recommendation after parsing and
resolution. This run does not repeat the original incomplete-child watchdog
failure, but still fills none of the ten requested slots.

Case 11 resolves the original “Teardrop” recording but fails at 600,000 ms.
Its saved timings cover 8,236 ms of parsing and zero reported milliseconds of
resolution, leaving 591,764 ms unallocated. Its seed is uninitialized. The
isolated cache contains completed audio analyses for “Teardrop,” “Paradise
Circus” and “Angel”; the failed result has no final assessment/search snapshot.
Those cache records cannot establish that ranking consumed the analyses or
identify the blocked operation. These four cases do not demonstrate improved
musical quality or useful coverage.

The additional Daft Punk trace used a new frozen build and fresh store with
explicit `-diagnostics` enabled. Its recommendation policy remains v58; only
diagnostic collection changed after the four-case build. It has its own ten
requested slots and is excluded from every comparison above. It returned zero
songs, with ten missing slots and a deadline error at **600,001 ms**. The
completed child, parent report, executable and listening sidecar agree; its saved
seed is uninitialized. No song verdicts or human listening grades were assigned.
The [separate trace summary](data/forty-prompt-trace-2026-09-27.json) records
the outcome, safe counters, limitations and artifact hashes.

The trace retained 885 consecutive diagnostic entries, including 828 progress
events. At elapsed **585,000 ms**, the last progress entries report final assembly
with 128 candidates considered, 128 marked eligible, zero marked supported and
the search stopping reason `deadline`. The frozen call path confirms that
iterative collection returned and execution reached the final assembly call.
The eligible counter is an intermediate pipeline count, not 128 confirmed
musical matches or a completed playlist. No function-level assembly or cleanup
spans were captured, so the exact call consuming the remaining interval is
unresolved. Wall-clock log timestamps do not agree exactly with elapsed counters;
the completed-child measurement is the authority for generation duration.

Progress records are intact, but two large metadata/request diagnostic entries
were truncated by the existing per-entry bound, and the prompt diagnostic
contains a serialization error. These limit inspection of detailed inputs;
neither establishes the cause of the generation failure. The raw diagnostic
payload remains private. The
[evaluation guide](enhanced-relevance-evaluation.md) documents single-case
capture, bounded retention, private output and forced-shutdown limits.

## Remaining quality work and acceptance criteria

The implemented behavior is documented in [recording-evidence.md](recording-evidence.md).
Enhanced hybrid now treats imported genre labels as hints, requires independent
corroboration of defining genres, and omits unconfirmed candidates. Fresh mixed-genre
requests use playlist-wide coverage. The changes also preserve complete references
and musical wording, validate preview recording/version identity, align compatible
MERT ranking and sequencing evidence, and propagate cancellation through metadata
and acquisition work. Saved results and the other recommendation modes retain
their documented compatibility policies.

The current results support the following priorities. These are remaining work,
not features silently enabled in the frozen treatment or completed quality gains.

An independent count of all frozen treatment snapshots found 4,589 candidate
occurrences across the 29 cases that considered candidates; twenty of those
cases had no eligible candidate. The saved recording knowledge contains 41
publisher genre fields across twenty case snapshots and no `linked_statement`
claims. Cached recordings and claims can recur across cases, and a broad genre
field may not support the requested subtype. These are retained-evidence counts,
not HTTP attempts, distinct independent sources or catalog-wide coverage.
Four clarification cases retain empty search snapshots; seven failed cases have
no completed search snapshot. The verified measurement and its definitions are
in `raw40-recording-source-coverage.json`, with independent review receipt
`raw40-recording-source-coverage-independent-review.json`.

| Priority | Concrete change | Evidence required before adoption |
| --- | --- | --- |
| 0: completing reference requests | Add bounded start/end spans around final assembly substeps and deferred cleanup, using the existing opt-in diagnostic store, then reproduce and fix the measured bottleneck. Preserve completed work and report errors accurately. | The trace reaches final assembly at 585 seconds but does not identify the blocking call. Capture ranking, preparation, selection, sequencing, validation and cleanup durations using one elapsed-time origin. Require a failing regression for the identified defect and measure completion, partial results and cancellation; preserve the original failures. |
| 1: corroborated coverage | Extend recording-level sources through the existing verifier and cache; preserve identity, version, scope and provenance. Add opt-in bounded acquisition counters so attempts and failure categories are measured directly. | Demonstrate usable evidence on preselected recordings and supported requested genres, within the existing request/time budgets. The completed publisher probes did not demonstrate additional eligible genre recovery. Metadata counts are not request counts. |
| 2: reference retrieval | Build a separate, explicitly compatible preview-query encoder for the installed full MERT index, reusing the verified decoder, resampler, worker and neighbor search. | Decoder and numerical parity, cancellation and cache isolation, truthful preview coverage, and measured retrieval quality. Never relabel the existing preview vectors as full-source pack vectors. Available Linux assets do not establish other-platform support. |
| 3: general audio evidence | Calibrate genre/facet decisions on independently reviewed development recordings, with unknown and conflict outcomes retained. | Fixed splits, recording identity, model/preprocessing versions, measured false positives and misses, and subsequent held-out evaluation. Synthetic tests and raw cosine thresholds cannot supply the missing musical labels. |
| 4: recording format and variety | Preserve complete recording-performer credits and explicit live/remix/mixed-excerpt relationships through display/export; assess performer and album concentration within the supported pool. | Exact-identity ingestion/display/exclusion/diversity fixtures, versioned catalog migration where needed, and eligible-alternative measurements. Avoid blanket duration or artist limits that would break long works, only-artist prompts or required waypoints. |
| 5: subjective quality | Use the blind listening packet for similarity, texture, energy and transitions. | Actual human grades with missing outputs retained. Web judgments remain a separate evidence type. |

A successful follow-up must improve supported matches per requested slot at a
comparable budget, without restoring the demonstrated hard-constraint violations
or false verification claims. Higher precision achieved only by returning empty
playlists is insufficient. Keep raw end-to-end behavior, fixed-input controls,
coverage, latency and failures separate; the current exploratory suite alone
cannot establish a general listening-quality improvement.

## Reproducible baseline

The baseline preserves the working tree at the start of implementation on
`feat/preindexed-paipack`, base commit
`7b608bfccc5543b7221dce4db9000845a670ec13`. It includes the existing uncommitted
work, rather than treating that commit alone as the baseline. Only the corrected
evaluation harness was overlaid; production recommendation code was frozen.

Local artifacts are under `/tmp/playlist-ai-forty-quality-tvrgkat4/`:

- `source-manifest.json`, `initial.patch`, `initial-status.txt`: starting state.
- `baseline-source/`, `baseline-harness-manifest.json`: frozen source and harness.
- `run-baseline.sh`, `baseline-run-manifest.json`: executable run conditions.
- `baseline-raw.json` and its `.cases/` directory: actual desktop outcomes.
- `web-audit/baseline-NN.json`: per-occurrence web judgments, sources, identity
  limitations, search attempts and playlist-level findings.
- `blind-listening-packet.json`: pooled original baseline/treatment listening
  worksheet, blinded to variant and engine scores. Artist/title identities remain
  visible; all human grades are blank. Its answer key is stored separately.
- `treatment-source-manifest.json`, `treatment-run-manifest.json`: integrated
  treatment frozen at 2026-09-26 13:16 UTC; executable SHA-256
  `f064681d236dac2d3802730b45148175b3ffc408b457fd536bdab0ca71c115ae`.
- `first-freeze-native-release.json`, `first-freeze-native-continuation.jsonl`:
  initial queue launched at 13:17 UTC, then intentionally stopped at 13:26 UTC
  before any treatment/control process launched. A newly confirmed alphabetical
  discovery cutoff required a replacement treatment freeze.
  The original baseline is unchanged.
- `treatment-source-manifest-v2.json`, `treatment-run-manifest-v2.json`: reviewed
  replacement with strict genre/vocal admission, deterministic discovery ties,
  source-recognition fixes and proof-dependent runtime capability status.
  Its executable SHA-256 is
  `2452ec25f3a439244099bdb136e59eb65e31cd8aecde98b2a47852081e33aa2a`.
  The source snapshot contains 1,348 files; 158 differ from the preserved initial
  working tree. The final combined gate and independent reviews passed.
- `treatment-v2-incomplete/`: archived release, journal and reports from the serial queue
  started at 14:12 UTC. At 15:04:08 it verified all 40 baseline child reports and
  closed-process identity, then launched treatment at 15:04:09. Baseline report
  SHA-256 is `86073f7ad5966e00edacca33db03eba540ec1a806cd9a75826bc70d218a8b720`.
  The original parent exit code is unavailable and remains null; case failures
  are preserved. The two completed cases returned no tracks after 585,030 and
  585,081 ms. Case 3 was intentionally canceled; all native descendants stopped.
  This interrupted attempt is diagnostic evidence, not a full treatment result.
  A fresh store was rebuilt from the original immutable asset template.
- `treatment-source-manifest-v3.json`, `treatment-run-manifest-v3.json` and
  `native-release.json`: corrected treatment, 1,355 frozen files and 165 changes
  from the preserved initial working tree. Source manifest SHA-256:
  `a907db2a6075896410942348bddf4df8bb11ac2192948cd9d238dcfd781b1e44`.
  Executable SHA-256:
  `6a4a722cd314f88bc6c31e72ce4ff356ddaae4cee98939b6e2120709b57b1961`.
  The new runner reverified the unchanged baseline and started the full treatment
  at 16:02:13 UTC. The preselected controls follow serially after its reports verify.

The baseline uses its original five-minute cap. Treatment uses the ten-minute
policy; an explicit five-minute treatment control is needed to distinguish
architecture effects from extra search time. Stores contain fresh writable
evaluation data and the same installed immutable assets. No private taste/history
was copied. Evaluation work must not overwrite the normal user store.
The raw suites generate their own seeds; their end-to-end difference also includes
seed variation and live source conditions. Only the extra controls hold the typed
intent and lossless seed fixed across implementations and time budgets.
The host also runs bounded offline regression checks during this work. Observed
latencies are operational measurements, not isolated performance benchmarks.

The recorded CLAP capability was available but uncalibrated for general musical
fit in cases 1–5 and 25 (`generalFitAvailable=false`). Preview similarities can
rank candidates, but cannot corroborate genre membership under this policy.
Vocal screening remains separate and cannot prove absence across an entire
recording. The read-only receipt is `recorded-audio-capability-diagnosis.json`.

## Completed baseline findings

All 40 cases and all 219 returned occurrences have completed web reviews:
97 good, 62 partial, 47 mismatches and 13 unknown. Eighteen cases returned no tracks
(seven timeouts and eleven clarification outcomes), and case 25 returned nine,
leaving 181 unfilled requested slots. Good matches comprise 44.3% of returned songs
and 24.3% of all 400 requested slots. These are AI web judgments, not listening
results. Broad
genre membership does not establish that a playlist has good artist diversity,
transitions, or the correct recording edition.

The baseline's own fit labels also overstate certainty. Its 81 “strong” tracks
received 65 good, 12 partial and four mismatch judgments. Its 138 “close” tracks
received 32 good, 50 partial, 43 mismatch and 13 unknown judgments. Engine labels
concern parsed criteria; the web review grades the original prompt. Dropping
close results would remove many errors and some good matches, which is why the
treatment also needs better evidence acquisition rather than a label filter alone.

The [portable baseline audit](data/forty-prompt-audit-2026-09-26/baseline.json)
contains all 40 prompts, every returned occurrence, source links, recording/version
caveats and run receipts. It contains no personal history, credentials, downloaded
assets or raw provider responses. The remaining raw diagnostic artifacts stay in
the local audit directory.
The [current comparison](data/forty-prompt-audit-2026-09-26/comparison.json) and
[package manifest](data/forty-prompt-audit-2026-09-26/manifest.json) retain unrun,
unfilled and unaudited slots separately and explicitly report incomplete work.

| Finding | Observed trigger | Implemented remediation |
| --- | --- | --- |
| Wrong genre admitted as verified | Electronic playlist includes The Hollow, Combination and Run to Me; jazz includes Rats in the Cellar | Flattened `AB:GENRE`/`AB:MOOD` predictions remain hints and cannot validate a requirement |
| Imported tags mistaken for independent corroboration | Several Bad Bunny tracks carry House tags despite differing recording-specific musical evidence | Ordinary genre tags, copied sidecar labels and text-index matches remain hints; applicable independent evidence is needed for supported fulfillment |
| Engineer counted as artist | Rubinstein recording displays Richard Gardner, while raw roles identify him as recording engineer | Preserve typed roles; narrow role-based display correction and performer-aware diversity |
| Composite artist bypass | Multiple Rubinstein credit strings allow adjacent performances and excessive concentration | Structured overlapping performer identity in selection and sequencing |
| Recording artist IDs lost in acquisition | Local typed MusicBrainz artist IDs were absent from verifier input for composite credits | Forward validated recording-credit IDs and preserve compatible membership through metadata merges; never align unordered IDs with names |
| Unreliable free-text proof | Exact quotes can still describe another song, qualified negation or one excerpt | Local-model quotes are non-decisive hints; ambiguous extractions rejected |
| Incomplete runtime diagnosis | Readiness could refer to rules rather than the requested parser; later failures could lose parser status | Requested-backend readiness and per-case actual backend/fallback, separate outcome categories |
| Non-musical wording resolved as an artist | “including other artists” introduces an artist named Other and requests clarification | Contextual recognition excludes that determiner; a unique complete canonical artist match outranks an alias, while genuine ambiguity and explicit predecessor recordings remain supported |
| Correct artist absent from clarification | The intended Tony Allen is eleventh among twelve namesakes in the installed index, beyond an eight-candidate presentation cutoff | Retain the bounded 64-candidate identity set independently of the shorter model presentation; preserve genuine ambiguity and source truncation |
| Artist context discarded before asking for a choice | Fela Kuti has performance credits on two recordings billed to the intended Tony Allen | Bounded recording-credit association can corroborate an explicit same-playlist identity, preserving candidates and cited source hashes; the submitted UI preview shares that evidence with generation |
| Early-alphabet artists dominate retrieval | Exact baseline electronic profiles are the alphabetically first twelve of a much broader indexed artist set | Break equal-relevance ties deterministically using the request seed and stable identity before bounded profile/seed truncation; preserve higher relevance and explicit references |
| Genre journey becomes namesake artists | Ambient, Downtempo and Melodic replace explicit genre stages and trigger clarification | Protect typed genre stages unless explicit artist wording/quoting applies; correct quote pairing and retain sentence-final identity candidates |
| Negative mood becomes an artist exclusion | Installed-index preflight turns “not sleepy” into a hard exclusion of thirteen Sleepy identities | Preserve negative musical source spans, including canonical instrumental wording; explicit artist wording and quotation still work |
| Collective genre coverage lost | Each recording is tested against every genre in a requested mix, while a naive OR would permit an all-house playlist | Preserve explicit playlist coverage groups, reserve supported genre examples, and check every requested genre in the final result |
| Spoken word admitted despite no vocals | A voice classifier assigns near-certain instrumental support to The Last Poets; a separate regression admits preview-only absence | Treat native absence labels as weak hints; enforce reconciled strict clauses after acquisition and again at output, preserving OR alternatives |
| Static capability warning outlives verified evidence | Source-parsed no-vocals request remains partial despite cited whole-recording absence | Supersede only the exact registry warning during proven per-track assessment; preserve unrelated unsupported requirements and the original serialized intent |
| Time limit and replay ambiguity | Old fixed limit and provider/version changes prevent reproducible comparison | Shared ten-minute cap, recorded actual limits and validated frozen-result replay |

Baseline cases 6 and 8 returned deadline errors after 319,186 ms and 493,267 ms
respectively, exceeding the nominal 300-second generation budget. Both wrote
completed child reports; these are generation timings, not startup-inclusive
watchdog fallback timings. A separate process watchdog was configured to cap
startup plus generation. Investigation reproduced cancellation gaps
in SQL reference resolution, artist-index iteration, waiting for the enhanced
operation mutex, and stopping prefetch while scheduling held its mutex. Those
paths now respect cancellation, with focused regressions. These reproductions do
not establish which path caused either observed baseline overrun; native treatment
measurements are still required before making a strict runtime claim.

The Bee Gees' [official release description](https://www.beegees.com/releases-archive/to-whom-it-may-concern/)
identifies Run to Me as a ballad. The audit found its electronic classification
came from imported classifier output, despite ordinary Pop metadata. The same
source issue occurs in the other named examples. Exact citations and
recording/version caveats are retained for each occurrence in the audit files.

## Remaining evaluation and quality work

The interrupted treatment attempt returned zero tracks for classical and
electronic prompts that had returned ten baseline tracks each. This is a source
coverage failure, not an improvement claim. An offline reproduction confirmed
that recording-credit artist IDs were lost before verification; forwarding those
IDs alone cannot manufacture missing musical evidence. A separate regression
showed that failed identity searches could exhaust the verification shortlist.
The corrected scheduler retains the 32-attempt ceiling while limiting optional
identity searches to 16, leaving opportunities for already identified recordings.
The live reports did not retain exact verifier-attempt counters, so the regression
does not establish that this quota was exhausted in either empty case.

A bounded source pilot adds exact MusicBrainz-linked Apple song metadata.
An opt-in diagnostic for the unchanged local Air — Don't Be Light recording
completed in 1.523 seconds with three public requests: recording, exact release
track, and publisher song lookup. It retained the release-track identity proof
and acquired the publisher's Electronic classification. The
[specific Apple lookup](https://itunes.apple.com/lookup?country=gb&id=1589310682)
and [recording link](https://musicbrainz.org/recording/464dd116-69ef-43db-954a-f7398317f45b)
support this one example. This is not a coverage estimate or proof of playlist
improvement. The two sampled classical recordings yielded no new decisive genre
source, and album/work genres remain scoped to their own entities.

The first completed v3 case again returned zero classical tracks after 585,026 ms.
Its saved search considered 137 candidates, all with unknown classical support.
There were 247 unique claims on 28 fact-bearing recording identities, including
two publisher genre fields reading Holiday. None of the ten baseline selections'
catalog IDs or MusicBrainz recording identities appeared in this candidate pool.
This exposes both missing corroborated genre coverage and limited retrieval
coverage; it is not a same-recording before/after test. The raw case seeds differ.

The second v3 case returned one electronic recording after 585,040 ms:
RÜFÜS DU SOL — Belong, the original 2024 *Inhale / Exhale* version. Its
[exact song page](https://music.apple.com/us/song/1766410909),
[official release tracklist](https://rufusdusol.store/products/inhale-exhale-vinyl)
and [independent album review](https://www.allmusic.com/album/inhale-exhale-mw0004378884)
support a good broad electronic match. Nine requested slots remain empty. The
saved pool contains 161 candidates, one eligible and 160 with unknown electronic
support; 30 recording identities retain 147 unique claims, including three
publisher genre fields. The baseline had four good electronic matches among ten
returned songs, so this early result improves the fraction of good returned
songs while reducing the number of good requested slots. It is not an overall
quality improvement claim.

A read-only source check on those first three completed cases found exact Apple
recording links for only 3/28 classical, 3/30 electronic and 1/28 jazz fact-bearing
recordings. The linked classical classifications were Holiday; the linked jazz
collaboration's cached classification was Alternative. Both excluded collaborations
also had feature-credit title differences. Removing the single-performer guard
alone would therefore add no observed classical/jazz support. These denominators
cover acquired facts, not every considered candidate or the whole catalog.

The follow-up feasibility study froze four classical/jazz selections before
browsing and made twelve metadata attempts without substitutions. It confirmed
two cached release-track mappings but established no publisher album/song ID and
no useful new genre field. Inaccessible sources and incomplete relationships
limited the probe; this does not prove that such metadata is absent elsewhere.

A second bounded probe froze three different baseline-good recordings before
browsing. André Watts's Hungarian Rhapsody No. 13 had no linked publisher path.
Helloween's exact release led to an individual song object, but its classification
was only Rock, insufficient for the metal request. Alborosie's linked release
was unavailable; a barcode lookup found song-level Reggae fields in two other
editions without an explicit matching edition link. This did not demonstrate a
policy-eligible recovery. No source adapter or request-quota expansion followed.
The attempted identities, response hashes and limitations are retained in
`post-freeze-publisher-feasibility.json`; this three-recording diagnostic cannot
estimate catalog coverage. An exact release-to-song extension remains conditional
on a preserved identity chain and useful recording-level evidence.
The inspected [Rubinstein release](https://musicbrainz.org/release/3600d255-9ce7-4b03-80fc-335e8cbbb160)
had Discogs/Amazon links but no Apple relationship. A Richter release-track
duration also differed from the recording duration by eight seconds, requiring
edition/segment checks. Exact attempts and hashes are retained in
`release-song-feasibility-selection.json` and `release-song-feasibility.json`.

The classical/jazz samples retain release-track IDs for 26/28 and 28/28 recordings,
but useful publisher access through those links remains unestablished. Defer this
source expansion until an exact positive example supports it. An extension would
require preserved release-track, recording
and publisher-song identity; a song-level genre field; existing request/cache
budgets; and negative checks for editions, repeated movements, performer conflicts,
live/remix versions, duration and ISRC conflicts. Album-wide genres would remain
context. Record bounded acquisition outcomes before changing quotas, because the
current snapshots cannot distinguish skipped attempts from provider failures or
missing links. These are evidence-backed follow-up criteria, not implemented
source expansion or measured treatment gains.

The first five genre prompts are now complete and web-audited: treatment returned
0 classical, 1 electronic, 0 jazz, 0 reggae and 0 metal songs. Its one returned
song is good and 49 slots remain unfilled. The corresponding baseline cases had
42 good matches among 50 returned songs. This is a substantial coverage regression,
even though the retained treatment song is supported. The metal case acquired two
publisher fields reading Rock, which does not establish the narrower metal
criterion; the jazz and reggae cases acquired no decisive requested-genre claims.
The completed raw-suite and matched-control comparisons are above.

Treatment case 6, inspired by Daft Punk, failed with `context deadline exceeded`
and zero tracks after **641,739 ms** of generation, 41,739 ms beyond its configured
600-second cap. Its completed child report proves this is generation time;
startup-inclusive process time was separately reported as 12m12s. Parsing used
the requested local model without fallback; saved parse/resolve timings were
9,495/3,819 ms. The resolved request survives, but no completed search or recording
evidence snapshot does. The bridge discards the playlist when generation returns
an error, so the absent snapshot cannot locate the blocking call. The live cause
is under investigation; this case must remain an operation failure, not a genre
rejection or ordinary partial result. The baseline also failed on this prompt.

Case 7 correctly resolved Radiohead and preserved “including other artists,”
removing the baseline's false Other/alias clarification. Generation nevertheless
failed after **613,601 ms**, 13,601 ms beyond the configured limit, with no tracks.
The retained parse/resolve stages took 8,966/1,390 ms. As in case 6, the missing
completed search snapshot does not identify the live blocking call. Across seven
completed treatment cases, one good song is returned, 69 slots are unfilled and
two cases are operation failures. A parsing correction alone is not fulfillment.

Case 8, Nina Simone/Bill Withers, returned no tracks after the supervisor stopped
its process tree. Its child report is incomplete with no run row. The parent
reports **910,075 ms of process elapsed time**, spanning startup, the watchdog
allowance and handling; this is not a completed generation measurement. Actual
generation duration, parser status and failure stage remain unknown. The configured
watchdog combines 600 seconds for generation and 300 seconds for startup.
Across eight audited treatment cases, one good song is returned, 79 slots are
unfilled and three cases are operation failures. One parser status is unknown.

Case 9 resolved Kraftwerk, Tangerine Dream and Brian Eno, but failed with a
completed-child generation deadline after **639,328 ms**, returning no tracks.
Case 10 retained all twelve Tony Allen identities and corroborated the intended
drummer through two Fela Kuti recording credits. Its catalog seed lookup stopped
after checking 53 recordings within the metadata time limit, leaving no usable
reference track. The resulting clarification after **41,969 ms** is not an
operation error, but it also does not fulfill the request. The generic “not found”
reason does not establish catalog-wide absence. Through ten audited treatment
cases: one good returned recording, 99 missing slots, four operation failures and
one unknown parser status. The exact causes of the live overruns remain unproven.

Case 11, similar to Massive Attack's Teardrop, returned ten tracks after
599,584 ms: one good match, five partial matches and four mismatches. Paradise
Circus is the good match; live Anthrax and Oscar Peterson recordings depart
substantially from the reference. All ten engine assessments were close/unknown.
The web judgments were frozen before the detailed ranking investigation, although
incidental score fields had been visible during initial identity inspection.
This is not a fully blinded review. A subsequent offline reproduction found
that saved audio-search evidence was unavailable to normal candidate scoring,
allowing raw similarity fallback to outrank a completed weighted assessment.
The correction is separate from frozen v3. The four-file change passed independent
review, focused race checks and historical replay checks. This introduced
`multichannel/v56`, retained in the later v57 correction; no native
musical-quality gain is established yet.
The vector, observed coverage and fingerprint now come from the same validated
record when choosing between preview and packed audio evidence. A combined
review reproduced and corrected a source-choice inversion caused by losing the
saved hit's coverage. Full ranking is identical when the same observation is
stored in either supported snapshot location. Sequencing uses that same
observation, including when preview evidence exists only in saved search hits;
regressions verify identical transition scores and complete playlist order.

The same case exposed a recording-identity risk: an Anthrax live recording lasting
222,533 ms was linked to cached preview analysis carrying a studio-recording
identifier and a substantially shorter published duration. The actual cached
audio bytes were not retained, so this does not identify what was heard. Local
provider fixtures reproduced acceptance of a conflicting 164-second recording.
The corrected resolver rejects that conflict and preserves ambiguity, using
metadata from the request's pinned catalog. Review also caught and fixed loss of
valid source-linked duration constraints between catalog and provider.

New preview analyses carry an identity-policy version. Fresh CLAP, DSP and MERT
lookups exclude older-policy rows, while retaining original cached records and
frozen historical results. New search projections rebuild lazily. Independent
policy and integration reviews are clean; focused race, cache-migration and
actual historical-replay checks pass. These later changes are absent from v3;
their cold-cache cost and native musical-quality effect remain unmeasured.

Case 12, based on So What and Take Five, resolved both references and returned
zero tracks after 599,276 ms. Its snapshot retains 64 eligible candidates and no
completed enhanced-audio snapshot. This is a partial budget outcome, not evidence
that genre admission rejected those candidates; the saved diagnostics do not
locate the unfinished internal stage. Across twelve audited treatment cases:
11 returned songs (two good, five partial, four mismatches), 109 unfilled slots,
four operation failures and one unknown parser status.

Case 13, house/techno/UK garage, preserved collective genre coverage but returned
zero tracks after 585,080 ms. Its 162 candidates had no corroborated requested
genre support. Two baseline-good recordings were considered and omitted for
missing corroboration. Through thirteen audited cases, 119 slots are unfilled;
the 11 returned-song judgments are unchanged.

Case 14, bossa nova/samba/Brazilian jazz, also returned zero after 585,068 ms.
All 150 candidates lacked corroboration for a requested genre. Two Jorge Ben Jor
publisher fields said Pop, which is insufficient for these narrower requirements.
Three baseline-good recordings were reconsidered but lacked usable corroboration.
Through fourteen audited cases, 129 slots are unfilled; returned-song judgments,
four operation failures and one unknown parser status are unchanged.

Case 15, bluegrass/country/Americana, returned zero after 585,071 ms. Collective
coverage remained intact, but none of 154 considered candidates had eligible
support. Through fifteen audited cases, 139 slots are unfilled and the eleven
returned-song judgments are unchanged. This is a normal partial budget outcome,
not an operation failure or proof that all possible sources were exhausted.

Case 16, Japanese city pop/funk/disco, likewise preserved collective coverage and
returned zero after 585,059 ms. None of 153 considered candidates became eligible.
Through sixteen audited cases, 149 slots are unfilled; the eleven returned songs
still comprise two good, five partial and four mismatches.

Case 17, spacious electronic music, returned zero after 585,073 ms. All 150
candidates lacked confirmed electronic support; three publisher fields said
Alternative. The parser correctly kept “not sleepy” as a negative mood rather
than an artist exclusion. Through seventeen audited cases, 159 requested slots
are unfilled, with the same eleven returned-song judgments.

Case 18, warm acoustic instruments and an intimate late-night feel, returned ten
tracks after 586,268 ms: six good matches, two partial matches and two mismatches.
The two Ted Greene selections are documented solo-electric-guitar performances,
which conflict with the acoustic request. Six selections come from the same
Jesper Bodilsen album, including five consecutively. The four deterministic
findings report adjacent artist repetition; they are not operation errors or
explicit artist-exclusion violations. All ten declared recording identities were
corroborated, without audio fingerprinting or listening. Through eighteen audited
cases: 21 returned songs (eight good, seven partial and six mismatches), 159
unfilled slots, four operation failures and one unknown parser status.

Case 19, jangly guitars and bright indie-pop energy, returned zero after
585,151 ms. None of 134 considered candidates became eligible; all retained
genre/style assessments were unknown. The six publisher fields say Alternative,
Rock or Country. The saved intent separately requires indie and pop, so the
compound handling was investigated separately. An offline reproduction confirmed
that recognition split the known compound and reconciliation removed literal
jangly-guitar/melodic-bass detail from an otherwise intact model-shaped input.
The original pre-reconciliation model output was not retained; this diagnostic
does not establish what the model originally emitted or how much parsing versus
missing corroboration caused the empty result. The later workspace correction
preserves known complete genre phrases and exact, softly interpreted texture
descriptions. Separate genres, exclusions and quoted reference identities remain
protected. Independent review caught and corrected an explicit quoted-genre
regression; affected race, replay, vet and lint checks pass. Fresh parsing uses
`artist-first/v11` and `source-atoms/v13`. No native recovery is claimed.
Through nineteen audited cases, 169 slots are unfilled; the 21 returned-song
judgments are unchanged.

Case 20, orchestral music with sweeping strings and dramatic contrasts, returned
ten tracks after 586,691 ms: one good, seven partial and two mismatches. Lindsey
Stirling's Eye of the Untold Her has recording-specific support. Kim Wilde's
Dream Sequence and Tinlicker's The Whale have documented electronic arrangements
that conflict with the orchestral brief. Related string arrangements account for
the partial judgments; The Lumineers' Strings is a documented symphonic interlude,
so neither its artist's usual genre nor its 34-second duration establishes a
mismatch. Nine declared recording IDs were corroborated; one selection lacks a
catalog recording identifier and a mastering caveat remains for Dream Sequence.
The engine classified orchestral/strings facets as preferences, with all forty
selected facet assessments unknown. Genre-only omission therefore did not apply.
Through twenty audited cases: 31 returned songs (nine good, fourteen partial and
eight mismatches), 169 unfilled slots, four operation failures and one unknown
parser status. These are web judgments, with no human listening grades.

Case 21, ambient electronic through downtempo to melodic house, returned zero
after 585,034 ms. All three scoped genres and the rising energy curve survived
parsing. None of 141 candidates became eligible; all 423 retained stage
assessments were unknown. The one publisher genre field was broad electronic,
insufficient for the requested subtypes. This is a partial budget result with no
operation error, rather than the baseline's clarification failure. Through
twenty-one audited cases, 179 slots are unfilled; returned-song judgments and
operation-failure counts are unchanged.

Case 22, acoustic folk through folk rock to alternative rock, returned zero after
585,161 ms. The stages and energetic ending survived parsing; the baseline's
false Acoustic/folk artist references did not recur. Its snapshot has 65
considered candidates and three middle-stage matches attributed only to catalog
evidence. A subsequent offline reproduction with the actual imported tag shapes
confirmed a strong-match and final-admission bypass: “Folk-Rock” could be counted
as independent catalog support, while “folk rock” correctly remained unknown.
The actual case returned no songs. The implemented correction treats every
aggregate catalog genre/style result as a non-decisive discovery hint, including
missing annotations and negative results. Independent recording claims and
applicable audio evidence retain their authority. A real-catalog reproduction
and an independent 24-case authority matrix fail before the fix and pass after
it. This changes fresh generation to `multichannel/v57` and
`recording-evidence/v4`; historical replay passes. It is absent from the frozen
v3 experiment, and no native recovery is claimed.

Case 23, Brian Eno through Boards of Canada to Jon Hopkins, returned zero after
48,963 ms and requested clarification between two retained Boards of Canada
identities. Brian Eno and Jon Hopkins resolved, and the other-artists instruction
survived. The local model truncated its output, so the rules fallback ran; the
two deterministic findings report that fallback, not an operation error.
Through twenty-three audited cases: 31 returned songs (nine good, fourteen
partial and eight mismatches), 199 unfilled slots, four operation failures,
one parser fallback and one unknown parser status.

Case 24, traditional soul through funk into disco, returned zero after
585,079 ms without an operation error. All three stages, smooth transitions and
the hard adjacent-artist exclusion survived parsing. None of 169 candidates
became eligible; all 507 retained genre-stage assessments were unknown. The
snapshot contains no publisher genre claim. Through twenty-four audited cases,
209 requested slots are unfilled; returned-song judgments and failure counts
are unchanged.

Case 25, instrumental focused work with no vocals, returned zero after 585,064 ms.
Its collective genre group and strict vocal constraints survived parsing.
None of 133 candidates became eligible: 107 vocal-absence assessments were
unknown and 26 contradicted the request. The baseline's spoken-word selection
was reconsidered and rejected, without claiming that the engine established its
spoken-word content. One candidate had supported Electronic publisher metadata
but only a 28.18-second instrumental preview, insufficient to establish
whole-recording absence. The baseline's good SOLITUDE recording was absent from
the considered pool. Through twenty-five audited cases, 219 slots are unfilled;
the 31 returned-song judgments and failure counts are unchanged.

Case 26, punk rock, returned zero after 585,038 ms without an operation error.
All 145 candidate assessments were unknown. Two exact recordings graded good
in the baseline web audit—Ramones' “Judy Is a Punk” and Wire's “Champs”—were
considered but rejected for unconfirmed genre. Both retained fetched
MusicBrainz community tags and imported tags; neither had qualifying independent
corroboration. A publisher's broad Rock classification was insufficient for
punk rock. This demonstrates loss of valid candidates at admission, not bad
musical fit or an absence of evidence acquisition. Through twenty-six audited
cases, 229 requested slots are unfilled; returned-song and failure totals are
unchanged.

Case 27, blues, returned zero after 585,049 ms without an operation error.
All 166 candidate genre assessments remained unknown. The retained claims
include community/imported tags and unknown preview observations, with no
decisive recording-level genre statement. The frozen result also repeats the
misleading reference-similarity notice despite having no explicit references;
the later workspace already uses neutral musical-fit wording. Through
twenty-seven audited cases, 239 requested slots are unfilled; the 31 returned
songs still comprise nine good, fourteen partial and eight mismatch judgments.

Case 28, salsa, returned zero after 585,039 ms without an operation error.
All 149 candidate genre assessments were unknown. A retained publisher claim
identified Sade's “The Sweetest Taboo” as R&B/Soul, which does not corroborate
salsa; other retained genre support consisted of weak tags and unknown preview
observations. Its 13-minute-13-second process duration includes startup and
cleanup, separate from the completed-child generation measurement. Through
twenty-eight audited cases, 249 requested slots are unfilled; returned-song
judgments and operation-failure counts are unchanged.

Case 29, drum and bass, returned zero after 585,085 ms without an operation
error. Parsing preserved the compound phrase as one essential genre. All 150
candidate genre assessments were unknown. The two retained publisher genres,
Electronic and Hip-Hop/Rap, were insufficient to establish drum and bass;
community/imported tags and preview similarities remained non-decisive.
Through twenty-nine audited cases, 259 requested slots are unfilled, with no
change to the returned-song judgments or operation-failure count.

Case 30, inspired by Björk, returned zero with a deadline operation error after
649,627 ms of verified generation time, exceeding the cap by 49,627 ms.
Björk resolved correctly with four representative recordings; Brant Bjork was
only a lower-ranked alternative. The saved result contains one artist context
plan and six offline artist pools, but no completed candidate/search/audio
ledger. The exact blocking call and any role of genre admission remain unknown.
The later cancellation changes are absent from this frozen build. Through
thirty audited cases, 269 requested slots are unfilled and five operation
failures are recorded; returned-song judgments are unchanged.

Case 31, songs like 宇多田ヒカル with other artists, returned ten songs after
540,415 ms without an operation error: five good, three partial and two
mismatches. The correct reference resolved; three Utada originals and seven
other-artist songs satisfy the inclusion request. Track-specific evidence
supports TENDRE's “LIFE” and Yein's “Plus n Minus,” while MFS's “BOW” and
Lil Cherry's “MUKKBANG!” have rap-led traits judged poor reference matches.
All ten exact Spotify IDs were checked. The base catalog lacks recording
MBIDs, ISRCs and durations, so supplemental preview identifiers do not establish
the selected master. The exact public “MUKKBANG!” recording also credits
GOLDBUUDA, whom the returned catalog row omits. These judgments and their
per-song citations are retained in the portable treatment data. Through
thirty-one audited cases: 41 returned songs (14 good, 17 partial, ten mismatches),
269 missing slots and five operation failures. No human listening was performed.

The co-credit trace found that the installed base catalog already omits
GOLDBUUDA. Three offline converter round trips preserve complete supplied artist
text; the catalog, bridge and display forward it unchanged. The pinned upstream
[dataset builder](https://github.com/teticio/Deej-AI/blob/eac284be4724d5dc69bb67266cd6b1c7bfe99fb8/train/get_tracks.py#L173-L175)
selects the first Spotify artist. That supports an upstream-data explanation,
but the historical exported pickle was unavailable, so its exact provenance
remains unconfirmed. A future catalog rebuild should retain complete structured
recording credits keyed by exact identity and feed existing performer evidence,
display, exclusions and diversity. This result does not justify a song-specific
patch; no secondary-artist exclusion was requested in case 31.

Case 32, David Bowie to Talking Heads, returned zero after the supervisor
watchdog stopped the process tree. Its initialized child report has no completed
run, so the 910,077 ms parent duration is process elapsed time; generation time,
parser behavior and the blocking call remain unknown. Cases 33 and 34 returned
zero through clarification, after 31,313 and 45,211 ms respectively. Bonobo has
five retained identities; Nick Drake has singer-songwriter and poet candidates.
The other references resolved, but neither case began candidate assessment.
These are unresolved-reference outcomes, not evidence of genre admission failure.
Through thirty-four audited cases, 299 slots are unfilled and six operation
failures are recorded; returned-song judgments are unchanged.

Case 35, hip-hop/neo-soul/jazz, returned zero after 585,085 ms without an
operation error. Collective genre coverage was preserved; 130 candidates were
considered and none admitted. Two exact-linked BabyTron publisher claims said
“Hip-Hop/Rap,” but the category matcher discarded that label. An offline
reproduction verifies both identity/provenance checks and shows that replacing
only the label with canonical “hip hop” restores genre support. This establishes
a matching defect, not evidence of neo-soul or jazz or of a completed playlist.
The later workspace correction remains outside the frozen result. Through
thirty-five audited cases, 309 requested slots are unfilled; returned-song
judgments and the six operation failures are unchanged.

Case 36, distorted guitars, pounding drums and a tense/restless mood, returned
ten after 587,416 ms: two good, two partial, four mismatches and two unknowns.
Recording-specific evidence supports Mastodon's “Gobblers of Dregs” and
Rammstein's “Deutschland.” The documented ballad or sparse acoustic arrangements
of “Beautiful,” “Dream Sequence,” “Soar” and “Hot Knife” conflict with the
requested combination. All ten engine labels remain close/unknown; generic
instrument presence did not verify the requested sound. Completed request-fit
scores exist for every selection, so this is not the earlier missing-score bug.
The saved intent loses “pounding,” retaining plain drums. Offline synthetic
model responses reproduced a current parser defect: the exact phrase survived
as a texture but was dropped as instrumentation. Source policy v14 now retains
the supplied literal instrumentation, with exact-source, qualifier and identity
guards. Actual text-encoder probes verify the preserved query wording. The
original model response was not retained, so this does not establish the cause
of the native omission or recover words the model never supplied.
Through thirty-six audited
cases: 51 returned (16 good, 19 partial, 14 mismatches, two unknown), 309 missing
slots and six operation failures.

Case 37, soft piano, spacious reverberation and a reflective atmosphere, returned
ten after 599,658 ms: all ten are partial matches. Recording-specific piano
evidence is relevant, but spacious reverberation remains unverified. The frozen
typed intent also loses “soft” and “reverberation.” Separately, Brubeck's selected
“Softly, William, Softly” from *Time In* carries preview evidence linked to the
shorter *Lullabies* recording. This is another observed frozen identity mismatch
of the class addressed by preview policy v2; the audio bytes were not identified.
The intended recording's musical grade remains separate from this diagnostic.
Through thirty-seven audited cases: 61 returned (16 good, 29 partial,
14 mismatches, two unknown), 309 missing slots and six operation failures.

Case 38, syncopated bass, lively percussion and a celebratory dance groove,
returned zero after 585,052 ms without an operation error. Its saved intent
turns “dance groove” into an essential Dance genre, while several instrument
and rhythmic details disappear. All 194 candidates have unknown Dance support;
192 are explicitly rejected for an unconfirmed requested genre. This identifies
a source-interpretation discrepancy, reproduced in the current rules parser and
source-aware model fixtures. The later source-v15 correction retains “dance
groove” as a soft literal texture, preserves explicit Dance genre requests,
and passes provider-vocabulary and reference protection checks. No native
playlist recovery is claimed. The retained publisher Pop label cannot verify either Dance or the
requested rhythmic traits. Through thirty-eight audited cases, 319 slots are
unfilled; the 61 returned-song grades and six operation failures are unchanged.

Case 39, blues through soul to funk, returned zero after 585,233 ms without an
operation error. Unlike the baseline interpretation failure, the three journey
stages are retained correctly. All 195 genre assessments across 65 candidates
remain unknown; none has a retained linked-statement or publisher-field genre
claim. The audio budget was exhausted, but this does not establish genre support.
No new implementation defect is confirmed from this case. Through thirty-nine
audited cases, 329 slots are unfilled; returned-song grades and operation
failures are unchanged.

Case 40, Radiohead only, returned zero with a deadline error after 627,955 ms,
27,955 ms beyond its nominal generation budget. The canonical Radiohead identity,
four representative recordings and artist-only requirement are retained. Parse
and resolution timings account for 9,163 ms; no completed search/audio snapshot
identifies the remaining blocking call. Total process time was 12m10s and is
separate from child-reported generation time. The full forty-case treatment
therefore ends with 61 returned songs, 339 missing slots and seven operation
failures. The later cancellation fixes are not measured by this frozen result.

The Teardrop audio-search corpus was independently reconstructed: all 48 compatible
preview representations participated, including 44 with pack IDs. This was not
an arbitrary 48-track cutoff. The immutable discovery pack separately contains
247,475 MERT vectors for 252,516 tracks, with a different preprocessing contract.
Reusing installed weights alone cannot establish compatibility. A future query
adapter would need validated decoding, resampling, sampling and pooling, with
preview coverage preserved. The required local assets exist on this Linux host,
but no such query adapter or parity result is claimed.

A follow-up read-only check found that the two corroborating Tony Allen recording
IDs are absent from the installed discovery pack. Same-title catalog rows lack
independent recording identifiers, so they cannot safely replace the missing
seed. The workspace now says that no usable recording was found “during this
lookup,” and suggests a specific track or retry. Identity and admission policies
are unchanged.

Post-freeze regressions reproduced two further cancellation gaps: processing
returned similarity hits after cancellation, and a named-reference lookup waiting
on an occupied SQLite connection with a background context. The workspace now
forwards the operation context through catalog adapters and shared reference
callers, checks cancellation before treating missing metadata as an empty result,
and preserves earlier completed retrieval rows. Atomic metadata/identity binding
discards interrupted merges. Four-mode parity, real SQLite cancellation, assembly
cache and history replay checks pass; independent combined review found no
actionable defects. The full recommendation-package race suite passed in 23.288s.
These fixes do not identify the live blocking calls or establish a universal hard
timeout. They are absent from frozen v3; the later supplementary V2 build passed
the full workspace gate. Exact changes and review hashes are in
`post-freeze-workspace-changes.json`.

The remaining metadata paths now carry cancellation through selection,
sequencing, performer diversity, duration, seed admission and recording evidence.
Occupied-SQL regressions cover selection and sequencing in all four modes, and
the complete recommendation-package race suite passed in 21.755s. Independent
review of the 17-file extension also passed. Its full-build probe confirms that
the ordinary search deadline preserves accepted tracks using the assembly
reserve, while cancellation of the parent operation still discards the result.
No direct context-free metadata calls remain in multichannel production code.
These are post-freeze corrections, absent from the original v3 results and its
fixed-build controls.

A further 14-file acquisition correction carries cancellation through cached
audio scans, application analysis and feedback preparation, and MusicBrainz
identity, seed and knowledge lookups. Six before-fix regressions demonstrated
publication of interrupted metadata or apparent success after cancellation.
Focused race checks, vet, scoped lint and independent review pass after the
correction. The final repository gate subsequently passed after the serial
native comparison queue completed.

1. Baseline complete: every case and returned occurrence has a source review;
   failures and unfilled slots are retained. Preserve those reports and hashes.
2. Frozen integrated treatment complete: all forty cases and every returned song
   have source reviews. Keep its raw end-to-end results separate from controls
   using the same frozen intent and seed, and from later workspace fixes.
3. Complete: all approved extra controls on cases 2, 10, 14, 17, 21,
   23, 25 and 40: original/new generation at five minutes, plus new generation at
   ten minutes, each using the same baseline-derived intent and seed. Do not
   replace ineligible cases. Clear acquisition snapshots in a separate input copy
   so replay does not suppress fresh verification. Keep assets, profile, source
   mode, cache preparation and measurement conditions explicit. These controls
   measure generation downstream of fixed preparation; the full raw suites also
   measure parsing and reference recovery. Do not attribute all gains from a
   longer search to ranking improvements.
4. Report identity correctness, hard violations, supported/unknown facets,
   supported matches per returned track and per requested slot, duplication,
   performer concentration, journey order, failures and latency separately.
5. The blind listening packet is prepared for subjective similarity, texture,
   energy and transition quality. Human judgments remain unfilled.
6. Complete after the original queue and supplementary V2 gate: separate targeted
   checks for cases 6, 7, 8 and 11 using the corrected build. These cover the
   observed deadline failures and Teardrop evidence/ranking/sequence defects.
   They measure the combined later implementation, with fresh per-case stores,
   and cannot replace the original comparison or isolate one change's effect.
   They returned zero songs. The completed separate opt-in case-6 trace also
   returned zero songs and reached final assembly at 585 seconds, before its
   deadline error at 600,001 ms. The exact inner operation remains unresolved.

The four genre mismatches were explicitly marked strong musical matches in the
first five baseline cases' candidate evidence. This is a false verification claim, beyond a weak
ranking choice. Ordinary source metadata can also be wrong: the four Bad Bunny
recordings in the electronic result all carry House tags, while recording-specific
web evidence supports different degrees of fit. Treatment requires corroboration
for those tags. Hidden live/remix versions and malformed ISRC fields require
exact-identity review; invalid ISRC strings now provide no identity proof.

Cases 13 and 14 add ordinary imported-tag errors, beyond classifier contamination.
The house/techno/UK-garage playlist has no corroborated techno selection. The
bossa-nova/samba/Brazilian-jazz result includes reggaeton/bomba and dancehall,
while generic jazz is insufficient proof of Brazilian jazz. Those latter tracks
were already labeled close/unknown: honest uncertainty labels alone do not repair
weak selection or missing genre coverage. Per-recording judgments and collective
playlist coverage remain separate in the audit.

Case 17 amplifies the classifier failure: nine orchestral/rock/pop recordings
carry imported Electronic/Ambient predictions despite the explicit electronic
foundation of the request. The remaining result is a 94-minute continuous mix;
recording membership alone does not establish suitability as one requested song.
Case 16 also lacks supported Japanese city-pop coverage. These observations remain
in the baseline; no treatment gain is assumed before measurement.

Case 25 returned a spoken-word recording by The Last Poets for an explicit
instrumental/no-vocals request. [PBS's account of the group](https://www.pbs.org/thisfarbyfaith/popup/4_the_last_poets.html)
names the poem and describes their poetry/rap work. The saved assessment reports
`voice_instrumental` support of 0.99466166528842, with unknown preview evidence,
and admits the song as a close match. This is a concrete classifier error and
hard-constraint violation. A separate full-generation regression also reproduces
admission from a vocal-free preview despite an unknown whole-recording absence
assessment. Final admission and source-method corrections pass focused tests and
independent review. Required collective genres and mixed-strength alternatives
are checked as groups before strictness is applied, avoiding an accidental return
to per-track conjunction. The frozen v3 repository gate passed with its
recognition and runtime capability-status corrections included. The later
workspace changes subsequently passed the full supplementary V2 gate.

The user explicitly selected omission of songs whose defining requested genre
cannot be corroborated, accepting fewer confirmed matches. The replacement
treatment implements that admission policy while keeping evidence acquisition
open to unverified candidates. Explicit soft hints remain preferences. Genre
mixtures require one supported member per track and every member across the
playlist, as separately confirmed by the user. The comparison still measures both
returned-song precision and supported requested slots.
Independent review also identified bounded evidence-source coverage, continuous
mix format, and soft performer concentration as remaining policy/data limits.
No arbitrary duration or artist cutoff was added without measuring eligible
alternatives. The source acquisition and corroboration limits remain explicit.

Architecture and migration behavior are documented in
[recording-evidence.md](recording-evidence.md). No human-quality improvement is
claimed by synthetic tests.

## Executed implementation checks

The final current-source gate passed: `./scripts/test.sh`.
It includes all shell checks, regenerated Wails bindings, 253 frontend tests in
25 files, typechecking, the production build, Go vet, pure-Go core compilation,
the full Go race suite and golangci-lint with zero issues. All 1,391 source files
were unchanged across the gate. The separately frozen trace build and its
72 reviewed production changes relative to frozen v3 passed independent review.
The eight tracing production/test files have their own review and regressions
for error/cancellation retention, opt-out, bounded concurrent collection, child
forwarding, private export and path-collision rejection before writes. Receipts
are `full-gate-trace.receipt.json`, `traced-case06-build-receipt.json` and
`traced-case06-build-independent-review.json` in the local audit directory.
The earlier supplementary V2 gate/build receipts remain unchanged. Reports were
refreshed after the final source freeze; no recommendation code changed afterward.

The final raw/control report review independently verified all 104 runs and
314 returned occurrences, their identities, grades and source links, plus the
24 matched children and their common input/seed triples. It also verified that
all 12,600 human grade fields remain null. Its receipt is
`final-raw-control-report-independent-review.json`; the four supplementary cases
and additional trace have separate records and denominators.

The detailed entries below preserve earlier checkpoints. Their statements about
a pending final gate describe those earlier checkpoints; the final current-source
gate above supersedes that status. Native quality diagnostics remain separate.

- The standalone song report preserves all 280 ordered occurrences, 712 source
  references, 653 facet entries and 290 identity/version fields. Independent
  review and browser checks verify exact grades/reasons/source order, filtering,
  empty results, Unicode search and keyboard disclosure. Dark/light layouts at
  390/1100 pixels have no horizontal overflow. This validates reporting only;
  it assigns no new musical grades or listening scores.
- `go test -p 2 ./...` passes with the integrated source-v16 fixes
  (`post-freeze-go-test-after-source-v16.log`). The final CI-equivalent gate
  remains scheduled after the serial native queue.
- The source-v16 qualifier correction passes affected intent/audio race tests,
  vet, lint and actual frozen-history replay (16.590s). Independent review
  verifies complete negative/reduced wording, unknown-detail preservation,
  same-occurrence generic suppression, journey scope and snapshot immutability.
  A separate root regression (race 1.102s) confirms that a partial “ringing
  guitar” interpretation cannot broaden “no ringing guitar samples.” The exact
  current post-freeze production delta is 66 Go files plus one frontend component,
  all reviewed. This bounded interpretation guard supplies no new musical
  evidence or native quality measurement; the final workspace gate is pending.
- The source-v15 Dance-groove correction passes focused regressions, affected
  intent/audio race tests, vet, lint and frozen-history replay. Independent
  review found no actionable issue; a separate race probe (1.112s) verifies that
  the same request can retain both a soft groove and an explicit positive or
  negative Dance genre. This is an offline interpretation correction.
- The source-v14 literal-instrumentation correction passes the affected intent
  and audio race suites, vet, lint and frozen-history replay. An independent
  six-case integration probe fails before the correction and passes after it
  (1.217s), including positive phrases on either side of a separate exclusion.
  The scoped review found no introduced defect. It also exposed a separate
  existing limitation: an unknown modifier can leave the contained plain
  instrument positive despite “no” or “avoid.” The bounded source-v16 guard
  described above subsequently addressed that limitation.
- `go test -p 2 ./...` passes after the combined v58/evidence-v5 correction
  (`post-freeze-go-test-after-v58.log`, recommendation package 13.510s).
  The correction also passes the core/recommender race
  suites (1.030s/21.447s), vet, lint and actual frozen-history replay. Independent
  root review covers its six production files and two regression files. The
  pruning regression changes eight unnecessary preview resolutions to zero and
  retains a later supported candidate; a separate-page race probe passes in
  1.087s. A full-generation publisher integration probe passes in 1.105s: one
  confirmed hip-hop recording returns without a redundant preview, with exactly
  two missing-genre reasons and an honest partial outcome. That review checkpoint
  covers 65 Go files and one frontend component. These are
  offline checks, not native latency or musical-quality measurements; the final
  repository gate remains pending.
- The source details now label `catalog_genre_hint` as “library genre label;
  needs corroboration,” while the requested genre remains unverified. Three
  component tests, frontend typechecking and browser interaction checks pass
  in dark/light themes at 390/1100 pixels. Independent review found no actionable
  issue; saved narrow and desktop renders were inspected. That post-freeze
  review checkpoint covers 64 Go files and this frontend component, with the
  earlier review receipt preserved. Browser checks use mocked native APIs.
- `go test -p 2 ./...` passes after the catalog-genre provenance correction
  (`post-freeze-go-test-after-genre-policy.log`). The recommendation, core and
  local-catalog race suites, frozen replay, vet and lint also pass. Independent
  review found no remaining defect. Two seed-planner fixtures now provide cited
  genre evidence; their assertions and candidate layouts are unchanged. The
  post-freeze Go production changes span 64 independently reviewed files;
  the final full repository gate remains pending.
- `go test -p 2 ./...` passes again after the compound-genre and literal-texture
  correction (`post-freeze-go-test-after-parser.log`). Its six affected package
  race suites, frozen-history replay, vet and lint also pass. Root independent
  integration probes pass after the quoted-genre correction. These three
  production files supplement the sixty reviewed below; the final full
  repository gate remains pending.
- After the later preview-identity, cancellation, ranking and sequencing fixes,
  `go test -p 2 ./...` passes across the workspace. The two initially failing
  packages passed after four healthy cache-fixture markers were added; their
  assertions and legacy-cache rejection tests are unchanged. A fifth benchmark
  fixture now uses the current supported identity contract. The full
  recommendation race suite also passes (22.546s), and combined review of all
  60 later production files has no remaining findings. This is separate from
  the frozen v3 gate below; the final full workspace gate is still pending.
- `./scripts/test.sh`: passed on Linux/WSL2,
  including shell checks, regenerated bindings, 253 frontend tests, typecheck,
  production build, Go vet, pure-Go core compilation, the full Go race suite and
  golangci-lint (zero issues). Latest complete output: `full-gate-v3.log` in the audit
  artifact directory. The frontend build reports its existing bundle-size warning.
- The v3 gate includes metadata membership preservation, bounded identity
  scheduling and the exact linked publisher adapter. Focused provider race tests
  passed in 11.953s; the live Air diagnostic is reported separately above.
  Updated browser captures in `ui-v3/` and `ui-v3.log` cover publisher disclosure
  and both citation links, alongside existing omission/export and keyboard checks.
- Subsequent parser boundary fixes passed the complete lexicon/schema race suites
  (`parser-coverage-final.log`), with independent overlay checks for strict
  per-track wording, unrelated instrument requirements and model-invented coverage.
- The subsequent recognition v10 correction passed its full race suite (5.119s).
  An independent installed-index/rules preflight reran all original 40 prompts
  under race (4.327s): only case 17 changed semantically, removing the false
  Sleepy artist exclusion. Genre journeys, Unicode references and only-Radiohead
  intent were preserved. This check used embedded vocabulary and no native model
  or provider calls; it does not establish end-to-end playlist quality.
- Runtime capability-status regressions use source-parsed requests: exact cited
  absence reaches fulfilled and the quality target; weak absence is omitted, and
  an unrelated unsupported playback request still prevents fulfillment. Stored
  unsupported annotations remain intact. Full affected race tests (21.591s),
  vet/lint, frozen baseline replay (0.055s) and the combined gate passed.
- A later narrow legacy-history fallback passed focused bridge race regressions:
  `go test -race ./internal/bridge -run 'TestFrozen|TestLegacyIDOnly' -count=1`.
- A subsequent selection-resolution timeout regression passed focused bridge
  race checks: a work deadline returns an honest partial result before the parent
  deadline, preserving parsed intent and actual parser status.
- Recognition/schema/lexicon/resolution race tests passed after separating the
  64-candidate identity bound from model-message presentation bounds. A read-only
  installed-index check retains all twelve Tony Allen identities, including the
  intended artist in position eleven.
- `python3 -m unittest scripts/test_summarize_forty_audit.py`: passed; the summary
  rejects mismatched recording occurrences/run identities and retains missing slots.
  A post-freeze artifact correction also retains each canonical raw-case hash.
  Timing comparisons now require the complete matching child payload and executable,
  with failed generation timings retained and process fallback timings separate.
  Independent review and seven adversarial probes passed; no observed timing
  changed. Raw reports and frozen benchmark executables remain unchanged.
- Rendered Generate/Settings and Playlist captures cover dark/light themes and
  390-pixel/desktop widths. Playlist source links and keyboard expansion passed;
  images and the latest run log are under `ui/` and `ui-coverage.log`.
  The coverage explanation was also inspected at narrow width.
- Subsequent empty and short-result browser checks passed in both themes at
  390/1100 pixels (`ui-admission.log`), including the omission explanation and
  disabled export for zero tracks. The app's managed-discovery regression now
  verifies zero output, rejected unknown-genre evidence, distinct checked local
  recordings and the later provider opportunity (`app-admission-after.log`).
- The actual completed v3 electronic result also passed browser checks at
  390/1100 pixels in both themes: one-of-ten partial status, omission reason,
  enabled export callback, keyboard details and the actual Apple source link.
  It exposed an overbroad bridge notice claiming musical-fit checks were
  unavailable despite a corroborated genre. A post-freeze presentation-only
  correction now says **some** checks were unavailable. Independent review,
  focused bridge race tests (1.542s), an actual-result bridge projection and
  separate before/after browser captures passed. Exactly two notice detail fields
  changed; tracks, assessments, requests and the frozen benchmark remain intact.
  Receipts are `ui-v3-live-case02*-validation.json` and
  `post-freeze-workspace-changes.json`. Browser tests mock native APIs and external
  opening; they do not test an operating-system export save. The final repository
  gate must be rerun after this correction before delivery.
- Actual frozen baseline v1 snapshots validated and replayed under current code
  in separate read-only Go-overlay checks. The installed MusicBrainz index also
  reproduces the corrected Radiohead/Other recognition behavior.

These are local Linux checks. Native Windows/macOS packaging and human listening
were not performed. The first gate's outdated tag-only fulfillment expectation
was changed to assert partial output and unknown criteria. The user's subsequent
omission decision intentionally changes that fixture's expected output to zero;
local checking and provider-timing checks remain, with rejection evidence retained.
Selection/deduplication are covered by the separate recommendation regressions. An intermediate
identity gate exposed an eight-candidate fixture assumption; the twelve synthetic
John Williams rows now correctly require twelve untruncated candidates, and the
subsequent full gate passes. Lint corrections
did not change behavior. The raw forty-prompt web comparison is complete;
the matched controls and four later-build diagnostics are also complete. The
additional opt-in progress trace is complete and reviewed separately. It retains
the final assembly boundary but establishes no runtime repair or quality gain.
