# Results for the original 40 prompts

Both raw 40-prompt audits are complete. This table compares the baseline with
the frozen v3 treatment. Matched controls and later-build diagnostics are
reported separately; their completion is tracked in the audit report.

AI web assessment, not human listening; no generalization to held-out prompts.

End-to-end original implementation at 5 minutes vs reviewed treatment at 10 minutes, with independently generated seeds and live source conditions; combines preparation, implementation, seed and budget effects. Matched intent/seed controls are reported separately.

Generation time from matching completed child reports, including failures and clarification outcomes, excludes desktop startup. Unverified parent fallback timings are separate because they may include startup. Shared host and live providers; not isolated performance measurements.

| Arm | Audited cases | Returned / 400 | Good | Partial | Mismatch | Unknown | Unreviewed returned | Missing attempted | Unrun slots |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| baseline | 40/40 | 219 | 97 | 62 | 47 | 13 | 0 | 181 | 0 |
| treatment | 40/40 | 61 | 16 | 29 | 14 | 2 | 0 | 339 | 0 |

Zero returned tracks have undefined returned-track precision. Missing songs and unrun cases retain their requested slots.

| Case | Original prompt | Baseline returned / good | Treatment returned / good |
|---:|---|---:|---:|
| 1 | Classical, 10 tracks. | 10 / 10 | 0 / 0 |
| 2 | Electronic music. Make a 10-song playlist. | 10 / 4 | 1 / 1 |
| 3 | Give me 10 jazz songs. | 10 / 9 | 0 / 0 |
| 4 | Make a 10-song reggae playlist. | 10 / 10 | 0 / 0 |
| 5 | Give me 10 heavy metal songs. | 10 / 9 | 0 / 0 |
| 6 | Make a 10-song playlist inspired by Daft Punk. | 0 / 0 | 0 / 0 |
| 7 | Give me 10 songs similar to Radiohead, including other artists. | 0 / 0 | 0 / 0 |
| 8 | Make a 10-song playlist around Nina Simone and Bill Withers. | 0 / 0 | 0 / 0 |
| 9 | Give me 10 tracks inspired by Kraftwerk, Tangerine Dream, and Brian Eno. | 0 / 0 | 0 / 0 |
| 10 | Make a 10-song playlist inspired by Fela Kuti and Tony Allen, with a variety of artists. | 0 / 0 | 0 / 0 |
| 11 | Give me 10 songs like 'Teardrop' by Massive Attack. | 0 / 0 | 10 / 1 |
| 12 | Make a 10-song playlist inspired by 'So What' by Miles Davis and 'Take Five' by the Dave Brubeck Quartet. | 0 / 0 | 0 / 0 |
| 13 | Give me 10 tracks mixing house, techno, and UK garage. | 10 / 3 | 0 / 0 |
| 14 | Make a 10-song playlist combining bossa nova, samba, and Brazilian jazz. | 10 / 4 | 0 / 0 |
| 15 | Give me 10 songs spanning bluegrass, country, and Americana. | 10 / 9 | 0 / 0 |
| 16 | Make a 10-song playlist mixing Japanese city pop, funk, and disco. | 10 / 5 | 0 / 0 |
| 17 | Give me 10 tracks of spacious electronic music with detailed textures, a deep groove, and occasional sparkle—relaxing but not sleepy. | 10 / 0 | 0 / 0 |
| 18 | Make a 10-song playlist with warm acoustic instruments, gentle rhythms, and an intimate late-night feel. | 10 / 2 | 10 / 6 |
| 19 | Give me 10 songs with jangly guitars, melodic bass lines, and bright indie-pop energy. | 10 / 0 | 0 / 0 |
| 20 | Make a 10-track playlist of orchestral music with sweeping strings, dramatic contrasts, and a cinematic atmosphere. | 10 / 0 | 10 / 1 |
| 21 | Make a 10-song journey from ambient electronic through downtempo to melodic house, gradually increasing the energy. | 0 / 0 | 0 / 0 |
| 22 | Give me a 10-song playlist that starts with acoustic folk, moves through folk rock, and ends with energetic alternative rock. | 0 / 0 | 0 / 0 |
| 23 | Make a 10-track journey from Brian Eno's ambient sound through Boards of Canada to Jon Hopkins, including other artists along the way. | 0 / 0 | 0 / 0 |
| 24 | Give me 10 songs moving from traditional soul through funk into disco. Keep the transitions smooth and avoid placing the same artist back to back. | 0 / 0 | 0 / 0 |
| 25 | Make a 10-track instrumental playlist for focused work, blending modern classical, ambient, and gentle electronic music. Start sparse, build a subtle pulse in the middle, and finish calmly. No vocals. | 9 / 1 | 0 / 0 |
| 26 | Punk rock, 10 songs. | 10 / 5 | 0 / 0 |
| 27 | Give me 10 blues songs. | 10 / 6 | 0 / 0 |
| 28 | Make a 10-song salsa playlist. | 10 / 9 | 0 / 0 |
| 29 | Give me 10 drum and bass tracks. | 10 / 4 | 0 / 0 |
| 30 | Make a 10-song playlist inspired by Björk. | 0 / 0 | 0 / 0 |
| 31 | Give me 10 songs like 宇多田ヒカル, including other artists. | 0 / 0 | 10 / 5 |
| 32 | Make a 10-song journey from David Bowie to Talking Heads, with related artists bridging the transition. | 0 / 0 | 0 / 0 |
| 33 | Give me 10 songs that move from Bonobo to Massive Attack, including related discoveries. | 0 / 0 | 0 / 0 |
| 34 | Make a 10-song playlist inspired by Joni Mitchell, Nick Drake, and Leonard Cohen. | 0 / 0 | 0 / 0 |
| 35 | Give me 10 songs blending hip-hop, neo-soul, and jazz. | 10 / 7 | 0 / 0 |
| 36 | Make a 10-track playlist with distorted guitars, pounding drums, and a tense, restless mood. | 10 / 0 | 10 / 2 |
| 37 | Give me 10 tracks with soft piano, spacious reverberation, and a reflective atmosphere. | 10 / 0 | 10 / 0 |
| 38 | Make a 10-song playlist with syncopated bass, lively percussion, and a celebratory dance groove. | 10 / 0 | 0 / 0 |
| 39 | Give me a 10-song journey from blues through soul to funk. | 0 / 0 | 0 / 0 |
| 40 | Make a playlist only by Radiohead, 10 songs. | 0 / 0 | 0 / 0 |

Per-song sources, grades and recording caveats: [baseline audit](data/forty-prompt-audit-2026-09-26/baseline.json) and [frozen treatment audit](data/forty-prompt-audit-2026-09-26/treatment.json). Implementation, limitations and remaining evaluation: [audit report](forty-prompt-quality-audit-2026-09-26.md).
