// Render the runtime dictionary for reviewers; no network or model inference.
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
const root = new URL('../', import.meta.url);
const read = (path) => JSON.parse(fs.readFileSync(new URL(path, root), 'utf8'));
const dictionary = read('internal/musicconcepts/concepts.json');
const schema = read('internal/musicconcepts/acoustic_schema.json');
const count = dictionary.concepts.reduce((n, c) => n + 1 + (c.aliases?.length ?? 0), 0);
const escape = (s) => String(s).replaceAll('|', '\\|').replaceAll('\n', ' ');
const lines = [
 '# Prompt thesaurus reference', '',
 `Generated from \`${dictionary.version}\`: **${dictionary.concepts.length} concepts and ${count} canonical terms/aliases**.`, '',
 'Regenerate with `node scripts/render-music-thesaurus.mjs`; verify with `--check`.', '',
 'These are contextual vocabulary mappings, not guarantees of musical fit. The parser preserves original source spans, polarity, strength, alternatives and journey scope. Unknown wording remains available to the language model and CLAP; it is not replaced with a guessed parent.', '',
 '## Provider contracts', '',
 'AcousticBrainz is an archive of recording analysis, not a prompt encoder. Its legacy classifier labels are checked against a [public high-level document](https://acousticbrainz.org/ac2bbf32-0f6a-48bf-95d5-5edf4d060feb/high-level/view?n=0) and the [Essentia legacy classifier documentation](https://essentia.upf.edu/gaia_svm_models.html). Newer Essentia neural-model labels must not be substituted into this archive schema.', '',
 'CLAP embeds natural-language audio descriptions. The [upstream implementation](https://github.com/LAION-AI/CLAP) demonstrates text/audio embedding and contextual genre prompts. Captions below are project-authored proposals, not an upstream whitelist or measured optimal prompts. The current uncalibrated best-available query policy averages the canonical phrase and one caption. Strict clauses and calibrated policies retain their existing raw-query contract; changing those requires recalibration.', '',
 'A dash in the AcousticBrainz column means no exact supported mapping. Missing records, incompatible versions and missing classes remain unknown. A classifier margin and CLAP cosine use different scales and neither is a probability of satisfying the prompt.', '',
 '## Archived classifier schema', '',
 '| Classifier | Source labels |', '| --- | --- |',
 ...Object.entries(schema.classifiers).map(([model, labels]) => `| ${model} | ${labels.join(', ')} |`), '',
 'Combined Dortmund classes are not aliases for their narrower members: `folkcountry` cannot prove folk or country, `funksoulrnb` cannot prove funk, soul or R&B, and `raphiphop` is not treated as a direct synonym. The [archive label display](https://acousticbrainz.org/a818fa37-5e4d-4245-80aa-d4093eb142ef) identifies ROSAmerica `rhy` as rhythm and blues. Unmapped schema labels remain unused.', '',
 '| Disabled classifier | Reason |', '| --- | --- |',
 ...Object.entries(schema.disabled).map(([model, reason]) => `| ${model} | ${reason} |`), '',
 '## Numerical and ambiguous language', '',
 '| User wording | Interpretation |', '| --- | --- |',
 '| 120 BPM, between 100 and 120 BPM | Preserve explicit numerical intent; archive `rhythm.bpm` is a measurement. The thesaurus does not convert a CLAP caption into a verified BPM result. |',
 '| fast, slow, upbeat | Context is needed: tempo, mood and rhythmic feel differ. Do not invent numerical BPM thresholds. |',
 '| loud, energetic, dynamic | Loudness, perceived energy and dynamic variation differ. `lowlevel.average_loudness` and `lowlevel.dynamic_complexity` do not prove the other meanings. |',
 '| danceable | Uses the high-level danceability class. Low-level DFA danceability is a separate numerical scale, not a 0–1 probability. |',
 '| minor key, major key | Tonal key/scale are measurements; do not infer sadness or happiness. |',
 '| dark mood / dark timbre | Emotional darkness stays CLAP-only; spectral darkness maps to timbre.dark. Bare dark keeps the existing mood-first parser behavior. |',
 '| bright mood / bright timbre | Use explicit cheerful/joyful wording for mood. Bare bright keeps the existing timbre interpretation; it does not prove happiness. |',
 '| acoustic guitar / acoustic sound | Guitar identity remains an instrument request. mood_acoustic supports acoustic character, not the presence of a particular instrument. |',
 '| electronic / electronica / electronic production | Genre, narrower genre and production character remain separate. mood_electronic is only production evidence. |',
 '| no vocals / no screaming | Preserve distinct negated clauses; the second does not exclude all singing. No synonym replacement erases no, less, mostly, or stage boundaries. |',
 '| not happy / sad | Not-happy is a classifier complement, not a synonym for sadness. Score polarity is handled outside the dictionary. |',
 '| dissonant / atonal | Dissonance can occur in tonal music. Only explicit atonal terminology maps to tonal_atonal.atonal. |',
 '| dreamy, nostalgic, lo-fi, piano, workout | CLAP descriptions with no direct archive class; do not map to relaxed, sad, acoustic, instrumental or aggressive by association. |',
 '| artist or song names, quoted titles | Keep in the identity resolver and protected source spans, not lexical substitution. |', '',
 '## Full vocabulary', '',
 'Aliases are matched case-insensitively within their typed facet. The parser uses token boundaries and prefers longer phrases, so specific subgenres are retained. Provider mapping never expands parent or related concepts. English is the authored language; a few conventional accented spellings are included, not general multilingual coverage.', ''
];
for (const kind of ['genre', 'mood', 'instrumentation', 'vocal', 'texture', 'activity']) {
 lines.push(`### ${kind}`, '', '| Canonical term | Accepted aliases | CLAP caption | AcousticBrainz model:class |', '| --- | --- | --- | --- |');
 for (const c of dictionary.concepts.filter(c => c.kind === kind)) {
  const mapping = Object.entries(c.providers?.acousticBrainz ?? {}).map(([m,l])=>`${m}:${l}`).join('; ') || '—';
  lines.push('| '+[c.value, c.aliases?.join('; ') || '—', c.providers?.clap?.[0] || '—', mapping].map(escape).join(' | ')+' |');
 }
 lines.push('');
}
lines.push('## Validation and remaining evaluation', '',
 'Offline tests cover schema validity, alias equivalence, provider-specific unknowns, original source spans, negation, protected names and CLAP query-policy boundaries. They establish mapping behavior, not recognition quality on real recordings.', '',
 'Before claiming a quality improvement, freeze the installed CLAP checkpoint, preprocessing, vocabulary version, corpus and seeds. Compare raw prompt, canonical-only and canonical-plus-caption retrieval on a held-out, artist-disjoint listening set. Include synonyms, near-synonyms, ambiguous words, negative clauses and unseen descriptions. Report per-facet ranking metrics and human relevance judgments, failure rates, and changes for exclusion cases. For AcousticBrainz, evaluate each classifier separately on matched recording IDs and report coverage/conflicts; do not tune new hard thresholds from synonym examples. No model inference or listening-set evaluation was performed to author this table.', '');
const output = lines.join('\n');
const target = new URL('docs/prompt-thesaurus.md', root);
if (process.argv.includes('--check')) {
 if (!fs.existsSync(target) || fs.readFileSync(target, 'utf8') !== output) throw Error('Thesaurus reference is stale');
} else fs.writeFileSync(target, output);
console.log(fileURLToPath(target));
