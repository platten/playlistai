# Paipack recommendation implementation

Implemented on the existing `feat/artist-first-prompt-matching` worktree. Existing
indexer/runtime work is preserved. No commits, publication, model downloads or
changes to the source `testlibrary.paipack` are part of this work.

## Connected behavior

- Personal and enabled shared packs participate in request-pinned recognition:
  exact artists, artist-scoped tracks, explicitly named albums, and bounded
  learned genre vocabulary. Parse-cache identities include pack generations.
- Reviewed parser cases cover prominent piano, ordered Radiohead → Marilyn
  Manson, `chello` → cello, and lively dance with original-release 1990–1999.
  Lively is not inferred from loudness or tempo.
- Versioned typed postings split supported musical lists, recognize reviewed
  aliases/parents, and exclude administrative tags from musical search. Typed
  values retain zeros, signed descriptors, decimal BPM, key, original versus
  edition/composition dates, missingness and conflicts.
- Enhanced personal-library requests enable stored evidence. Compatible paired
  CLAP text queries join direct metadata, reference, MERT, CLAP, taste and recent
  continuation retrieval. Explicit starts, inferred anchors and waypoints retain
  their distinct roles. Repeated pack observations do not buy extra fusion votes.
- Bounded grounded seed search precedes model proposals. Artist representatives
  are selected within resolved identities. Seed fit, opening choice and output
  requirements remain separate; unknown fit is not relabeled as verified.
- Ranking uses compatible packed clause comparisons, typed soft metadata and
  the existing MERT/DSP channels. Local and outside candidates have no source
  bonus or fixed quota. Authoritative recording joins share available evidence.
- Candidate pools are bounded by relevance, not pack provenance. Stored evidence
  can still be used after preview budgets expire; explicit stop/cancellation and
  strict requirements remain enforced.
- Sequencing uses separate compatible Deej, MERT and CLAP comparisons, bounded
  outgoing-edge consideration for openings, and independent packed journey
  trajectories. It never compares vectors from incompatible spaces.
- Saved packed assessments retain query/model identity, uncertainty and observed
  coverage, including when a preview observation also exists. Their combined
  fingerprint participates in history identity. Recommendation/query policies
  are versioned; absent new history fields retain legacy defaults.

## Portable contract and compatibility

New writers produce paipack v7. Readers retain v5/v6 support. V7 carries the
explicit paired CLAP identity and optional bounded per-record excerpt evidence:
observed intervals, padding/input distinction, validity, partial reasons and
vectors. Merges preserve rich records without inventing missing legacy coverage.
Legacy v6 uses the indexer's paired fingerprint already recorded in GraphSHA256;
model-name/dimension agreement alone never permits text/audio comparisons.
Explicit runtime identities also separate audio-vector comparison spaces.
Unknown runtime vectors are compared only within their immutable pack. Mixed
v7 packs without one global CLAP identity use compatible per-record groups;
neighbor retrieval is unavailable above 50,000 tracks until identities have a
dedicated index. Metadata, MERT and per-record CLAP assessment remain available.

Positive rich comparisons use observed-duration weighting; negative comparisons
retain the strongest valid observed segment. Neither is a probability or proof
about unheard audio. Strict/no-vocal requirements are not certified by pooled
similarity. DSP statistics include usable measurements independently of MERT
job success. See [format details](paipack-format.md).

## Evaluation and limits

[Evaluation tooling](paipack-quality-evaluation.md) adds optional judged seed,
opening and neighborhood measures and source-specific judgment coverage. The
four-prompt fixture is a synthetic workflow test, not human musical judgments.

Weights, neighborhood heuristics and opening/transition terms are conservative
engineering defaults, not demonstrated optimum settings. Held-out blind listening
and tuning remain necessary before claiming musical-quality improvement.

Standalone composer/work entity resolution, complete classical movement ordering,
new explicit numeric BPM/key intent syntax, calibrated instrument prominence,
and per-window DSP trajectory tuning are not added. Their underlying metadata
is retained; unsupported requests must not be presented as verified. Automatic
thesaurus induction and parser/model replacement are intentionally not introduced.
No native Windows/macOS packaging or live-provider musical evaluation is claimed.

## Validation

The supplied v6 pack was imported into temporary test storage and checked through
production derivative construction, typed search and stored CLAP retrieval:
889 recordings, 888 MERT vectors, 889 CLAP vectors and 889 DSP records. This is a
structural compatibility check, not a musical listening result. The source pack
and active application library were not modified.

Reproduce the opt-in check without logging private track names:

```sh
PLAYLISTAI_TEST_PAIPACK=/absolute/path/testlibrary.paipack \
  go test ./internal/localcatalog -run '^TestUserPackReadOnlyRecommendationEvidence$' -count=1 -v
```

Normal regression tests remain offline and synthetic. The repository gate ran
binding generation, frontend typechecking, 233 frontend tests, production build,
Go vet, pure-Go compilation and the full race-enabled Go suite successfully.
Its last lint step found a spelling issue in a test variable; that identifier was
corrected and lint plus the affected package's race tests were rerun separately.
The build retains a non-failing frontend bundle-size warning. No 95% coverage
claim is made.
