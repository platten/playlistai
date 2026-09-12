# Intent NLU calibration expansion v1 — unreviewed diagnostic review sheet

**All 120 prompts and all labels are agent-authored and unreviewed.** This is a diagnostic supplement, not approved training or calibration data. It is not an independent benchmark, and no musical-quality or model-performance claim is made.

The author used a fresh isolated context and only the permitted `data-reviewed-v2/train.jsonl`, `data-reviewed-v2/calibration.jsonl`, `data-reviewed-v2/labels.json`, and `python/prepare_intent_nlu_data.py` schema helper. No original full corpus or held-out evaluation split was read by this author. No evaluation-split requests, model inference, model reads, or downloads were executed during corpus creation.

Provenance limitation: the coordinating agent previously encountered full-reviewed-corpus interpretation snippets via an overbroad search. Those snippets were not supplied to this author. The overall workflow is therefore not claimed as an independently blinded study.

The corpus contains 12 families of 10 distinct English prompts. Every record has an interpretation, family/group, exact nonoverlapping UTF-8 byte spans, existing BIO kind:role labels, polarity, strength, scope, and ambiguity notes. The only relation kinds are `modifies`, `refers_to`, `alternative_member`, and `performed_by`, as present in the permitted sources. Every named artist, composer, performer, album, and track text was checked against the permitted train/calibration raw, canonical, and candidate-canonical identity fields after NFKC normalization and case folding; no matches were found. Identities repeat within this supplement where role distinctions matter; do not split connected identities into separate supposedly independent evaluation sets.

A required count includes required tracks and journey endpoints. When source wording supplies no numeric duration tolerance, the interpretation leaves it unspecified. This does not remove or change the application’s separate current 60-second generic tolerance. Relative artist eras have no invented date cutoff. Artist/album/track resolution and grounded musical suitability remain separate work. Existing heads cannot fully encode ordered multiple waypoints, conditional priorities, or unavailable reference baselines, so these limitations are stated in the interpretations and ambiguity notes.

Mechanical author checks: 120 unique IDs and case-insensitive unique prompts; 10 records per family; zero exact prompt matches and zero NFKC/case-folded identity matches against allowed train/calibration raw/canonical/candidate-canonical fields; schema validation; matching byte slices; no overlapping spans; existing label vocabulary only; valid relation endpoints and the four allowed relation kinds; all review objects exactly `{"status":"unreviewed"}`. Independent native/tokenizer checks and semantic review are outside these author checks.

Corpus: [intent-nlu-calibration-expansion-v1.json](../internal/evaluation/testdata/intent-nlu-calibration-expansion-v1.json).

## Track totals and listening time

1. **calx-count-duration-01 — unreviewed diagnostic**

   Prompt: The bus ride takes 38 minutes; fill that time with neo-soul and let the number of songs vary.

   Interpretation: Target 38 minutes of neo-soul for a bus ride; no fixed track count.

   Ambiguity / limits: No duration tolerance is specified; do not invent exact-fit feasibility.

2. **calx-count-duration-02 — unreviewed diagnostic**

   Prompt: I have six empty slots left in this mix. Pick jazz pieces with vibraphone for those slots.

   Interpretation: Request six jazz tracks; vibraphone is a preference, not exclusive instrumentation.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

3. **calx-count-duration-03 — unreviewed diagnostic**

   Prompt: For a lunch break, aim for half an hour of gentle folk, even if that means an uneven song count.

   Interpretation: Target 30 minutes of gentle folk; the count remains unconstrained.

   Ambiguity / limits: Approximate duration wording has no numeric tolerance.

4. **calx-count-duration-04 — unreviewed diagnostic**

   Prompt: Make the queue 21 tracks long, with dub running through it. I am counting songs, not 21 minutes.

   Interpretation: Twenty-one dub tracks; explicitly reject a 21-minute duration target.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

5. **calx-count-duration-05 — unreviewed diagnostic**

   Prompt: Can you fit nine soul songs into roughly forty minutes for cooking dinner?

   Interpretation: Nine soul songs and an approximate 40-minute total are simultaneous targets.

   Ambiguity / limits: The duration tolerance and policy for an infeasible count/time combination remain unspecified.

6. **calx-count-duration-06 — unreviewed diagnostic**

   Prompt: Keep ambient music going for 1 hour 20 minutes while I sort photographs; a little wordless singing would be nice.

   Interpretation: Target 80 minutes of ambient music for sorting photographs; wordless singing is a soft positive preference.

   Ambiguity / limits: Duration tolerance and the amount of preferred wordless singing are unspecified.

7. **calx-count-duration-07 — unreviewed diagnostic**

   Prompt: Put a dozen reggae selections on the list for the drive home, with cheerful energy.

   Interpretation: Twelve reggae tracks for the drive home, with a cheerful mood preference.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

8. **calx-count-duration-08 — unreviewed diagnostic**

   Prompt: I need 90 minutes of chamber music for sketching. Do not turn that into a nine-minute sampler.

   Interpretation: Target 90 minutes of chamber music; reject a nine-minute interpretation.

   Ambiguity / limits: No exact duration tolerance is provided.

9. **calx-count-duration-09 — unreviewed diagnostic**

   Prompt: The last set was too long. This time give me five funk cuts and leave out extended jams.

   Interpretation: Five funk tracks with extended jams excluded; no duration target is supplied.

   Ambiguity / limits: Extended jams is a qualitative style exclusion, not a numeric per-track length threshold.

10. **calx-count-duration-10 — unreviewed diagnostic**

   Prompt: Two hours should cover the train journey; use downtempo and keep the whole thing instrumental.

   Interpretation: Target two hours of downtempo; instrumental is a global requirement.

   Ambiguity / limits: No duration tolerance or count is specified.

## Composers and performers

11. **calx-composer-performer-01 — unreviewed diagnostic**

   Prompt: For studying scores, choose Clara Schumann compositions; Isata Kanneh-Mason would be my first choice at the piano.

   Interpretation: Require Clara Schumann as composer; prefer Isata Kanneh-Mason as performer and piano instrumentation.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

12. **calx-composer-performer-02 — unreviewed diagnostic**

   Prompt: The composer must be Claude Debussy, and I want the Arturo Benedetti Michelangeli performances exclusively.

   Interpretation: Debussy compositions and Michelangeli performances are both hard requirements.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

13. **calx-composer-performer-03 — unreviewed diagnostic**

   Prompt: Build a short introduction to Florence Price with seven recordings. Favor strings over solo piano.

   Interpretation: Seven recordings of Florence Price compositions; strings preferred and solo piano relatively disfavored.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

14. **calx-composer-performer-04 — unreviewed diagnostic**

   Prompt: Mitsuko Uchida at the piano is my preference for Franz Schubert, although another player is acceptable.

   Interpretation: Schubert is the required composer; Uchida and piano are preferred, with other performers allowed.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

15. **calx-composer-performer-05 — unreviewed diagnostic**

   Prompt: Keep the repertoire to Jean Sibelius, but make it chamber music instead of any symphonies.

   Interpretation: Sibelius compositions in chamber music form; symphonies excluded.

   Ambiguity / limits: The phrase instead of any supplies exclusion; no performer is named.

16. **calx-composer-performer-06 — unreviewed diagnostic**

   Prompt: I am comparing interpretations: four recordings of music by Maurice Ravel, all performed by Martha Argerich and nobody else.

   Interpretation: Four Ravel recordings performed only by Argerich; comparing interpretations does not request duplicate recordings.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

17. **calx-composer-performer-07 — unreviewed diagnostic**

   Prompt: Give me Erik Satie for a quiet gallery opening, with Aldo Ciccolini favored and no choirs.

   Interpretation: Satie composer required; Ciccolini preferred; choir vocals excluded.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

18. **calx-composer-performer-08 — unreviewed diagnostic**

   Prompt: Béla Bartók is the composer, Béla Bartók is also the performer I want; limit the selection to six recordings.

   Interpretation: Six recordings with Bartók required independently as composer and performer.

   Ambiguity / limits: Repeated identical text has two distinct identity roles; label identity does not prove catalog availability.

19. **calx-composer-performer-09 — unreviewed diagnostic**

   Prompt: Find eight pieces written by Caroline Shaw; I would welcome singing alongside the strings.

   Interpretation: Eight Caroline Shaw compositions; singing is allowed and strings preferred, without assuming Shaw performs.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

20. **calx-composer-performer-10 — unreviewed diagnostic**

   Prompt: For late-night reading, use Domenico Scarlatti played on harpsichord alone, preferably by Scott Ross.

   Interpretation: Scarlatti compositions on harpsichord alone; Ross preferred as performer.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

## Artist references and output membership

21. **calx-artist-output-roles-01 — unreviewed diagnostic**

   Prompt: I have worn out my Little Simz records. Find neighboring sounds for a city walk, leaving her own catalog off the queue.

   Interpretation: Little Simz is a similarity reference whose own output is excluded.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

22. **calx-artist-output-roles-02 — unreviewed diagnostic**

   Prompt: Let me spend the evening inside Khruangbin’s catalog; do not branch out to related artists.

   Interpretation: Output is restricted to Khruangbin; related-artist expansion is forbidden.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

23. **calx-artist-output-roles-03 — unreviewed diagnostic**

   Prompt: Tame Impala is the sonic compass, but I would like a range of artists across these eleven tracks.

   Interpretation: Eleven tracks with Tame Impala similarity and artist diversity; Tame Impala output remains permitted.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

24. **calx-artist-output-roles-04 — unreviewed diagnostic**

   Prompt: Make a soul mix that borrows from Cleo Sol, then exclude Sault from the results.

   Interpretation: Soul genre and Cleo Sol similarity; Sault is a separate excluded artist.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

25. **calx-artist-output-roles-05 — unreviewed diagnostic**

   Prompt: Tonight is a PJ Harvey deep dive, strictly her recordings, leaning toward acoustic arrangements.

   Interpretation: Only PJ Harvey output, with an acoustic-arrangement preference.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

26. **calx-artist-output-roles-06 — unreviewed diagnostic**

   Prompt: Use The Comet Is Coming and Sons of Kemet to steer the sound. Give each group a place in the finished mix.

   Interpretation: Both groups are similarity references and both must appear in the output.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

27. **calx-artist-output-roles-07 — unreviewed diagnostic**

   Prompt: I like the atmosphere around Broadcast; surprise me with different acts and avoid Stereolab.

   Interpretation: Broadcast similarity with artist diversity; Stereolab excluded, Broadcast not excluded.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

28. **calx-artist-output-roles-08 — unreviewed diagnostic**

   Prompt: For a rainy afternoon, stay entirely with Arooj Aftab and prefer her meditative side.

   Interpretation: Only Arooj Aftab output; meditative mood preferred for a rainy afternoon.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

29. **calx-artist-output-roles-09 — unreviewed diagnostic**

   Prompt: Take cues from Big Thief as well as Wednesday, but neither band should actually appear.

   Interpretation: Two similarity references with both bands excluded from output.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

30. **calx-artist-output-roles-10 — unreviewed diagnostic**

   Prompt: I want Janelle Monáe-inspired pop, especially the playful side, and vocals are welcome.

   Interpretation: Pop with Janelle Monáe similarity and playful mood; vocals allowed, with no artist-only restriction.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

## Relaxing electronic listening

31. **calx-relaxing-electronic-01 — unreviewed diagnostic**

   Prompt: While I answer emails, keep electronica unhurried and airy; skip the festival drops.

   Interpretation: Electronica for email work; unhurried mood and airy texture preferred; festival drops excluded.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

32. **calx-relaxing-electronic-02 — unreviewed diagnostic**

   Prompt: I need ambient techno for an evening bath. A soft-edged sound matters more than a glossy one.

   Interpretation: Ambient techno for bathing; soft-edged texture preferred and glossy texture relatively disfavored.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

33. **calx-relaxing-electronic-03 — unreviewed diagnostic**

   Prompt: Make electronic music for a breathing exercise, with calm pacing and every selection instrumental.

   Interpretation: Electronic music for breathing exercises; calm preferred and instrumental required globally.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

34. **calx-relaxing-electronic-04 — unreviewed diagnostic**

   Prompt: Kiasmos gives you the direction for unwinding after work, but turn the intensity down so it feels calmer.

   Interpretation: Kiasmos similarity with a relative decrease in intensity and calmer mood for unwinding.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

35. **calx-relaxing-electronic-05 — unreviewed diagnostic**

   Prompt: For watching snow fall, find dub techno with hazy textures; distant voices would not bother me.

   Interpretation: Dub techno with hazy texture preference; distant vocals explicitly allowed.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

36. **calx-relaxing-electronic-06 — unreviewed diagnostic**

   Prompt: My night-shift paperwork needs downtempo that feels serene. Keep out ominous moods.

   Interpretation: Downtempo for paperwork; serene preferred and ominous mood excluded.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

37. **calx-relaxing-electronic-07 — unreviewed diagnostic**

   Prompt: Choose thirteen electronic pieces with mallet percussion for resting on the balcony; mostly instrumental would suit me.

   Interpretation: Thirteen electronic tracks for resting; mallet percussion preferred and instrumental is a soft preference.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

38. **calx-relaxing-electronic-08 — unreviewed diagnostic**

   Prompt: I want chillout for a screen-free evening, without spoken samples, but sung vocals can stay.

   Interpretation: Chillout with spoken samples excluded and sung vocals allowed; do not turn this into a blanket voice exclusion.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

39. **calx-relaxing-electronic-09 — unreviewed diagnostic**

   Prompt: For stretching before bed, use ambient house; favor rounded textures over brittle ones.

   Interpretation: Ambient house for bedtime stretching; rounded texture preferred and brittle texture disfavored softly.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

40. **calx-relaxing-electronic-10 — unreviewed diagnostic**

   Prompt: Build 45 minutes around Loscil’s sound for reading essays. There is no need to play his records.

   Interpretation: Target 45 minutes with Loscil similarity for reading; reference recordings are not required and are not excluded.

   Ambiguity / limits: The no-recording-requirement label is diagnostic when its antecedent is an artist catalog rather than one track.

## Exercise contexts and constraints

41. **calx-workout-context-01 — unreviewed diagnostic**

   Prompt: My rowing intervals need drum and bass with a driving feel. Leave out long ambient introductions.

   Interpretation: Drum and bass for rowing intervals; driving mood preferred and long ambient introductions excluded.

   Ambiguity / limits: Long introductions has no numeric cutoff; interval timing does not specify a music duration.

42. **calx-workout-context-02 — unreviewed diagnostic**

   Prompt: For hill repeats, I want seventeen punk songs; shouted vocals are welcome, but not acoustic interludes.

   Interpretation: Seventeen punk tracks for hill repeats; shouted vocals allowed, acoustic interludes excluded.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

43. **calx-workout-context-03 — unreviewed diagnostic**

   Prompt: Give my yoga practice a raga soundtrack with sitar, and avoid frantic energy.

   Interpretation: Raga music for yoga; sitar preferred and frantic mood excluded.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

44. **calx-workout-context-04 — unreviewed diagnostic**

   Prompt: I am walking back from the gym; choose soul that feels restorative, with fewer big anthems.

   Interpretation: Soul for post-gym walking; restorative mood preferred and big anthems softly reduced.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

45. **calx-workout-context-05 — unreviewed diagnostic**

   Prompt: For boxing drills, alternate the flavor between grime or hard techno; either works, but no whispered vocals.

   Interpretation: Grime or hard techno are accepted genres for boxing drills; whispered vocals excluded.

   Ambiguity / limits: Alternate the flavor expresses variety here, not a strict every-other-track alternation schedule.

46. **calx-workout-context-06 — unreviewed diagnostic**

   Prompt: I need 50 minutes for indoor cycling, with disco and an upbeat mood throughout.

   Interpretation: Target 50 minutes of upbeat disco for indoor cycling; no count requested.

   Ambiguity / limits: Duration tolerance is unspecified; throughout extends the mood request globally without supplying a measurable threshold.

47. **calx-workout-context-07 — unreviewed diagnostic**

   Prompt: My resistance-band session should sound like Run the Jewels, with more forceful energy than that reference.

   Interpretation: Run the Jewels similarity with a relative forcefulness increase for resistance-band exercise.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

48. **calx-workout-context-08 — unreviewed diagnostic**

   Prompt: Choose Afrobeat for a dance warm-up. I prefer brass, though electric guitars are equally welcome.

   Interpretation: Afrobeat for dance warm-up; brass preferred and electric guitar allowed.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

49. **calx-workout-context-09 — unreviewed diagnostic**

   Prompt: For a long easy jog, keep it house and buoyant; a few ballads are fine if the flow holds.

   Interpretation: House with buoyant mood for an easy jog; a small amount of ballad material is acceptable, not forbidden.

   Ambiguity / limits: Flow and a few have no quantified threshold; ballads are conditionally allowed, not positively requested.

50. **calx-workout-context-10 — unreviewed diagnostic**

   Prompt: I want twenty metal tracks for deadlifts, all of them instrumental so I can focus on my breathing.

   Interpretation: Twenty metal tracks for deadlifts; instrumental is a global hard requirement.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

## Artist endpoints and waypoints

51. **calx-artist-journeys-01 — unreviewed diagnostic**

   Prompt: Make sixteen tracks with an actual Nina Simone recording up front and an actual Brittany Howard recording at the close.

   Interpretation: Sixteen total tracks, Nina Simone first and Brittany Howard last; both endpoints are required output artists.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

52. **calx-artist-journeys-02 — unreviewed diagnostic**

   Prompt: The opener should be Portishead; visit Tricky somewhere in the middle, then close on Massive Attack.

   Interpretation: Required Portishead start, Tricky middle waypoint, and Massive Attack destination; count unspecified.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

53. **calx-artist-journeys-03 — unreviewed diagnostic**

   Prompt: I have ten spaces total. Lead with Fela Kuti, pass through Ebo Taylor, and save Tony Allen for last.

   Interpretation: Ten total tracks including required Fela Kuti start, Ebo Taylor waypoint, and Tony Allen last.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

54. **calx-artist-journeys-04 — unreviewed diagnostic**

   Prompt: Make Björk the final artist, after opening with Kate Bush; singing is welcome throughout.

   Interpretation: Kate Bush starts and Björk ends the playlist despite reverse mention order; vocals allowed globally.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

55. **calx-artist-journeys-05 — unreviewed diagnostic**

   Prompt: Between an Otis Redding opening and a D’Angelo ending, make room for Donny Hathaway and Erykah Badu in that order.

   Interpretation: Required Otis Redding start and D’Angelo end; Donny Hathaway precedes Erykah Badu as ordered intermediate waypoints.

   Ambiguity / limits: Waypoint order is expressed in interpretation and source order; the existing relation vocabulary has no dedicated precedes relation.

56. **calx-artist-journeys-06 — unreviewed diagnostic**

   Prompt: I want the journey to begin with The xx. Jamie xx gets the last slot, but Burial is just a sound reference along the way.

   Interpretation: Required The xx first and Jamie xx last; Burial is similarity only, not a required waypoint.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

57. **calx-artist-journeys-07 — unreviewed diagnostic**

   Prompt: Open on Ryuichi Sakamoto, finish with Hania Rani, and keep Nils Frahm out of the entire set.

   Interpretation: Sakamoto start and Rani destination are required output artists; Nils Frahm excluded globally.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

58. **calx-artist-journeys-08 — unreviewed diagnostic**

   Prompt: The twelve-track arc should start at Parliament, pass through Prince, and land on Anderson .Paak; those artists must actually appear.

   Interpretation: Twelve tracks including Parliament start, Prince waypoint, and Anderson .Paak destination.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

59. **calx-artist-journeys-09 — unreviewed diagnostic**

   Prompt: Set Enya at the front of this one-hour journey and Julianna Barwick at the end, with tranquil music connecting them.

   Interpretation: Target one hour with Enya first and Julianna Barwick last; tranquil connecting music preferred.

   Ambiguity / limits: The endpoints count toward the hour; exact duration tolerance is unspecified.

60. **calx-artist-journeys-10 — unreviewed diagnostic**

   Prompt: Use Yves Tumor for the general sound, but the actual sequence must open with Grace Jones and finish with FKA twigs.

   Interpretation: Yves Tumor is a global similarity reference; Grace Jones first and FKA twigs last are required artist outputs.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

## Genre journeys and local scope

61. **calx-genre-journey-scope-01 — unreviewed diagnostic**

   Prompt: Take me from bossa nova into broken beat over fourteen tracks, keeping percussion prominent as a preference.

   Interpretation: Fourteen-track genre journey from bossa nova to broken beat; percussion preferred throughout.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

62. **calx-genre-journey-scope-02 — unreviewed diagnostic**

   Prompt: Let the ambient opening be instrumental, then reach trip-hop by the end, where voices are fine.

   Interpretation: Ambient start and trip-hop destination; instrumental required at the opening, voices permitted at the end.

   Ambiguity / limits: The exact boundary of the opening section is unspecified.

63. **calx-genre-journey-scope-03 — unreviewed diagnostic**

   Prompt: Across an hour, move from soul to house; exclude live versions at every stage.

   Interpretation: One-hour soul-to-house genre journey with live versions excluded globally.

   Ambiguity / limits: Duration tolerance is unspecified; live-version evidence must be resolved separately.

64. **calx-genre-journey-scope-04 — unreviewed diagnostic**

   Prompt: I would like eighteen tracks that begin in acoustic folk and arrive at shoegaze, growing more intense along the way.

   Interpretation: Eighteen-track acoustic-folk to shoegaze journey with a relative intensity increase.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

65. **calx-genre-journey-scope-05 — unreviewed diagnostic**

   Prompt: End the set in jazz fusion, but start it with cool jazz. Put electric piano near the middle if possible.

   Interpretation: Cool jazz start and jazz fusion destination; electric piano preferred only around the middle.

   Ambiguity / limits: Near the middle does not define an exact track position.

66. **calx-genre-journey-scope-06 — unreviewed diagnostic**

   Prompt: For an evening drive, travel from synth-pop toward electro. During the opening stretch, favor sung vocals.

   Interpretation: Synth-pop to electro journey for driving; sung vocals are preferred at the start, with no later vocal rule.

   Ambiguity / limits: Opening stretch length is unspecified.

67. **calx-genre-journey-scope-07 — unreviewed diagnostic**

   Prompt: Build a bridge between a minimal techno start and a drum and bass finish. The entire queue must be free of lyrics.

   Interpretation: Minimal-techno to drum-and-bass journey; absence of lyrics required across the playlist.

   Ambiguity / limits: No lyrics does not necessarily exclude wordless human voices.

68. **calx-genre-journey-scope-08 — unreviewed diagnostic**

   Prompt: Give me a nine-track progression from bluegrass into country rock, with drums absent at the start.

   Interpretation: Nine tracks from bluegrass to country rock; drums excluded only at the start.

   Ambiguity / limits: Start may mean an opening segment rather than exactly one track; no segment length is supplied.

69. **calx-genre-journey-scope-09 — unreviewed diagnostic**

   Prompt: I want dub at the beginning and jungle at the end, but keep the overall mood joyful instead of anything menacing.

   Interpretation: Dub-to-jungle genre journey; joyful mood preferred and menacing mood excluded globally.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

70. **calx-genre-journey-scope-10 — unreviewed diagnostic**

   Prompt: Use baroque to open and contemporary classical to close; only the first section should be solo organ.

   Interpretation: Baroque to contemporary-classical journey; solo organ is required only in the opening section.

   Ambiguity / limits: Opening section length and later instrumentation are unspecified.

## Instruments, voices, and negation

71. **calx-instrument-vocal-logic-01 — unreviewed diagnostic**

   Prompt: For a pottery session, favor cello with neoclassical music, and let woodwinds join in.

   Interpretation: Neoclassical music for pottery; cello preferred and woodwinds allowed.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

72. **calx-instrument-vocal-logic-02 — unreviewed diagnostic**

   Prompt: Nothing but solo flute for this classical list, with no humming in the background.

   Interpretation: Classical solo flute only; humming excluded as a vocal form.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

73. **calx-instrument-vocal-logic-03 — unreviewed diagnostic**

   Prompt: I enjoy saxophone in jazz, but leave out trumpet and keep scat singing available.

   Interpretation: Jazz with saxophone preferred; trumpet excluded; scat vocals allowed.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

74. **calx-instrument-vocal-logic-04 — unreviewed diagnostic**

   Prompt: Make folk where unaccompanied voices are required on every track.

   Interpretation: Every folk track must feature unaccompanied voices, implying no instrumental accompaniment.

   Ambiguity / limits: Unaccompanied is encoded within a vocal-required span because the existing heads lack a separate accompaniment relation.

75. **calx-instrument-vocal-logic-05 — unreviewed diagnostic**

   Prompt: Use post-rock with mostly instrumentals; one song with a singer would not spoil it.

   Interpretation: Post-rock with a soft instrumental preference; singing is not excluded.

   Ambiguity / limits: One song with a singer is illustrative permission rather than a required count of vocal tracks.

76. **calx-instrument-vocal-logic-06 — unreviewed diagnostic**

   Prompt: I want soul with organ, without distorted guitar, while lead vocals remain welcome.

   Interpretation: Soul; organ preferred, distorted guitar excluded, and lead vocals allowed.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

77. **calx-instrument-vocal-logic-07 — unreviewed diagnostic**

   Prompt: Percussion is fine in my ambient mix; I am not asking for percussion alone.

   Interpretation: Ambient with percussion permitted; the apparent percussion-only wording is explicitly rejected.

   Ambiguity / limits: The second mention rejects exclusive percussion; it neither excludes percussion nor requires another specific instrument.

78. **calx-instrument-vocal-logic-08 — unreviewed diagnostic**

   Prompt: For a quiet breakfast, choose acoustic jazz and prefer female vocals, but skip spoken-word tracks.

   Interpretation: Acoustic jazz for breakfast; female vocals preferred; spoken-word tracks excluded.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

79. **calx-instrument-vocal-logic-09 — unreviewed diagnostic**

   Prompt: Let strings and brass appear in cinematic music; the one thing I cannot have is pipe organ.

   Interpretation: Cinematic music allows strings and brass, while pipe organ is a hard exclusion.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

80. **calx-instrument-vocal-logic-10 — unreviewed diagnostic**

   Prompt: Please use chamber music with clarinet. Avoid lyrics, although wordless choir is acceptable.

   Interpretation: Chamber music with clarinet preferred; lyrics excluded but wordless choir allowed.

   Ambiguity / limits: No lyrics is narrower than no human voice; the annotation preserves the explicit wordless-choir exception.

## Release periods and alternatives

81. **calx-temporal-alternatives-01 — unreviewed diagnostic**

   Prompt: Our disco theme covers 1976–1979. Use the original release dates, not the dates on reissues.

   Interpretation: Disco originally released in the inclusive 1976–1979 interval; reissue dates do not determine eligibility.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

82. **calx-temporal-alternatives-02 — unreviewed diagnostic**

   Prompt: For the synth-pop quiz, draw from 1984 or 1991, with nothing from the intervening years.

   Interpretation: Synth-pop from either 1984 or 1991; do not broaden to the continuous interval.

   Ambiguity / limits: The prompt does not explicitly distinguish original release dates from other release metadata.

83. **calx-temporal-alternatives-03 — unreviewed diagnostic**

   Prompt: I want the pre-fame feel of Pulp, with other bands carrying that feeling too.

   Interpretation: Pulp similarity is scoped to a relative pre-fame era; other artists are allowed.

   Ambiguity / limits: Pre-fame has no supplied year boundary; do not invent one.

84. **calx-temporal-alternatives-04 — unreviewed diagnostic**

   Prompt: Choose jazz whose first issue falls anywhere from 1958 through 1964, and skip concert recordings.

   Interpretation: Jazz originally released in the inclusive 1958–1964 interval, with concert recordings excluded.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

85. **calx-temporal-alternatives-05 — unreviewed diagnostic**

   Prompt: The sound should recall Talk Talk in their later years, but their records need not be present.

   Interpretation: Similarity to a relative later period of Talk Talk; the band’s actual recordings are optional.

   Ambiguity / limits: Later years is not a numeric release filter.

86. **calx-temporal-alternatives-06 — unreviewed diagnostic**

   Prompt: Fill eight slots with soul originally issued in 1967 or 1972; singing is welcome.

   Interpretation: Eight soul tracks from either original-release year, 1967 or 1972; vocals allowed.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

87. **calx-temporal-alternatives-07 — unreviewed diagnostic**

   Prompt: For a retro game night, use electro from the 1980s, preferably with a grainy sound.

   Interpretation: Electro from the 1980s, with grainy texture preferred for game night.

   Ambiguity / limits: Release metadata is assumed for the decade request, but original-versus-reissue basis is not explicitly settled.

88. **calx-temporal-alternatives-08 — unreviewed diagnostic**

   Prompt: I am after early PJ Harvey energy in alternative rock, while excluding Hole from the output.

   Interpretation: Alternative rock similar to an unspecified early PJ Harvey era; Hole excluded.

   Ambiguity / limits: Early is artist-relative and must not be converted to an invented year range.

89. **calx-temporal-alternatives-09 — unreviewed diagnostic**

   Prompt: Use pop from the 1990s or the 2010s for cleaning the apartment; the decade in between is off limits.

   Interpretation: Pop from the 1990s or 2010s, excluding the intervening 2000s decade.

   Ambiguity / limits: Original-release versus reissue-date semantics are not explicitly stated.

90. **calx-temporal-alternatives-10 — unreviewed diagnostic**

   Prompt: Restrict this folk set to 2008–2012 by initial release year, and favor banjo.

   Interpretation: Folk initially released in the inclusive 2008–2012 interval; banjo preferred.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

## Preference strength and Boolean logic

91. **calx-preference-strength-01 — unreviewed diagnostic**

   Prompt: For a dinner party, jazz can be lively without feeling frenetic; favor that balance.

   Interpretation: Jazz for dinner; lively mood preferred and frenetic mood softly disfavored, without a categorical mood ban.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

92. **calx-preference-strength-02 — unreviewed diagnostic**

   Prompt: Choose gospel or spiritual jazz, and make choir vocals a preference rather than a necessity.

   Interpretation: Gospel or spiritual jazz accepted; choir vocals preferred but optional.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

93. **calx-preference-strength-03 — unreviewed diagnostic**

   Prompt: Make Americana for a porch evening, neither bluegrass nor country pop.

   Interpretation: Americana request with both bluegrass and country-pop substyles excluded.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

94. **calx-preference-strength-04 — unreviewed diagnostic**

   Prompt: I like rough-edged production in garage rock. Go lighter on the polished sound, but do not rule it out.

   Interpretation: Garage rock with rough-edged texture preferred and polished texture softly reduced, not banned.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

95. **calx-preference-strength-05 — unreviewed diagnostic**

   Prompt: For an afternoon reset, use soul with hopeful songs; absolutely no despairing mood.

   Interpretation: Soul for an afternoon reset; hopeful preferred and despairing mood categorically excluded.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

96. **calx-preference-strength-06 — unreviewed diagnostic**

   Prompt: Give me jazz with more double bass and less piano, without banning the piano.

   Interpretation: Jazz with more double bass and less piano as relative preferences; piano remains allowed.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

97. **calx-preference-strength-07 — unreviewed diagnostic**

   Prompt: Use pop that feels intimate. Occasional theatrical arrangements are welcome, but intimacy comes first.

   Interpretation: Pop with intimacy preferred; theatrical arrangements are an occasional soft preference.

   Ambiguity / limits: Priority is expressed in the interpretation; the existing span heads do not encode a numerical preference ordering.

98. **calx-preference-strength-08 — unreviewed diagnostic**

   Prompt: For a small café, find bossa nova with crisp sound; not necessarily acoustic recordings.

   Interpretation: Bossa nova for a café; crisp texture preferred and acoustic recordings are optional rather than required.

   Ambiguity / limits: Not necessarily does not forbid acoustic recordings or establish a positive acoustic preference.

99. **calx-preference-strength-09 — unreviewed diagnostic**

   Prompt: I want funk, and instrumental jams are allowed. Do exclude novelty songs, though.

   Interpretation: Funk with instrumental jams allowed and novelty songs excluded.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

100. **calx-preference-strength-10 — unreviewed diagnostic**

   Prompt: Keep the electronic mix bright and spacious; I can accept lo-fi production if those two qualities survive.

   Interpretation: Electronic music with bright mood and spacious texture preferred; lo-fi production conditionally allowed.

   Ambiguity / limits: The allowed style is conditional on the two qualitative preferences; no existing relation kind encodes that condition exactly.

## Album and recording identity

101. **calx-album-track-identity-01 — unreviewed diagnostic**

   Prompt: The atmosphere of Vespertine by Björk is my reference for a late evening; the album itself is optional.

   Interpretation: Qualified album similarity reference, with no requirement to include its tracks.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

102. **calx-album-track-identity-02 — unreviewed diagnostic**

   Prompt: Choose six cuts from Since I Left You by The Avalanches, all from that one album.

   Interpretation: Six output tracks restricted to the album Since I Left You, qualified by The Avalanches.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

103. **calx-album-track-identity-03 — unreviewed diagnostic**

   Prompt: For the train home, find music in the vein of Archangel by Burial, without making that recording mandatory.

   Interpretation: Archangel qualified by Burial is a track similarity reference, not a required recording or excluded output.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

104. **calx-album-track-identity-04 — unreviewed diagnostic**

   Prompt: The eleven-song set needs Paper Trails by Darkside once, but you can choose its position.

   Interpretation: Eleven total tracks including the qualified Paper Trails recording exactly once, with no fixed position.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

105. **calx-album-track-identity-05 — unreviewed diagnostic**

   Prompt: I want Roads by Portishead to guide the sound, while Teardrop by Massive Attack is required in the result.

   Interpretation: Roads/Portishead is similarity only; Teardrop/Massive Attack must appear as a separate required recording.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

106. **calx-album-track-identity-06 — unreviewed diagnostic**

   Prompt: Begin the queue with Svefn-g-englar by Sigur Rós, then look for expansive post-rock.

   Interpretation: The qualified Svefn-g-englar recording is a required opening track; expansive post-rock guides the rest.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

107. **calx-album-track-identity-07 — unreviewed diagnostic**

   Prompt: Stay inside Kiwanuka, the Michael Kiwanuka album; do not pull songs from any other release.

   Interpretation: Output is restricted to the album Kiwanuka by Michael Kiwanuka; other releases excluded.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

108. **calx-album-track-identity-08 — unreviewed diagnostic**

   Prompt: Borrow the dreamy texture of Heaven or Las Vegas by Cocteau Twins, then exclude Cocteau Twins from the finished playlist.

   Interpretation: Qualified album similarity and dreamy texture preference; Cocteau Twins output excluded separately.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

109. **calx-album-track-identity-09 — unreviewed diagnostic**

   Prompt: Make eight selections around Bachelorette by Björk; include that recording as well as Hyperballad by Björk.

   Interpretation: Eight total tracks; Bachelorette is a similarity reference also required by anaphora, and Hyperballad is separately required.

   Ambiguity / limits: The required status of the first track is carried by the require relation to its similarity span; do not discard the dual intent.

110. **calx-album-track-identity-10 — unreviewed diagnostic**

   Prompt: For a jazz listening session, use the album Blue Train by John Coltrane as inspiration and favor tenor saxophone.

   Interpretation: Blue Train is explicitly an album similarity reference qualified by John Coltrane; tenor saxophone preferred.

   Ambiguity / limits: The album wording disambiguates an album-versus-track title collision; no album membership restriction is requested.

## Ambiguity and reference boundaries

111. **calx-ambiguity-and-reference-01 — unreviewed diagnostic**

   Prompt: Give me nine tracks inspired by the band Air for a slow morning, with vocals allowed.

   Interpretation: Nine tracks with Air as the explicitly identified band reference; vocals allowed for a slow-morning context.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

112. **calx-ambiguity-and-reference-02 — unreviewed diagnostic**

   Prompt: Use 10cc as my reference for pop with witty energy; let the list length take care of itself.

   Interpretation: 10cc is an artist name, not a requested count; pop and witty mood guide the music.

   Ambiguity / limits: No fixed count is stated. Witty is a requested mood-like quality, not a verified lyric-content fact.

113. **calx-ambiguity-and-reference-03 — unreviewed diagnostic**

   Prompt: Four Tet is the direction for a painting session. I would prefer wordless vocals to clearly sung lyrics.

   Interpretation: Four Tet is an artist reference, not the number four; wordless vocals preferred for painting.

   Ambiguity / limits: The comparison disfavors intelligible lyrics softly without imposing a blanket vocal exclusion.

114. **calx-ambiguity-and-reference-04 — unreviewed diagnostic**

   Prompt: I keep coming back to a song called Tomorrow; find similar indie pop without assuming which artist I mean.

   Interpretation: Tomorrow is an unresolved track-title similarity reference, with indie pop requested.

   Ambiguity / limits: Multiple recordings may share this title; artist or recording clarification is needed before canonical resolution.

115. **calx-ambiguity-and-reference-05 — unreviewed diagnostic**

   Prompt: I want something like 1999 by Prince, with more energetic funk.

   Interpretation: 1999 is the qualified track title, not a release-year filter; seek more energetic funk relative to that reference.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

116. **calx-ambiguity-and-reference-06 — unreviewed diagnostic**

   Prompt: For journaling, I love the hushed side of Ólafur Arnalds; no spoken narration, please.

   Interpretation: Ólafur Arnalds is a similarity reference written with a decomposed accent; hushed mood preferred and spoken narration excluded.

   Ambiguity / limits: Preserve the literal decomposed Unicode spelling and its UTF-8 byte offsets; no correction or canonical lookup is implied.

117. **calx-ambiguity-and-reference-07 — unreviewed diagnostic**

   Prompt: Make seven jazz tracks, more relaxed than the playlist I made yesterday.

   Interpretation: Seven jazz tracks with a relative relaxation request against an unavailable prior playlist.

   Ambiguity / limits: The prior playlist is not provided in this record; the baseline cannot be invented or inferred from the words alone.

118. **calx-ambiguity-and-reference-08 — unreviewed diagnostic**

   Prompt: I mean the artist Low, not a low-volume setting. Build slowcore around that sound with tender moods.

   Interpretation: Low is explicitly an artist similarity reference; slowcore and tender mood are requested, with no playback-volume instruction.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

119. **calx-ambiguity-and-reference-09 — unreviewed diagnostic**

   Prompt: Choose modern classical for a museum lobby, with viola preferred; I am naming a style, not setting a release-date limit.

   Interpretation: Modern classical is a genre request rather than a temporal cutoff; viola preferred for a museum lobby.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.

120. **calx-ambiguity-and-reference-10 — unreviewed diagnostic**

   Prompt: I would like jazz with strings, but not a string of gloomy songs.

   Interpretation: Jazz with string instrumentation preferred; gloomy mood excluded. The second string is an idiom, not another instrument mention.

   Ambiguity / limits: No unresolved linguistic ambiguity identified; musical suitability and catalog resolution are not established by these labels.
