# Codebase audit and remediation plan

**Date:** 15 September 2026

**Scope:** recommendation correctness, setup and runtime lifecycle, storage and exports, UI state, build tooling, performance, and maintenance.

**Deliverable:** analysis and a proposed implementation sequence. No production fixes, dependency upgrades, commits, or publication were performed for this audit.

**Subsequent implementation:** the accepted plan has been implemented in a local
topic branch. See [implementation results and remaining verification](codebase-remediation-results-2026-09-15.md);
the audit findings and baseline measurements below retain their original meaning.

## Baseline and assessment

The audit used commit `9053ee316ffc811d4f069e990d69c3deecaaf7c6` on `codex/fix-main-windows-ci`. During the audit, [PR #48](https://github.com/platten/playlistai/pull/48) was merged into main as `923318942464a6ade836472d96457a69d1c8f0a3`. Both commits have tree `a240937a06362acf513c005b2c0532dab2ec519b`: their checked-in files are identical. Source links below use the immutable main revision.

The Windows executable-replacement repair from PR #47 and the MusicBrainz rollback race repair from PR #48 are already included. They are not new findings here. The [PR #48 CI run](https://github.com/platten/playlistai/actions/runs/34949592540) passed Linux lint/tests and Windows, Linux amd64/arm64, and macOS builds; [CodeQL](https://github.com/platten/playlistai/actions/runs/34949585379) passed too. Those checks do not establish that all installation, recommendation, or navigation paths work.

The most urgent remaining problems are unsafe ownership of runtime cleanup directories, catalog paths escaping their destination, a CSV overwrite-confirmation gap, and valid journeys being rejected. Fix these before broad refactoring. Several other reproducible bugs concern retry recovery, feedback corrections, dynamic-track evidence, and asynchronous exports. The clearest measured performance opportunity is loading playlist-history summaries without loading full result payloads.

Severity describes impact and urgency, not likelihood of exploitation:

- **P1:** address first; possible unintended deletion/overwrite or a core valid request rejected.
- **P2:** address in the next remediation series; incorrect state, recovery failures, privacy exposure, or substantial avoidable work.
- **P3:** maintenance or profiling opportunity; do not optimize without a measured benefit.

“Reproduced” means a bounded local fixture demonstrated the behavior. “Source-confirmed” means the implementation establishes the path, but the full native/provider scenario was not exercised. “Measure first” is a hypothesis, not an observed performance defect.

| ID | Priority | Finding | Evidence |
| --- | --- | --- | --- |
| R01 | P1 | Catalog manifest filenames can escape the destination directory | Reproduced |
| R02 | P1 | Runtime cleanup can delete an independent llama installation | Reproduced |
| R03 | P1 | CSV extension normalization can bypass confirmation for the actual file overwritten | Source-confirmed; native dialog check required |
| R04 | P1 | Journey spacing spends a necessary separator too early and rejects a valid request | Reproduced through the orchestrator |
| R05 | P2 | Catalog extraction leaves partial files and lacks adequate resource/activation boundaries | Partial-file leak reproduced; remaining risks source-confirmed |
| R06 | P2 | MusicBrainz installation cannot recover from a stale lock or corrupt existing target | Reproduced |
| R07 | P2 | Dynamic references with valid MERT representations lose similarity evidence | Reproduced |
| R08 | P2 | Dynamic candidates bypass recent-exposure scoring | Reproduced |
| R09 | P2 | Corrected feedback has inconsistent backend and UI semantics | Backend reproduced; UI source-confirmed |
| R10 | P2 | Wizard reports completion after persistence failure; legacy setup policy needs alignment | Source-confirmed |
| R11 | P2 | Removed recommendation modes remain active and mode labels disagree | UI reproduced; restoration path source-confirmed |
| R12 | P2 | Navigation loses export operation state and permits duplicate handoffs | Reproduced in component tests |
| R13 | P2 | Ordinary logs include a playlist share URL; privacy copy understates metadata lookups | Source-confirmed |
| R14 | P2 | History listing reads large JSON payloads it immediately discards | Source-confirmed and measured synthetically |
| R15 | P2 | Startup performs repeated bundle verification and health checks before showing the shell | Source-confirmed; latency unmeasured |
| R16 | P2 | Offline model-export pins disagree with preflight and include vulnerable dependencies | Source-confirmed; advisory applicability needs targeted validation |
| R17 | P2/P3 | Coverage gates remain unmet and operational documentation is stale | Measured and source-confirmed |

## File safety and installation recovery

### R01 — Constrain catalog manifest paths before any write

**Evidence.** [Manifest validation](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/dataset/manifest.go#L76) does not enforce a safe filename policy. [Fetch](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/dataset/fetch.go#L56) joins the supplied name to the destination. A local HTTP fixture serving a manifest with `../sibling.txt` overwrote a sibling of the catalog directory and returned success. The affected trust boundary is an operator-selected or compromised manifest source; the audit found no evidence that the official manifest is malicious.

**Remediation.** Define and enforce a manifest contract before creating files: supported local names, containment, duplicate/case-collision rules, nonnegative sizes with overflow-safe total limits, and correctly encoded digests. Prefer flat filenames if subdirectories are unnecessary. Reject absolute, parent-relative, and Windows-reserved path forms. Account for pre-existing symlinks/reparse points at the write boundary. Stage downloads in a uniquely owned directory and activate only validated results.

**Acceptance.** Table-driven Windows/Unix path cases; a malicious manifest cannot change a sentinel outside the destination; rejection leaves the installed catalog intact; valid published manifests remain compatible. Decide the contract before R05 so fetch and extraction share validation.

### R02 — Separate runtime detection from directories owned by the installer

**Evidence.** [Runtime detection](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/intent/llama/runtime.go#L157) accepts an external `llama-app` installation. [Installer scratch selection and cleanup](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/intent/llama/runtime.go#L227) use that same path. A fixture with redirected application-data paths detected an independent installation, then demonstrated that `CleanStaged` deleted it. Normal installation also schedules scratch cleanup.

**Remediation.** Give each operation a new scratch directory under app-owned storage. Change or replace the installer invocation so the subprocess actually writes there; changing `installerScratch()` alone would leave the external installer writing to its global location. Pass that exact ownership handle through download, extraction, activation, and cleanup. Detection of an external runtime must never grant deletion ownership. Preserve the previous usable runtime until its replacement passes validation. Report cleanup failures without masking the original failure.

**Acceptance.** External-runtime sentinels survive success, failure, cancellation, and retry; concurrent attempts cannot clean each other's files; failed replacement retains a working runtime. Exercise native Windows paths and Unix layouts. No migration should move or remove an independently installed runtime.

### R03 — Confirm the final CSV target and write safely

**Evidence.** [ExportCSV](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/bridge/export.go#L89) applies `EnsureCSVExt` after the save dialog, then calls `os.WriteFile`. [The dialog](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/bridge/export.go#L105) has no extension filter. If it returns `mix` while `mix.csv` already exists, the app writes to a different target from the one the dialog confirmed. Native dialog normalization was not exercised during this audit.

**Remediation.** Configure the CSV dialog appropriately and ensure overwrite confirmation refers to the exact normalized destination. If normalization changes the selected target and that target exists, explicitly confirm that target. Write to a same-directory temporary file, then use a platform-appropriate replacement path that preserves the old file if writing fails. Handle the non-dialog fallback explicitly too.

**Acceptance.** Existing `mix.csv` plus a selection of `mix`; wrong extensions; cancellation; Unicode paths; failed writes; replacement errors. Verify actual Windows/macOS/Linux dialogs on available hosts before claiming platform coverage. Existing content must survive canceled or failed export.

### R05 — Bound catalog extraction and make activation recoverable

**Evidence.** [Bundle extraction](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/dataset/bundle.go#L164) reads the manifest without a bound and copies members before validating their final sizes. At [line 194](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/dataset/bundle.go#L194), a partial file is registered for cleanup only after copying succeeds. A truncated tar member inside valid zstd framing returned `unexpected EOF` and left one partial file. Publication then uses separate file renames, so later failures can leave a mixed generation; that fault was not injected in this audit.

**Remediation.** Register cleanup immediately after creation; bound manifest bytes, member bytes, and total extracted bytes before accepting them; compare archive headers to validated manifest declarations. Make long copies cancellation-aware. Extract into an operation-owned staging directory. Verify the complete set before activation, using a recoverable generation switch or explicit rollback for multi-file publication. Publish the manifest consistently with its files.

**Acceptance.** Truncation, oversized declarations, malformed manifests, cancellation during a large member, disk-full simulation, and failure during the second promotion. No orphan stages or mixed active generation; a previously healthy catalog remains usable. Reuse R01's manifest policy rather than inventing a second one.

### R06 — Recover MusicBrainz installs after crashes and corruption

**Evidence.** [Install locking](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/mbindex/bundle.go#L314) relies on an exclusive marker file removed only during orderly cleanup. Two consecutive fixture retries against a leftover marker both failed. [An existing hash-named index](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/mbindex/bundle.go#L321) that fails validation prevents repair instead of triggering a fresh staged download; a corrupt-target fixture reproduced this too.

**Remediation.** Use a process/OS-backed lock that is released on process death, with documented Windows and Unix behavior. Do not delete a lock solely because it is old. Repair a corrupt target through a separate stage and validated activation; quarantine or replace it safely while respecting active readers and Windows file locks. Preserve a healthy alternate snapshot. Extend the publication review to [build output replacement](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/mbindex/build.go#L251), which removes the previous output before renaming its replacement. Keep PR #48's transaction-cleanup ordering intact.

**Acceptance.** Kill an installer child process and retry; reject a concurrent live installer; reuse only verified partial downloads; repair active/inactive corrupt targets; inject replacement failure and verify the previous usable snapshot remains. Test the actual lock implementation on all supported operating systems.

## Recommendation and preference correctness

### R04 — Reserve mandatory journey separators before distributing optional tracks

**Evidence.** [Multichannel sequencing](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/reco/multichannel/sequencer.go#L162) distributes optional tracks before satisfying all required spacing. For required artists A → B → B, four total tracks, one eligible artist C, and no consecutive same artist, it spends C early and constructs A → C → B → B. It then rejects the request, although A → B → C → B is valid. Both the sequencer and full orchestrator reproduced this false conflict. A related [Deej-AI regression](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/reco/deejai/spacing_test.go#L85) already exists; the multichannel path missed it.

**Remediation.** Calculate and reserve the separators needed by required waypoints before distributing surplus tracks. Keep exclusion, identity, recording-deduplication, count, and journey-order constraints intact. Distinguish bounded search exhaustion from a proven impossible request. Share behavioral fixtures across policies without merging their ranking semantics.

**Acceptance.** A-B-B, A-A-B, and A-A-B-B; scarce separators; excluded separators; duplicate recordings; fixed destinations; insufficient total count; deterministic seeds. Add a generation-algorithm version change where the existing identity contract requires it, and retain replay of previously saved results.

### R07 — Resolve reference identities independently of Deej-AI vectors

**Evidence.** [MERT retrieval](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/reco/multichannel/mert_retrieval.go#L35) and [enhanced scoring](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/reco/multichannel/enhanced.go#L130) obtain references through [helpers requiring dense Deej-AI vectors](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/reco/multichannel/vectors.go#L117). Dynamic catalog tracks intentionally lack those vectors. A dynamic reference with a cached, compatible MERT vector identical to the candidate's vector produced unavailable reference similarity instead of cosine 1.

**Remediation.** Represent resolved weighted reference identities/groups separately from any channel's vectors. Let each channel load and validate its own representation. Preserve positive/negative reference roles, representative-track group weights, and exact model/catalog compatibility. Dense retrieval should retain its own availability guard.

**Acceptance.** Dynamic/base references; positive and negative references; artist/album representatives; missing representations; incompatible model versions and dimensions; valid fallback; saved requests replaying without new provider calls. Never convert missing evidence into a zero-confidence assertion or mix embedding spaces.

### R08 — Apply exposure scoring to tracks without dense vectors

**Evidence.** [The ranker](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/reco/multichannel/ranker.go#L58) continues early when dense vectors are absent, bypassing metadata-based exposure scoring at line 86. A dynamic candidate with recorded exposure received no penalty in the fixture.

**Remediation.** Move representation-independent scoring outside the vector guard; keep affinity computations conditional. Verify candidate availability aggregation still produces the intended blend when a candidate has only some channels.

**Acceptance.** Equivalent base/dynamic tracks receive the same configured exposure treatment; equivalent recordings share the appropriate exposure identity; absent metadata and zero exposure remain explicit; exposure never becomes positive feedback. Update generation identity if final ranking changes require it.

### R09 — Give feedback corrections one consistent meaning

**Evidence.** [Dense taste-profile aggregation](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/taste/profile.go#L102) retains both an earlier dislike and a later like, while content feedback takes the latest preference. A fixture produced both positive and negative centroids for the corrected track. [Content feedback ordering](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/taste/content.go#L13) uses random event IDs to break timestamp ties, overriding the store's insertion order; another fixture selected the older preference. In the [playlist UI](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/frontend/src/screens/PlaylistScreen.tsx#L512), historical membership disables buttons, so like then dislike can leave both disabled.

**Remediation.** Introduce a shared reducer for current explicit preference per track and scope, preserving request-over-history precedence. Specify separately how accept/remove events affect history; do not silently reinterpret every event as a preference toggle. Preserve append-only events while persisting a stable ordering/sequence and versioning derived profile semantics. The UI should show latest acknowledged state, transient pending state, and recoverable errors.

**Acceptance.** Repeated like/dislike and more/less corrections; equal timestamps; request/durable scopes; persistence reload; failed saves; navigation; equivalent representations. Dense and content rankers consume consistent effective preferences. Preserve old saved profile snapshots rather than rewriting historical results.

## Setup, UI lifecycle, and privacy

### R10 — Finish setup only after successful persistence and required readiness

**Evidence.** [Wizard completion](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/frontend/src/screens/FirstRunWizard.tsx#L92) swallows persistence errors and calls `onDone` in `finally`. [Backend completion](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/app/app.go#L273) changes the completion flag without revalidating required readiness. These are distinct from the legacy setup-policy question: MERT/DSP enabled defaults are already implemented. [Readiness policy](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/app/onboarding_status.go#L67) still bases some requirements on prior installation/opt-in and accepts a disabled preview provider as ready. Provider-off is separate from the DSP checkbox/default. Existing tests intentionally preserve some rules-only/preview-off choices; their presence does not establish that the new defaults are broken.

**Remediation.** Keep the final step visible when saving fails and offer retry. Define a policy matrix before changing readiness: fresh supported installations should meet the required-wizard behavior; document which intentional legacy choices remain supported and which require a one-time migration. Reuse the implemented MERT/DSP defaults. Validate required supported capabilities at final completion and direct the user to the missing step. Preserve installed-module detection and avoid offering a download while detection is still pending. Give unsupported hosts explicit states instead of an impossible wizard loop.

**Acceptance.** First install and upgraded profiles; previously disabled features; already installed modules; detection pending; missing assets at final confirmation; unwritable preferences; restart after completion; unsupported native runtime. Test enabled defaults and persistence through the actual UI-to-bridge contract.

### R11 — Migrate removed current modes and label active/historical modes correctly

**Evidence.** [App startup](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/app/app.go#L119) restores legacy settings; [the getter](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/app/recommendation.go#L13) still returns valid legacy modes. [Settings](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/frontend/src/components/RecommendationSettings.tsx#L4) offers only two modes. A component fixture restoring `clap_first` showed no selected radio while displaying Enhanced hybrid explanatory copy. [Playlist labeling](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/frontend/src/screens/PlaylistScreen.tsx#L282) labels Enhanced hybrid as AcousticBrainz-first.

**Remediation.** Migrate removed modes in current preferences to Enhanced hybrid, retain Deej-AI only, and persist the migration once. Centralize exhaustive labels. Preserve legacy mode identity in saved requests/history, where replay compatibility still matters; removing settings choices does not justify deleting old backend policies.

**Acceptance.** Each legacy/current/missing preference; restart after migration; selected radio and description agree with actual generation mode; historical playlists have accurate labels and retain their saved semantics. Coordinate with R10's settings migration.

### R12 — Retain export operations across navigation

**Evidence.** [ReviewExport](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/frontend/src/screens/ReviewExport.tsx#L55) keeps pending state locally, while its retained draft omits operation state. [Navigation unmounts the screen](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/frontend/src/App.tsx#L216). A component fixture started a deferred Soundiiz handoff, navigated away and back, and submitted it again before the first completed.

**Remediation.** Own the operation in retained workspace state. Capture an immutable playlist name, selected tracks, and operation identity at submission. Preserve progress/result across navigation and suppress duplicate submissions. Guard late results so an older operation cannot modify a newly edited draft. Use provider idempotency only if its contract supports it; otherwise implement local operation deduplication.

**Acceptance.** Navigate during submission and during acceptance recording; return before/after completion; edit another draft; reject/retry; export a new version intentionally. Verify exactly one external call for one pending operation and keep its outcome accessible. Regenerate bindings only if contracts change.

### R13 — Remove share links from normal logs and correct privacy copy

**Evidence.** [Soundiiz handoff logging](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/bridge/export.go#L145) records the returned playlist share URL in ordinary warning/info logs. The URL exposes the handoff resource to someone reading shared diagnostics; no external disclosure of a real user's link was demonstrated. [Wizard privacy text](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/frontend/src/screens/FirstRunWizard.tsx#L209) also understates the automatic metadata/reference preparation performed by [generation](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/bridge/intent.go#L242).

**Remediation.** Log operation IDs, provider outcome, and track count instead of share URLs. Sanitize error paths too. Keep the actual share URL available in the requested export result. Describe the actual artist/track/metadata lookups and explicit export handoff without implying that the full prompt leaves the device.

**Acceptance.** Captured normal logs contain no fixture share token, private playlist title, or track payload. Export still opens/copies its URL. Review privacy copy against real provider calls and the existing diagnostic opt-in policy.

## Performance, tooling, and maintenance

### R14 — Query history summaries directly

**Evidence.** [History.List](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/history/history.go#L120) reads four JSON payloads per row. [ListSavedPlaylists](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/bridge/history.go#L42) discards those payloads and returns seven summary fields. A synthetic fixture of 50 rows with a 256 KiB result payload per row measured:

| Operation | Time/op | Allocated bytes/op | Allocations/op |
| --- | ---: | ---: | ---: |
| Current full List | 45.73 ms | 27.83 MB | 16,588 |
| Summary-only SQL prototype | 69.56 µs | 9,304 B | 577 |

These are one benchmark sample on Windows amd64, Intel Core Ultra 9 285H, Go 1.27.0, with automatic `testing.Benchmark` iteration selection. They are not observed user-library latency or a promised end-to-end speedup.

**Remediation.** Add a summary query/method and use it for the sidebar/list. Keep full `Get` and replay loading unchanged. Measure ordering/pagination with representative histories before adding an index such as `(created_at, id)`.

**Acceptance.** Stable ordering/limits; empty and legacy rows; unused malformed payloads do not break summaries; full selected-playlist loading remains exact. Repeat allocation/latency measurements on small and large fixtures with recorded payload sizes.

### R15 — Move expensive runtime readiness off initial window creation

**Evidence.** [Startup](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/main.go#L89) constructs the app before the host window. [CLAP wiring](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/app/analysis.go#L62) and [MERT wiring](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/app/enhanced.go#L73) repeat active-bundle validation and then run worker health checks. Native workers validate bundles again. The [declared CLAP ONNX files](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/audio/recommended.go#L44) total 782,722,887 bytes: three validation passes imply roughly 2.35 GB of reads on that installed/native path. Hashing is context-free. This is a static byte-count estimate, not measured boot time; configured 60/90-second health timeouts are ceilings, not observed delays.

**Remediation.** Show the shell with explicit validating/loading state, then initialize runtimes asynchronously under application-lifetime cancellation and revision guards. Reuse validated activation results within a controlled operation instead of rehashing redundantly. Preserve integrity validation before native execution; do not replace it with an unqualified file-exists check. Make hashing cancelable and prevent stale readiness results after a pack changes.

**Acceptance.** Native cold/warm startup on SSD and an available slower-disk host; time-to-window separately from time-to-ready; absent/corrupt/replaced bundles; shutdown during validation; failed health checks; retry. Pending validation must not reopen repair, offer duplicate downloads, or permit operations requiring an unready capability. Establish latency budgets from these measurements before enforcing them.

### R16 — Repair and secure the offline model-export toolchain

**Evidence.** [CLAP requirements](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/python/requirements-laion-clap-pack.txt#L2) pin torch 2.13.0, while [export preflight](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/python/prepare_laion_clap.py#L208) requires exactly 2.9.1. Following the requirements therefore produces an environment the exporter rejects. No large model installation/export was performed to rediscover that explicit version check.

The requirements also pin Transformers 4.57.1. GitHub reported four open alerts for that offline requirement: three high, one medium. The advisories cover [save_pretrained path traversal, fixed in 5.10.0](https://github.com/advisories/GHSA-xrqw-3rrv-vx5w), [nested LightGlue loading, fixed in 5.5.0](https://github.com/advisories/GHSA-fgcw-684q-jj6r), [remote-kernel loading, fixed in 5.3.0](https://github.com/advisories/GHSA-29pf-2h5f-8g72), and [Trainer RNG loading under older torch, fixed in 5.0.0rc3](https://github.com/advisories/GHSA-69w3-r845-3855).

These are offline Python tooling dependencies, not evidence of a desktop runtime exploit. CLAP uses local-only loading and `weights_only=True`; not every advisory route is demonstrated in this exporter, and the pinned torch is newer than the Trainer advisory's older-torch condition. MERT's local `trust_remote_code=True` is a separate provenance boundary to review.

**Remediation.** Choose and validate one compatible patched dependency set; Transformers 5.10.0 or later is a candidate, not a proven drop-in replacement. Make preflight and installation requirements derive from one tested source. Use trusted, hash-pinned source assets and an isolated export environment. Add cheap consistency checks to ordinary CI and a separate explicitly enabled conversion/parity job.

**Acceptance.** Fresh environment passes preflight; official advisory applicability is documented; CLAP/MERT exports validate; numerical parity, dimensions, model identities, provenance, and bundle manifests remain correct. Version changed model outputs and do not silently reuse incompatible caches. Avoid solving the mismatch by blindly downgrading to a vulnerable environment.

### R17 — Close risk-driven test gaps and refresh operating documentation

**Evidence.** All 205 frontend tests passed during this audit, but `pnpm test:coverage` exited 1 because three thresholds were below 95%:

| Frontend metric | Coverage |
| --- | ---: |
| Lines | 97.18% |
| Statements | 94.83% |
| Functions | 92.64% |
| Branches | 89.18% |

The repository coverage checker also failed at **20,064 / 24,435 Go statements = 82.11%**, using the same-day Windows race/all-package profile from the PR #48 validation. That profile predates only the final test-cleanup annotation; it is reused evidence, not a new whole-repository audit run. Its inclusive denominator contains the existing ignored `bin/prompt-layer-investigation` Go helper discovered by `./...`. Record that provenance and arrange temporary tools deliberately; do not exclude application modules to raise coverage. Do not compare this Windows profile directly with older Linux percentages as a regression.

Prioritize behavior gaps in app lifecycle (436 uncovered statements), MusicBrainz enrichment (407), updater (373), multichannel recommendations (362), audio (340), bridge (245), native audio runtime (239), and MB index (215). Frontend missing branches concentrate in Generate, FirstRunWizard, Playlist, Settings, and enhanced-audio states. Percentages alone do not show whether meaningful failure paths are covered.

**Remediation.** Add each R01–R16 regression alongside its fix. Establish reproducible clean-checkout coverage per host, isolate temporary diagnostic packages, and retain the 95% target. Update [test coverage documentation](test-coverage.md), [the earlier review](codebase-review.md), and [startup setup documentation](startup-setup.md): some current-facing text still mentions unpushed work, 150 frontend tests, optional/skippable setup, and startup not hashing models. Preserve dated historical measurements but identify the current authoritative status. Distinguish the cheap readiness-query method from the entire startup sequence.

**Acceptance.** Every coverage claim includes commit, platform, flags, denominator, and command; test failures and coverage-threshold failures remain distinguishable; setup instructions match installed behavior. Do not weaken assertions or remove real modules to make the gate green.

### Additional optimization candidates — measure first

| Candidate | Evidence and proposed investigation | Guardrail |
| --- | --- | --- |
| Combined audio-worker memory | [Worker sizing](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/audio/worker_pool.go#L15) is CPU-based; CLAP and MERT can each start up to four workers. Measure combined RSS/VRAM, load time, throughput, and peak concurrency on low-memory and GPU hosts before adding shared admission control or idle unloading. No OOM was reproduced. | Keep cancellation and capability reporting; declared per-model memory is not a measurement of combined use. |
| Dynamic catalog growth | [DynamicCatalog](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/internal/catalog/dynamic.go#L57) eagerly loads records, has no implemented retention bound, and sorts IDs during resolution. Profile large synthetic collections, then consider indexed/lazy lookup. | Any later retention policy must preserve references reachable from history and saved profiles; do not arbitrarily prune identities. |
| Playback render fan-out | [PreviewPlayer](https://github.com/platten/playlistai/blob/923318942464a6ade836472d96457a69d1c8f0a3/frontend/src/components/PreviewPlayer.tsx#L63) places frequent playback-time updates in shared context consumed by broad screens. Profile a 100-track playlist with expanded panels before separating clock updates from stable actions/status. | Preserve keyboard controls, selected/playing states, and workspace persistence. No rendering-latency improvement is claimed yet. |

The existing 100-track synthetic journey benchmark measured about 30.78–31.95 ms/op in three short five-iteration runs on this Windows host. Assembly-key runs were about 145–183 µs/op with noisy allocation results. These samples do not justify another sequencing rewrite for speed or direct comparison with older Linux benchmarks. R04 should change correctness first and then check for material regressions.

## Proposed delivery sequence

Use focused topic branches from current main. Commit, push, merge, release, and deploy only within the requested delivery scope; this document does not perform those actions. Keep one editing owner per file when parallelizing, and use a separate reviewer against each stable diff.

Effort labels are planning estimates for implementation and focused validation, not delivery promises: **S** is roughly up to one engineer-day, **M** roughly 1–3 days, and **L** roughly 3–5 days. Native-platform availability and model conversion can extend elapsed time.

| Order | Proposed change | Size | Dependencies and completion evidence |
| --- | --- | --- | --- |
| 1A | Fix runtime scratch ownership (R02) | M | Independent; sentinel preservation across all cleanup paths. |
| 1B | Constrain catalog manifests (R01), then harden extraction/activation (R05) | M + L | Agree on manifest contract first; separate reviewable commits/PRs. Fault injection preserves the previous catalog. |
| 1C | Fix final CSV target confirmation (R03) | M | Independent; native dialog checks and replacement failure tests. Coordinate later export edits with R12/R13. |
| 1D | Fix mandatory journey separator allocation (R04) | M | Independent of storage/UI work; full-orchestrator fixtures and generation-version decision. |
| 2A | Recover MB index locks and corrupt targets (R06) | M–L | Main already contains PR #48; preserve its rollback ordering. Native process-death and activation tests. |
| 2B | Decouple reference identities and correct exposure scoring (R07/R08) | M | Separate commits; preserve channel policies and embedding compatibility. Integrate after R04 fixtures are stable. |
| 2C | Unify feedback state from storage through UI (R09) | L | Define reducer and stable event ordering before UI edits. Derived-profile migration/replay tests. |
| 2D | Migrate current settings and make wizard completion reliable (R10/R11) | M–L | One current-preference migration contract; preserve historical modes and unsupported-host handling. Rendered interaction checks. |
| 2E | Retain export operations and redact shared URLs (R12/R13) | M | Coordinate ownership of export bridge/UI files with 1C. Deferred-promise tests and captured-log assertions. |
| 3A | Add history-summary query (R14) | S–M | Independent; before/after benchmark plus exact full-history replay. |
| 3B | Make runtime startup asynchronous and avoid redundant validation (R15) | L | Prefer after R02/R05 lifecycle contracts settle. Native time-to-window/time-to-ready measurements. |
| 3C | Validate patched offline export environment (R16) | M plus conversion time | Begin applicability/pin work in parallel; finish conversion and parity before publishing new packs. |
| Ongoing | Coverage, docs, and measured optimization candidates (R17) | Per change | Update evidence with every PR; profile candidates before approving implementation scope. |

For recommendation work, freeze shared fixtures and versioning decisions before parallel edits to rankers, sequencers, and profile construction. For UI work, specify pending, disabled, retry, failure, successful, and navigation states before implementation; use existing controls/tokens and inspect both themes and small windows.

Before the next Windows release, run an isolated installed-app upgrade check in addition to build tests: install the prior released version, launch through the Start Menu, update, verify the installed executable/version and shortcut target, relaunch twice, and confirm that the same update is no longer offered. Include canceled elevation, locked target, installer failure, and recovery. This is release verification for the already-fixed updater, not a proposal to replace its design without new evidence.

## Validation evidence and limitations

Executed during this audit:

```powershell
go run ./bin/_audit-core
go run ./bin/_audit-runtime
go run ./bin/_audit-recommendations
go run ./bin/_audit-recommendations/perf

go test ./internal/reco/... ./internal/catalog ./internal/resolution ./internal/taste ./internal/semantic ./internal/history -count=1
go test ./internal/updater ./internal/mbindex ./internal/audio ./internal/modelpack ./internal/intent/llama ./internal/app
go test ./internal/app -run 'TestRecommendationSettingsPersistAndPreserveOtherPrefs|TestSetupReadinessOptionalPoliciesAndPreviewOff' -count=1
go test ./internal/reco/multichannel -run '^$' -bench 'Benchmark(CategoryJourney100Tracks|AssemblyKey100Tracks)$' -benchmem -benchtime=5x -count=3

# From frontend/
pnpm exec vitest run --config ../bin/audit-ui/vitest.config.mts
pnpm test:coverage

# From repository root; reused same-day profile
go run ./cmd/coveragecheck -profile bin/main-ci-repair-validation/backend.out
```

- Core/runtime/recommendation diagnostics reproduced the reported audit defects using temporary data. They assert or print the current incorrect behavior; they are not evidence of fixes.
- The recommendation/catalog/resolution/taste/semantic/history tests passed. The runtime package batch passed except updater tests, which encountered sandbox `Access denied` during `EvalSymlinks`; redirecting temporary storage did not resolve that restriction. This audit failure is an environment limitation, not a demonstrated updater regression. Earlier full validation and hosted checks passed.
- The targeted app tests passed. Both isolated UI audit tests passed by demonstrating legacy-mode and duplicate-handoff bugs. The 205 regular frontend tests passed; frontend coverage and the reused Go coverage check failed their thresholds as reported above.
- Initial sandbox restrictions on GitHub CLI configuration and frontend tools were resolved with approved elevated read/test execution. No private provider requests, real-user-data reset, native installation, or large model download was used for reproduction.
- Diagnostics live under ignored `bin/` paths; Go audit packages use `_audit-*` names so ordinary `./...` does not discover them. They are local audit aids, not committed regression tests. Each implementation PR should promote its fixture into the affected package's test suite.
- The full repository gate was not rerun solely for this planning document. Reused same-day PR #48 validation and fresh audit checks are distinguished above. During implementation, run targeted checks, then `./scripts/test.sh` or `.\scripts\test.ps1`, frontend behavioral tests, and relevant coverage checks. Report native/platform checks actually executed and every skipped check.

This was a broad, risk-directed code audit with independent recommendation, runtime/storage, and UI/bridge reviews, followed by integration and bounded reproductions. It is not an exhaustive proof of correctness or a musical-quality benchmark. Native inference latency, real provider behavior, full installer upgrades, save-dialog behavior, and low-memory contention remain explicit verification tasks.

## Completion criteria for the remediation series

1. Downloads, cleanup, and exports affect only validated, operation-owned or explicitly selected targets; failures preserve previous usable assets.
2. Interrupted setup can recover, and completion/settings state remains truthful after persistence failures and restarts.
3. Valid journeys succeed; reference evidence and exposure treatment do not depend on unrelated embedding availability; preference corrections have one reproducible meaning.
4. Navigation cannot duplicate an in-flight export or lose its result; normal logs contain no playlist share token.
5. Saved history, seeds, modes, profile snapshots, and embedding identities retain explicit compatibility behavior through every migration.
6. Performance changes include reproducible measurements; dependency upgrades include conversion evidence; coverage and documentation report actual checked results.
