# Sample music requests

These examples cover artist, album, and track references, named genres, descriptive musical qualities, and ordered transitions. Named artists were active before 2018. The application interprets descriptions locally, retrieves candidates using the Deej-AI audio and playlist-context vectors, and uses the installed CLAP model to compare corroborated Deezer previews with the description.

1. Radiohead for a rainy train ride: intimate, restless, and quietly hopeful.
2. Prince-inspired funk for cooking with friends: playful bass, crisp drums, and a little swagger.
3. Something like Massive Attack and Portishead: shadowy grooves for a city after midnight.
4. Nina Simone for a slow Sunday morning, with expressive singing and warm piano.
5. Songs with the glossy, joyful feel of Daft Punk's album Discovery, with room for other artists.
6. Build around the springy groove of Stevie Wonder's track Superstition, then introduce related discoveries.
7. Jazz-funk with elastic basslines and lively percussion for a sunny walk.
8. Shoegaze that feels like sunlight through fog: blurred guitars, gentle momentum, and dreamy melodies.
9. Dub techno for a late-night workspace: spacious echoes and a steady pulse.
10. Warm analog synthesizers, arpeggios, and a retro-futuristic soundtrack for driving through neon streets.
11. A small jazz trio with brushed drums, upright bass, and piano; instrumental, no vocals.
12. Romantic soul with expressive female vocals, relaxed drums, and a warm, unhurried feel.
13. A journey from ambient to energetic electronic, like a quiet room gradually becoming a dance floor.
14. A journey from soul to disco: begin with a warm groove and finish ready to dance.
15. A journey from acoustic folk to indie rock, moving from a fireside conversation to a small festival stage.

CLAP similarities guide ranking; they are not probabilities or calibrated genre, mood, or instrumentation judgments. Instrumental requests also screen every preview segment for vocals and reject vocal or uncertain previews. Preview assessments do not cover unheard portions of a recording.

All 15 produced six-track playlists in live development checks with the installed LLM, Deej-AI catalog and CLAP bundle. All 15 also passed a final replay using cached audio features without downloading previews. These are execution and evidence-coverage checks; musical-fit outcomes remain partial because general CLAP scores are uncalibrated.

The reproducible evaluation fixture is [`creative-prompts-v1.json`](../internal/evaluation/testdata/creative-prompts-v1.json), and the [results report](data/creative-prompts-v1-live.json) records selected tracks and model provenance. See [`recommendation-correctness.md`](recommendation-correctness.md) for executed checks and coverage limitations.
