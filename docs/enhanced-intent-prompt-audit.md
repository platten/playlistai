# Enhanced Hybrid: intent extraction and LLM prompt audit

Research snapshot: 2026-09-12, branch `codex/enhanced-audio-remaining`, commit
`7e91fc1`. This is a proposed remediation design, not an implemented change or a
claim of improved listening quality. The provider and ranking audits complement
this report. No production files, model files, or user data were changed here.

## Findings from the current code

1. **There is no common deterministic extraction before the LLM.**
   `internal/app/app.go:334` invokes the active parser first. Rules run after an
   error; `internal/intent/llama/client.go:327` constructs messages from the raw
   request and examples without extracted facts. `schema.go:157` then runs a
   collection of repairs after JSON generation: counts, category journeys,
   defining categories, negation, artist-only intent, destinations, periods,
   quality clauses, emotions, and vocal meaning. Reusing these facilities is
   preferable to building a competing interpreter, but their common output
   should exist before model inference and remain authoritative afterwards.

2. **Known omissions can survive or trigger another entire completion.**
   `rules/rules.go:514` has 13 known style phrases; Dubstep is absent.
   `schema/semantic_preservation.go:49` can only restore the defining categories
   the fallback understands. `schema/meaning.go:26` detects an omitted
   affirmative phrase before “but with no…” and rejects it; it cannot recover
   that phrase's category. `llama/client.go:99` then regenerates the whole
   object. The documented Dubstep failure follows this path. Preserve the exact
   affirmative concept first, then ask the model only about unresolved meaning.

3. **The rules fallback is not a safe universal override.**
   `rules/rules.go:407` excludes via a regex that lacks plain `no`; its captured
   name is not an occurrence-aware exclusion list parser. The current style
   list, several mood/instrument checks, and control keyword matches are narrow
   English rules. Merely unioning the fallback's entire output with the LLM
   would add mistakes, including a default artist-spacing constraint from
   `rules.go:87`. A new prepass must carry certainty and source scope instead.

4. **Genre preferences can become too strict.**
   `schema/schema.go:374` promotes every positive genre that lacks a criterion
   into an essential genre unless its evidence contains `influence` or
   `touch of`. In a journey its default scope is the start. “A bit of rock,”
   “ideally jazz,” alternatives, and unfamiliar ways of expressing a hybrid do
   not share an explicit strength/group contract. The model can classify a soft
   phrase as a genre and create an unnecessary gate. `core/intent_normalize.go:24`
   also preserves a single playlist genre as essential. Keep the primary
   requested identity essential, but encode optionality, conjunction and
   alternatives rather than inferring them later from two English phrases.

5. **The existing provenance contract is useful but incomplete for merging.**
   `core/intent.go:35` already supports byte offsets. Model-to-core conversion
   at `schema/schema.go:576` sets both offsets to `-1`. The grammar carries only
   copied text, and `validateOpenIntent` uses accent/punctuation-normalized word
   containment, not an exact occurrence identifier. Repeated words with
   different polarity or journey stages cannot be merged reliably by value.
   `IntentPreference` at `core/intent.go:133` has no scope; criteria do. Preserve
   occurrence IDs and explicit scopes on all relevant atoms, including soft
   moods/textures. Existing repairs deliberately skip ambiguous repeated words;
   preserve that caution rather than flattening them.

6. **The prompt makes a small model do interpretation and music retrieval at
   once.** `schema/prompt.go:3` contains 6,498 characters/885 whitespace words;
   four full JSON examples add 3,020 characters, for 9,518 characters before
   example request text and the chat template. These are character counts, not
   token measurements. The desktop defaults to 4,096 context at
   `config/config.go:172` and `llama/server.go:50`; generation reserves
   1,800 then 2,400 output tokens. The coordinator measured the owned Qwen
   server's actual chat template and tokenizer: the first new classical request
   produces 10 messages, 10,092 characters and **2,071 prompt tokens**. That fits
   the first nominal budget (2,071 + 1,800 = 3,871), but the retry's requested
   maximum exceeds the configured context (2,071 + 2,400 = 4,471), even before
   correction feedback. Runtime truncation/context shifting behavior must be
   measured; this arithmetic alone does not establish the cause of a particular
   wrong interpretation or closed connection. The retained measurement is
   `bin/enhanced-translation-investigation/prompt-size.json`.
   `musiccheck`'s directly started runtime uses 8,192, so tests must explicitly
   compare the actual desktop setting. All four current examples have no
   inferred anchors even though the system asks for them on vague requests.
   There is no example of a journey, unknown genre, multi-artist exclusion,
   negative mood, or soft hybrid in the active few-shot set.

7. **Correction feedback disturbs a cached prefix and duplicates guidance.**
   `llama/client.go:132` appends the same validation error to both the system and
   final user message. The prefix cache is enabled, but this changes the
   beginning of the request. Retry is bounded and greedy decoding is already
   enabled; retain those safeguards. Prefer compact field/atom-level feedback
   in a separate final context block, with literal facts still fixed. Measure
   copy fidelity, truncation and latency separately from model-server transport
   failures; there is no evidence that a dictionary alone fixes a closed HTTP
   connection.

8. **Names need a separate, explicit correction policy.**
   `schema/open_intent.go:37` correctly keeps user surnames rather than allowing
   an LLM expansion to assert identity. `catalog/resolver.go:154` searches exact,
   alias, prefix and complete token matches. `musicbrainz/artist_seeds.go:42`
   and `:192` require a normalized name/alias match, not arbitrary fuzzy
   substitution. This protects identity but does not by itself establish
   “christrian loeffler” as Christian Löffler. Preserve the original string,
   generate a bounded typo candidate list separately, corroborate identity and
   catalog recordings, and show a correction/choice. Do not add a musical genre
   simply because the corrected artist is associated with one.

9. **Locale is not used in the model messages.** It participates in the bridge
   cache key (`bridge/lifecycle.go:230`) and session input, but `userMessage` at
   `llama/client.go:339` emits only prompt, now-playing and recent tracks. The
   lexical repair patterns are English. Keep language and locale explicit,
   preserve original spans, and translate only semantic query captions; do not
   translate proper names or turn language detection into a genre assumption.

10. **Duration is missing from the contract.** `Wire` and `IntentControls` carry
    track count, not a target duration. The deterministic count parser already
    tries to distinguish time units (`rules/count.go:85`), but cannot preserve
    minutes in a typed destination field that does not exist. A 30-minute
    request should become a duration target plus a bounded estimated count;
    until duration targeting is supported, preserve an explicit unsupported
    requirement rather than silently making 30 tracks. `centuryPattern` at `schema/open_intent.go:12`
    also accepts a whitespace separator only, so the common hyphenated
    `20th-century` form misses deterministic period normalization.

11. **A named journey start is not currently an output requirement.** The user
    has clarified that Nine Inch Nails → Marilyn Manson means an actual Nine
    Inch Nails recording first, an actual Marilyn Manson recording last, and
    related discoveries between. `core/intent.go:206` has `Destination` but no
    corresponding required-start endpoint. At
    `reco/multichannel/orchestrator.go:734`, required tracks come from
    `RequiredTracks`; the destination block at `:737` adds the final recording
    and explicitly avoids promoting an arbitrary starting reference.
    `journeyAnchors` at `:1247` supplies retrieval/trajectory references, not
    mandatory output. `sequencer.go:54` fixes the sole destination last and
    selects its beginning from candidates. This behavior is intentional for
    general references, as `artist_requests_test.go:91` verifies. Add explicit
    endpoint output roles rather than changing every reference into a required
    recording. Resolve an eligible real recording for each required artist
    endpoint and count both within the requested total. Preserve the ordinary
    “like Aerosmith” reference-only behavior.

## Observed native interpretation failures on the new prompts

The coordinator ran the production prompt/model on the ten new realistic
requests and retained the first report at
`bin/enhanced-translation-investigation/native-parser/ai-parser-1.json`.
This audit inspected that artifact, rather than treating passing schema tests as
evidence of satisfactory parsing. The first run had only two cases without the
fixture's current errors; manual inspection found semantic errors in both.
This is one development run, not a stable quality estimate; repeat results and
the strengthened evaluation belong in the integrated report.

| Request detail | Actual first-run interpretation | Remediation target |
| --- | --- | --- |
| Relaxing classical, **mostly piano and strings** | Piano and strings became essential textures; the narrow fixture still passed | Instrumentation versus texture; optionality and strength |
| Dramatic **20th-century classical**, not film scores | `dramatic` and `20th-century classical` became genres; no period; film scores became an excluded album | Typed senses, hyphenated dates, musical-category exclusion |
| Aerosmith-like workout, **don't include Aerosmith** | Correct exclusion was rejected as lacking a negative instruction | Contracted negation and negative-clause boundaries |
| Christian Löffler/Kiasmos, **melancholic but comforting, mostly instrumental** | Melancholic and comforting became essential genres; mostly instrumental became texture | Broad mood/vocal lexicon; soft scope |
| Weightlifting, **heavy, aggressive**, no slow ballads | Heavy/aggressive became essential genres; slow ballads became an excluded track | Sound description versus genre/entity discrimination |
| **30-minute** running, **no harsh vocals** | Count became 30; no harsh vocals became an affirmative essential texture; narrow fixture passed | Typed duration, negative vocal quality and stronger assertions |
| Dark industrial rock, **less aggressive** | `dark industrial rock` was one genre; less aggressive became essential style | Separate mood/category and comparative negative preference |

The rejection of “don't include Aerosmith” is directly consistent with
`schema/meaning.go:15`: its negative prefixes omit `don't`/`do not`, while an
`include` boundary further complicates the clause. The new extractor must not
reclassify a legitimately excluded reference as an invented exclusion.
Likewise, `validateConstraintMeaning` currently checks grounding of names, but
grounded words such as “slow ballads” are still not an explicitly named track.
The dictionary should validate entity versus descriptor kind before compiling
an exclusion to a provider identity filter.

## Proposed extraction and merge contract

Introduce one versioned Go-native `IntentAtom`/`ExtractionSnapshot` intermediate
representation. An atom needs a stable occurrence ID, original text and byte
range, concept/sense ID where known, kind, polarity, stage, strength, optional
alternative-group ID, origin, and ambiguity candidates. Source grounding and
musical evidence must stay separate. A perfect lexical match proves what was
requested; it never proves that a recording meets that request.

Process the original request once, before the LLM:

1. Preserve the original UTF-8 text. Build normalized search text with a mapping
   back to original byte spans; use Unicode boundaries, longest phrase matches,
   and normalization that does not lose identity evidence.
2. Identify quoted/named-reference syntax, counts, periods, negation boundaries,
   coordination, contrast, scalar modifiers, and journey clauses. Protect names
   such as Electronic, Jungle, Air, No Doubt and song titles from genre/mood
   extraction. “Like” can introduce an entity or a musical comparison; context
   and catalog candidates must resolve ambiguity, not capitalization alone.
3. Match reviewed lexicon entries within those scopes. Lock only unambiguous
   explicit atoms. Exact known genre matches in musical-category context,
   track counts, explicit exclusion lists, and clear instrumental instructions
   are good initial targets. Unknown terms remain open-vocabulary residuals.
4. Send the original request plus the small extracted snapshot and unresolved
   spans to the LLM. Ask it to interpret unresolved relationships and describe
   sound, without renaming/deleting locked IDs. Never send the entire taxonomy.
5. Validate and merge by occurrence ID. User-confirmed choices outrank locked
   explicit facts; locked facts outrank conflicting model output. Reviewed
   unambiguous aliases normalize concept identity while preserving display text.
   Model inferences are lower-priority retrieval hints. Ambiguous matches are
   candidates, not overrides. Surface genuine user contradictions instead of
   silently choosing a side. Do not union whole rule and model intents.
6. Compile the merged contract separately for providers. Store the contract,
   lexicon/compiler versions and model identity in cache/history fingerprints;
   migrate old history without reparsing the original request on every replay.

Strength must be explicit:

| User wording | Meaning to preserve |
| --- | --- |
| “No vocals,” “exclude Aerosmith” | Explicit exclusion; never relax during filling |
| “Classical music” | Defining musical category; strongest candidates first |
| “Some rock influence,” “a bit of jazz,” “ideally acoustic” | Soft influence/preference unless explicitly made strict |
| “Classical or ambient” | Alternative group, not requiring both labels on every track |
| “Less aggressive,” “not too sleepy” | Scalar reduction; not necessarily complete absence |
| “Not only piano,” “no more than ten” | Additive/count semantics, not piano/artist exclusion |
| “Start calm, finish intense” | Distinct stages; no global averaging that erases direction |

The user has now chosen **strong matches first, then clearly labeled close
matches to fill**. This should affect eligibility tiers and fulfillment wording,
not rewrite the extracted request. Exact artist/track exclusions and required
recordings remain enforced. Broader genre/sonic neighbors can fill later tiers
only with honest reasons, coverage information and a bounded distance policy.

## Dictionary structure and examples

Use an embedded, compressed/versioned data asset with a compiled Go loader and
validator. Offline generation may use Python; distributed binaries must not.
Keep dictionary entries reviewable with source, license, version, ambiguity and
mapping relation. Separate four kinds of data:

- Lexical equivalences: `D&B`, `DnB`, `drum 'n' bass` → a canonical concept while
  preserving the user's spelling. Locale aliases can join the same concept.
- Context-sensitive meanings: `classical` as category; `Romantic` as historical
  period only with supporting context; `minimal` may concern arrangement or a
  genre; `dark` may mean mood/timbre. Each needs context rules and abstention.
- Colloquial sound descriptions: `warm`, `punchy`, `airy`, `chill`, `driving`,
  `microdetail`, `workout`. Store candidate sonic interpretations, not fabricated
  BPM thresholds or genres. For example, “workout” does not itself mean EDM.
- Provider projections: exact alias, broader/narrower relation, related style,
  free-text caption, measurable proxy, unsupported. Never use a single synonym
  table for all of these relationships.

Provider output is intentionally heterogeneous: MusicBrainz uses identity and
genre/tag queries; AcousticBrainz adapters use actual installed classifier
labels/features; CLAP receives short acoustic English descriptions with separate
positive/negative clauses; MERT uses compatible audio embedding references or
validated trained heads rather than genre strings as if it were a text model.
Requested tempo/dynamics can route to calibrated DSP evidence. Keep evidence
strength and missingness provider-specific. A broad `electronic` match must not
be rewritten into proof of `Dubstep`; a mood classifier cannot prove listening
context, vocalist identity, or microdynamics.

For Christian Löffler-like music, entity normalization first resolves the typo
against identity evidence. Reference audio then anchors MERT/CLAP similarity;
“relaxing electronic” remains separately preserved positive sound/category
intent. For Aerosmith, retrieve the verified reference identity without creating
an artist-only restriction. For Nine Inch Nails → Marilyn Manson, preserve the
two artist stages and, under the user's confirmed preference, require an actual
Nine Inch Nails first recording and Marilyn Manson last recording. Do not
replace either name with an inferred `industrial` genre requirement. Classical
requests must retain
composition versus original-recording dates and exact named work/performer
distinctions. These are architectural examples, not measured retrieval results.

## Shorter system-prompt candidate for a controlled probe

The upstream [llama.cpp grammar documentation](https://github.com/ggml-org/llama.cpp/blob/master/grammars/README.md)
states that constraining a JSON shape does not inject its semantic description
into the model prompt. Retain a concise contract description and examples when
reducing prompt size. The [server documentation](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)
also warns that prompt caching can yield different logits across batch sizes;
greedy sampling alone is not a guarantee of identical repeated outputs. Record
cold/warm cache state, actual runtime revision and backend during comparisons.
The [Qwen2.5-3B-Instruct model card](https://huggingface.co/Qwen/Qwen2.5-3B-Instruct)
specifies a 32,768-token context and 8,192-token generation capacity for this
model, but the application's configured 4,096 limit is the relevant budget.
These upstream documents were checked on 2026-09-12; verify behavior against the
packaged runtime rather than assuming every current upstream option exists.

The following candidate is designed for the current production grammar and examples;
it does not require the proposed new atom schema. It is an experimental ablation,
not a replacement already validated for shipping. First compare it using the
same model, tokenizer, context, examples, seeds and requests. Then separately
test diverse example selection and the locked-atom contract, so their effects
are not confused.

```text
Translate the user's music request into the exact JSON contract shown in the examples. Return only that object. Preserve every explicit instruction; do not invent instructions or evidence of musical fit.

Copy spans from the original request. Keep artist/album/track names exactly as written, including abbreviations or typos; identity resolution happens later. References contain only named entities with explicit=true. A bare artist means similar music, not artist-only. Categories, moods and activities are not artist names. Required_tracks contains only explicitly required recordings. Destination is empty unless the user explicitly asks to finish at a named entity.

Preserve unfamiliar genres verbatim. Put named categories in genres, manner/influences in styles, emotions in moods, instruments in instrumentation, production/dynamic detail in textures, and vocal descriptions in vocal_preference. Instrumental means vocal_preference.value=instrumental. Do not duplicate a genre across these fields. Romantic is an emotion unless historical/classical context indicates a period. Microdynamics is texture, not tempo.

Essential_criteria contains the defining musical requirements. Keep ordinary adjectives and optional influences soft. Preserve polarity and scope: 'no X or Y' excludes both named entities; 'not only' is additive; 'less' is a reduction. Never lose the positive request before 'but no'. Exclusion spans include the negative instruction. Never exclude a required recording or destination without an explicit contradiction in the request.

Use hard_constraints for explicit strict demands. Always-supported constraints are exclude_artist, exclude_reference_artists and no_back_to_back_artist. For other strict requirements, preserve the demand and explain its evidence need in unsupported_requirements. Artist-only/album-only requests use require_artist/require_album; album exclusions use exclude_album. Do not invent spacing restrictions.

For journeys, put category requirements in journey_start/journey_via/journey_end scopes. Journey_waypoints and destination contain only named entities. Preserve separate stage moods/energy; a transition is not itself a strict energy constraint. Temporal contains composition or original_release years with the correct stage. Decades are original releases, not remasters; century N is (N-1)*100+1 through N*100. Do not put eras in textures.

Counts include required tracks and belong only in total_count. Sound/context weights, discovery, variety and smoothness belong in their separate 0..1 controls. Use the example defaults when unspecified.

Inferred_anchors are optional retrieval proposals, never instructions or verified matches: at most three real Artist - Title tracks, no excluded artists, concise role/reason. Genre_expansions are optional unfamiliar-genre retrieval hints, never substitutions. Use empty lists when unnecessary and an empty vocal preference when unspecified. Keep output concise. Before returning, check all names, categories, exclusions, era, vocals, count and stage ordering against the original request.
```

The stronger next contract should remove inferred anchors and expansions from
the interpretation completion altogether. The existing bounded
`llama/parser.go:47` proposal API is a reusable boundary; extend its payload to
include structured temporal requirements and exclusions rather than relying on
the original description alone. Generate proposals after extraction/identity
resolution and only when retrieval needs them. Provider taxonomies and model
capabilities should be compiled in Go, not memorized into a longer system prompt.

## Validation and delivery plan

1. Freeze existing 12 cases plus the coordinator's ten realistic held-out-style
   cases; include the user's actual typo and favorite artists. Label expected
   atoms, polarity, strength, grouping, stage, count and original span, without
   making particular track outputs into parser goldens.
2. Add deterministic adversarial cases: genre/name collisions, compound genres,
   exclusion lists, typo versus a different real artist, unusual language,
   negation scope, optional genres, alternatives, repeated stage words, dates
   inside titles, and cancelled/replayed requests. Assert exact original spans
   and no mutation of unknown concepts. Keep confidence thresholds separate
   from interpretation correctness.
3. Run a controlled matrix: current prompt; shorter prompt only; extraction plus
   current prompt; extraction plus shorter prompt. Repeat at the desktop's real
   context limit and a larger context. Record first-pass success, repair count,
   truncation, transport errors, tokens and time. Do not tune and score on the
   same ten cases alone; keep further paraphrases private to validation.
4. At fixed candidate/evidence budget, compare candidate recall and final match
   tier, number returned, strict violations, per-facet relevance, journey
   progression and evidence coverage. Evaluate unknown evidence separately from
   measured mismatches. Test three input forms: raw, deterministically extracted,
   and curated correct intent; the last isolates downstream limitations.
5. Blindly compare shortlisted tracks for sound/mood similarity on the user's
   chosen categories. Report both fill-rate and relevance; ten results with more
   violations is not an improvement. Audit correlated CLAP/AB/DSP features so
   one sonic dimension cannot win by being counted several times.
6. Increment intent/parser/compiler identities; preserve stored history,
   cancellations, seed losslessness, fallback notices, provider budgets, native
   packaging and zero desktop Python prerequisites. Deliver through the same
   consolidated PR, with no merge or release implied by this research.

Executed read-only checks for this audit:

```text
go test ./internal/intent/rules ./internal/intent/schema -count=1
PASS: rules 0.071s; schema 0.116s
```

Those existing tests passing does not resolve the audit findings; they establish
the baseline only. Native model runs and ten-prompt output measurements are
coordinated separately to avoid competing local model-server processes.

## Controlled prompt ablation

The coordinator subsequently ran four arms in order: `original-1`, `compact-1`,
`compact-2`, `original-2`. All used the same four few-shot examples, grammar,
model and 4,096-token context; only the system instruction was replaced in the
compact arms. The same intermediary transport was used in every arm. Request
bodies, reports and per-arm tokenizer measurements are retained under
`bin/enhanced-translation-investigation/<arm>/`. This audit read all four
`report.json` files and manually compared each interpretation with the complete
user request, including optionality, polarity, era, duration, and both actual
artist endpoints for the confirmed Nine Inch Nails → Marilyn Manson journey.

| Arm | First-request input tokens | Existing narrow fixture passes | Fully faithful interpretations, manual audit |
| --- | ---: | ---: | ---: |
| original-1 | 2,071 | 3/10 | 0/10 |
| compact-1 | 1,448 | 1/10 | 0/10 |
| compact-2 | 1,448 | 1/10 | 0/10 |
| original-2 | 2,071 | 3/10 | 0/10 |

The compact prompt reduced measured first-request input tokens by 623 (30.1%).
The parsed intents and error arrays were identical between the two compact
repetitions. These are a small, controlled development comparison, not a
population error-rate estimate. The narrow fixture is insufficient: an accepted
JSON object with the right count and one desired category can still lose the
listener's important meaning.

| Case | Original arms | Compact arms | Full semantic verdict |
| --- | --- | --- | --- |
| 1: relaxing classical, mostly piano/strings, no singing | Parses, but piano and strings become essential textures instead of preferred instrumentation | Rejects an ungrounded defining criterion named `piano, strings` after correction | Neither preserves kind and optionality correctly; compact introduces a copy/grounding failure |
| 2: dramatic 20th-century classical, not film scores | Dramatic is a genre; era absent; film scores treated as an album exclusion | Dramatic becomes a mood and film scores a category exclusion, but era is still absent and dramatic remains essential | Some kinds improve; request still incorrect |
| 3: Aerosmith-like workout, don't include Aerosmith | Exclusion rejected as lacking a negative instruction | Negative entity reference rejected for lacking an exclusion | Neither returns a valid complete interpretation |
| 4: early Aerosmith, bluesy hard rock, raw rather than polished pop | Parses but drops `early` and the preference against polished pop; notes incorrectly mention excluding the artist | Rejects a wrongly typed exclusion of polished pop | Compact loses a parseable partial interpretation; neither is fully faithful |
| 5: typo in Christian Löffler, warm/gentle, nothing too clubby | Negative entity reference lacks an exclusion | Same final error | Neither returns a valid complete interpretation |
| 6: Löffler/Kiasmos, melancholic/comforting, mostly instrumental | Moods become genres; vocal preference missing | Moods become moods, but remain essential; mostly instrumental is essential instrumentation with no vocal preference | Better kinds do not fix strength/vocal routing |
| 7: weightlifting, heavy/aggressive/energetic, no slow ballads | Heavy/aggressive are genres; slow ballads is an excluded track | Heavy/aggressive become moods; steady beat becomes essential texture; slow ballads becomes an unsupported mood exclusion | Genre/entity mistakes improve, but descriptive qualities are still unnecessarily hardened |
| 8: 30-minute running rise/fall, no harsh vocals | 30 becomes a track count; harsh vocals is affirmative essential texture; a rise/fall trajectory is present | Still 30 tracks; positive vocal value `no harsh vocals`; trajectory absent; easy/up/down become global essential moods; invents two 1990–2023 periods | Compact introduces extra unrequested constraints and loses progression |
| 9: actual Nine Inch Nails first, Marilyn Manson last, 15 tracks | Rejects an ungrounded empty constraint | Rejects a constraint without kind/value | Neither preserves a usable journey; downstream actual-start support is also required |
| 10: dark industrial rock, less aggressive, no Manson/screaming | Dark remains fused into genre; less aggressive is essential style; screaming exclusion omitted | Less aggressive becomes essential mood; dark still fused; screaming still omitted | Neither preserves the comparative negative preference and vocal exclusion |

Case 8 exposes a separate validation defect: the compact output introduces both
composition and original-release requirements for 1990–2023 without any era in
the request, and they survive schema parsing. `validateOpenIntent` does not
ground temporal requirements; `normalizePeriods` returns when no recognized
period exists. The next contract must bind every temporal constraint to a
source occurrence or remove/reject it. The same completeness audit must verify
that a duration instruction is supported or explicitly reported unsupported,
rather than accepting the invented count. A negative vocal phrase needs explicit
negative semantics; changing only its field from texture to vocals is inadequate.

**Do not ship the shorter prompt as tested.** It saves context and improves
several type assignments, but yields no fully faithful request in this test,
regresses two parseable cases and invents new temporal restrictions. Prioritize
the extraction/locked-fact contract, typed duration, strength and endpoint roles,
and missing validation before another prompt-only rewrite. Keep the production
prompt as the control; retest the compact candidate after those changes, with
additional held-out paraphrases. This ablation establishes no transport fix,
no higher recommendation fill-rate, and no improvement in listening quality.
