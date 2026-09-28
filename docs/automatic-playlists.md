# Automatic playlists

The [matching upgrade](automatic-matching-implementation.md) describes Automatic v8 / fit v5 and its validation limits. The [reliable-seeds validation](reliable-seeds-validation.md) remains the historical v7 40-prompt baseline; it has not been relabelled as a v8 result. Earlier measurements below retain their original build scope.

Automatic is the default for new installations and unset preferences, with
separate saved engine and evidence versions. Enhanced Hybrid and Deej-AI only
remain selectable in Settings, and saved mode preferences take precedence over the
configuration default. An explicit `recommendation.strategy = "multichannel"`
or `"deejai"` still selects that mode when no preference is saved. The
independent musical-quality gates in [automatic-evaluation.md](automatic-evaluation.md)
remain unmet; choosing Automatic as the default does not establish that its
results pass those gates. Saved playlists keep their pinned mode.

## Generation

The desktop submission starts one two-minute deadline. Interpretation, identity
resolution and candidate preparation share the first 115 seconds; the remaining
five seconds are reserved for local assembly. Reusing a preview never restarts
the clock. Cancellation and stop-and-keep remain separate operations.

1. Parse the request locally and pin the installed identity/popularity snapshot.
2. Resolve explicit artist references by provider ID. Among compatible exact
   names and aliases, public unique-listener counts lead and listen counts break
   ties. Explicit identifying context and user choices take precedence. Missing
   counts remain unknown. An automatic choice preserves alternatives and does
   not become user confirmation.
3. Retrieve a bounded pool from catalog relationships, compatible prepared audio,
   recording metadata and prepared artist/neighbor suggestions. Related artists
   are looked up by recording-credit MBID, independently of nominated songs. Source/query
   round-robin selection gives retrieval sources and queries a bounded opportunity
   within the 512-candidate budget; it does not guarantee artist balance.
   Suggested recordings join the catalog by recording
   MBID; matching a title or artist name does not establish recording identity.
   Retrieval uses at most eight seconds and half the remaining preparation time.
   The graph gets an initial bounded opportunity, and local retrieval limits
   query count and page size before searching. Automatic skips legacy dynamic
   artist-profile expansion. Completed query results survive a retrieval timeout
   while the enclosing preparation context remains available to freeze evidence.
4. Check every cached recording identity conflict first. Assess the prepared pool
   with the actual selection and journey rules and stop when count, required
   recordings, strict constraints and ordering can be met. Otherwise prefer cached
   evidence, then acquire bounded missing metadata and authorized previews within
   the same deadline. Skip audio that cannot resolve a missing strict literal
   calibration. Copy the complete available metadata, compatible vectors, source provenance,
   fit assessments and popularity priors into one request-owned batch.
5. Perform final ranking, selection and sequencing using that frozen batch. There are no provider,
   model or disk-metadata calls after the boundary. Prepared popularity adds a
   small bounded ranking preference among suitable tracks; it never fills an
   evidence gap. This implementation does not target a 60/40 output ratio.
6. Derive a title locally and save the result, seed, intent, artist decisions,
   profile, source versions, candidate decisions and fit evidence together.

Changing an artist in Generate or Playlist creates an explicit identity choice.
The other identities remain selectable. Playlist controls and navigation preserve
the choice; replay of an unchanged saved request uses the frozen result even if
today's models, catalog, profile or prepared graph have changed.

## What a fit label means

`strong`, `plausible`, `unknown` and `conflicting` are categorical estimates,
not calibrated probabilities. Copied tags count as one evidence family. Ordinary
recording tags need independent support before satisfying a defining musical
criterion; compatible audio and independent metadata may agree. Attributed
recording claims retain their own applicability rules.

Defining genre requirements and strict no-vocals requests require strong support.
Unknown or conflicting candidates are omitted even when this shortens the
playlist. Positive vocal detection vetoes no-vocals admission; silence in a
sample alone does not establish absence throughout the recording. Evidence UI
shows measured audio coverage. Ordinary mood, instrumentation and texture
descriptions retain their complete wording and guide estimated ranking. An
explicit `required` strength remains strict; a historical essential criterion
with missing strength is not silently relaxed. A generic instrument cannot
establish its requested tone or playing style. Raw model outputs are ranked within
the same model and complete clause, then combined by rank; they do not become
calibrated probabilities. Descriptive journey stages have separate query
opportunities, scores and ordered membership. A full playlist can still report
“Best estimates — some qualities are unconfirmed.”

Strict audio-only subjective support still requires an independently validated
literal-facet calibration, including its model fingerprint and sampled coverage.
No production calibrations ship with this change. See
[calibration](descriptive-calibration.md).

For a requested genre mix, each track can support one member and the playlist
must cover the requested members. Explicit dates, recording versions, required
tracks, artist exclusions, deduplication and journey order remain constraints.
Popularity is never evidence for genre, mood or instrumentation.

For a freshly parsed ordinary “like Radiohead” reference, a pinned artist
relationship with a catalog identity join or compatible reference-audio evidence
can admit estimated discoveries. “Radiohead only” remains an artist restriction.
Reference strength survives parsing, bridge transport and saved inputs. An old
reference with missing strength keeps the earlier conservative calibrated
graph-plus-audio policy until explicitly reinterpreted. Similarity never proves
influence or genre. Explicit required tracks keep their separate identity and
musical-constraint checks.

Song references can retrieve graph suggestions through the exact recording's
typed artist credits. Display names and album-artist tags cannot supply that
identity. Credit expansion is bounded, happens before the frozen boundary, and
does not turn the song reference into an artist-only requirement.

## Optional ListenBrainz connection

Settings supports connect, replace and disconnect. Credentials use the OS
credential store, with explicit session-only fallback when it is unavailable.
Only connection status crosses the settings read API. This connection retrieves
discovery metadata; it neither imports listening history nor submits listens.
Authenticated top-recording results improve seed choices among exact identities.
They cannot establish musical suitability or repair missing joins by themselves.
Metadata proposals must still pass the existing recording resolver and exact
catalog identity checks. Public discovery remains available without a token.

## Prepared data and rollout

The graph producer/installer is documented in
[automatic-music-data.md](automatic-music-data.md). It uses public aggregate data,
requires no user account, stores provenance and omissions, and activates a
verified immutable artifact atomically. It retains prior hashes for replay.
There is no published default graph manifest in this change. A maintainer must
prepare and verify broad artist coverage, publish a versioned release artifact,
and configure `metadata.music_graph_manifest_url`; periodic publication is
separate from foreground playlist work. The Settings card supports explicit
verified updates when a source is configured.

[Resumable preparation](prepared-music-jobs.md) builds a stratified artist plan
from catalog identities, checkpoints public batches and reports exact recording
coverage independently of development prompts. [Discogs-EffNet preparation](discogs-effnet.md)
adds optional specialist scores to checksummed packs. Existing packs stay readable;
uncalibrated heads sharing an encoder remain one source and cannot satisfy strict
requirements. The additional prepared-data budget is 10 GB.

Embedding compatibility remains a release prerequisite. The installed discovery
pack examined on 2026-09-27 contains `laion/larger_clap_music` vectors, while the
recommended installed text encoder is the original LAION HTSAT-base checkpoint.
Those spaces are incompatible, so the runtime correctly withholds text/audio
comparisons. The stored GPU-created vectors still work for same-pack audio-seed
neighbors, direct recording-to-recording similarity, and playlist sequencing;
Automatic and Enhanced Hybrid use the installed discovery pack for these paths.
This does not establish prompt-to-audio fit or calibrate related-artist admission.
Prompt comparison against this pack requires the exact paired
`laion/larger_clap_music` text encoder and CUDA runtime identity, or a new pack
encoded with the currently installed original LAION bundle. Reinstalling the
same recommended model does not repair the old pack. The older larger checkpoint
also has a recorded text discrimination problem; see
[clap-model-candidates.md](clap-model-candidates.md).
The completed 40-prompt development run recorded three returned-track
`library_clap` retrieval-source occurrences from this installed discovery pack.
That establishes actual use of its stored vectors, not musical-fit precision.

The original 40 prompts are development diagnostics, not held-out acceptance.
Published independent labels and production-engine ablations must establish
precision, retrieval recall, usable output, vocal leakage and actual end-to-end
latency before claiming that Automatic meets the musical-quality gates. Unit
tests or higher track counts cannot substitute for these measurements.

The historical `automatic/v3+automatic-fit/v3` 40-prompt development run returned
120 of 400 requested tracks: seven operations reported fulfilled, 32 partial,
and one needed clarification. Median generation time was 13.55 seconds,
nearest-rank p95 25.000 seconds, and maximum 25.024 seconds after application
setup. The harness exited nonzero for parser fallback and artist-concentration
findings. The repaired interpretation timeout retained the requested ten tracks
and all three genre stages, while reporting a rules fallback and empty result.
These observations use one reused isolated store; they do not establish the
separate cold/warm acceptance gates. See the
[final runtime receipt](data/automatic-forty-v3-development-2026-09-27.json).

The [final song-by-song audit](automatic-v3-song-audit-2026-09-27.md) judged 90
returned occurrences good, 19 partial, eight mismatches and three unknown, with
280 missing slots. All 90 good judgments concern explicitly requested artists;
none of the 30 descriptive-request returns earned a good judgment. Artist
concentration and unmet discovery/journey requirements remain. This is 75% good
among returned songs and 22.5% of requested slots, not a release-quality pass.

The earlier v2 run returned 140 tracks. Its
[song-by-song source audit](automatic-song-audit-2026-09-27.md) judged 43 good,
47 partial, 25 mismatches and 25 unknown. That earlier audit motivated the strict
reference-admission repair. Both runs remain development evidence; different
seeds and cache conditions prevent a causal numerical improvement claim.
An engine's `fulfilled` status is not a musical-quality grade. No human listening
grades were assigned.

The completed [development experiment](automatic-evaluation.md#development-measurements--2026-09-27)
returned 80 of 200 requested slots with combined evidence; 76 returned slots
lacked independent labels. It does not support promotion. The remaining release
work is concrete:

- Prepare a catalog with audio features compatible with the selected encoder,
  then measure retrieval quality in that exact embedding space.
- Supply and validate signed vocal-presence evidence and independent recording
  metadata for strict instrumental admission. Raw cosine and sampled silence
  cannot fill this gap.
- Expand independently labelled genre, mood and texture coverage before freezing
  a policy and opening heldout results. Keep unsupported requests partial.
- Improve interpretation and coverage of defining non-genre descriptions. The
  development audit found string-bearing pop/rock admitted for an orchestral
  request when orchestral and dramatic properties remained unknown soft
  preferences. A full track count and a plausible label do not establish those
  requested properties.
- Prepare broad artist coverage and a verified public graph release, with a
  monthly publication process. The installer is ready; a default published
  artifact is not included.
- Define and validate familiarity groups before implementing the planned soft
  60/40 mix. The current listener percentile is relative to the installed graph,
  and a small popularity ranking bonus does not establish that output ratio.
- Measure full-application retrieval recall and both cache-condition latency
  gates before claiming musical-quality acceptance.

## Validation commands

```sh
./scripts/test.sh
go test ./internal/bridge -run '^TestAutomatic' -count=1
# Explicit isolated prepared store; never point this at personal application data.
go run ./cmd/musiccheck -app-data-dir /external/isolated-store \
  -mode automatic -catalog /external/catalog \
  -prompts internal/evaluation/testdata/varied-prompts-v1.json \
  -output /external/automatic-development.json
```

Rendered interaction checks reuse `capture-generate-settings.mjs` with
`PLAYLISTAI_CAPTURE_MODE=automatic` and `capture-automatic-playlist.mjs`.
They cover submission timing, cancellation/stale responses, data updates,
reversible identity, draft restoration, partial outcomes, exact seed strings,
evidence details and both themes at small/large desktop widths. These are mocked
bridge UI checks, not native-host or musical-quality measurements.

The final Linux/WSL2 repository gate passed: Go race tests, vet, pure-Go core
compilation, lint, generated bindings, frontend typechecking, all 255 frontend
tests and the production build. The
[validation receipt](data/automatic-validation-2026-09-27.json) pins the logs and
independent reviews. Windows/macOS native behavior and installer execution were
not measured in this change.
