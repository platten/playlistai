package schema

const SystemPrompt = `You translate a music request into the exact JSON intent contract shown by the examples.
Never invent artists or tracks. Preserve the user's wording in "span" and in semantic values.
References may be artist or track, positive or negative, and guide recommendation without being required output.
Only tracks the user explicitly requires belong in "required_tracks".
Styles, moods, instrumentation, vocal preference, and textures are preferences, not hard filters.
Mark explicit=false only for a genuinely useful inference; otherwise preserve explicit wording.
Supported hard constraints are exclude_artist, exclude_reference_artists, and no_back_to_back_artist.
Put every other strict demand in hard_constraints and unsupported_requirements; never claim it is enforced.
The current catalog cannot enforce style, mood, instrumentation, vocals, texture, year, BPM, energy, or genre.
Use journey_waypoints for an ordered from/to/via route. Energy trajectory points use positions and energies from 0 to 1.
total_count is total output tracks. audio_weight and cooccurrence_weight independently express sound versus playlist-context similarity.
discovery, artist_diversity, and transition_smoothness are independent 0..1 controls.
Use an empty vocal preference object with empty strings, false, and empty span when unspecified.
Reply with ONLY the JSON object.`

type Example struct {
	Prompt string
	JSON   string
}

var FewShot = []Example{
	{
		Prompt: "upbeat instrumental tracks like Justice, about 25 songs, a little unpredictable",
		JSON:   `{"references":[{"kind":"artist","value":"Justice","influence":"positive","explicit":true,"span":"like Justice"}],"required_tracks":[],"styles":[],"moods":[{"value":"upbeat","influence":"positive","explicit":true,"span":"upbeat"}],"instrumentation":[{"value":"instrumental","influence":"positive","explicit":true,"span":"instrumental"}],"vocal_preference":{"value":"instrumental","influence":"positive","explicit":true,"span":"instrumental"},"textures":[],"hard_constraints":[],"unsupported_requirements":[],"mode":"similar","journey_waypoints":[],"energy_trajectory":[],"total_count":25,"audio_weight":0.5,"cooccurrence_weight":0.5,"discovery":0.35,"artist_diversity":0.7,"transition_smoothness":0.5,"notes":"Upbeat instrumental music around Justice; semantic preferences are preserved but not enforced."}`,
	},
	{
		Prompt: "a 12-track journey from Nick Drake to Aphex Twin via Radiohead",
		JSON:   `{"references":[{"kind":"artist","value":"Nick Drake","influence":"positive","explicit":true,"span":"Nick Drake"},{"kind":"artist","value":"Radiohead","influence":"positive","explicit":true,"span":"Radiohead"},{"kind":"artist","value":"Aphex Twin","influence":"positive","explicit":true,"span":"Aphex Twin"}],"required_tracks":[],"styles":[],"moods":[],"instrumentation":[],"vocal_preference":{"value":"","influence":"positive","explicit":false,"span":""},"textures":[],"hard_constraints":[],"unsupported_requirements":[],"mode":"journey","journey_waypoints":[{"kind":"artist","value":"Nick Drake","influence":"positive","explicit":true,"span":"from Nick Drake"},{"kind":"artist","value":"Radiohead","influence":"positive","explicit":true,"span":"via Radiohead"},{"kind":"artist","value":"Aphex Twin","influence":"positive","explicit":true,"span":"to Aphex Twin"}],"energy_trajectory":[],"total_count":12,"audio_weight":0.5,"cooccurrence_weight":0.5,"discovery":0.1,"artist_diversity":0.7,"transition_smoothness":0.8,"notes":"A smooth twelve-track journey through the named artists."}`,
	},
	{
		Prompt: "ambient electronic with microdetail, a deep groove, occasional sparkle, relaxing but not sleepy, no abstract drone",
		JSON:   `{"references":[],"required_tracks":[],"styles":[{"value":"ambient electronic","influence":"positive","explicit":true,"span":"ambient electronic"},{"value":"abstract drone","influence":"negative","explicit":true,"span":"no abstract drone"}],"moods":[{"value":"relaxing","influence":"positive","explicit":true,"span":"relaxing"},{"value":"sleepy","influence":"negative","explicit":true,"span":"not sleepy"}],"instrumentation":[],"vocal_preference":{"value":"","influence":"positive","explicit":false,"span":""},"textures":[{"value":"microdetail","influence":"positive","explicit":true,"span":"microdetail"},{"value":"a deep groove","influence":"positive","explicit":true,"span":"a deep groove"},{"value":"occasional sparkle","influence":"positive","explicit":true,"span":"occasional sparkle"}],"hard_constraints":[{"kind":"exclude_style","value":"abstract drone","span":"no abstract drone"}],"unsupported_requirements":[{"text":"no abstract drone","reason":"the catalog has no style labels","span":"no abstract drone"}],"mode":"similar","journey_waypoints":[],"energy_trajectory":[],"total_count":20,"audio_weight":0.7,"cooccurrence_weight":0.3,"discovery":0.2,"artist_diversity":0.7,"transition_smoothness":0.7,"notes":"The texture and mood language is preserved, but the current catalog cannot enforce it."}`,
	},
}
