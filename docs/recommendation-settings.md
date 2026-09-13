# Recommendation settings

Open **Settings → Recommendations** to choose how new playlists are built.
The choice is saved locally and takes effect without restarting. Use **Regenerate**
on a playlist to apply the current setting to a fresh generation.

| Mode | Ranking behavior | When to use it |
| --- | --- | --- |
| Enhanced Hybrid (default) | Combines the existing evidence pipeline with available local DSP preferences and optional MERT similarity. | Richer matching with supported audio evidence; MERT installation is optional. |
| AcousticBrainz first | Decisive archived classifier predictions lead overlapping characteristics; CLAP fills gaps. | Prefer available recording-level archived analysis. |
| CLAP first | Scored preview comparisons lead overlapping characteristics; AcousticBrainz fills gaps. | Prefer matching previews against your description. |
| Deej-AI only | The original audio/co-occurrence embedding walk, without provider enrichment, semantic retrieval, personalization, CLAP, AcousticBrainz or MMR selection. | Fast catalog-reference exploration without musical-fit analysis. |

## What priority means

Priority controls ranking, **not permission to ignore constraints**. Both sources
can still be checked in analysis-enabled modes. Opposing essential evidence,
strict no-vocals checks and required-track conflicts remain eligibility gates.
Source disagreement remains visible rather than being concealed by the preferred
source. Unknown evidence is not proof of musical fit.

AcousticBrainz-first uses archive weight 0.55 and the configured semantic weight
(default 0.35). Decisive archive margins suppress the corresponding preview
ranking contribution. CLAP-first uses archive weight 0.15 and retains preview
scores, suppressing the corresponding archive contribution when a preview score
exists. Missing clauses retain fixed denominators; raw evidence is never edited.
These are interpretable policy settings, not calibrated probabilities or a
listening-benchmark claim that either source is always better.

## Recommendation shortlist and analysis

For recommendation-based generation, a request for **10 tracks starts with a
20-candidate shortlist** (or fewer if retrieval cannot supply enough candidates).
The engine chooses and orders that shortlist using recommendation relevance and
the existing diversity selector before checking musical fit. Request-local
channel budgets expand as needed, within the configured candidate cap. Preparation
tops up partially excluded or duplicate-heavy pages before analysis, subject to
the request's cancellation/time budget and a bounded number of retrieval calls.
Required tracks count once toward both the shortlist and final playlist: two
required tracks leave 18 optional candidates and eight output slots.

Available AcousticBrainz evidence screens conflicting characteristics; CLAP
checks previews against the description when analysis is enabled and needed.
The selected source-priority policy still governs final ranking. Checks stop
when enough eligible tracks can satisfy selection, diversity and journey order,
not merely after counting passing previews. Provisional ordering cannot discard
tracks at the final relevance floor before their preview scores are available;
final selection still enforces that floor. If necessary, the engine consumes
the remaining shortlist and refills with untried recommendations, keeping the
original references and incorporating accepted tracks as continuation context.

Missing evidence, hard exclusions and recording deduplication are never bypassed
to fill the count. Existing analysis/time limits can produce a smaller playlist
with a structured explanation. Pure artist/reference requests without descriptive
clauses skip preview analysis. Deej-AI-only remains analysis-free. Direct metadata
genre discovery also stops when selection and sequencing can fill the requested
count; it no longer analyzes surplus tracks just to improve ranking. Preparing
a larger recommendation shortlist does not require analyzing every entry.
Soft preferences for more distinct artists do not prolong checking once a valid
full-length sequence exists; hard artist-spacing and journey rules still apply.

## Understanding playlist messages

Track count and musical fulfillment are separate. A ten-track result for a
ten-track request says **“Playlist created with 10 tracks”** if some request
details remain uncertain; the reasons stay visible below that heading. A genuine
shortfall says **“Created 6 of 10 requested tracks.”** Neither message claims
that unverified tracks are verified. Fully fulfilled results have no partial
warning. Clarification and unsupported-request messages remain distinct.

Older saved results use the same presentation: generic notices that incorrectly
claimed a track shortfall are corrected when displayed. Saved fulfillment status,
musical-fit evidence and history data are not rewritten.

## Engine-only limitations

Descriptions are still interpreted by the selected local parser. The walk needs
a resolved catalog artist/track or required track; it does not fetch a genre's
artists from metadata services. Missing references produce a refinement request.

Artist exclusions, reference-artist exclusions, explicit artist spacing and
recording deduplication still apply. Unsupported strict constraints (including
no vocals, artist-only/album requirements and date limits) return an explanation.
The newer resolved-destination contract also requires an analysis-enabled mode;
the original waypoint journey remains available. Descriptive requests with
best-available verification can return suggestions marked **partial/unverified**,
never a claim that their genre or mood was checked. Verified-only essential
requests cannot be fulfilled by the walk alone.

The artist-diversity slider is disabled for engine-only results. Discovery and
transition controls use the original walk's seeded noise and recent-track
lookback, not the enhanced selector's exploration/MMR rules. Existing model
files and derived features are not deleted by switching modes.

## Persistence and reproducibility

`prefs.json` stores `recommendationMode`; each new desktop request pins it in
`intent.controls.recommendationMode`. The field is optional for compatibility:
old requests without it use the current desktop default when rebuilt. Existing
saved playlist results are not rewritten. Settings changes do not replace the
mode pinned in an existing request or interrupt a running generation.

The LLM does not choose this setting. The desktop applies it after parsing, and
generation fingerprints include it. Engine-only parsing has a separate cache
key and skips even provider genre-name confirmation. New requests use
`multichannel/v20` or `deejai/v4+engine-only/v1`. The retained `deejai/v4`
evaluation baseline remains unchanged. Engine-only generation has no profile
snapshot; exposure logging remains separate from ranking and positive feedback.

The legacy TOML `recommendation.strategy = "deejai"` supplies the engine-only
default when there is no valid saved setting. Both engines are wired once so
changing Settings does not swap mutable services during generation.

## Validation

Deterministic tests cover source-priority reversal, missing-source fallback,
preserved evidence, strict checks, unchanged baseline tracks, missing seeds,
settings persistence/errors, concurrent preference updates, disabled provider
calls, history replay and generation/cache invalidation. These tests establish
control-flow correctness, not comparative musical quality. No new model download
or held-out listening benchmark is required or claimed for this setting.

Executed validation: focused Go packages passed, followed by the complete
`scripts/test.sh` gate (Wails binding generation, frontend typecheck/build, vet,
pure-Go core checks, race-enabled Go tests and lint with zero issues).
`git diff --check` also passed. No rendered-browser smoke test was performed.

The separate [forty-prompt, three-mode live regression](three-mode-regression.md)
records minimum-count acceptance, interpretation failures, fulfillment outcomes,
latency and actual evidence coverage. Its failures remain visible; enabling a
source-priority setting is not a guarantee that every prompt can be fulfilled.
# Enhanced hybrid

Enhanced Hybrid is the default for new installations and unset preferences. Saved
mode choices and an explicit Deej-AI configuration remain respected. It adds local DSP preferences and MERT audio similarity to
the existing evidence pipeline. It does not require MERT installation. See
[enhanced settings, limits, replay and evidence policy](enhanced-audio.md).
