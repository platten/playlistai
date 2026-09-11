# Single-genre playlist verification

When a request specifies one playlist-wide genre, every output track must have
affirmative evidence for that genre. This applies before ranking/selection and
again to the assembled playlist, including required tracks. Discovery, artist
diversity, personalization and best-available mode cannot bypass the check.

Evidence may come from reliable, provenanced feature metadata, resolved track
genre tags, a compatible grounded-description index with complete query coverage,
or a supported calibrated audio assessment. Catalog identity, artist/title words,
unresolved tags and uncalibrated preview similarities do not prove genre.
Existing directional alias/subgenre relationships remain valid: techno can match
electronic, but a generic electronic tag does not prove techno. Mere influence
or fusion is not treated as equivalence.

Unknown/mismatching tracks are removed. If too few survive, the app returns a
partial result with `single_genre_evidence_exhausted`, not unrelated filler.
Conflicting required tracks produce clarification. Deej-AI-only deliberately
does no musical verification, so single-genre requests return an unsupported
outcome advising an evidence-enabled mode. Multi-genre requests, category journeys
and soft mood/texture preferences retain their existing semantics.

Single positive genre preferences are preserved as essential playlist criteria
during normalization, including older saved requests when rebuilt. Existing
stored playlists are not rewritten or retroactively claimed to be checked.
Generation versions are `multichannel/v22` and `deejai/v5+engine-only/v2`; the
baseline walk remains unchanged for evaluation.

Regression fixtures cover both evidence priorities, best-available/verified-only,
aliases/subgenres, high discovery/diversity, conflicting taste, missing metadata,
required-track conflicts, provenance/identity and semantic query coverage.
The previous classical-artist/no-evidence and engine-only tests now explicitly
expect no unchecked genre playlist. Unknown-mood suggestion behavior remains
tested separately. These synthetic tests establish mechanics, not real musical
quality or catalog-wide evidence coverage.

The musiccheck interpretation fixtures now supply explicit synthetic genre
annotations from their expected-case definitions; their previous no-evidence
success expectation is no longer valid. Real musical-quality fixtures and data
are unchanged.

Validation: `scripts/test.sh` passed, including race-enabled Go tests, `go vet`,
pure-Go core compilation, lint (zero issues), generated bindings, frontend
typechecking, all 150 frontend tests and the production frontend build.
No live provider/model or held-out musical-quality evaluation was run for this
change; evidence coverage remains dependent on installed assets and metadata.
