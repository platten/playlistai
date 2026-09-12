# Preparing the intent dictionary

The enhanced intent vocabulary is a small, reviewed, project-authored dataset in
`internal/musicconcepts/concepts.json`. Go embeds it in every release binary on
Windows, Linux and macOS. It contains descriptions and label identifiers, not
model weights, audio, learned embeddings, or a provider database. Desktop users
need neither Python nor a separately installed dictionary. No new external
dataset is necessary to use this registry.

The Python script is optional contributor tooling for validation, reproducible
compression, and retaining source evidence for future changes. It uses Python
3.10 or later and only the standard library. Existing model, MusicBrainz,
AcousticBrainz and local-catalog preparation remains in the documented dataset
tools; their licenses and model compatibility requirements still apply.

## Validate and retain the data

From the repository root in PowerShell:

```powershell
python python/prepare_intent_dictionary.py --check
go test ./internal/musicconcepts ./internal/audio ./internal/enrich/musicbrainz
python python/prepare_intent_dictionary.py --output "C:\Users\pawel\Downloads\playlistai-intent-dictionary-v1"
```

On Linux/macOS the same script works with `python3`; the default output is
`~/Downloads/playlistai-intent-dictionary-v1`. `--check` performs no writes or
provider requests. The ordinary command retains:

- `concepts.source.json`: the exact original input for future review.
- `concepts.json`: deterministic JSON with sorted object keys.
- `concepts.json.gz`: reproducible level-9 gzip, without a filename or timestamp.
- `manifest.json`: registry version, concept count, byte sizes, licenses and SHA-256 hashes of all three forms.

The native binary embeds the checked-in JSON; the gzip archive is for retention
or redistribution. It is not an additional desktop prerequisite. Keep generated
archives, downloaded sources, model weights and private data outside Git.

## Extend the reviewed vocabulary

1. Make a working copy of `concepts.json` and review the source phrase in context.
   Assign a facet: genre, mood, instrumentation, texture, vocal, or activity.
   Artist names belong in reference resolution, not in this dictionary.
2. Add a stable `kind.value-with-hyphens` ID, display value, source and license.
   Add only exact orthographic/lexical aliases. For example, `hip-hop` and
   `hip hop` share an identity. `electronica` retains its own identity and a
   directional `parents: ["genre.electronic"]` relation. A parent or related
   concept never becomes an exact provider query or verified recording label.
3. Preserve compositional language outside aliases. `mostly instrumental` is
   instrumental with degree `mostly` and preferred strength. `less aggressive`
   is a reduced negative preference. `no screaming` is a required exclusion of
   screaming; it is not an instruction to exclude all singing. The lexer owns
   polarity, scope, strength and alternatives before the language-model step.
4. Add provider mappings only with direct evidence. MusicBrainz entries are
   approved search spellings, never proof of track suitability. AcousticBrainz
   entries name an exact classifier and output class. The electronic-subgenre
   classifier is deliberately absent because it needs an independent electronic
   applicability gate. Mood darkness and spectral darkness are distinct senses.
   MERT has no text-query or genre-class interface; do not add fabricated labels.
5. A CLAP caption describes the positive audible trait. Negation stays in the
   typed clause and scoring direction. Captions and DSP proxies require review;
   an uncalibrated cosine is not a match probability or proof of absence.
6. Validate the working copy with `--input`, add alias/meaning/provider regression
   cases, and inspect the diff. Replace the embedded input only after review.
   Bump the registry version in JSON and Go together when behavior changes;
   update the preparation script's supported version and record compatibility.

Example with an existing, downloaded, public AcousticBrainz metadata sample:

```powershell
python python/prepare_intent_dictionary.py `
  --input "internal/musicconcepts/concepts.json" `
  --output "C:\Users\pawel\Downloads\playlistai-intent-dictionary-v1" `
  --evidence "C:\Users\pawel\Downloads\acousticbrainz-reviewed-sample.json" `
  --evidence-license "CC0-1.0"
```

Use an actual retained file path; the example sample is not supplied by this
change. The script preserves its bytes under `sources/` and records its hash and
declared license. It does not download anything, infer a license, or accept audio
for training. Source samples are bounded to 64 MiB; retain full provider archives
in Downloads using their existing preparation tools. Never assign CC0 to
MusicBrainz supplementary data or model weights merely because an AcousticBrainz
sample uses it. The project's authored registry uses GPL-3.0-only; linked data
and models retain their own license obligations.

The registry detects words and selects supported queries. It does not train a
model, establish the fit of a recording, resolve ambiguous user meaning, or
guarantee additional recommendations. The ten-prompt evaluation and provider
evidence checks remain necessary before claiming a musical-quality gain.
