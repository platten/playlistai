# Bounded local-library exercise, 27 September 2026

The standalone `playlist-indexer` successfully copied and processed 16 explicitly
selected local recordings, then acquired identity-linked web-source evidence.
The aggregate [receipt](data/reliable-subset-receipt-2026-09-27.json) records artifact
hashes, model identity, coverage, and source results. Audio, source paths, titles,
the selection manifest, and per-recording evidence remain outside the repository.
Follow [the standalone workflow](library-annotations.md) to repeat the commands.

## Audio preparation

The deterministic selection contained 901,221,473 bytes within a 1 GiB bound.
It deliberately exercised compound performer credits and varied musical material;
it is not a random or held-out evaluation sample. All 16 files had embedded
recording MBIDs and 15 had ISRCs. Those tags are identity inputs, not independent
verification. Source audio was copied without alteration and was not uploaded.

Metadata preparation and original LAION CPU encoding both completed with 16
successful tracks and zero failed tracks. An initially available older codec
bundle was rejected by the existing integrity checks; an already available,
hash-verified schema-v2 codec bundle supplied the required Chromaprint support.
No incompatible model or codec was relabelled.

The separately exported version-8 pack contains 512-dimensional original LAION
HTSAT-base music vectors using ONNX Runtime 1.26.0 CPU. Its weight digest begins
`f208f3bff6cfd4de`; the receipt contains the complete identity and preprocessing
version. A read-only Go diagnostic passed the actual exported manifest through
`Manifest.CLAPCompatible(audio.RecommendedBundle().Model)`: exact pairing passed,
while substituted paired weights and a changed runtime were rejected. The model
fingerprint is `2f89df6b0880eca66db79401263859c9587262a6950add78e66eb8c6cf302247`.
This checks the pinned audio/text/tokenizer identity and runtime; it does not infer
compatibility from dimension or model name. Native text-query inference was not
exercised against this subset.

Processing used serial concurrency, one inference session and thread,
balanced profile, deferred integrity, a 4 GiB RAM bound, and offline asset mode.
There are no MERT or DSP results in this pack. The evidence records two distributed
10-second excerpts per recording: 32 segments and 320 sampled seconds in total.
This coverage does not establish properties of unsampled portions. Wall-clock
processing duration was not measured, so this exercise supplies no latency claim.

## Web-source evidence

The real standalone `annotate` command completed all 16 rows within its five-minute
request deadline, using existing bounded providers and caches without an LLM.
Thirteen recording identities resolved and matched; three remained unresolved
and received unknown musical assessments. The sidecar retained 321 claims:
317 from MusicBrainz and four from recording-linked Apple metadata. They include
150 community-tag claims, 150 recording-credit claims, 13 recording-metadata
claims, four publisher song links, and four publisher fields. These counts include
different facts and overlapping representations; they are not 321 independent
musical annotations.

The nine requested criteria produced these source assessments:

| Criterion | Explicit source matches | Unknown |
| --- | ---: | ---: |
| Piano | 3 | 13 |
| Guitar | 3 | 13 |
| Drums | 0 | 16 |
| Afrobeat | 2 | 14 |
| Soft piano | 0 | 16 |
| Spacious reverberation | 0 | 16 |
| Distortion | 0 | 16 |
| Tense mood | 0 | 16 |
| Instrumental vocals policy | 0 | 16 |

Unknown does not mean absent. Generic instrument credits did not become qualified
descriptions. Album descriptions, artist biographies, and unverified publisher
recording versions did not establish track-level descriptive evidence. No negative
listening labels were inferred from missing text.

The private JSONL retains recording identity, source URL, revision, locator,
retrieval time, and method. It is marked `web_source`, with zero independent human
listening labels. It remains an audit sidecar: it has not been imported into the
desktop catalog, and it cannot calibrate audio thresholds or establish strong-match
precision. The optional local-model quotation path was not exercised in this run.

## Acceptance boundary

This verifies the native Linux binary, bounded subset preparation, compatible
audio export, and real metadata acquisition. It does not measure the requested
90% strong-match precision, 80% slot fulfillment, discovery recall, vocal leakage,
or the two-minute generation lifecycle. Those require their own fixed-prompt,
independently assessed runs. Automatic remains staged; successful processing and
web facts do not satisfy the musical-quality promotion gates.
