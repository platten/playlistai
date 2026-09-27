# Automatic v3: final development song audit

The final `automatic/v3+automatic-fit/v3` build completed all 40 development prompts. Public-source checks covered every returned occurrence. This is an AI source assessment, not human listening or held-out acceptance.

| Result | Count |
| --- | ---: |
| Good individual match | 90 |
| Partial individual match | 19 |
| Mismatch | 8 |
| Unknown | 3 |
| Missing requested slot | 280 |

Good matches account for 75% of the 120 returned songs and 22.5% of the 400 requested slots. **All 90 good matches are recordings by explicitly requested artists.** None of the 30 returned songs for the descriptive requests in cases 20, 36 and 37 earned a good judgment. These results do not establish high-quality general recommendation or discovery.

The Radiohead request in case 7 returns only Radiohead and correctly reports partial fulfillment. Artist concentration, short album segments, missing bridging artists and unverified journey progression remain playlist-level problems even when individual recordings belong to the requested artist. The audit does not invent a minimum-duration ban or treat every soft spacing preference as a hard constraint.

The run returned 120 tracks, with seven operations marked fulfilled, 32 partial and one needing clarification. Generation median was 13.55 seconds, p95 25.000 seconds and maximum 25.024 seconds after setup. The harness completed but exited nonzero. One reused isolated store was used; these are neither paired cache-condition latency gates nor a controlled numerical comparison with v2. No held-out examples were opened.

[Complete evidence JSON](data/automatic-v3-song-audit-2026-09-27.json) includes literal rubrics, scoped citations, declared recording identities, exact-identity reuse receipts and immutable report/build hashes. [Runtime receipt](data/automatic-forty-v3-development-2026-09-27.json), [code/UI validation](data/automatic-validation-2026-09-27.json), [independent feature experiment](automatic-evaluation.md), and [architecture and release work](automatic-playlists.md) separate code correctness from musical acceptance. The [earlier v2 audit](automatic-song-audit-2026-09-27.md) is preserved unchanged.

## What changed and what remains

- Artist identities resolve against prepared public listener counts, with explicit context and reversible user choices taking precedence.
- Automatic freezes one bounded evidence batch, then ranks and assembles locally within a shared 30-second generation budget.
- Discoveries require a prepared artist relationship and a compatible audio-neighbor hit tied to the same explicit reference. Missing corroboration leaves a partial playlist. Required songs and journey endpoints retain their separate roles.
- Interpretation timeouts preserve the source count and genre stages and explicitly report the fallback. The final native run confirmed this for case 21.
- The installed catalog and text encoder remain incompatible. Descriptive evidence, reference-neighborhood quality, artist variety and recording/version data still need improvement. The planned soft 60/40 mix is not implemented; current popularity contributes only a small ranking preference.
- Automatic stays staged. A compatible measured catalog, stronger independent labels and vocal evidence, broader published graph coverage and passing held-out quality/latency gates are needed before promotion.

## 1. Classical, 10 tracks.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 2. Electronic music. Make a 10-song playlist.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 3. Give me 10 jazz songs.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 4. Make a 10-song reggae playlist.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 5. Give me 10 heavy metal songs.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 6. Make a 10-song playlist inspired by Daft Punk.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Daft Punk — Funk Ad | good | Official Homework track and publisher duration identify this direct Daft Punk selection. It is only 51 seconds, so it supplies little full-song listening time despite fitting the reference; no duration minimum was requested. [Source 1](https://www.daftpunk.com/homework/) [Source 2](https://music.apple.com/us/album/homework/696884422) |
| 2 | Daft Punk — WDPK 83.7 FM | good | Official Daft Punk tracklist identifies this recording on Homework; direct material fits the requested reference. [Source 1](https://www.daftpunk.com/homework/) |
| 3 | Daft Punk — Daftendirekt | good | Official Daft Punk tracklist identifies this recording on Homework; direct material fits the requested reference. [Source 1](https://www.daftpunk.com/homework/) |
| 4 | Daft Punk — Rollin’ & Scratchin’ | good | Official Daft Punk tracklist identifies this recording on Homework; direct material fits the requested reference. [Source 1](https://www.daftpunk.com/homework/) |
| 5 | Daft Punk — Around the World | good | Official Daft Punk tracklist identifies this recording on Homework; direct material fits the requested reference. [Source 1](https://www.daftpunk.com/homework/) |
| 6 | Daft Punk — Teachers | good | Official Daft Punk tracklist identifies this recording on Homework; direct material fits the requested reference. [Source 1](https://www.daftpunk.com/homework/) |
| 7 | Daft Punk — Oh Yeah | good | Official Daft Punk tracklist identifies this recording on Homework; direct material fits the requested reference. [Source 1](https://www.daftpunk.com/homework/) |
| 8 | Daft Punk — Indo Silver Club | good | Official Daft Punk tracklist identifies this recording on Homework; direct material fits the requested reference. [Source 1](https://www.daftpunk.com/homework/) |
| 9 | Daft Punk — High Fidelity | good | Official Daft Punk tracklist identifies this recording on Homework; direct material fits the requested reference. [Source 1](https://www.daftpunk.com/homework/) |
| 10 | Daft Punk — Nightvision | good | Official Daft Punk tracklist identifies this recording on Discovery; direct material fits the requested reference. [Source 1](https://daftpunk.com/discovery/) |

All ten are Daft Punk, nine from Homework. Direct reference fit is supported, but discovery variety is absent; WDPK 28 seconds and Funk Ad 51 seconds are brief album segments.

## 7. Give me 10 songs similar to Radiohead, including other artists.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Radiohead — Sulk | good | Radiohead’s official label page identifies this The Bends recording; it supplies direct reference fit but contributes no other artist. [Source 1](https://radiohead.bandcamp.com/album/the-bends) |
| 2 | Radiohead — In Limbo | good | Radiohead’s official label page identifies this Kid A recording; it supplies direct reference fit but contributes no other artist. [Source 1](https://radiohead.bandcamp.com/album/kid-a) |
| 3 | Radiohead — Morning Bell/Amnesiac | good | Radiohead’s official label page identifies this Amnesiac recording; it supplies direct reference fit but contributes no other artist. [Source 1](https://radiohead.bandcamp.com/album/amnesiac) |
| 4 | Radiohead — Pyramid Song | good | Radiohead’s official label page identifies this Amnesiac recording; it supplies direct reference fit but contributes no other artist. [Source 1](https://radiohead.bandcamp.com/album/amnesiac) |
| 5 | Radiohead — How to Disappear Completely | good | Radiohead’s official label page identifies this Kid A recording; it supplies direct reference fit but contributes no other artist. [Source 1](https://radiohead.bandcamp.com/album/kid-a) |
| 6 | Radiohead — Bones | good | Radiohead’s official label page identifies this The Bends recording; it supplies direct reference fit but contributes no other artist. [Source 1](https://radiohead.bandcamp.com/album/the-bends) |
| 7 | Radiohead — Fake Plastic Trees | good | Radiohead’s official label page identifies this The Bends recording; it supplies direct reference fit but contributes no other artist. [Source 1](https://radiohead.bandcamp.com/album/the-bends) |
| 8 | Radiohead — (Nice Dream) | good | Radiohead’s official label page identifies this The Bends recording; it supplies direct reference fit but contributes no other artist. [Source 1](https://radiohead.bandcamp.com/album/the-bends) |
| 9 | Radiohead — My Iron Lung | good | Radiohead’s official label page identifies this The Bends recording; it supplies direct reference fit but contributes no other artist. [Source 1](https://radiohead.bandcamp.com/album/the-bends) |
| 10 | Radiohead — Optimistic | good | Radiohead’s official label page identifies this Kid A recording; it supplies direct reference fit but contributes no other artist. [Source 1](https://radiohead.bandcamp.com/album/kid-a) |

All ten are Radiohead. Individual reference fit is supported, but the explicit requirement to include other artists is unmet.

## 8. Make a 10-song playlist around Nina Simone and Bill Withers.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Bill Withers — Look to Each Other for Love | good | Withers' official album page names this recording and describes its characteristic mellow songwriting. A direct requested-artist recording is an appropriate match. [Source 1](https://billwithers.com/discography/albums/bout-love/) |
| 2 | Nina Simone — Suzanne | good | Nina Simone's official studio discography places Suzanne on To Love Somebody and supplies personnel. The declared compilation is consistent with that studio version; a direct requested-artist selection fits despite unverified master details. [Source 1](https://www.ninasimone.com/studio-albums/) |
| 3 | Nina Simone — The Assignment Sequence | good | Official live discography identifies this Nina Simone recording; library metadata corroborates the 6:57 Black Gold cut. Direct reference fit. [Source 1](https://www.ninasimone.com/live-sessions/) [Source 2](https://www.muziekweb.nl/Link/HDX6540/Emergency-ward-It-is-finished-Black-gold) |
| 4 | Bill Withers — Love Is | good | Sony-distributed song metadata identifies Love Is on Bill Withers’ ’Bout Love at 4:24, matching the direct soul reference. [Source 1](https://www.qobuz.com/us-en/album/bout-love-bill-withers/0884977267716) [Source 2](https://music.apple.com/au/song/322981837) |
| 5 | Bill Withers — Dedicated to You My Love | good | Official Withers album commentary names Dedicated to You My Love and its mellow approach, grounding the direct-reference fit. [Source 1](https://billwithers.com/discography/albums/bout-love/) |
| 6 | Nina Simone — The Other Woman | good | The Philips compilation identifies Nina Simone’s 3:06 performance, matching the declared compilation duration and requested reference. [Source 1](https://www.allmusic.com/album/four-women-the-nina-simone-philips-recordings-mw0000029581) [Source 2](https://www.muziekweb.nl/en/Link/JFX3162/Four-women-the-Philips-recordings) |
| 7 | Nina Simone — Com' By H'Yere - Good Lord | good | Official Simone live-session discography identifies Com' By H'Yere on It Is Finished and documents the personnel/release; direct requested-artist material fits. Exact compilation mastering unverified. [Source 1](https://www.ninasimone.com/live-sessions/) [Source 2](https://www.ninasimone.com/albums/it-is-finished/) |
| 8 | Nina Simone — Black Is the Color of My True Love's Hair (vocal Nina Simone) | good | Official notes distinguish Simone’s performance from Emile Latimer’s alternative; library metadata supports the selected 5:58 Nina cut. [Source 1](https://www.ninasimone.com/live-sessions/) [Source 2](https://www.muziekweb.nl/Link/HDX6540/Emergency-ward-It-is-finished-Black-gold) |
| 9 | Nina Simone — Forbidden Fruit | good | Official Simone discography identifies the title recording with her voice and piano, supporting the direct jazz/blues/soul reference. [Source 1](https://www.ninasimone.com/studio-albums/) [Source 2](https://musicbrainz.org/release/bb65432f-8a16-429c-86c9-c03dc9a86130) |
| 10 | Bill Withers — Don't It Make It Better | good | Official Withers commentary specifically identifies this horn-led R&B single, grounding both recording identity and the requested soul/funk reference. [Source 1](https://billwithers.com/discography/albums/bout-love/) [Source 2](https://billwithers.com/biography/) |

Both references represented: six Nina Simone and four Bill Withers. All Withers selections come from one album; live/compilation version limits are disclosed.

## 9. Give me 10 tracks inspired by Kraftwerk, Tangerine Dream, and Brian Eno.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Tangerine Dream — Phaedra | good | Direct Tangerine Dream reference: the official Phaedra page describes the long title recording with Moog, Mellotron and sequenced electronics. The declared 17:46 album recording fits this direction; the exact digital master is not authenticated. [Source 1](https://www.tangerinedreammusic.com/en/music/detail.asp?id=12&tit=Phaedra) |
| 2 | Brian Eno — Pierre in Mist | good | Publisher identifies Pierre in Mist on Brian Eno’s Nerve Net. It is direct material by a requested reference in his electronic catalogue; this supports reference inclusion, without claiming that every Eno recording is ambient or that this exact master was verified. [Source 1](https://music.apple.com/be/song/684277495) [Source 2](https://classical.music.apple.com/us/album/684277489) |
| 3 | Kraftwerk — La Forme | good | Contemporary Billboard review names La Forme in Kraftwerk’s electronic Tour de France project: direct requested-artist material. [Source 1](https://www.worldradiohistory.com/Archive-All-Music/Billboard/00s/2003/BB-2003-09-06.pdf) [Source 2](https://music.apple.com/us/album/tour-de-france-remastered/726341054) |
| 4 | Kraftwerk — Régéneration | good | Direct Kraftwerk selection from Tour de France supports the electronic reference. This is the short 1:17 Régéneration segment, not a separate full-length remix or the later 3-D live performance; exact remaster remains unverified. [Source 1](https://music.apple.com/us/album/tour-de-france-remastered/726341054) |
| 5 | Brian Eno — The Ship | good | Warp’s retailer identifies Eno’s 21:19 title recording and its experimental ambient/vocal construction, matching the Eno reference. [Source 1](https://bleep.com/format/181565-the-ship?lang=ca%3Flang%3Den_US) |
| 6 | Kraftwerk — Vitamin | good | Contemporary Billboard review names Vitamin on Kraftwerk’s Tour de France project, supporting the requested electronic direction. [Source 1](https://www.worldradiohistory.com/Archive-All-Music/Billboard/00s/2003/BB-2003-09-06.pdf) [Source 2](https://music.apple.com/us/album/tour-de-france-remastered/726341054) |
| 7 | Kraftwerk — Abzug | good | Publisher identifies this 2:18 Abzug as the 1991 remix on The Mix, a direct Kraftwerk electronic selection. [Source 1](https://music.amazon.co.uk/tracks/B002P41LUW) [Source 2](https://musicbrainz.org/release/d4670162-0b6b-4d9e-8689-f9ecabec027f) |
| 8 | Kraftwerk — Trans-Europe Express | good | Kraftwerk’s named album recording is directly relevant; release metadata corroborates the 6:52 version declared in the catalog. [Source 1](https://musicbrainz.org/release/bc280dce-5b04-4c34-9585-59ef319ebd57) [Source 2](https://www.qobuz.com/de-de/album/trans-europe-express-2009-digital-remaster-kraftwerk/5099996602058) |
| 9 | Kraftwerk — It's More Fun to Compute | good | Contemporary electronics-magazine criticism specifically describes Kraftwerk’s vocoder/electronic recording, supporting this direct reference selection. [Source 1](https://www.worldradiohistory.com/UK/Elecctronics-Music-Maker/Electronics-%26-Music-Maker-1981-07-S-OCR.pdf) |
| 10 | Kraftwerk — Autobahn | good | Rhino confirms Kraftwerk’s Autobahn recording; the declared 22:46 long album version fits the requested electronic lineage. [Source 1](https://media.rhino.com/release-info/autobahn) |

All three references represented, with seven Kraftwerk tracks. Long Phaedra, The Ship and Autobahn album works are included; no maximum duration requested.

## 10. Make a 10-song playlist inspired by Fela Kuti and Tony Allen, with a variety of artists.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 11. Give me 10 songs like 'Teardrop' by Massive Attack.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 12. Make a 10-song playlist inspired by 'So What' by Miles Davis and 'Take Five' by the Dave Brubeck Quartet.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 13. Give me 10 tracks mixing house, techno, and UK garage.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 14. Make a 10-song playlist combining bossa nova, samba, and Brazilian jazz.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 15. Give me 10 songs spanning bluegrass, country, and Americana.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 16. Make a 10-song playlist mixing Japanese city pop, funk, and disco.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 17. Give me 10 tracks of spacious electronic music with detailed textures, a deep groove, and occasional sparkle—relaxing but not sleepy.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 18. Make a 10-song playlist with warm acoustic instruments, gentle rhythms, and an intimate late-night feel.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 19. Give me 10 songs with jangly guitars, melodic bass lines, and bright indie-pop energy.

Returned 0/10; 10 missing.

No returned recordings to grade; all ten requested slots remain missing.

## 20. Make a 10-track playlist of orchestral music with sweeping strings, dramatic contrasts, and a cinematic atmosphere.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | The Beatles — Something | partial | The Beatles’ ballad has documented strings alongside the rock-band arrangement. This satisfies a string-colour aspect, but does not establish predominantly orchestral music with the requested sweeping dramatic contrasts. Compilation master remains unverified. [Source 1](https://www.thebeatles.com/something) [Source 2](https://bobbyowsinskiblog.com/the-beatles-something-isolated-tracks/) |
| 2 | Jamey Johnson — Mary Go Round | mismatch | Publisher explicitly describes Mary Go Round as an intense outlaw-country ballad. Emotional drama is present, but the evidence does not support the defining orchestral/sweeping-string request. [Source 1](https://music.apple.com/us/song/1440844788) [Source 2](https://music.apple.com/us/album/that-lonesome-song/1440844465) |
| 3 | Imogen Heap — Entanglement | partial | Official artist/label material identifies the Sparks recording; track-specific criticism and credits document strings within an electronic vocal arrangement. String presence is supported, while dominant orchestral scale and dramatic contrasts are not. [Source 1](https://www.youtube.com/watch?v=HQAPa1zIqbQ) [Source 2](https://abcnews.com/Entertainment/music-reviews-latest-imogen-heap-kimbra-smokey-robinson/story?id=25091332) [Source 3](https://mb.videolan.org/release/c8b2e00e-f02e-40af-840f-2e9134c97f35) |
| 4 | Curt Cress;Peter Weihe;Paul Badley;Jan-Eric Kohrs;Frank Peterson;Matthias Meissner;Michael Soltau;Jeremy Budd;Carsten Heusmann;Reiner \\"Kallas\\" Hubert;Alex Grube;Alexander Pfeffer;Gunther Laudahn;Lawrence White;William Gaunt;Daniel Hoadley;René Laack;Ashley Turnell;David Tilley;Edward Hands;Freddy Wiese;Berwyn Pearce;Sheyda Minia;Tristan Stocks;Ben Alden;Brendan Matthew;Jonathan Clucas;Gregorian;London Symphony Orchestra;Prague Philharmonic Orchestra;Chris Forster — Policy of Truth | unknown | Gregorian’s official tracklist identifies Policy of Truth as a new song on 25/2025; song-level publisher credits list choir voices, drums and keyboards. The imported artist field aggregates many album contributors and two orchestras, which cannot establish that either orchestra performs on this track. Sweeping strings and dramatic orchestral contrast remain unverified. [Source 1](https://www.gregorian.de/products/25-/-2025-cd---3-.html) [Source 2](https://www.shazam.com/song/1795644216/policy-of-truth) |
| 5 | Story of the Year — Cannonball | mismatch | Publisher confirms Cannonball on The Black Swan; contemporary album/track criticism describes guitar-led post-hardcore/alternative rock. The available evidence supports forceful rock dynamics rather than the requested orchestral/string setting. [Source 1](https://music.apple.com/gb/song/1485044184) [Source 2](https://www.antimusic.com/reviews/08/StoryoftheYear.shtml) [Source 3](https://www.sputnikmusic.com/review/26388/Story-of-the-Year-The-Black-Swan/) |
| 6 | Shakira — Costume Makes the Clown | mismatch | Publisher identifies the Oral Fixation album recording. A secondary summary attributes its forceful guitar arrangement to the contemporary Rolling Stone review; direct contemporary criticism also places it in Shakira’s dramatic pop/rock setting. That supports a mismatch to the defining orchestral focus, with a weaker source chain for the precise guitar description. [Source 1](https://open.spotify.com/track/3q3AqMVGu0h9WoNh0TXya4) [Source 2](https://en.wikipedia.org/wiki/Oral_Fixation%2C_Vol._2) [Source 3](https://www.slantmagazine.com/music/shakira-oral-fixation-vol-2/) |
| 7 | Bahamas — All I've Ever Known | partial | The artist interview/review specifically describes All I’ve Ever Known with piano, rising strings, choir-like voices and guitar, giving credible cinematic/string adjacency. It remains a vocal songwriter arrangement, and the saved 6:15 duration is not independently reconciled with the commonly listed shorter album track. [Source 1](https://www.straight.com/music/750901/bahamas-takes-musical-detour-through-california) |
| 8 | Ludovico Einaudi — Un mondo a parte | partial | Official label/publisher documentation supports piano and strings on the 4:07 Eden Roc recording and a film-like album atmosphere. This is a credible chamber/classical direction, but sweeping orchestral scale and pronounced dramatic contrasts are not established by the sources. [Source 1](https://www.universalmusic.it/popular-music/album/eden-roc_33261100640/) [Source 2](https://classical.music.apple.com/gb/album/1526309326) [Source 3](https://store.deutschegrammophon.com/en-en/products/ludovico-einaudi-eden-roc) |
| 9 | Coldplay — Amsterdam | partial | Official discography identifies Amsterdam; published track commentary supports a quiet piano opening that grows into urgent band dynamics. This is a dramatic contrast, but no adequate source establishes sweeping orchestral strings on the returned studio version. [Source 1](https://www.coldplay.com/song/amsterdam/) [Source 2](https://www.nme.com/features/every-coldplay-song-ranked-in-order-of-greatness-2704676) |
| 10 | Leonard Cohen — Puppets | partial | The official recording and track-specific release reporting support Cohen’s dramatic poem with choral voices. That gives atmosphere and drama, but choral presence is not evidence of the requested sweeping string/orchestral arrangement. [Source 1](https://www.leonardcohen.com/track/puppets) [Source 2](https://www.youtube.com/watch?v=ArLaIUS3sgs) [Source 3](https://cdn.fedweb.org/fed-10/2/191209%2520-%2520OJB%2520-%2520December%25209%252C%25202019.pdf) |

No returned recording has sufficiently corroborated orchestral scope plus sweeping strings and dramatic contrasts to earn good under the original rubric. String-coloured pop/chamber works provide partial aspects. Album-wide performer blobs cannot certify a track’s orchestration.

## 21. Make a 10-song journey from ambient electronic through downtempo to melodic house, gradually increasing the energy.

Returned 0/10; 10 missing.

No returned recordings to grade; all10 requested slots remain missing. This completed run is not a completed playlist.

## 22. Give me a 10-song playlist that starts with acoustic folk, moves through folk rock, and ends with energetic alternative rock.

Returned 0/10; 10 missing.

No returned recordings to grade; all10 requested slots remain missing. This completed run is not a completed playlist.

## 23. Make a 10-track journey from Brian Eno's ambient sound through Boards of Canada to Jon Hopkins, including other artists along the way.

Returned 0/10; 10 missing.

No returned recordings to grade; all10 requested slots remain missing. This completed run is not a completed playlist.

## 24. Give me 10 songs moving from traditional soul through funk into disco. Keep the transitions smooth and avoid placing the same artist back to back.

Returned 0/10; 10 missing.

No returned recordings to grade; all10 requested slots remain missing. This completed run is not a completed playlist.

## 25. Make a 10-track instrumental playlist for focused work, blending modern classical, ambient, and gentle electronic music. Start sparse, build a subtle pulse in the middle, and finish calmly. No vocals.

Returned 0/10; 10 missing.

No returned recordings to grade; all10 requested slots remain missing. This completed run is not a completed playlist.

## 26. Punk rock, 10 songs.

Returned 0/10; 10 missing.

No returned recordings to grade; all10 requested slots remain missing. This completed run is not a completed playlist.

## 27. Give me 10 blues songs.

Returned 0/10; 10 missing.

No returned recordings to grade; all10 requested slots remain missing. This completed run is not a completed playlist.

## 28. Make a 10-song salsa playlist.

Returned 0/10; 10 missing.

No returned recordings to grade; all10 requested slots remain missing. This completed run is not a completed playlist.

## 29. Give me 10 drum and bass tracks.

Returned 0/10; 10 missing.

No returned recordings to grade; all10 requested slots remain missing. This completed run is not a completed playlist.

## 30. Make a 10-song playlist inspired by Björk.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Björk — Aurora | good | The named Björk recording is directly within the requested reference; scoped review describes its detailed arrangement. [Source 1](https://pitchfork.com/reviews/albums/727-vespertine/) |
| 2 | Björk — Venus as a Boy | good | Scoped album/edition evidence corroborates this as a recording by the requested Björk identity. Direct reference music supports the individual-song relationship; no listening or generic artist-genre shortcut is used. [Source 1](https://shop.bjork.com/products/bjork-debut) |
| 3 | Björk — Aeroplane | good | Scoped album/edition evidence corroborates this as a recording by the requested Björk identity. Direct reference music supports the individual-song relationship; no listening or generic artist-genre shortcut is used. [Source 1](https://shop.bjork.com/products/bjork-debut) |
| 4 | Björk — Sorrowful Soil | good | Scoped album/edition evidence corroborates this as a recording by the requested Björk identity. Direct reference music supports the individual-song relationship; no listening or generic artist-genre shortcut is used. [Source 1](https://www.fossora.com/) [Source 2](https://www.fossora.com/sorrowful-soil) |
| 5 | Björk — Atlantic | good | Scoped album/edition evidence corroborates this as a recording by the requested Björk identity. Direct reference music supports the individual-song relationship; no listening or generic artist-genre shortcut is used. [Source 1](https://77island.bjork.info/debutalbum.html) [Source 2](https://musicbrainz.org/release/01141733-ea65-4ca7-87fc-81ae3ca72d59) |
| 6 | Björk — The Anchor Song | good | Scoped album/edition evidence corroborates this as a recording by the requested Björk identity. Direct reference music supports the individual-song relationship; no listening or generic artist-genre shortcut is used. [Source 1](https://shop.bjork.com/products/bjork-debut) |
| 7 | Björk — Komið | good | Scoped album/edition evidence corroborates this as a recording by the requested Björk identity. Direct reference music supports the individual-song relationship; no listening or generic artist-genre shortcut is used. [Source 1](https://77island.bjork.info/medullaalbum.html) [Source 2](https://musicbrainz.org/release/effd4986-0a5f-41fb-afb9-9d7b7b02e577) |
| 8 | Björk — Submarine | good | Scoped album/edition evidence corroborates this as a recording by the requested Björk identity. Direct reference music supports the individual-song relationship; no listening or generic artist-genre shortcut is used. [Source 1](https://bjork.bandcamp.com/album/med-lla) |
| 9 | Björk — Ancestors | good | Scoped album/edition evidence corroborates this as a recording by the requested Björk identity. Direct reference music supports the individual-song relationship; no listening or generic artist-genre shortcut is used. [Source 1](https://bjork.bandcamp.com/album/med-lla) [Source 2](https://bjork.bandcamp.com/track/ancestors) |
| 10 | Björk — Ovule | good | Scoped album/edition evidence corroborates this as a recording by the requested Björk identity. Direct reference music supports the individual-song relationship; no listening or generic artist-genre shortcut is used. [Source 1](https://www.fossora.com/) [Source 2](https://bjork.bandcamp.com/album/fossora) |

All10 occurrences are corroborated recordings by the intended Björk identity; direct reference music supports individual-song fit. All10 slots use one reference artist. The prompt does not expressly require other artists, so this is concentration rather than a proven scope violation. Ovule has a documented updated2023 repress; selected ISRC and declared Fossora title do not independently authenticate that repress. Atlantic and Komið are bonus tracks supported by edition-specific listings.

## 31. Give me 10 songs like 宇多田ヒカル, including other artists.

Returned 0/10; 10 missing.

No returned recordings to grade; all10 requested slots remain missing. This completed run is not a completed playlist.

## 32. Make a 10-song journey from David Bowie to Talking Heads, with related artists bridging the transition.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | David Bowie — Friday on My Mind | good | Official album listing supports the requested opening artist and named recording. [Source 1](https://www.davidbowie.com/pin-ups) |
| 2 | Talking Heads — With Our Love | good | Official tracklist corroborates this destination-artist recording. [Source 1](https://media.rhino.com/press-release/more-songs-about-buildings-and-food-super-deluxe-edition) |
| 3 | David Bowie — Don't Bring Me Down | good | Scoped release evidence corroborates this selected song by David Bowie, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://www.davidbowie.com/pin-ups) |
| 4 | David Bowie — Young Americans | good | Scoped release evidence corroborates this selected song by David Bowie, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://www.davidbowie.com/young-americans) |
| 5 | Talking Heads — Air | good | Scoped release evidence corroborates this selected song by Talking Heads, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://open.spotify.com/embed/album/4OLsnJQPTX0S6lODXw1MqC?theme=0) [Source 2](https://musicbrainz.org/release/288718d4-5737-3e6c-8c6f-ab16c0073684) |
| 6 | Talking Heads — Found a Job | good | Official tracklist corroborates a recording by the requested destination artist. [Source 1](https://media.rhino.com/press-release/more-songs-about-buildings-and-food-super-deluxe-edition) |
| 7 | David Bowie — Somebody Up There Likes Me | good | Official tracklist corroborates Bowie; its placement after Talking Heads is recorded separately as an order concern. [Source 1](https://www.davidbowie.com/young-americans) |
| 8 | David Bowie — Fascination | good | Scoped release evidence corroborates this selected song by David Bowie, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://www.davidbowie.com/young-americans) |
| 9 | Talking Heads — Warning Sign | good | Official tracklist corroborates the requested destination artist. [Source 1](https://media.rhino.com/press-release/more-songs-about-buildings-and-food-super-deluxe-edition) |
| 10 | Talking Heads — The Girls Want to Be With the Girls | good | Official tracklist corroborates the requested final artist and song. [Source 1](https://media.rhino.com/press-release/more-songs-about-buildings-and-food-super-deluxe-edition) |

Correct named start/end artists are present, but all10 slots are David Bowie or Talking Heads: the explicitly requested related bridging artists are absent. Individual direct-reference song grades do not establish playlist fulfillment. Talking Heads occurs at2 before Bowie at3–4; Bowie returns at7–8 after Talking Heads at5–6. The artist order does not demonstrate a gradual one-direction bridge, and subjective musical transitions were not listened to. Both artists occupy5/10 slots; consecutive same-artist pairs occur at3–4,5–6,7–8 and9–10. Original prompt has no explicit adjacent-repeat ban.

## 33. Give me 10 songs that move from Bonobo to Massive Attack, including related discoveries.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Bonobo — Second Sun | good | Label identifies the named Bonobo recording and its arrangement, supporting the starting endpoint. [Source 1](https://www.ninjatune.net/release/bonobo/migration) |
| 2 | Massive Attack — Risingson | good | Scoped release evidence corroborates this selected song by Massive Attack, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://open.spotify.com/album/0NDZWNHJ5ySx3YeFLbsdMe) [Source 2](https://musicbrainz.org/release/f52f5b91-773e-43cc-b15f-f5f53eddaa30) |
| 3 | Massive Attack — Mezzanine | good | Scoped release evidence corroborates this selected song by Massive Attack, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://open.spotify.com/album/0NDZWNHJ5ySx3YeFLbsdMe) [Source 2](https://musicbrainz.org/release/f52f5b91-773e-43cc-b15f-f5f53eddaa30) |
| 4 | Bonobo — Figures | good | Scoped release evidence corroborates this selected song by Bonobo, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://downloads.ninjatune.net/release/bonobo/migration) |
| 5 | Massive Attack — Unfinished Sympathy | good | Scoped release evidence corroborates this selected song by Massive Attack, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://www.universal-music.co.jp/massive-attack/products/uicy-25486/) |
| 6 | Massive Attack — Light My Fire (live) | good | Scoped release evidence corroborates this selected song by Massive Attack, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://www.universal-music.co.jp/massive-attack/products/uicy-25487/) [Source 2](https://www.bravado.de/en/products/massive-attack-protection) [Source 3](https://open.spotify.com/track/1JQLVgeRWU7Hf7y8EpmOqd) |
| 7 | Massive Attack — Sly | good | Scoped release evidence corroborates this selected song by Massive Attack, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://www.universal-music.co.jp/massive-attack/products/uicy-25487/) [Source 2](https://www.bravado.de/en/products/massive-attack-protection) |
| 8 | Massive Attack — Spying Glass | good | Scoped release evidence corroborates this selected song by Massive Attack, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://www.universal-music.co.jp/massive-attack/products/uicy-25487/) [Source 2](https://www.bravado.de/en/products/massive-attack-protection) |
| 9 | Massive Attack — Inertia Creeps | good | Scoped release evidence corroborates this selected song by Massive Attack, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://open.spotify.com/album/0NDZWNHJ5ySx3YeFLbsdMe) [Source 2](https://musicbrainz.org/release/f52f5b91-773e-43cc-b15f-f5f53eddaa30) |
| 10 | Massive Attack — Paradise Circus | good | The named Massive Attack album recording is directly relevant to the requested destination; specific review describes its sparse piano and handclap arrangement. [Source 1](https://www.the-independent.com/arts-entertainment/music/reviews/album-massive-attack-heligoland-virgin-1890452.html) |

Bonobo starts and Massive Attack finishes; the other Bonobo recording is at4, while Massive Attack occurs at2–3 and5–10. The set contains2 Bonobo and8 Massive Attack recordings. No additional artists are returned. Related-discovery coverage and a gradual change between the two reference styles remain unestablished; individual direct-reference matches are not proof of journey quality. Light My Fire is the documented live1994 Protection recording. The three Mezzanine (Deluxe) occurrences correspond to original-song titles/durations rather than explicitly titled Mad Professor mixes, but exact transfers remain unverified.

## 34. Make a 10-song playlist inspired by Joni Mitchell, Nick Drake, and Leonard Cohen.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Nick Drake — Man in a Shed | good | Scoped release evidence corroborates this selected song by Nick Drake, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://nickdrake.com/five_leaves_left.html) |
| 2 | Joni Mitchell — Just Like This Train | good | Scoped release evidence corroborates this selected song by Joni Mitchell, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://jonimitchell.com/music/album.cfm?id=7) |
| 3 | Joni Mitchell — Free Man in Paris | good | Scoped release evidence corroborates this selected song by Joni Mitchell, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://jonimitchell.com/music/album.cfm?id=7) |
| 4 | Joni Mitchell — Same Situation | good | Scoped release evidence corroborates this selected song by Joni Mitchell, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://jonimitchell.com/music/album.cfm?id=7) [Source 2](https://store.jonimitchell.com/en-eu/products/court-and-spark-1lp) |
| 5 | Nick Drake — Hazey Jane I | good | Scoped release evidence corroborates this selected song by Nick Drake, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://nickdrake.com/albums.html) [Source 2](https://music.apple.com/us/song/1704061579) |
| 6 | Nick Drake — Time Has Told Me | good | Scoped release evidence corroborates this selected song by Nick Drake, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://nickdrake.com/five_leaves_left.html) |
| 7 | Joni Mitchell — Don't Interrupt the Sorrow | good | Scoped release evidence corroborates this selected song by Joni Mitchell, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://jonimitchell.com/music/album.cfm?id=9) |
| 8 | Joni Mitchell — Twisted | good | Scoped release evidence corroborates this selected song by Joni Mitchell, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://jonimitchell.com/music/album.cfm?id=7) |
| 9 | Joni Mitchell — Blue Motel Room | good | Scoped release evidence corroborates this selected song by Joni Mitchell, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://jonimitchell.com/music/album.cfm?id=10) |
| 10 | Nick Drake — Saturday Sun | good | Scoped release evidence corroborates this selected song by Nick Drake, one of the expressly named musical references. This establishes individual reference relevance; it does not establish playlist-wide journey, discovery or all-reference coverage. [Source 1](https://nickdrake.com/five_leaves_left.html) |

All10 recordings are directly by named inspirations:4 Nick Drake and6 Joni Mitchell. No Leonard Cohen recording is returned. The rubric does not require each named artist to appear. However, a supported connection reflecting the Leonard Cohen inspiration across this collection has not been established; two direct references do not automatically prove coverage of all three. Individual direct-reference grades do not measure collective balance or subjective musical flow.

## 35. Give me 10 songs blending hip-hop, neo-soul, and jazz.

Returned 0/10; 10 missing.

No returned recordings to grade; all10 requested slots remain missing. This completed run is not a completed playlist.

## 36. Make a 10-track playlist with distorted guitars, pounding drums, and a tense, restless mood.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Sonny Boy Williamson — Bye Bye Bird | partial | The compilation and blues session instrumentation are corroborated, but distortion, drum force and tense/restless mood are not established. [Source 1](https://open.spotify.com/album/5rfQCyWOHTjuM97JnOEIMy) [Source 2](https://www.johnleehooker.se/R%26B-files/05_TheBluesgiants.pdf) |
| 2 | Christina Aguilera — Fighter | partial | Producer testimony supports powerful rock-driven chords and Navarro’s guitar; credits include drums. Exact distortion, pounding character and restless mood remain incompletely documented. [Source 1](https://www.wmagazine.com/culture/christina-aguilera-stripped-album-oral-history) [Source 2](https://www.shazam.com/song/279647282/fighter) |
| 3 | Alanis Morissette — mania-resting in the fire | partial | The recording-specific review describes abrasive riffs and driving drums, unlike the album’s generally meditative context. Restless mood and exact distortion remain only partly established. [Source 1](https://www.swissinfo.ch/ger/statt-rock-nun-om-alanis-morissette-mit-spiritueller-meditation/47673148) |
| 4 | Sonny Boy Williamson — Trying to Get Back on My Feet | partial | The compilation supports recording identity and a blues-band setting. No scoped source establishes the requested force or mood. [Source 1](https://open.spotify.com/album/5rfQCyWOHTjuM97JnOEIMy) [Source 2](https://www.johnleehooker.se/R%26B-files/05_TheBluesgiants.pdf) |
| 5 | Christina Aguilera — Cruz | unknown | The named Stripped song is corroborated; available evidence does not establish the required guitar/drum intensity and mood. [Source 1](https://www.allmusic.com/album/mw0000662221) |
| 6 | Beyoncé — Spirit | mismatch | The song-specific source describes an uplifting gospel-oriented arrangement; this conflicts with the requested tense, restless character. Distorted guitar is not supported. [Source 1](https://time.com/5623569/beyonce-lion-king-spirit/) |
| 7 | Ry Cooder;Ali Farka Touré — Amandrai | partial | The label corroborates this guitar collaboration. No applicable source establishes pounding drums, distortion or restless mood. [Source 1](https://www.youtube.com/watch?v=BkNKiBJPj2c) |
| 8 | Christina Aguilera — Loving Me 4 Me | mismatch | The contemporary review describes a sultry torch ballad, conflicting with the requested tense/restless character; no distorted-guitar basis is documented. [Source 1](https://www.slantmagazine.com/music/christina-aguilera-stripped/) |
| 9 | Christina Aguilera — Beautiful | mismatch | Producer testimony describes the sparse piano/string/guitar arrangement, with no drum part in that account; this conflicts with a pounding-drum centerpiece. [Source 1](https://www.soundonsound.com/people/linda-perry-songwriter-producer) |
| 10 | Christina Aguilera — The Voice Within | mismatch | Producer account and contemporary review identify an inspirational piano ballad, conflicting with the requested tense/restless guitar-and-drums setting. [Source 1](https://www.wmagazine.com/culture/christina-aguilera-stripped-album-oral-history) [Source 2](https://www.slantmagazine.com/music/christina-aguilera-stripped/) |

Several exact recording sources conflict with the requested forceful/tense setting; guitar/drum credits alone were not treated as proof of distortion or intensity. Christina Aguilera occupies5/10 slots, including8–10 consecutively. No explicit adjacent-artist prohibition in original prompt.

## 37. Give me 10 tracks with soft piano, spacious reverberation, and a reflective atmosphere.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Arthur Rubinstein — Sonata for Piano No. 23 in F minor, Op. 57 \\"Appassionata\\": I. Allegro assai | partial | Solo piano is corroborated. The tempo/title alone cannot establish soft dynamics; this exact recording’s reverberation and mood remain unverified. [Source 1](https://www.sonyclassical.com/releases/releases-details/beethoven-sonatas-sony-classical-originals-1) |
| 2 | Jascha Heifetz;Arthur Rubinstein — Sonata for Violin and Piano in A: Recitativo-Fantasia | partial | The piano/violin performance is corroborated. Softness throughout, spacious reverberation and reflective atmosphere of this selected movement/transfer remain unverified. [Source 1](https://www.naxos.com/Handler/GetBackcover.ashx/?file=https%3A%2F%2Fcdn.naxos.com%2FSharedFiles%2Fpdf%2Frear%2F8.110990r.pdf) [Source 2](https://musicwebinternational.com/2025/04/jascha-heifetz-violin-sonatas-naxos/) |
| 3 | André Hodeir — Jazz Cantata IV | unknown | Universal corroborates the exact 2:24 cantata section and ensemble, but does not establish soft piano or the requested production/mood. [Source 1](https://www.universalmusic.it/musica-jazz/album/jazz-et-jazz_40053603326/) |
| 4 | Miles Davis — Tadd's Delight | partial | Official recording credits include Red Garland on piano. They do not establish soft delivery, spacious reverb or reflective mood for this track. [Source 1](https://www.milesdavis.com/albums/round-about-midnight/) [Source 2](https://www.milesdavis.com/person/red-garland/) |
| 5 | Jascha Heifetz;Arthur Rubinstein — Sonata for Violin and Piano in A: Allegro poco mosso | partial | The piano/violin performance is corroborated. Softness throughout, spacious reverberation and reflective atmosphere of this selected movement/transfer remain unverified. [Source 1](https://www.naxos.com/Handler/GetBackcover.ashx/?file=https%3A%2F%2Fcdn.naxos.com%2FSharedFiles%2Fpdf%2Frear%2F8.110990r.pdf) [Source 2](https://musicwebinternational.com/2025/04/jascha-heifetz-violin-sonatas-naxos/) |
| 6 | The Beatles — Something | partial | The official recording account includes piano, but the mixed-band arrangement and selected compilation do not establish piano softness or spacious reverb. [Source 1](https://www.thebeatles.com/something-0) |
| 7 | No Doubt — Sunday Morning | mismatch | The song-specific review describes driving percussion and reggae rhythms, conflicting with the requested soft, reflective piano-centered setting. [Source 1](https://pitchfork.com/reviews/albums/no-doubt-tragic-kingdom/) |
| 8 | Stan Getz — Zigeuner Song | partial | Session discography includes piano alongside tenor saxophone, guitar, rhythm section and chorus. The piano’s softness, prominence and reverberation remain unknown. [Source 1](https://www.jazzdisco.org/verve-records/catalog-8700-series/) |
| 9 | Jascha Heifetz;Arthur Rubinstein — Sonata for Violin and Piano in A: Allegro | partial | The piano/violin performance is corroborated. Softness throughout, spacious reverberation and reflective atmosphere of this selected movement/transfer remain unverified. [Source 1](https://www.naxos.com/Handler/GetBackcover.ashx/?file=https%3A%2F%2Fcdn.naxos.com%2FSharedFiles%2Fpdf%2Frear%2F8.110990r.pdf) [Source 2](https://musicwebinternational.com/2025/04/jascha-heifetz-violin-sonatas-naxos/) |
| 10 | Jascha Heifetz;Arthur Rubinstein — Sonata for Violin and Piano in A: Allegro ben moderato | partial | The piano/violin performance is corroborated. Softness throughout, spacious reverberation and reflective atmosphere of this selected movement/transfer remain unverified. [Source 1](https://www.naxos.com/Handler/GetBackcover.ashx/?file=https%3A%2F%2Fcdn.naxos.com%2FSharedFiles%2Fpdf%2Frear%2F8.110990r.pdf) [Source 2](https://musicwebinternational.com/2025/04/jascha-heifetz-violin-sonatas-naxos/) |

Piano presence is supported for several recordings; no occurrence has sufficient applicable evidence for all three requested facets. Reverb remains unknown throughout. Heifetz/Rubinstein occupies4/10 slots, including9–10 consecutively; Rubinstein also appears at1. This is concentration, not an explicit original-prompt ban.

## 38. Make a 10-song playlist with syncopated bass, lively percussion, and a celebratory dance groove.

Returned 0/10; 10 missing.

No returned recordings to grade; all10 requested slots remain missing. This completed run is not a completed playlist.

## 39. Give me a 10-song journey from blues through soul to funk.

Returned 0/10; 10 missing.

No returned recordings to grade; all10 requested slots remain missing. This completed run is not a completed playlist.

## 40. Make a playlist only by Radiohead, 10 songs.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Radiohead — Everything in Its Right Place | good | Official/label-supplied release identifies this distinct song as a Radiohead recording. [Source 1](https://radiohead.bandcamp.com/album/kid-a) |
| 2 | Radiohead — Idioteque | good | Official/label-supplied release identifies this distinct song as a Radiohead recording. [Source 1](https://radiohead.bandcamp.com/album/kid-a) |
| 3 | Radiohead — We Suck Young Blood. (Your Time Is Up.) | good | Official/label-supplied release identifies this distinct song as a Radiohead recording. [Source 1](https://radiohead.bandcamp.com/track/we-suck-young-blood) |
| 4 | Radiohead — Morning Bell/Amnesiac | good | Official/label-supplied release identifies this distinct song as a Radiohead recording. [Source 1](https://music.apple.com/us/song/1097864836) |
| 5 | Radiohead — In Limbo | good | Official/label-supplied release identifies this distinct song as a Radiohead recording. [Source 1](https://radiohead.bandcamp.com/album/kid-a) |
| 6 | Radiohead — Sulk | good | Official/label-supplied release identifies this distinct song as a Radiohead recording. [Source 1](https://radiohead.bandcamp.com/album/the-bends) |
| 7 | Radiohead — Bones | good | Official/label-supplied release identifies this distinct song as a Radiohead recording. [Source 1](https://radiohead.bandcamp.com/album/the-bends) |
| 8 | Radiohead — Fake Plastic Trees | good | Official/label-supplied release identifies this distinct song as a Radiohead recording. [Source 1](https://radiohead.bandcamp.com/album/the-bends) |
| 9 | Radiohead — High and Dry | good | Official/label-supplied release identifies this distinct song as a Radiohead recording. [Source 1](https://radiohead.bandcamp.com/album/the-bends) |
| 10 | Radiohead — How to Disappear Completely | good | Official/label-supplied release identifies this distinct song as a Radiohead recording. [Source 1](https://radiohead.bandcamp.com/album/kid-a) |

All10 named songs are corroborated as Radiohead recordings, with10 distinct titles and10 distinct declared recording IDs. Single-artist concentration fulfills the explicit artist-only scope; no artist-variety penalty.
