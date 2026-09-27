# Reliable seeds and descriptive matching: development validation

Automatic remains staged. Software correctness checks and a bounded compatible audio pack passed; independent listening evidence needed for musical-quality promotion has not been supplied. This report separates actual generation results, public-source audits, and unavailable quality gates.

## Protocol

The final native run uses all 40 original development prompts in `internal/evaluation/testdata/varied-prompts-v1.json` (400 requested slots). Each prompt is freshly interpreted by the installed local Qwen3.5 9B Q4_K_M parser. One application stays open across the suite, matching a warmed desktop session. Each operation has a 120-second deadline, with preparation finishing around 115 seconds to leave five seconds for assembly. Initial model/catalog startup is measured separately and excluded from generation latency.

The Linux amd64 run uses Go 1.27.1 on WSL2, an Intel Core Ultra 9 285H with 16 visible CPUs. Mutable development stores were copied to an isolated directory; only immutable large assets were hardlinked. The installed prepared catalog was read without modification. Existing development caches and public metadata/derived previews from earlier development epochs were retained. A targeted direct verifier probe populated the previously absent Nina Simone recording and exact-release responses before the final run. Cache condition is explicitly warm; the final run cannot establish cold online acquisition coverage. No ListenBrainz credential or connection marker was copied, loaded, or used.

The exact executable SHA-256 is `c74f9eb05695e22d099ef0c5ab7cc709e87e84a5e6ab6c68ff40005589b8dcfc`. The fixture SHA-256 is `35370384e41ea812c4a2a63878c0dddd7477cebdd50d56561f8869f2d834572e`. The receipt records runtime, model, graph and source-manifest hashes, every lossless RNG seed, prepared snapshot, per-case output, errors and timings. The source tree was dirty; the recorded base revision alone does not identify the executable.

Reproduction command, with private locations replaced by placeholders:

```sh
go build -o "$EVAL_ROOT/musiccheck" ./cmd/musiccheck
"$EVAL_ROOT/musiccheck" -mode automatic \
  -app-data-dir "$ISOLATED_APP_DATA" -catalog "$READ_ONLY_CATALOG" \
  -runtime "$LOCAL_LLAMA_RUNTIME" \
  -prompts internal/evaluation/testdata/varied-prompts-v1.json \
  -case-timeout 120s -eval-split development \
  -variant reliable-seeds-final -cache-condition warm \
  -output "$EVAL_ROOT/forty.json" -supervised-child
```

`-supervised-child` keeps this benchmark in one process. An external 90-minute watchdog bounds the whole suite. Two early startup observations, a 25-case pre-guard development baseline, a 10-case v5 epoch, and v6 diagnostic controls remain separate from the final results. They are not counted as completed final cases. Earlier 30-second runs used different random seeds and cache states, so differences cannot establish causal improvement.

## Final 40-prompt results

The [complete receipt](data/reliable-seeds-development-2026-09-27.json) includes all 40 prompts and all 206 returned song occurrences. The [source audit](data/reliable-seeds-independent-audit-2026-09-27.json) keeps the earlier interrupted epochs separate.

| Measurement | Final result |
| --- | --- |
| Requested / returned slots | 400 / 206 (51.5%) |
| Outcomes | 2 fulfilled, 38 partial |
| Operation errors / outer timeouts | 0 / 0 |
| Generation median / p95 / maximum | 115.023 / 115.068 / 115.111 seconds |
| Startup, measured separately | 92.782 seconds |
| Reference-discovery cases | 84 / 130 slots; all 84 have a requested artist credit |
| Detailed-description cases | 0 / 80 slots across eight prompts |
| Direct Radiohead-only control | 10 / 10 slots |

The 80% good/requested-slot gate **fails** even if every returned song were good: its upper bound is 51.5%. The 90% independent strong-match precision gate is unmeasured. No other-artist discoveries were established, and detailed descriptions remain unsupported. These failures cannot be hidden by direct-artist matches. The harness also reports 54 adjacent-artist-repeat findings and five minimum-artist-variety findings; no benchmark assertions were weakened. These generic checks are distinct from the user's explicit constraints. Case 23 recorded a `parser_error` fallback and returned no tracks; zero operation errors does not mean every primary parser call succeeded. There were no interpretation findings or repeated declared recording MBIDs within the final playlists.

CLAP was active. Returned results contain 185 CLAP criterion-signal occurrences from preview assessments across 100 distinct catalog recordings; three returned-track source occurrences explicitly identify `library_clap` retrieval. These counts overlap tracks and are not accuracy labels. Metadata or MERT can retrieve a track that CLAP subsequently assesses. The installed pinned original LAION CPU bundle supports inference, while general calibrated fit remains unavailable; the capability DTO's `enabled: false` refers to that calibrated general-fit feature, not absence of CLAP inference. Literal subjective phrases still require their own validated calibration.

The warmed generation measurements satisfy the 120-second limit. Cold startup plus the first generation took about 207.8 seconds and does not satisfy a two-minute launch-to-result bound. No cold-cache or cross-platform latency claim is made.

## Auditing and limits

Each returned occurrence is checked separately. Exact prompt, catalog ID, displayed artist/title, declared recording MBID, ISRC, album and duration must match before an earlier public-source audit can be reused. Such reuse establishes only its original cited facts. Independent-agent notes cover the initial completed occurrences; the coordinator reviewed all 206 final occurrences. The final review conservatively retains 148 unknown occurrences and 58 with scoped public-source support. New or changed occurrences without independent sources remain unknown. Neither audit is an independent listening label, and neither proves that an uncalibrated subjective criterion is satisfied.

Direct reference credits include compound and featured artist credits. Other returned artists are counted separately as other/unverified, never automatically as successful discoveries. A declared recording MBID is catalog identity evidence, not proof that a particular audio master was independently authenticated. Similar titles or durations alone do not establish a known duplicate.

The main run retains its original prepared audio pack. The separately prepared [bounded original-audio subset](reliable-subset.md) contains 16 compatible original-LAION vectors, with exact model compatibility and negative compatibility controls. It is not substituted into the all-40 run. The subset's web annotations remain a separate sidecar and do not calibrate sonic thresholds. See [descriptive calibration](descriptive-calibration.md) for the distinction between existing taxonomy development experiments and independently validated defining facets.

## Identity regression and prior observations

The earlier development audit found a Nina Simone row whose declared recording/release identified a roughly 380-second performance while its reliable decoded duration was 576,093 ms. The full-master duplicate suspicion remains unproven. The final implementation checks cached exact recording evidence for every prepared row before the bounded online work. Release-track duration takes precedence over aggregate recording length: the exact Bill Withers edition of “Don’t It Make It Better” remains valid at 253,859 ms despite a different aggregate length.

The [matched identity control](data/reliable-seeds-development-identity-control-2026-09-27.json) used the final binary and the original case 8 resolved intent and seed. The conflicting Nina row was among 435 considered candidates, but was neither eligible nor returned. The valid Bill edition remained in the 10-track result (six Nina, four Bill). Generation took 115,022 ms; startup took 96,936 ms separately. Generic harness artist-diversity findings remain recorded. A separate actual-data cached preparation probe also rejected Nina and retained Bill; its 5.49 ms timing describes only that narrow cached check, not generation latency.

## Executed software checks

The final Automatic v7 / source-atoms v21 repository gate passed: Go vet, pure-Go core compilation, race tests, golangci-lint (zero issues), generated bindings, frontend typechecking, 259 frontend tests, and production frontend build. The Python calibration helper passed three tests.

Rendered Generate/Settings checks passed in dark and light themes at 390- and 1000-pixel widths, including cancel, stop-and-keep, stale response protection, navigation and reset. Dark 390 and light 1000 captures were visually inspected. These are browser interaction checks; they do not establish native behavior on Windows or macOS.

## Promotion gates

The 80% good/requested-slot gate failed by the returned-slot upper bound. The 90% independent strong-match precision gate, held-out recall, and vocal-leakage listening gate remain unevaluated. Qualified descriptions without independent calibration remain unknown. Passing code tests, web corroboration, compatible vectors, or a full playlist does not satisfy these gates. Automatic stays staged.

## Representative controls

The [seven completed controls](data/reliable-seeds-development-controls-2026-09-27.json) cover cases 1, 6, 8, 10, 36, 37 and 40. Every control used the same final binary, resolved intent, exact lossless seed, algorithm, profile snapshot and 120-second budget as its corresponding final raw run. These are warm-cache regenerations, not saved-result replay; cache growth and bounded acquisition can change the output.

The controls returned 36 of 70 requested tracks, with 0 operation errors and 0 outer timeouts. Median generation was 115.014 seconds and maximum 115.052 seconds. All returned occurrences have source-audit entries; none is a human listening grade.

| Original case | Raw / control tracks | Same ordered tracks |
| --- | --- | --- |
| 1 | 1 / 2 | No |
| 6 | 10 / 10 | No |
| 8 | 10 / 10 | No |
| 10 | 4 / 4 | No |
| 36 | 0 / 0 | Yes |
| 37 | 0 / 0 | Yes |
| 40 | 10 / 10 | No |

The known Nina conflict remained excluded. General diversity/coverage findings and missing descriptive calibration remain; successful controls do not justify promotion.
