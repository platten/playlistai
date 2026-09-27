# Recording evidence and playlist quality

Enhanced hybrid checks whether evidence applies to the requested recording and
musical characteristic before using it to claim fulfillment. A catalog ID,
similarity score, or ten returned tracks does not establish musical suitability.
The approved quality policy requires independent corroboration of imported genre
tags, omitting songs whose defining requested genre cannot be corroborated.
The original 40-prompt audit is tracked in
[the audit report](forty-prompt-quality-audit-2026-09-26.md).

## Acquisition and reconciliation

```mermaid
flowchart LR
  I[Resolved intent and seed] --> R[Existing retrieval channels]
  R --> P[Bounded candidate pool]
  P --> V[Targeted recording verification]
  V --> E[Cited claims and sampled audio observations]
  E --> C[Scope, identity, conflict and coverage checks]
  C --> S[Eligibility, fit ranking and performer diversity]
  S --> F[Frozen result, decisions and evidence]
```

`ports.RecordingVerifier` separates acquisition from repeated scoring. The
MusicBrainz client reuses its request budget, rate limiter and HTTP cache. The
orchestrator investigates at most 32 insufficiently supported candidates, with
a ten-second child deadline per candidate and slots reserved for later retrieval
channels. At most 16 optional attempts can first search for recording identity,
preserving opportunities for later candidates with established identities.
Required output tracks bypass that optional subquota, while retaining the total
32-attempt ceiling. An unresolved identity does not dispatch a recording verifier.
The provider additionally caps requests and linked-page reads. Provider
failure means missing evidence; completed claims survive optional subrequest
failure. Cancellation of the overall operation still propagates.

MusicBrainz recording IDs must match the requested recording, with compatible
artist identity and title. A different canonical title requires an additional
exact release-track-to-recording link; a similar work title is insufficient.
Invalid ISRC strings cannot corroborate identity. MusicBrainz's
[API](https://musicbrainz.org/doc/MusicBrainz_API) supports recording/release
lookups and requires at most one request per second and a meaningful user agent.

The verifier reads explicit recording instrument/vocal/performer relationships,
and a composer relationship only through an unambiguous work link. It preserves
source URL, entity and recording IDs, response revision, locator, method and
retrieval time. Referenced Wikidata statements require reciprocal recording/work
identifiers, supported ranks and unqualified claims. Artist-wide or album-wide
descriptions cannot become facts about every track.

An exact, active MusicBrainz recording link to an Apple Music song can supply a
publisher genre classification. The pilot accepts only canonical country/song-ID
URLs and one billed performer, with compatible title, version and duration.
Remaster suffixes may identify the same recording; live, remix and edit versions
remain distinct. This does not verify an exact release master. The claim retains
both the MusicBrainz relationship response and the Apple field response, with
URLs, hashes and retrieval timestamps; missing link evidence leaves it unknown.
Only genre/style assessment can use this publisher field.
The exact Apple category `Hip-Hop/Rap` can support the existing hip-hop concept
after those same attribution and recording-link checks. The original label stays
in the cited claim. This does not split arbitrary slash labels, add a global
genre alias, or establish neo-soul, jazz or a rap subgenre.

The lookup makes at most one dispatch per candidate within the existing shared
budget. It uses application-wide three-second spacing, rejects redirects, and
refreshes cached responses after 24 hours. As with existing metadata providers,
offline or exhausted-budget use can retain historical cached evidence with its
original timestamp; it is not presented as newly fetched. No artwork or previews
are requested. Apple's archived documentation describes
[ID lookups](https://developer.apple.com/library/archive/documentation/AudioVideo/Conceptual/iTuneSearchAPI/LookupExamples.html),
[rate and cache guidance](https://developer.apple.com/library/archive/documentation/AudioVideo/Conceptual/iTuneSearchAPI/Searching.html),
and [content conditions](https://developer.apple.com/library/archive/documentation/AudioVideo/Conceptual/iTuneSearchAPI/index.html).
The observed recording example establishes a working source path, not broad
genre coverage. Album, artist and composition classifications remain out of scope.

Linked official recording pages are bounded in size/count and validated against
the recording or credited artist's official site. The local language model has
no tools and receives source text as untrusted data. Extracted values and quotes
must occur in that text and name the recording. These checks cannot establish
full natural-language entailment, so quoted text remains a visible, non-decisive
hint. A model assertion never becomes an independent source.

| Evidence | Permitted use | Limitation |
| --- | --- | --- |
| Exact recording credit | Applicable instrument, vocal or performer fact | A credit does not establish prominence or absence of other instruments |
| Referenced recording statement | Its specifically attributed musical fact | Conflicts remain explicit and unverified |
| Exact linked publisher song genre | Recording genre/style classification | Needs preserved identity link; cannot establish vocal absence or exact master equivalence |
| Imported recording genre/style tag | Retrieval and soft preference support | A strong match needs independent corroboration; duplicating tags does not provide it |
| Aggregate catalog genre/style result | Visible discovery hint | The result carries no independent genre provenance; neither a match nor a mismatch can establish a requirement or exclusion |
| Flattened `AB:GENRE` / `AB:MOOD` | Retrieval and soft preference hints | Missing model confidence and coverage prevent requirement verification |
| Native instrumental label / voice classifier | Retrieval and visible vocal hint | Even high confidence cannot prove whole-recording vocal absence |
| Community tag | Supporting hint | One vote, or many copied tags, is not proof |
| Model-extracted quotation | Source inspection and supporting hint | Wording, subject and negation may remain ambiguous |
| Preview audio | Observed portion and ranking | Vocal detection can refute no-vocals; absence in a sample cannot prove whole-recording absence |
| Artist/release description | Context and discovery | Does not transfer to the recording |

Packed CLAP cosine scores remain uncalibrated observations; combining one with
a genre tag cannot manufacture verification. Existing accepted preview evidence
or a properly attributed recording statement can corroborate a musical facet.

The policy preserves independent evidence and disagreement instead of choosing
the most favorable source. Each candidate stores criterion assessments and its
selection/rejection reason. Track details show supported, contradicted,
conflicting or unverified criteria, applicable source links, and audio coverage.
Raw preview audio is not retained.

Preview identity is checked against the request's pinned catalog as well as
cached recording metadata. A known full-recording duration can reject a
different version even when its title and artist match; the comparison allows
small provider rounding differences. An unresolved identity stays unresolved.
Missing duration is unknown, and matching duration alone does not authenticate
a recording. Preview coverage remains separate from full-recording duration.

New analyses carry `preview-recording-identity/v2`. Fresh CLAP, DSP and MERT
lookups skip older identity-policy rows; the disposable MERT search projection
rebuilds under the new policy. Original cached records and frozen history remain
intact. A new generation may need to reacquire analysis, with unmeasured cost.
Refreshing an old snapshot also omits its old-policy search evidence and
centroids, without changing the source snapshot or rereading taste feedback.

For Enhanced generation, a defining positive genre/style is also an admission
requirement. Unknown candidates remain available for bounded evidence acquisition,
but cannot fill output slots until the requested genre is corroborated. A shorter
or empty result is preferable to an uncorroborated genre suggestion. Explicitly
soft genre hints remain preferences; reference-only and texture-only requests do
not acquire mandatory genres from retrieval profiles. Other uncertain musical
facets remain visible. Required tracks and journey waypoints cannot bypass this
check; an unsatisfied requirement remains in the result explanation.
Strict musical requirements are checked again after evidence acquisition and at
final assembly. A vocal-free preview or an instrumental classifier prediction
cannot satisfy a whole-recording no-vocals requirement. A cited applicable
recording statement can support absence; observed or credited vocals contradict
it. Grouped alternatives remain alternatives at every admission boundary.

An initial static-catalog capability warning does not override later decisive
recording evidence. Only an exact registry-generated warning for the same
constraint and source span can be superseded during per-track assessment.
Unrelated or model-reported unsupported requests remain unresolved. The original
intent and its warnings stay serialized; recording assessments and the final
outcome report what was actually proved. An enforcement flag alone is insufficient.

## Search and completion

After a candidate's verification opportunity, Enhanced can skip its preview
when an ordinary required genre remains unconfirmed and the active audio session
cannot produce calibrated genre evidence. Other candidates and retrieval sources
continue. Existing decisive evidence is checked first. Journeys, mixed musical
alternatives, instrumental screening, calibrated sessions and required/reference
analysis retain their existing paths; this is not a global stop at the verifier
quota. The optimization changes acquisition, not the genre admission standard.

The ten-minute clock starts at submission and includes parsing, resolution,
provider work and ranking. A nested work context cannot reset the deadline or
consume the final assembly reserve twice. Search normally reserves 15 seconds;
short diagnostic budgets scale the reserve down.

Reference resolution passes the operation context through catalog SQL, dynamic
catalog lookups and provider recovery. Cancellation while waiting for an enhanced
operation lock or prefetch shutdown does not wait for the current lock holder.
Catalog metadata has an optional contextual API, preserving the legacy interface.
Database-backed, dynamic and composite catalogs forward that context through
metadata and annotation reads. A canceled lookup cannot publish partially merged
metadata. Request-local recording identity binding builds a temporary map and
publishes it only while the context remains active, retaining the previous map
on cancellation. Generation passes this context through reference preparation,
candidate metadata conversion, reference ranking and assembly cache inputs.
Selection, sequencing, performer diversity, duration, seed admission and
recording-evidence reads use the same contextual boundary. Cached audio scans,
analysis and feedback preparation, and MusicBrainz identity, seed and knowledge
lookups also discard interrupted metadata reads. The ordinary search
deadline retains completed eligible tracks and uses the still-active assembly
reserve; cancellation of the parent operation remains cancellation.
Cancellation remains an error rather than an apparently empty reference set;
completed earlier retrieval rows remain available to the work-deadline handling.
These corrections are later than the frozen v3 audit build;
its native results do not measure them.
An expired work budget preserves completed results and parsed intent; a user
cancellation remains a cancellation. Legacy context-free catalog APIs still
exist for other callers, so the process supervisor remains a separate evaluation
guard rather than evidence of universal hard preemption.

Finding enough tracks alone does not stop the search. The quality target requires
the requested count, applicable journey stages and strong evidence for each
selected track, after a minimum comparison pool and retrieval-source opportunity.
Explicit genre lists introduced by “mixing,” “combining,” or “spanning” use
playlist-wide coverage: a strong track match needs support for at least one
member, and full fulfillment requires the playlist to cover every requested member. Selection
reserves supported examples from the eligible pool; a real multigenre recording
can cover several members. Ordinary “or” alternatives remain one coverage
obligation. Separate coverage sets remain independent requirements. Missing
genres produce named partial-result reasons and prevent the quality target from
stopping search. Explicit “every track blends” instructions retain per-track
requirements, as do unmarked legacy intents. This interpretation is applied only
to positive playlist genre/style source atoms; exclusions, vocals and journey
order retain their existing meaning.
The existing bounded pool/source-exhaustion limits remain. A request that cannot
be supported returns an honest partial or unsupported outcome. Pure reference
similarity and subjective texture/mood can remain uncertain even with ten tracks.

`Stop and keep checked tracks` preserves completed eligible results. `Cancel`
discards the operation. The Deej-AI-only, AcousticBrainz-first and CLAP-first
policies retain their source order; incompatible embedding spaces are never mixed.

## Credits, dates and identity

Identity recall and prompt presentation have separate bounds. Up to 64 identities
from the local MusicBrainz index remain available for clarification, while model
hints stay short. A presentation cutoff must not remove the correct namesake or
turn genuine ambiguity into an apparently unique match.

Typed genre journey spans take precedence over namesake artists unless the user
explicitly supplies artist wording or quotation. Recognition tries complete
artist spellings alongside sentence punctuation and possessive alternatives, so
“Brian Eno’s” cannot silently become the artist Brian. Quoted names preserve
their boundaries; connectors between them are not treated as quoted names.
Known negative musical descriptions also retain their type: “not sleepy” cannot
become an exclusion of artists named Sleepy. Explicit artist wording and quoted
names remain available when that artist is intended.

Known compound genres retain their complete source phrase: “indie-pop” and
“indie pop” become one criterion, while “indie and pop” remain separate.
Explicitly introduced quoted genre lists preserve their conjunctions; quoted
song and artist references remain protected. Longer literal instrumentation and
textures, such as “soft piano,” “pounding drums” and “melodic bass lines,” can
survive contained generic terms when their exact source occurrence supports a
positive, soft preference. This exception cannot override a negative clause,
strict requirement or reference identity; it does not invent model-omitted
details. “Dance groove” remains a literal texture; explicit “dance music” and
“dance tracks” retain their genre meaning, including when both uses occur in
one request. Fresh parsing uses `artist-first/v11` and `source-atoms/v16`; these later corrections
are absent from the frozen v3 audit. Provider vocabulary does not override an
independently recognized non-genre atom or become musical evidence about a song.

For bounded negative or reduced phrases around a recognized plain instrument or
texture, an exact complete model-supplied phrase keeps its source role. “No
pounding drums” cannot become a positive request for drums, and “no ringing
guitar samples” cannot become an exclusion of every ringing guitar sound. If the
full phrase is omitted or only partially supplied, the parser retains the exact
clause as unresolved and suppresses the unsafe generic fallback. Strict journey
restrictions keep their stage scope. This guard does not reinterpret quoted or
grounded identities, harden ordinary instrument preferences, or provide general
coverage for unknown wording. Saved intent snapshots are not reparsed.

Enhanced discovery keeps reference and relevance priority, then breaks equal
profile/seed scores using the canonical request seed and stable identities before
bounded truncation. Alphabetical position does not determine which equally ranked
artists get a retrieval opportunity. The same request and seed remain repeatable.

After an Enhanced request is submitted, an ambiguous explicit artist can be
corroborated by recording credits connecting it with another independently
identified artist in the same playlist request. The bounded check examines all
eligible anchors (at most two), permits two HTTP dispatches including retries,
and has an eight-second child deadline. At least two distinct recording links
must support one candidate; any positive link to a rival preserves ambiguity.
Incomplete candidate sets, unavailable responses and unrelated production credits
cannot select an identity. Journey endpoints, required artists and inferred
anchors do not use this contextual shortcut.

This is a contextual identity inference, not proof of musical similarity or of
which artist the user intended. The complete candidate set and public source
response hashes remain in an optional corroboration record; it never sets the
user-confirmed flag. A compatible catalog seed is still required. Submitted
preview and generation reuse the same immutable proof, while parsing without
submission performs no new online lookup. An explicit user identity selection
clears the earlier automatic decision. Saved results retain their evidence
without refetching changing provider data.

Performer overlap is based on structured credits or explicitly corroborated
artist-ID lists. Punctuation alone does not split a band or person into artists.
An engineer-only credit is excluded from performer diversity. Role annotations
can remove an engineer from a flattened display only when every component is
explicitly accounted for; raw tags and recording IDs remain unchanged.
MusicBrainz's [classical recording-artist guidance](https://musicbrainz.org/doc/Style/Classical/Recording_Artist)
also distinguishes the summary artist field from specific performer relationships.

Original recording/release dates remain separate from a selected edition or
release group's date. A later release lookup cannot overwrite known original
date evidence merely because its release happened to be returned first.

## Compatibility and privacy

New claim, release-track and decision fields are optional. Existing saved data
loads without manufacturing evidence. `enhanced-search/v2` snapshots include the
evidence policy and actual operation limit; validated v1 snapshots remain
readable. Exact historical delivery uses the immutable saved result even after
an engine update and validates the generation identity against its frozen
evidence. Explicit regeneration uses current providers and policies. Legacy
history without a valid frozen result retains its version checks.

The optional `coverageGroup` field is carried from source atoms through criteria,
preferences and audio clauses. It is never inferred again during history loading.
Enhanced generation uses `multichannel/v58` and `recording-evidence/v5`; marked audio requests also include
`playlist-genre-coverage/v1` in their policy identity. Omitted fields preserve
legacy serialized hashes. Other recommendation modes retain their prior genre
policy.

The v58 acquisition correction skips the narrowly identified unnecessary previews;
evidence v5 adds the source-specific Apple category interpretation. Neither
rewrites cached source claims or historical results. The v57 correction, retained
in the current policy, treats aggregate catalog genre/style results as
`catalog_genre_hint`, regardless of tag spelling or whether a separate annotation
is present. Only positive, non-strict discovery can use this weak support.
Properly attributed recording claims and applicable audio evidence retain their
authority, as do native composer and instrument credits. Fresh generations use
the new evidence policy; exact historical replay remains unchanged.
The source details label these hints “library genre label; needs corroboration”
and retain the aggregate criterion's unverified state.

The v56 ranking correction makes compatible, validated frozen MERT search-hit
representations available to the normal request-fit assessment. A raw retrieval
cosine cannot substitute for a missing weighted assessment. Ranking and
sequencing use the same validated observation's vector, coverage and fingerprint
when choosing between preview and packed evidence. Saved search hits can also
supply a transition comparison without being copied into a second snapshot map.
The frozen v3 audit
uses v55 and does not measure these corrections or the later preview-identity,
parser and cancellation changes.

Only public entity identifiers and ordinary provider search terms leave the
application through the documented online metadata path. Full prompts, listening
history and taste profiles remain local. Source text goes to the installed local
model. No paid API or hosted ChatGPT dependency was introduced. Discogs can be
used for manual audit citations; it is not an automated runtime provider.

## Validation boundary

Offline regressions cover identity mismatch, untrusted/ambiguous quotations,
weak classifier/tag evidence, conflicts, preview-only vocal absence, provider
failure/cancellation, budget preservation, performer overlap and frozen replay.
Rendered browser checks exercise source disclosure and links in both themes at
small and desktop widths. These checks do not substitute for exact-recording web
review or blind listening. The separate audit retains all 400 requested slots,
unknowns, missing outputs and failures; AI web judgments are never populated as
human listening grades.
