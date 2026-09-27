# Automatic development: every returned song

This is the source audit of the completed `automatic/v2+automatic-fit/v2` development run on 2026-09-27, before the stricter reference-admission repair. All 40 prompts and all 140 returned occurrences were checked against public sources. No audio was listened to and no human listening grades were assigned. The original prompts and rubrics were already familiar; this is not held-out acceptance.

| Result | Count |
| --- | ---: |
| Clear match | 43 |
| Partial match | 47 |
| Mismatch | 25 |
| Unknown | 25 |
| Missing requested slot | 260 |

Clear matches account for 30.7% of returned songs and 10.75% of the 400 requested slots. Partial and unknown judgments do not count as good. These are source-supported fit estimates; exact digital masters and playlist flow were not independently authenticated.

[Machine-readable occurrence evidence](data/automatic-forty-song-audit-2026-09-27.json) retains the literal prompts, rubrics, recording declarations, scoped citations and immutable run hashes. [Runtime receipt](data/automatic-forty-development-2026-09-27.json) records timings and harness findings. The [architecture](automatic-playlists.md) describes the resulting changes and remaining release gates.

The run used one warmed application and a reused isolated store. Generation p95 was 25.003 seconds after setup; this does not establish the separate cold/warm latency gates. The harness completed all cases but exited nonzero. Artist-spacing findings were soft quality observations in those particular prompts.

## Findings that changed the implementation

- Artist-inspired requests admitted unrelated music on embedding similarity alone. The subsequent policy requires an exact requested identity, or prepared artist relationship and compatible audio-neighbor evidence tied to the same explicit reference.
- One interpretation timeout lost the requested count and journey genres. Timeout finalization now preserves the parsed result or uses the existing local source rules without starting another model or provider call.
- Broad musical requests lacked corroborating features. Catalog and encoder incompatibility remains a data preparation blocker; the app continues to omit unsupported matches.
- Some declared catalog identities have unresolved version or duration anomalies. In particular, the declared P!nk “Sober” duration is about 52 minutes; the Genesis repeat family and an Ayumi orchestral variant need exact-recording review. Distinct IDs are not proof of distinct takes.

The audit below is immutable evidence about the earlier run. It is not a musical-quality grade for a later repaired build.

## 1. Classical, 10 tracks.

Returned 0/10; 10 missing.

No recordings returned. Ten requested slots remain missing; there are no song-level suitability grades or inferred listening judgments.

## 2. Electronic music. Make a 10-song playlist.

Returned 0/10; 10 missing.

No recordings returned. Ten requested slots remain missing; there are no song-level suitability grades or inferred listening judgments.

## 3. Give me 10 jazz songs.

Returned 0/10; 10 missing.

No recordings returned. Ten requested slots remain missing; there are no song-level suitability grades or inferred listening judgments.

## 4. Make a 10-song reggae playlist.

Returned 0/10; 10 missing.

No recordings returned. Ten requested slots remain missing; there are no song-level suitability grades or inferred listening judgments.

## 5. Give me 10 heavy metal songs.

Returned 0/10; 10 missing.

No recordings returned. Ten requested slots remain missing; there are no song-level suitability grades or inferred listening judgments.

## 6. Make a 10-song playlist inspired by Daft Punk.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Daft Punk — Phoenix | good | Direct Daft Punk Homework recording; the official tracklist and matching album/duration support the requested reference. Exact local master/ISRC was not externally authenticated. [Source 1](https://www.daftpunk.com/homework/) [Source 2](https://music.apple.com/us/album/homework/696884422) |
| 2 | Elton John — Warm Love in a Cold World | good | Official discography places this exact song on the Pete Bellotte-produced disco album Victim of Love. That documented disco/Moroder production lineage is a credible connection to the requested Daft Punk palette; master remains unverified. [Source 1](https://www.eltonjohn.com/discography/victim-of-love) [Source 2](https://store.eltonjohn.com/collections/studio-albums/products/victim-of-love-cd) |
| 3 | Wee Papa Girl Rappers — Wee Rule (Drummie Zap mix) | partial | The exact compilation title says Drummie Zap while original releases identify Drummie Zeb. The evidenced dancehall/dub/pop-rap version has dance-production adjacency but no strong Daft Punk-style house/disco connection; exact mix crosswalk remains uncertain. [Source 1](https://musicbrainz.org/release/8820a5d0-f2ce-4d5d-af53-1b8087206d1e) [Source 2](https://rectangletriangle.com/products/wee-rule-wee-papa-girl-rappers) [Source 3](https://imap.oye-records.com/releases/the-wee-papa-girl-rappers-wee-rule) |
| 4 | MHD — Fiesta | mismatch | The exact Mansa song features Naira Marley and belongs to MHD's Afro Trap/hip-hop release. Available recording/release evidence does not support the requested Daft Punk house/disco direction; the displayed co-credit is incomplete. [Source 1](https://music.apple.com/us/song/1573282522) [Source 2](https://music.apple.com/us/album/mansa/1573282168) |
| 5 | Raekwon;Ghostface Killah;Blue Raspberry — Rainy Dayz | mismatch | The identified Raekwon/RZA rap recording with Ghostface Killah and Blue Raspberry is grounded in the Cuban Linx hip-hop release, with no evidenced house/disco or Daft Punk-specific relation. Electronic production alone is insufficient. [Source 1](https://music.apple.com/us/song/258635827) [Source 2](https://music.apple.com/us/album/only-built-4-cuban-linx/258634938) |
| 6 | Daft Punk — Da Funk | good | Direct Daft Punk Homework recording; official artist tracklist and 5:28 publisher duration match the declared album version. Exact master/ISRC unverified. [Source 1](https://www.daftpunk.com/homework/) [Source 2](https://music.apple.com/us/album/homework/696884422) |
| 7 | Whethan — Sunshine | partial | Publisher identifies Sunshine with The Knocks on Fantasy, and the artist's release page establishes electronic production. A stronger house/disco relation to Daft Punk was not corroborated; featured artist is omitted from display. [Source 1](https://music.apple.com/us/song/1535704635) [Source 2](https://soundcloud.com/whethan/sets/fantasy) |
| 8 | DEVO — Soft Things | partial | Rhino confirms the exact New Traditionalists track and synth-pop/electronic release context. This gives a plausible electronic-pop adjacency, but not recording-specific Daft Punk-style house/disco affinity. [Source 1](https://www.rhino.com/product/new-traditionalists-grey-vinyl) [Source 2](https://www.rhino.com/aod/new-traditionalists-deluxe-remastered-edition-devo) |
| 9 | Nightcrawlers — Push the Feeling On | partial | The well-established MK house versions are relevant to Daft Punk's house direction, but the selected 3:29 compilation row omits its mix name and exact MBID page was unavailable. A remix-specific identity mapping was not established. [Source 1](https://www.youtube.com/watch?v=nx30Bnvf9CY) [Source 2](https://www.officialcharts.com/songs/nightcrawlers-push-the-feeling-on/) [Source 3](https://lighthouserecords.jp/?pid=164928470) |
| 10 | Daft Punk — Funk Ad | good | Official Homework track and publisher duration identify this direct Daft Punk selection. It is only 51 seconds, so it supplies little full-song listening time despite fitting the reference; no duration minimum was requested. [Source 1](https://www.daftpunk.com/homework/) [Source 2](https://music.apple.com/us/album/homework/696884422) |

The direct Daft Punk recordings occupy three slots, including a 51-second album outro. Other performers provide variety, but the two documented rap mismatches weaken the requested direction.

## 7. Give me 10 songs similar to Radiohead, including other artists.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Travis — Turn | good | Official Travis tracklist identifies Turn on The Man Who; contemporaneous CMJ reporting directly compares that album's melodic guitar sound with Radiohead. This supports an early-Radiohead connection rather than all eras of the reference. [Source 1](https://travisonline.com/the-man-who/) [Source 2](https://www.worldradiohistory.com/Archive-All-Music/CMJ/New-Music/CMJ-New-Music-2000-04.pdf) |
| 2 | Yes — Life on a Film Set | partial | The official Yes release confirms the Return Trip rerecording with Trevor Horn vocals and guitar/keyboard arrangement. Progressive-rock adjacency is plausible, but no specific Radiohead affinity was corroborated. [Source 1](https://app.yesworld.com/discography/fly-return-trip/) [Source 2](https://eurostore.yesworld.com/products/yes-fly-from-here-return-trip-blu-ray-edition) |
| 3 | The Rembrandts — I'll Come Callin' | partial | Rhino establishes the exact track on a melodic pop-rock release. Generic rock/pop similarity offers only weak support for the requested Radiohead connection; no stronger recording-level relationship was found. [Source 1](https://www.rhino.com/product/untitled) [Source 2](https://www.rhino.com/article/happy-25th-the-rembrandts-untitled) |
| 4 | Colbie Caillat;Brad Paisley — Merry Christmas Baby | mismatch | Publisher describes this exact Brad Paisley duet as a country-styled Christmas remake, a different musical direction from the requested Radiohead alternative/art-rock affinity. [Source 1](https://music.apple.com/us/album/christmas-in-the-sand/1445888481) [Source 2](https://music.apple.com/de/song/1442710139) |
| 5 | Barry Manilow;Sarah Vaughan — Blue | mismatch | Manilow's own recording documentary identifies Blue with Sarah Vaughan as part of his jazz-album project. This lounge/jazz duet does not establish the requested Radiohead relationship. [Source 1](https://www.youtube.com/watch?v=MKz6r9eFd74) [Source 2](https://barrymanilow.com/content/bio.html) |
| 6 | Ella Fitzgerald;Frank De Vol — What Will I Tell My Heart | mismatch | Universal's exact tracklist and publisher jazz classification establish the Fitzgerald/DeVol standards recording; that is not the requested Radiohead-related alternative/art-rock direction. [Source 1](https://www.universal-music.co.jp/ella-fitzgerald/products/uccv-9636/) [Source 2](https://music.apple.com/us/album/like-someone-in-love/1444378784) |
| 7 | George Benson — You Don't Know What Love Is | mismatch | The exact Tenderly recording is documented as a jazz-ensemble standard with Benson, Tyner and Carter. No recording-level Radiohead relation is supported. [Source 1](https://music.apple.com/us/song/281168939) [Source 2](https://ci.nii.ac.jp/ncid/BA9014107X) |
| 8 | Yes — Life on a Film Set (instrumental) | partial | Official Return Trip edition documents instrumental mixes, supporting version plausibility. Progressive-rock adjacency is broad and the exact instrumental master/MBID was not corroborated; this also repeats the preceding Yes composition. [Source 1](https://eurostore.yesworld.com/products/yes-fly-from-here-return-trip-blu-ray-edition) |
| 9 | Foo Fighters — Virginia Moon | mismatch | Grohl explicitly describes Virginia Moon as bossa nova in an interview. The acoustic Norah Jones duet is an atypical Foo Fighters track and its band identity does not supply Radiohead-style fit. [Source 1](https://www.guitarworld.com/acoustic-nation/acoustic-nation-archive-dave-grohl-how-other-half-lives) [Source 2](https://www.grammy.com/news/foo-fighters-road-to-but-here-we-are-videos-dave-grohl-taylor-hawkins/) |
| 10 | Aerosmith — Avant Garden | unknown | Official Aerosmith and Universal sources identify the album track but do not establish a recording-level Radiohead relationship. The selected 6:35 duration may include album-tail material; precise boundaries remain unverified. [Source 1](https://store.aerosmith.com/products/aerosmith-just-push-play-cd) [Source 2](https://www.universal-music.co.jp/aerosmith/products/uicy-16439/) |

Other artists are included as requested. Two versions of one Yes composition reduce variety; documented standards and Christmas-country material do not establish Radiohead affinity. Exact recording duplication was not established.

## 8. Make a 10-song playlist around Nina Simone and Bill Withers.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Bill Withers — Look to Each Other for Love | good | Withers' official album page names this recording and describes its characteristic mellow songwriting. A direct requested-artist recording is an appropriate match. [Source 1](https://billwithers.com/discography/albums/bout-love/) |
| 2 | Bill Withers — Don't It Make It Better | good | Official Withers commentary specifically identifies this horn-led R&B single, grounding both recording identity and the requested soul/funk reference. [Source 1](https://billwithers.com/discography/albums/bout-love/) [Source 2](https://billwithers.com/biography/) |
| 3 | Fleetwood Mac — Don’t Stop | partial | Rhino confirms the Rumours album recording, but its pop-rock context provides only broad rhythmic/songwriting adjacency to Simone/Withers. The retrieved source does not establish a closer soul/jazz connection. [Source 1](https://images.rhino.com/press-release/rumours) |
| 4 | Nina Simone — Suzanne | good | Nina Simone's official studio discography places Suzanne on To Love Somebody and supplies personnel. The declared compilation is consistent with that studio version; a direct requested-artist selection fits despite unverified master details. [Source 1](https://www.ninasimone.com/studio-albums/) |
| 5 | Bill Withers — Wintertime | good | Publisher identifies the Menagerie recording and the official Withers discography confirms that album. Direct reference-artist material fits the requested Withers songwriting direction. [Source 1](https://music.apple.com/us/song/989377781) [Source 2](https://billwithers.com/discography/albums/menagerie/) |
| 6 | Bill Withers — Watching You Watching Me | good | Official album commentary specifically endorses the title recording's characteristic mellow Withers sound; appropriate direct reference-artist selection. [Source 1](https://billwithers.com/discography/albums/watching-you-watching-me/) [Source 2](https://music.apple.com/us/album/the-best-of-bill-withers-lean-on-me/391725408) |
| 7 | Alan Walker — Springseeker | mismatch | Official artist upload and publisher identify a narrated cinematic/electronic World of Walker introduction. That recording has no corroborated Nina Simone/Bill Withers soul, jazz or songwriting connection. [Source 1](https://www.youtube.com/watch?v=12il7_Z8VQI) [Source 2](https://music.apple.com/us/album/world-of-walker-season-one-rise-of-the-drones/6796067932) |
| 8 | Nina Simone — Com' By H'Yere - Good Lord | good | Official Simone live-session discography identifies Com' By H'Yere on It Is Finished and documents the personnel/release; direct requested-artist material fits. Exact compilation mastering unverified. [Source 1](https://www.ninasimone.com/live-sessions/) [Source 2](https://www.ninasimone.com/albums/it-is-finished/) |
| 9 | Bill Withers — Dedicated to You My Love | good | Official Withers album commentary names Dedicated to You My Love and its mellow approach, grounding the direct-reference fit. [Source 1](https://billwithers.com/discography/albums/bout-love/) |
| 10 | Bill Withers — Heart in Your Life | good | Publisher identifies this Watching You Watching Me recording; Withers' official album context supports direct-reference fit. Exact catalog ISRC/master is not independently authenticated. [Source 1](https://music.apple.com/ca/song/290620329) [Source 2](https://billwithers.com/discography/albums/watching-you-watching-me/) |

Both requested artists are represented: six Bill Withers and two Nina Simone selections. This establishes reference coverage but strongly concentrates the playlist; Fleetwood Mac is only adjacent and the Alan Walker narrated selection is a mismatch.

## 9. Give me 10 tracks inspired by Kraftwerk, Tangerine Dream, and Brian Eno.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Tangerine Dream — Phaedra | good | Direct Tangerine Dream reference: the official Phaedra page describes the long title recording with Moog, Mellotron and sequenced electronics. The declared 17:46 album recording fits this direction; the exact digital master is not authenticated. [Source 1](https://www.tangerinedreammusic.com/en/music/detail.asp?id=12&tit=Phaedra) |
| 2 | Bessie Smith — Reckless Blues | mismatch | The Smithsonian identifies Reckless Blues as Bessie Smith’s 1925 Columbia blues recording. This documented acoustic vocal/cornet/organ performance does not supply the requested electronic/ambient/synth reference direction. [Source 1](https://americanhistory.si.edu/collections/object/nmah_1055146) [Source 2](https://musicbrainz.org/release/575ca2e7-4637-4750-aed4-a9806b37680f) |
| 3 | Kraftwerk — Régéneration | good | Direct Kraftwerk selection from Tour de France supports the electronic reference. This is the short 1:17 Régéneration segment, not a separate full-length remix or the later 3-D live performance; exact remaster remains unverified. [Source 1](https://music.apple.com/us/album/tour-de-france-remastered/726341054) |
| 4 | Brian Eno — Pierre in Mist | good | Publisher identifies Pierre in Mist on Brian Eno’s Nerve Net. It is direct material by a requested reference in his electronic catalogue; this supports reference inclusion, without claiming that every Eno recording is ambient or that this exact master was verified. [Source 1](https://music.apple.com/be/song/684277495) [Source 2](https://classical.music.apple.com/us/album/684277489) |
| 5 | Sonic Youth — Mary-Christ | mismatch | Publisher identifies the Goo studio song and describes its guitar-led alternative-rock release. Its noise-rock setting is a poor match for the requested electronic/ambient/synth reference blend; no recording-specific connection establishing that blend was found. [Source 1](https://music.apple.com/us/song/1440839020) [Source 2](https://music.apple.com/us/album/goo/1440838993) |
| 6 | Air — So Light Is Her Footfall | good | Official Air material and the Love 2 tracklist identify the 3:13 album recording. Contemporary criticism describes its psychedelic atmosphere within a vintage-electronic, ambient-pop release: a credible connection to the requested electronic reference palette. Each track need not resemble all three reference artists. Exact local master remains unverified. [Source 1](https://www.youtube.com/watch?v=AQM6TC2pkUU) [Source 2](https://open.spotify.com/album/2HczC60tm0oe5c7ZMeoJSc) [Source 3](https://www.allmusic.com/album/love-2-mw0000827798) |
| 7 | Blur — Mr. Robinsons' Quango | mismatch | Publisher places the named track among The Great Escape’s British pop/rock character songs. That specific release context points away from the requested synthesizer/ambient direction; broad artist eclecticism does not establish this recording’s fit. [Source 1](https://music.apple.com/us/album/the-great-escape/699808759) |
| 8 | New Order — Mr. Disco | partial | Mr. Disco belongs to Technique, whose label describes acid-house/electronic dance influences. This supports electronic adjacency, but the label discussion is album-wide and does not establish this song’s precise Kraftwerk/Tangerine Dream/Eno relationship. [Source 1](https://www.rhino.com/aod/technique-new-order) |
| 9 | Sonic Youth — Karen Koltrane | partial | The publisher confirms the A Thousand Leaves album version and an expansive, experimental guitar setting. That offers an exploratory connection, but the exact electronic/ambient reference resemblance remains unverified. [Source 1](https://music.apple.com/us/album/a-thousand-leaves/1443232704) [Source 2](https://store.universal-music.co.jp/products/4743104) |
| 10 | The Clash — Cheapskates | mismatch | Official Clash discography and publisher identify Cheapskates on the punk album Give ’Em Enough Rope. This is a poor fit for the electronic/ambient reference blend, despite both traditions allowing experimentation. [Source 1](https://www.theclash.com/discography/give-em-enough-rope/) [Source 2](https://music.apple.com/us/album/give-em-enough-rope/684738276) |

All three named reference artists are represented directly. Several guitar-rock/blues selections diverge from the electronic/ambient direction; the long Phaedra title track and short Régéneration segment create a substantial duration imbalance, without violating any explicit duration rule.

## 10. Make a 10-song playlist inspired by Fela Kuti and Tony Allen, with a variety of artists.

Returned 0/10; 10 missing.

No recordings returned. Ten requested slots remain missing; there are no song-level suitability grades or inferred listening judgments.

## 11. Give me 10 songs like 'Teardrop' by Massive Attack.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Massive Attack — Teardrop - Remastered 2019 | good | This is the requested recording itself, explicitly labeled 2019 remaster. Licensed UMG catalogue evidence identifies that remaster family, while contemporary criticism describes Teardrop’s atmospheric, sparse texture. Strong musical fit, although repeating the seed offers no discovery value; exact local master linkage is not independently authenticated. [Source 1](https://smartsolutions.com.al/wp-content/uploads/2023/10/Catalogue_UMG_AL-and-XK_Universal-Music-Group_Smart-Solutions-Shpk-Part-4.pdf) [Source 2](https://www.worldradiohistory.com/Archive-All-Music/Musician/1990/1998/Musician-1998-07.pdf) |
| 2 | Underworld — Nylon Strung | partial | Nylon Strung is a documented Underworld electronic album track, with contemporary review describing a rhapsodic vocal/melodic build. It offers atmospheric electronic adjacency, but its dance orientation does not establish the downtempo trip-hop feel of Teardrop. [Source 1](https://www.underworldlive.com/collections/all) [Source 2](https://pitchfork.com/reviews/albums/21509-barbara-barbara-we-face-a-shining-future/) |
| 3 | Coldplay — A Head Full of Dreams | mismatch | The artist confirms the title recording; contemporary Billboard and video coverage describe the bright pop-oriented, celebratory release. That is a poor match for the reference’s intimate, atmospheric trip-hop character. [Source 1](https://www.coldplay.com/song/a-head-full-of-dreams/) [Source 2](https://www.worldradiohistory.com/Archive-All-Music/Billboard/00s/2015/BB-2015-42-12-12-Issue-37.pdf) |
| 4 | Adele — Sweetest Devotion | mismatch | The official XL audio credit identifies the 25 album recording, and contemporary track-specific criticism describes a country-tinged pop closer. Vocal warmth alone does not make it a good match for Teardrop’s trip-hop production. [Source 1](https://www.youtube.com/watch?v=TyAoW20UKGU) [Source 2](https://www.musictimes.com/articles/55658/20151123/adele-25-album-review-hello.htm) |
| 5 | The Internet — Higher Times (feat. Jesse Boykins III) | partial | Publisher identifies the Jesse Boykins III feature on Feel Good and describes the release’s hazy, jazzy alternative soul. This is credible adjacent atmosphere, but album context is not proof of exact Teardrop similarity. The 10:16 album closer is a materially longer selection. [Source 1](https://music.apple.com/us/song/704790815) [Source 2](https://music.apple.com/us/album/feel-good/704790179) [Source 3](https://open.spotify.com/album/4Bpt4fHYxxgqR2GjrxyR6D) |
| 6 | The Invisible — London Girl | partial | Contemporary criticism of the original London Girl documents tense atmospheric production, synth effects and funk bass, alongside an explicit disco lineage. This supports a textured adjacent choice, but does not establish Teardrop-like downtempo trip-hop. A later LDN GRL Raw Version is distinct and was not used as evidence for the original master. [Source 1](https://pitchfork.com/reviews/tracks/11175-london-girl/) [Source 2](https://ninjatune.net/release/the-invisible/wings) |
| 7 | Massive Attack — Unfinished Sympathy - 2012 Mix/Master | good | Licensed catalogue identifies the 2012 mix/master of Massive Attack’s Unfinished Sympathy; contemporary criticism links its textured production with the group’s atmospheric work. This is a well-grounded adjacent trip-hop choice, while the exact local digital master remains unverified. [Source 1](https://smartsolutions.com.al/wp-content/uploads/2023/10/Catalogue_UMG_AL-and-XK_Universal-Music-Group_Smart-Solutions-Shpk-Part-4.pdf) [Source 2](https://www.worldradiohistory.com/Archive-All-Music/Musician/1990/1998/Musician-1998-07.pdf) |
| 8 | Alaska Y Dinarama — Hacia el abismo | unknown | Publisher verifies Hacia El Abismo on the 1987 Diez release, but provides only broad pop classification. The accessed sources do not establish this particular recording’s trip-hop or atmospheric resemblance; title/artist identity alone cannot grade it a good match. [Source 1](https://music.apple.com/es/song/726402255) [Source 2](https://music.apple.com/us/album/diez-remasters/726402013) |
| 9 | Pet Shop Boys — Violence - Haçienda Version; 2018 Remaster | partial | The artist explicitly distinguishes the Haçienda version; its published credits show programmed rhythm, keyboards and vocals, providing electronic adjacency. This does not establish Teardrop-like atmosphere, and the returned 2018 remaster is not independently linked to a master receipt. [Source 1](https://www.petshopboys.co.uk/lyrics/violence-hacienda-version) [Source 2](https://musicbrainz.org/release/8ceabc55-9cc4-3232-aa66-974924f388e2) |
| 10 | Manila Killa — Mean It | partial | Counter’s catalogue and publisher identify Mean It as Manila Killa with San Holo and Nick Lopez, collaborators omitted from the display artist. It is an adjacent electronic release, but precise downtempo/trip-hop resemblance is not established. [Source 1](https://www.ninjatune.com/releases/counter) [Source 2](https://music.apple.com/ro/song/1591605524) |

The seed itself is repeated, which fits musically but adds no discovery value. The additional Massive Attack recording is a good related choice; several adjacent electronic/soul tracks remain partial and bright pop/country material conflicts with the requested feel.

## 12. Make a 10-song playlist inspired by 'So What' by Miles Davis and 'Take Five' by the Dave Brubeck Quartet.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Makaya McCraven — A Queen's Intro | partial | The label identifies a short 33-second introduction on Universal Beings, a jazz/improvised beat-music project. That gives jazz context but little independent evidence of a developed modal/cool-jazz performance comparable to the two reference recordings. No minimum duration was imposed; brevity is a quality limitation. [Source 1](https://intlanthem.bandcamp.com/track/a-queens-intro) [Source 2](https://intlanthem.bandcamp.com/album/universal-beings) [Source 3](https://daily.bandcamp.com/album-of-the-day/makaya-mccraven-universal-beings-ef-sides-review) |
| 2 | Lee Morgan — Cunning Lee | good | Blue Note describes Cunning Lee’s post-bop lines and documents the trumpet/saxophone/piano/bass/drums quintet on Caramba. This is a grounded acoustic small-group jazz connection to the reference pair, though it is not claimed to share Take Five’s meter or So What’s exact harmonic form. [Source 1](https://store.bluenote.com/collections/all-1/products/lee-morgan-caramba-uhq-cd) |
| 3 | Miles Davis — Autumn Leaves - Live | partial | The exact Spotify identifier resolves to Miles Davis’s live Autumn Leaves. The artist’s discography lists multiple live recordings, and the title lacks date/location; acoustic-jazz adjacency is clear but the particular performance/version cannot be disambiguated from the saved display fields alone. [Source 1](https://open.spotify.com/track/6TIKuCQTAWmB2nQsB7BOtw) [Source 2](https://www.milesdavis.com/music/songs/) [Source 3](https://www.milesdavis.com/albums/miles-in-tokyo/) |
| 4 | Marcus Miller — Teen Town | partial | Publisher identifies Marcus Miller’s Teen Town on The Sun Don’t Lie. Its electric-bass/funk-jazz context provides jazz adjacency, but not the acoustic modal/cool-jazz character of the two references. The exact local master is unverified. [Source 1](https://music.apple.com/gb/song/1713838290) [Source 2](https://www.worldradiohistory.com/Archive-All-Music/Music-Connection/90/1994/Music-Connection-1994-05-25.pdf) |
| 5 | Brian Bromberg — Sanford and Son Theme | partial | Bromberg’s own release description specifically identifies his Sanford and Son cover within an electric-bass, horn-led funk/jazz project. This is related instrumental jazz, with a significantly different funky setting from the reference pair. [Source 1](https://www.brianbromberg.net/the-world-of-brian-bromberg) [Source 2](https://music.apple.com/us/song/322859908) |
| 6 | Les Fils du Calvaire — Rester avec toi | mismatch | Exact Spotify ID and official artist video identify a vocal electronic-pop collaboration with Miss Kittin, also documented by Circus Company. This is not a convincing modal/cool-jazz recommendation. The featured singer is omitted from the displayed artist/title. [Source 1](https://open.spotify.com/intl-fr/track/6iTjFPwAkD3NrIwtDFJNLY) [Source 2](https://www.youtube.com/watch?v=cV0bGr8HHmE) [Source 3](https://circuscompany.fr/releases/dop-email-from-a-beetle/) |
| 7 | Traditional — Twilight Mood - 1999 Remastered Version | mismatch | Exact Spotify ID credits Traditional and Yehudi Menuhin; publisher and release sources identify Twilight Mood as the Shankar/Menuhin Indian-classical collaboration with sitar, violin and tabla. Improvisation alone does not make it the requested jazz style. Displaying only Traditional loses the performers; 1999 remaster lineage remains a separate caveat. [Source 1](https://open.spotify.com/track/5ot3fRuNnZlOg37kCPBEgp) [Source 2](https://classical.music.apple.com/gb/album/1501888065) [Source 3](https://www.allmusic.com/album/west-meets-east-the-historic-shankar-menuhin-sessions-mw0000668223) |
| 8 | B.B. King — Lucille | mismatch | Exact Spotify ID identifies B.B. King’s Lucille; publisher classifies the release as blues and recording-specific review describes the spoken guitar story. It is blues rather than a strong modal/cool-jazz counterpart, notwithstanding jazz’s relationship to blues. [Source 1](https://open.spotify.com/track/4ZSJs1cqeincEi2KjUGmZC) [Source 2](https://music.apple.com/us/album/lucille/1440766615) [Source 3](https://www.allmusic.com/album/lucille-mw0000647122) |
| 9 | The Dave Brubeck Quartet — Blue Rondo à la Turk | good | The artist estate and Library of Congress identify Blue Rondo à la Turk on the same Time Out album as Take Five and document its unusual meter and improvised jazz setting. A strong direct stylistic connection; the precise digital master is not authenticated. [Source 1](https://www.davebrubeck.com/dave-brubecks-time-out) [Source 2](https://www.loc.gov/static/programs/national-recording-preservation-board/documents/TimeOut.pdf) |
| 10 | Miles Davis — Blue in Green (feat. John Coltrane & Bill Evans) | good | Exact Spotify identity and Miles Davis’s official track page identify Blue in Green from Kind of Blue, with Coltrane and Evans. This supplies a strong same-session/ensemble connection to So What, without claiming identical form or tempo. [Source 1](https://open.spotify.com/track/0aWMVrwxPNYkKmFthzmpRi) [Source 2](https://www.milesdavis.com/track/blue-in-green-feat-john-coltrane-bill-evans/) [Source 3](https://www.milesdavis.com/albums/kind-of-blue/) |

Blue in Green and Blue Rondo à la Turk directly cover both reference albums. Electric funk-jazz choices are broader adjacencies; electronic pop, Indian classical and the blues narration do not establish the requested modal/cool-jazz blend.

## 13. Give me 10 tracks mixing house, techno, and UK garage.

Returned 0/10; 10 missing.

No recordings returned. Ten requested slots remain missing; there are no song-level suitability grades or inferred listening judgments.

## 14. Make a 10-song playlist combining bossa nova, samba, and Brazilian jazz.

Returned 0/10; 10 missing.

No recordings returned. Ten requested slots remain missing; there are no song-level suitability grades or inferred listening judgments.

## 15. Give me 10 songs spanning bluegrass, country, and Americana.

Returned 0/10; 10 missing.

No recordings returned. Ten requested slots remain missing; there are no song-level suitability grades or inferred listening judgments.

## 16. Make a 10-song playlist mixing Japanese city pop, funk, and disco.

Returned 0/10; 10 missing.

No recordings returned. Ten requested slots remain missing; there are no song-level suitability grades or inferred listening judgments.

## 17. Give me 10 tracks of spacious electronic music with detailed textures, a deep groove, and occasional sparkle—relaxing but not sleepy.

Returned 0/10; 10 missing.

No recordings returned. Ten requested slots remain missing; there are no song-level suitability grades or inferred listening judgments.

## 18. Make a 10-song playlist with warm acoustic instruments, gentle rhythms, and an intimate late-night feel.

Returned 0/10; 10 missing.

No recordings returned. Ten requested slots remain missing; there are no song-level suitability grades or inferred listening judgments.

## 19. Give me 10 songs with jangly guitars, melodic bass lines, and bright indie-pop energy.

Returned 0/10; 10 missing.

No recordings returned. Ten requested slots remain missing; there are no song-level suitability grades or inferred listening judgments.

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

No returned recordings; all ten requested slots remain missing. Musical fulfillment cannot be established.

## 22. Give me a 10-song playlist that starts with acoustic folk, moves through folk rock, and ends with energetic alternative rock.

Returned 0/10; 10 missing.

No returned recordings; all ten requested slots remain missing. Musical fulfillment cannot be established.

## 23. Make a 10-track journey from Brian Eno's ambient sound through Boards of Canada to Jon Hopkins, including other artists along the way.

Returned 0/10; 10 missing.

No returned recordings; all ten requested slots remain missing. Musical fulfillment cannot be established.

## 24. Give me 10 songs moving from traditional soul through funk into disco. Keep the transitions smooth and avoid placing the same artist back to back.

Returned 0/10; 10 missing.

No returned recordings; all ten requested slots remain missing. Musical fulfillment cannot be established.

## 25. Make a 10-track instrumental playlist for focused work, blending modern classical, ambient, and gentle electronic music. Start sparse, build a subtle pulse in the middle, and finish calmly. No vocals.

Returned 0/10; 10 missing.

No returned recordings; all ten requested slots remain missing. Musical fulfillment cannot be established.

## 26. Punk rock, 10 songs.

Returned 0/10; 10 missing.

No returned recordings; all ten requested slots remain missing. Musical fulfillment cannot be established.

## 27. Give me 10 blues songs.

Returned 0/10; 10 missing.

No returned recordings; all ten requested slots remain missing. Musical fulfillment cannot be established.

## 28. Make a 10-song salsa playlist.

Returned 0/10; 10 missing.

No returned recordings; all ten requested slots remain missing. Musical fulfillment cannot be established.

## 29. Give me 10 drum and bass tracks.

Returned 0/10; 10 missing.

No returned recordings; all ten requested slots remain missing. Musical fulfillment cannot be established.

## 30. Make a 10-song playlist inspired by Björk.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Björk — Aurora | good | The named Björk recording is directly within the requested reference; scoped review describes its detailed arrangement. [Source 1](https://pitchfork.com/reviews/albums/727-vespertine/) |
| 2 | Genesis — Dancing With the Moonlit Knight | unknown | The progressive-rock song is identifiable, but no meaningful recording-specific Björk relationship was established. Remix identity also needs clarification. [Source 1](https://www.sputnikmusic.com/review/49482/Genesis-Selling-England-by-the-Pound/) |
| 3 | George Ezra — The Beautiful Dream | unknown | A review identifies electronic pads in this particular closer. That isolated feature does not establish a meaningful Björk relationship. [Source 1](https://musicmattersmedia.com/2018/12/28/george-ezra-staying-at-tamaras/) |
| 4 | Natalie Cole with the London Symphony Orchestra — Hark! the Herald Angels Sing | unknown | The orchestral Christmas recording is corroborated; shared orchestration alone does not establish the requested reference relationship. [Source 1](https://music.apple.com/us/song/1576791747) |
| 5 | Mitski — Because Dreaming Costs Money, My Dear | unknown | The artist’s own release corroborates this song. No applicable source established its relationship to Björk’s music. [Source 1](https://mitski.bandcamp.com/track/because-dreaming-costs-money-my-dear) |
| 6 | Ayumi Hamasaki — ever free | unknown | The declared album is an acoustic-orchestra reworking, not necessarily the original single. Orchestration alone cannot establish Björk-inspired fit. [Source 1](https://avex.jp/ayu/discography/detail.php?id=1003333) |
| 7 | Maria Callas;Philharmonia Orchestra;Tullio Serafin — Madama Butterfly: Act II. \\"Un bel dì vedremo\\" | unknown | The named operatic aria is corroborated, but its dramatic voice/orchestra does not itself prove a meaningful Björk relationship. [Source 1](https://www.warnerclassics.com/es/release/madama-butterfly-un-bel-di-vedremo) |
| 8 | Jefferson Airplane — Common Market Madrigal | unknown | A contemporary review identifies this 1989 song as poetic renaissance rock. It does not establish a Björk relationship. [Source 1](https://www.deseret.com/1989/10/14/18828039/jefferson-airplane-rides-out-the-turbulence-and-flies-high-again-in-new-album/) |
| 9 | Genesis — Dancing With the Moonlit Knight | unknown | The same named composition appears again under a different declared recording ID. Neither its Björk relationship nor exact mix distinction was established. [Source 1](https://www.sputnikmusic.com/review/49482/Genesis-Selling-England-by-the-Pound/) |
| 10 | The Smiths — I Know It's Over | unknown | The review corroborates the song’s emotional writing; that feature alone is insufficient to establish Björk-inspired fit. [Source 1](https://pitchfork.com/reviews/albums/the-smiths-the-queen-is-dead/) |

Björk reference identity is preserved at position1. Nine other occurrences lack enough scoped evidence for the requested reference relationship. Positions2/9 repeat the same Genesis title/album/duration (484133ms) under different declared MBIDs and ISRCs. One secondary identity result maps GBAAA0701757 to a 2007 mix, but source reliability and exact family equivalence remain unresolved; do not count a proven identical-recording violation. Ayumi title omits acoustic-orchestra/instrumental-melody suffix; exact variant remains unknown.

## 31. Give me 10 songs like 宇多田ヒカル, including other artists.

Returned 0/10; 10 missing.

No returned recordings; all ten requested slots remain missing. Musical fulfillment cannot be established.

## 32. Make a 10-song journey from David Bowie to Talking Heads, with related artists bridging the transition.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | David Bowie — Friday on My Mind | good | Official album listing supports the requested opening artist and named recording. [Source 1](https://www.davidbowie.com/pin-ups) |
| 2 | Kevin Rowland & Dexys Midnight Runners — Let’s Make This Precious | partial | The song’s soul instrumentation offers a plausible connection to Bowie’s documented soul period. A complete transition toward Talking Heads remains unverified. [Source 1](https://thequietus.com/quietus-reviews/reissue-of-the-week/dexys-midnight-runners-too-rye-ay-as-it-should-have-sounded-review/) [Source 2](https://www.davidbowie.com/blog/2025/3/7/young-americans-at-50-a-track-by-track-guide) |
| 3 | Talking Heads — Found a Job | good | Official tracklist corroborates a recording by the requested destination artist. [Source 1](https://media.rhino.com/press-release/more-songs-about-buildings-and-food-super-deluxe-edition) |
| 4 | David Bowie — Somebody Up There Likes Me | good | Official tracklist corroborates Bowie; its placement after Talking Heads is recorded separately as an order concern. [Source 1](https://www.davidbowie.com/young-americans) |
| 5 | The Smiths — How Soon Is Now? | partial | The named recording’s distinctive guitar production is documented. Related guitar/art-pop context is plausible, but the specific Bowie-to-Heads bridge is not established. [Source 1](https://www.guitarworld.com/features/the-secrets-behind-johnny-marrs-tone-on-the-smiths-how-soon-is-now) |
| 6 | Talking Heads — With Our Love | good | Official tracklist corroborates this destination-artist recording. [Source 1](https://media.rhino.com/press-release/more-songs-about-buildings-and-food-super-deluxe-edition) |
| 7 | Eagles — James Dean | unknown | The Eagles recording is corroborated, but no applicable evidence established it as a bridge between the two requested artists. [Source 1](https://eagles.com/blogs/news/eagles-to-the-limit-the-essential-collection) |
| 8 | Eric Carmen — Hey Deanie | unknown | Sony corroborates Carmen’s own Change of Heart recording; its relevance to this transition remains unsupported. [Source 1](https://www.sonymusic.co.jp/artist/EricCarmen/info/458030) |
| 9 | Talking Heads — Warning Sign | good | Official tracklist corroborates the requested destination artist. [Source 1](https://media.rhino.com/press-release/more-songs-about-buildings-and-food-super-deluxe-edition) |
| 10 | Talking Heads — The Girls Want to Be With the Girls | good | Official tracklist corroborates the requested final artist and song. [Source 1](https://media.rhino.com/press-release/more-songs-about-buildings-and-food-super-deluxe-edition) |

Correct named start/end artists are present. Talking Heads already appears at3, followed by Bowie at4; a consistent one-direction transition and all bridge relationships are not established. Talking Heads occupies4/10 slots and repeats at9–10. Original prompt has no hard adjacent-artist ban; report as concentration/sequence quality, not a proven explicit constraint violation.

## 33. Give me 10 songs that move from Bonobo to Massive Attack, including related discoveries.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Bonobo — Second Sun | good | Label identifies the named Bonobo recording and its arrangement, supporting the starting endpoint. [Source 1](https://www.ninjatune.net/release/bonobo/migration) |
| 2 | Ludovico Einaudi — Experience (Live at the Royal Albert Hall) | partial | The artist confirms this live release. Its piano/concert context offers an acoustic connection to Second Sun, but the move toward Massive Attack is unverified. [Source 1](https://ludovicoeinaudi.com/the-summer-portraits-live/) [Source 2](https://beardedgentlemenmusic.com/2017/01/26/bonobo-migration-review/) [Source 3](https://www.universalmusic.ca/2026/05/15/ludovico-einaudi-returns-with-the-summer-portraits-live/) |
| 3 | Arctic Monkeys — Secret Door | unknown | Label corroborates the Humbug recording; no recording-specific Bonobo/Massive Attack relationship was established. [Source 1](https://grants.dominomusic.com/releases/arctic-monkeys/humbug/standard-lp) |
| 4 | Bonnie Raitt — Need You Tonight | unknown | Artist describes this INXS cover as a slow-burning performance. A groove alone does not establish the requested relationship. [Source 1](https://www.bonnieraitt.com/discography/dig-in-deep/) |
| 5 | Weezer — Grapes of Wrath | unknown | The orchestral-pop recording is corroborated. Its acoustic orchestra is not sufficient evidence for a Bonobo-to-Massive Attack bridge. [Source 1](https://music.apple.com/us/album/ok-human/1549768766) |
| 6 | P!NK — Sober | unknown | The title and Funhouse album exist, but selected catalog duration is 3,115,267ms with no MBID/ISRC. Exact recording identity and bridge suitability cannot be established. [Source 1](https://music.apple.com/us/album/funhouse/293000131) |
| 7 | Seal — Waiting for You | unknown | A scoped review describes a Philadelphia-soul groove. It does not establish a specific relationship to either requested endpoint. [Source 1](https://tinnitist.com/2023/09/26/classic-album-review-seal-seal-iv/) |
| 8 | Tame Impala — Loser | partial | The recording has documented dance-inflected, looping psychedelic groove. This supports some bridging texture, but its precise relationship to the endpoints remains unverified. [Source 1](https://www.newyorker.com/magazine/2025/10/27/deadbeat-tame-impala-music-review) |
| 9 | Rich Brian — Love in My Pocket | partial | The broadcaster documents synth-led production on this exact single, providing a limited electronic bridge. No source establishes the full endpoint relationship. [Source 1](https://www.abc.net.au/triplej/news/rich-brian-new-single-love-in-my-pocket/12452834) |
| 10 | Massive Attack — Paradise Circus | good | The named Massive Attack album recording is directly relevant to the requested destination; specific review describes its sparse piano and handclap arrangement. [Source 1](https://www.the-independent.com/arts-entertainment/music/reviews/album-massive-attack-heligoland-virgin-1890452.html) |

Bonobo starts and Massive Attack finishes. Several middle relationships remain unverified; subjective continuity was not judged by listening. P!nk Sober catalog identity has no MBID/ISRC and a declared3115267ms duration. Public Funhouse song evidence does not authenticate this occurrence.

## 34. Make a 10-song playlist inspired by Joni Mitchell, Nick Drake, and Leonard Cohen.

Returned 10/10; 0 missing.

| # | Recording | Fit | Evidence and limits |
| ---: | --- | --- | --- |
| 1 | Johnny Cash — Thirteen | good | The official label describes this release as solo acoustic folk performances. Its sparse narrative song is a supported acoustic singer-songwriter connection, not a claim of direct influence. [Source 1](https://shop.mca.com/products/johnny-cash-american-recordings-vinyl) |
| 2 | The Velvet Underground — That’s the Story of My Life | partial | The exact song’s simple lyrical form and restrained arrangement provide a limited singer-songwriter connection; collective reference coverage remains unverified. [Source 1](https://www.sputnikmusic.com/review/10949/The-Velvet-Underground-The-Velvet-Underground/) |
| 3 | Fleetwood Mac — Never Forget | partial | The scoped Tusk review supports reflective songwriting and McVie’s contrasting melodic style; the specific three-reference relationship remains unverified. [Source 1](https://pitchfork.com/reviews/albums/21924-tusk/) |
| 4 | Portishead — It Could Be Sweet | unknown | The scoped review corroborates the delicate vocal track, but no sufficient Mitchell/Drake/Cohen relationship was established. [Source 1](https://www.sputnikmusic.com/review/58648/Portishead-Dummy/) |
| 5 | Ye;Bon Iver — Lost in the World | unknown | The source establishes an electronically treated Woods sample and layered percussion. Bon Iver’s presence alone does not establish the requested inspirations. [Source 1](https://ethnomusicologyreview.ucla.edu/content/lost-worlds-bon-iver-kanye-west-and-generic-environmentalisms) |
| 6 | The Doors — Down on the Farm | unknown | Official release identifies this post-Morrison Doors recording. No applicable reference relationship was established. [Source 1](https://thedoors.com/music) |
| 7 | Conway Twitty — You Are to Me | partial | The country ballad’s release and songwriting credits are corroborated, giving only a broad songcraft connection; the requested collective inspiration remains unverified. [Source 1](https://www.allmusic.com/album/final-touches-mw0000101221) |
| 8 | Chris Stapleton — More of You | partial | NPR documents an intimate country duet with harmony singing, offering a related acoustic-songcraft role. It does not establish all three inspirations. [Source 1](https://www.klcc.org/npr-music/2015-04-26/review-chris-stapleton-traveller) |
| 9 | George Strait — Noel Leon | unknown | The official Christmas album corroborates the song. Its narrative subject alone does not establish the requested inspirations. [Source 1](https://www.georgestrait.com/the-music-albums/merry-christmas-wherever-you-are/) |
| 10 | Don McLean — Vincent | good | McLean’s account identifies the original recording as voice and guitar and explains its poetic portrait of Van Gogh. This supports an acoustic literary-songwriting connection, without asserting direct influence. [Source 1](https://donmclean.com/story-behind-the-song-don-mcleans-vincent/) |

Two recordings have supported acoustic/literary songcraft roles; other connections are partial or unknown. Coverage of all three distinct inspirations is not established. No requirement to return each named reference artist was invented.

## 35. Give me 10 songs blending hip-hop, neo-soul, and jazz.

Returned 0/10; 10 missing.

No returned recordings; all ten requested slots remain missing. Musical fulfillment cannot be established.

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

No returned recordings; all ten requested slots remain missing. Musical fulfillment cannot be established.

## 39. Give me a 10-song journey from blues through soul to funk.

Returned 0/10; 10 missing.

No returned recordings; all ten requested slots remain missing. Musical fulfillment cannot be established.

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
