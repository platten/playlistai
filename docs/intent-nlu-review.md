# Review the English intent pilot

These **40 prompt interpretations are proposals, not approved training labels**.
No task-adapted intent model has been trained from this batch. Your review
establishes what the app should understand; it does not label how recordings
sound. Music/audio learning remains unsupervised.

Review one batch at a time. For each numbered prompt, mark **correct** or give a
short correction to the interpretation. “Unclear” is a useful answer. Pay
particular attention to whether an artist must appear, only guides the sound,
or is excluded; whether a trait applies everywhere or just to a section; and
whether a preference is being turned into a strict requirement.

The [annotation JSON](../internal/evaluation/testdata/intent-nlu-review-v1.json)
retains exact original UTF-8 byte spans, separate operators and their proposed
links. Every record has `review.status: unreviewed`. A maintainer must apply
actual review responses, preserve the original proposal and record reviewer and
timestamp; no script auto-approves it. The typo “christrian loeffler” stays in the
original text even if the proposed identity is Christian Löffler.

## Batch 1

1. **classical-reading** — “Please make twelve classical tracks for reading; I prefer piano, and singing is fine.”

   Proposed meaning: Twelve classical tracks; piano preferred, singing permitted.

2. **classical-silent** — “Please make twelve classical tracks for reading; I prefer piano, but no singing.”

   Proposed meaning: Same soft piano preference, with a real vocal exclusion.

3. **aerosmith-near** — “Fifteen songs with Aerosmith's sound, but do not play Aerosmith themselves.”

   Proposed meaning: Aerosmith sound reference and a separate forbidden output artist.

4. **aerosmith-only** — “Fifteen songs by Aerosmith only, with energetic guitars if possible.”

   Proposed meaning: Aerosmith-only output; energetic guitars remain preferences.

5. **loeffler-night** — “Ten tracks like Christian Löffler for a quiet evening. Vocals are welcome.”

   Proposed meaning: Artist sound reference, quiet context, vocals permitted.

6. **loeffler-run** — “Ten tracks like Christian Löffler, but more energetic for a run. Not just his own songs.”

   Proposed meaning: Relative energy increase and artist diversity, not an artist exclusion.

7. **weights** — “Give me eighteen rock tracks for lifting weights. Avoid slow ballads; vocals are fine.”

   Proposed meaning: Rock for weights; slow ballads excluded, singing permitted.

8. **weights-soft** — “Give me eighteen rock tracks for lifting weights. Prefer fewer slow ballads, but some are okay.”

   Proposed meaning: Fewer ballads is a soft preference rather than a universal exclusion.

9. **nin-first** — “Fourteen songs: begin with Nine Inch Nails and finish with Marilyn Manson, with other artists between.”

   Proposed meaning: Actual artists at endpoints, inside fourteen total tracks.

10. **manson-first** — “Fourteen songs: begin with Marilyn Manson and finish with Nine Inch Nails, with other artists between.”

   Proposed meaning: Reverse endpoints without changing the count.

## Batch 2

11. **nin-like** — “Twelve tracks that sound like Nine Inch Nails and Marilyn Manson. Neither needs to be first.”

   Proposed meaning: Two sound references; no required first artist.

12. **nin-through** — “Twelve tracks from Nine Inch Nails through Depeche Mode to Marilyn Manson.”

   Proposed meaning: Actual ordered artist start, waypoint and destination inside twelve.

13. **hurt-required** — “Twelve songs like Nine Inch Nails. Include Hurt by Nine Inch Nails exactly once, anywhere.”

   Proposed meaning: Hurt required once; separate NIN similarity reference retained, no forced position.

14. **hurt-reference** — “Twelve songs like Hurt by Nine Inch Nails, but Hurt itself does not have to be included.”

   Proposed meaning: Qualified recording sound reference, no required recording.

15. **album-moon** — “Ten tracks with the feel of the album Moon Safari by Air. Exclude Air recordings.”

   Proposed meaning: Album sound reference linked to Air; Air forbidden in output.

16. **album-only** — “Ten tracks from Moon Safari by Air only; no similar albums.”

   Proposed meaning: Required album domain, not an album similarity request.

17. **bach-gould** — “Ten Bach recordings, preferably played by Glenn Gould. Other performers are fine.”

   Proposed meaning: Bach composer required; Gould performer preferred.

18. **gould-only** — “Ten Bach recordings played only by Glenn Gould, with no singing.”

   Proposed meaning: Bach composer and Gould performer required; vocals excluded.

19. **duration-positive** — “One hour and fifteen minutes of relaxing electronic music for work, mostly instrumental.”

   Proposed meaning: 4500 seconds total; instrumental preferred, not universal.

20. **duration-negative** — “Twelve tracks, not a twelve-minute playlist: relaxing electronic music for work.”

   Proposed meaning: Twelve tracks, no positive 720-second target.

## Batch 3

21. **warm-positive** — “Eight electronic tracks with a warm sound, not an aggressive one.”

   Proposed meaning: Warm positive, aggressive negative.

22. **warm-negative** — “Eight electronic tracks with an aggressive sound, not a warm one.”

   Proposed meaning: Aggressive positive, warm negative.

23. **vocal-opening** — “Sixteen tracks moving from electronic to industrial rock. Only the opening section must be instrumental; vocals later are fine.”

   Proposed meaning: Instrumental restricted to opening; later vocals permitted.

24. **vocal-global** — “Sixteen tracks moving from electronic to industrial rock. Every track must be instrumental.”

   Proposed meaning: Same genre journey; global instrumental requirement.

25. **genre-or** — “Twelve workout tracks: rock or metal, either is fine. No slow ballads.”

   Proposed meaning: Rock OR metal, not both on every recording; ballads excluded.

26. **genre-neither** — “Twelve workout tracks: soul, with neither rock nor metal.”

   Proposed meaning: Soul positive; both rock and metal excluded.

27. **date-range** — “Ten electronic tracks first released between 2001 and 2009.”

   Proposed meaning: Continuous original-release interval 2001 through 2009.

28. **date-or** — “Ten electronic tracks first released in 2001 or 2009, excluding the years between.”

   Proposed meaning: Discrete original-release years, never a continuous range.

29. **early-aerosmith** — “Fifteen songs like early Aerosmith, not their later power ballads; other artists welcome.”

   Proposed meaning: Scoped early-artist sound reference; no invented year cutoff; power-ballad sound negative.

30. **dated-aerosmith** — “Fifteen songs by Aerosmith only, first released from 1973 through 1975.”

   Proposed meaning: Artist-only output and literal original-release range.

## Batch 4

31. **compound-band** — “Ten songs like Alice in Chains and Stone Temple Pilots, without either band's recordings.”

   Proposed meaning: Preserve both compound artist names; exclude both output identities.

32. **compound-band-allow** — “Ten songs like Alice in Chains and Stone Temple Pilots, and include both bands too.”

   Proposed meaning: Preserve both sound references and required presence of both output artists.

33. **not-only-piano** — “Ten classical tracks, not only piano: strings and orchestral pieces are welcome.”

   Proposed meaning: Piano not excluded; broaden allowed instrumentation.

34. **only-piano** — “Ten classical tracks with piano only, no strings or orchestra.”

   Proposed meaning: Piano-only instrumentation, strings and orchestra excluded.

35. **unicode-loeffler** — “🎵 Eight tracks like Christian Löffler, but no Christian Löffler recordings.”

   Proposed meaning: Decomposed accent and emoji do not move source ownership between mentions.

36. **typo-loeffler** — “Eight tracks like christrian loeffler for relaxing, and singing is okay.”

   Proposed meaning: Preserve literal typo as a candidate artist mention; suggested identity requires resolution.

37. **soft-instrumental** — “Thirteen electronic tracks. Include some instrumental pieces; vocals are okay too.”

   Proposed meaning: Instrumental subset preference, mixed vocals allowed.

38. **hard-instrumental** — “Thirteen electronic tracks. Absolutely no vocals, every piece instrumental.”

   Proposed meaning: Global instrumental requirement and vocal exclusion.

39. **party-negative** — “Ten late-night tracks like Massive Attack, with dark textures, not cheerful party music.”

   Proposed meaning: Artist sound reference; dark preferred, cheerful party music negative.

40. **party-positive** — “Ten late-night tracks like Massive Attack, with cheerful party energy rather than dark textures.”

   Proposed meaning: Same artist reference; cheerful party positive, dark texture negative.

## Annotation and validation contract

- `start`/`end` are half-open UTF-8 byte offsets into the original prompt.
  Precomposed and decomposed accents are distinct source bytes.
- Each span keeps its kind, mention role, polarity, strength and scope.
  `relations` connects operators to targets and titles to performers.
- Different occurrences of one artist keep different IDs. An artist can be a
  sound reference and also excluded from output.
- The first DistilBERT pilot learns **BIO token roles only**, such as
  `artist:similarity` and `artist:exclude_output`. It does not yet learn the full
  relation, scope or strength graph. Those annotations remain available for a
  later multi-head experiment. Overlapping supervision is rejected explicitly.
- Splits keep paraphrase families and canonical named identities together,
  transitively. Small connected groups can produce uneven splits; do not break
  them apart merely to improve a score.
- This public, authored development batch is not an independent benchmark. The
  30 earlier model-comparison prompts are also development regressions.

## Reproduce preparation and the offline pilot

All tools below are maintainer tools. Release users do not install Python.
Verified original model assets are retained under
`C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/{distilbert,minilm}`.
Keep trained checkpoints, reports and prepared data outside Git.

Create a **new** CPython 3.12 virtual environment; do not change an existing
audio/MERT preparation environment:

```powershell
py -3.12 -m venv C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/tools/venv
$intentPython = 'C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/tools/venv/Scripts/python.exe'
& $intentPython -m pip install torch==2.7.1 --index-url https://download.pytorch.org/whl/cpu
& $intentPython -m pip install -r python/requirements-intent_nlu.txt
& $intentPython python/prepare_intent_nlu_data.py --input internal/evaluation/testdata/intent-nlu-review-v1.json --validate-only
```

The last command can validate all 40 draft records. Ordinary preparation fails
until **every input record has actual approved review**, a reviewer identifier
and a timezone-bearing ISO timestamp. Save the reviewed derivative beside the
unchanged original proposal:

```powershell
& $intentPython python/prepare_intent_nlu_data.py --input C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/reviewed-v1.json --output C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/data-v1
& $intentPython python/train_intent_nlu.py --data C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/data-v1 --source C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/distilbert --output C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/training-v1 --epochs 5 --batch-size 4 --threads 4
& $intentPython python/export_intent_nlu.py --training C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/training-v1 --output C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/export-v1
```

Preparation creates checked JSONL splits, a label vocabulary and a SHA-256
manifest. Training checks review and input hashes again, reads training and
calibration splits, and never reads the evaluation split. Training settings are
pilot starting points, not measured optimal hyperparameters. Keep a separate
untouched test cohort before judging generalization.

Export creates `model.onnx`, tokenizer files, `nlu-head.json`,
`export-parity.json` and **`calibration-candidate.json` with
`reviewed: false`**. It verifies FP32 PyTorch/ONNX logits and decisions on
numerical fixtures. It does not produce activation-ready `calibration.json`.
A loss score or numerical parity does not prove semantic reliability. Calibrate
accepted-role precision, omissions and abstention using reviewed data, evaluate
fresh complete requests, then record the actual review decision before
considering activation. No script changes app settings.

Run the reproducible threshold scan on the separate calibration split:

```powershell
& $intentPython python/calibrate_intent_nlu.py --data C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/data-v1 --training C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/training-v1 --export C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/export-v1 --output C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/calibration-v1 --threads 2
```

The report scans thresholds from 0.5 to 0.999 with native-equivalent whole-word
BIO decoding. Inspect exact byte-span/role errors, omissions, per-label coverage,
accepted-span counts and the precision interval. The default observed precision
target is 0.99 with at least 20 accepted spans; a small perfect sample does **not**
establish 99% reliability. A null suggested threshold means none met those
candidate-selection conditions. This helper never reads the evaluation split and
always writes `reviewed: false` in `calibration-candidate.json`.

After independent complete-request evaluation and an actual approval of a
specific threshold, retain the report and review decision. Make a derivative
`calibration.json` beside the exported model with the approved numeric threshold,
actual validation-example count, `reviewed: true` and unchanged model/head/vocab/
config hashes from the candidate. Do not change review flags merely to pass
import validation. Import that directory in Settings → Intent language models →
Use a reviewed DistilBERT extractor. Native health checks must pass; enable the
optional suggestions switch separately. This first pilot still uses the LLM to
validate learned proposals and supplies no learned hard overrides.

The first role head cannot resolve all the reviewed relations by itself.
Continue the native compiler/LLM fallback for missing scope or ambiguous roles.
GLiNER Small is an entity baseline; GLiNER2.5 is experimental until its complete
native export and decoder are verified. Neither is a demonstrated native
replacement simply because an ONNX file exists.

## Native tokenizer and embedding parity

Generate original-checkpoint reference reports, then run the native parity CLI
against exactly the same text cases. A reference-only report expressly says
`nativeParity: false`.

```powershell
& $intentPython python/verify_intent_nlu_parity.py --source C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/minilm --kind minilm --with-embeddings --reference-output C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/minilm-reference.json
& $intentPython python/verify_intent_nlu_parity.py --source C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/distilbert --kind distilbert --reference-output C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/distilbert-reference.json
& $intentPython python/verify_intent_nlu_parity.py --reference-output C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/minilm-reference.json --native-output C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/minilm-native.json --output C:/Users/pawel/Downloads/playlistai-intent-nlu-v1/minilm-parity.json
```

The native report contract is `version: 1`, `kind`, and ordered `cases` with
the original `text`, `ids`, `attentionMask`, `typeIds`,
`tokens: [{id,start,end,special}]`, and optional `embedding`. Public native
`Encoding` may be nested under `encoding`. The verifier requires exact
IDs/masks/source offsets. For MiniLM it also requires finite, normalized
384-dimensional embeddings, maximum absolute error ≤0.001 and cosine ≥0.99999
against the original PyTorch encoder. These thresholds concern numerical
equivalence, not recommendation or intent accuracy.

Fixtures cover repeated artists, smart punctuation, emoji, tabs/newlines,
nonbreaking spaces, composed/decomposed accents, non-Latin text and ligatures.
Do not change the reference normalization to hide a native mismatch.

## Failure recovery and checks

Observed on the available Windows x86-64 host on 12 September 2026: all 17
MiniLM and all 17 DistilBERT tokenizer cases matched native IDs, masks and
UTF-8 offsets exactly. The 17 MiniLM native embeddings matched the original
PyTorch encoder with maximum absolute error 2.082e-7 and minimum cosine
0.999999999999381. These establish numerical parity for these fixtures only.
The sixteen offline regressions pass, including one epoch and ONNX export of a
tiny random DistilBERT with temporary synthetic test labels. The real review
batch remains **40 unreviewed records, zero approvals**, and has not been used
to train a task-adapted model. No semantic-calibration or playlist-quality
improvement is established by these checks.

Use a new output directory for each preparation/training/export; existing
outputs are not overwritten. An unreviewed-record error means obtain the real
review rather than change the validator. A token-overlap error means this
single-head architecture cannot represent that annotation; change the
architecture or refine the reviewed span representation without losing meaning.
A truncation error means split/review the request, never drop its final
exclusion. A checksum mismatch means restore the retained immutable inputs.

```powershell
& $intentPython -m unittest discover -s python -p 'test_intent_nlu*.py'
```

Tests use synthetic temporary records and a tiny randomly initialized encoder
where appropriate; synthetic “approved” fixture records are confined to tests
and are never merged into the real review dataset or used to claim musical
quality.
