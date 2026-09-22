# Combining portable library packs

`paipack-combine` creates one indexed format-v8 `.paipack` from two or more existing
format-v5 through v8 packs. The executable is pure Go and works offline. It does not load
audio, codecs, native inference runtimes, model files, or network services.

```sh
paipack-combine --out merged.paipack [options] first.paipack second.paipack
```

The output must not resolve to an input path. Every input is staged through the
normal pack reader, including archive limits, checksums, SQLite structure,
resource shapes, capabilities, paths, fingerprints, and vectors. Version 4 and
older inputs are rejected with a request to rebuild them using the current
`playlist-indexer`. Repeating the same semantic pack ID is safe: later copies
are validated and reported as skipped, but are not ingested twice.

## Recording identity and precedence

Recordings are grouped transitively using only these signals:

- canonical valid ISRC equality;
- canonical MusicBrainz recording UUID equality;
- canonical valid AcoustID UUID equality; or
- an exact Chromaprint with the same compatibility contract, corroborated by
  matching or very similar artist/title metadata and, when both durations are
  reliable, the existing duration tolerance.

Both `embedded_tag` and `full_selected_stream` fingerprint scopes are retained.
Their contracts remain distinct. Paths, track IDs, source identities, and
stored recording-identity labels never independently collapse tracks.

The first row by command-line input order and then source track-ID order is the
primary row. Missing album, album artist, identifiers, fingerprint, duration,
portable path, DSP, MERT, and raw-tag keys are filled from later duplicates.
The earliest non-empty value wins a conflict, and conflicts appear in the
report. The final recording identity is recomputed in MBID, ISRC, AcoustID ID,
then corroborated-fingerprint precedence. Capabilities are recomputed from the
result.

Unique track IDs stay unchanged. If distinct recording groups share an ID,
every colliding ID receives a deterministic, length-safe suffix derived from
its source pack ID. A root alias used by more than one distinct pack is likewise
rewritten for every owner, beginning with 12 pack-ID characters and extending
only if needed for uniqueness. Noncolliding aliases stay unchanged.

## Rebuilt resources

Source cluster assignments and corpus-relative models are discarded. The
combiner rebuilds metadata TF-IDF/SVD from genre tags only, retains mood and
style tags as raw metadata, and rebuilds DSP distributions. When at least four
compatible MERT vectors exist, it chooses a deterministic diverse,
RAM-capped training sample, fits spherical clusters, and streams new assignments
over the complete vector corpus. All vector-bearing inputs must have exactly
the same MERT representation contract. Metadata-only packs may join that space;
two incompatible vector spaces fail before the destination is replaced.

Options are:

- `--work-dir DIR`: parent for temporary SQLite and staged generations;
- `--max-ram SIZE`: fitting budget, default `2GiB`;
- `--workers auto|N`: fitting worker count, default `auto`;
- `--seed N`: deterministic learning seed, default `42`;
- `--training-sample N`: requested vector sample before the RAM cap, default
  `50000`;
- `--clusters N`: cluster count, or `0` for the recorded heuristic; and
- `--json`: emit the complete machine-readable report.

Temporary state is removed on success, error, or handled interruption. Pack
publication uses the normal atomic writer, so cancellation or failure leaves an
existing destination unchanged. The merged output remains subject to the
five-million-track consumer limit.

## Static builds

No new dependencies are required beyond the repository modules. Build a
standalone Linux executable with:

```sh
CGO_ENABLED=0 go build -trimpath -o paipack-combine ./cmd/paipack-combine
```

The same command can be cross-compiled with the appropriate `GOOS` and `GOARCH`.
Cross-compilation checks build compatibility; only a native run validates host
execution and filesystem behavior.
