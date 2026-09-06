package schema

// SystemPrompt instructs the model to act purely as a translator.
const SystemPrompt = `You translate a music request into a JSON intent for a playlist engine.
You do not know any songs and must never invent track or song titles.
The request must name at least one seed artist or band to start from. Put the
artist/band name(s) the user actually named into "seeds" — verbatim, nothing
you made up. If the user genuinely named nothing to seed from, return
"seeds": [] (the engine will then ask them to name an artist).
"required_tracks" contains only artist/title tracks the user explicitly says
must appear; never invent one. Reference seeds guide the result but need not appear.
"mode" is "journey" only when the user asks to travel FROM one thing TO another; otherwise "similar".
"creativity" 0..1: higher for "adventurous", "deep cuts", "surprise me"; lower for "safe", "familiar", "the hits".
"noise" 0..1: higher for "wandering", "unpredictable", "drifting"; lower for "cohesive", "smooth", "tight".
"count" is how many tracks (default 20). "lookback" 1..10 (default 3).
"exclude_artists" lists any artist the user said to avoid. "no_repeat_artist" defaults true.
"exclude_seed_artists" is true only when the user asks not to play other tracks
by the reference artists.
"notes" is one short sentence paraphrasing the request for the user.
Reply with ONLY the JSON object, nothing else.`

// Example is a few-shot pair: a user prompt and the exact JSON to emit.
type Example struct {
	Prompt string
	JSON   string
}

// FewShot are appended as alternating user/assistant turns before the real
// prompt. Kept short (3) so the first, uncached request isn't dominated by
// prompt processing on CPU-only machines.
var FewShot = []Example{
	{
		Prompt: "upbeat instrumental tracks like Justice, leaning 90s, about 25 songs — keep it a little unpredictable",
		JSON:   `{"seeds":["Justice"],"required_tracks":[],"mode":"similar","count":25,"creativity":0.6,"noise":0.35,"lookback":3,"exclude_artists":[],"no_repeat_artist":true,"exclude_seed_artists":false,"notes":"Upbeat, instrumental, 90s-leaning electronic with a bit of drift."}`,
	},
	{
		Prompt: "a journey from Nick Drake to Aphex Twin",
		JSON:   `{"seeds":["Nick Drake","Aphex Twin"],"required_tracks":[],"mode":"journey","count":10,"creativity":0.5,"noise":0.1,"lookback":3,"exclude_artists":[],"no_repeat_artist":true,"exclude_seed_artists":false,"notes":"A gradual journey from folk to electronic."}`,
	},
	{
		Prompt: "deep cuts inspired by Bonobo, nothing by Skrillex, one track at a time, 30 songs",
		JSON:   `{"seeds":["Bonobo"],"required_tracks":[],"mode":"similar","count":30,"creativity":0.85,"noise":0.2,"lookback":1,"exclude_artists":["Skrillex"],"no_repeat_artist":true,"exclude_seed_artists":false,"notes":"Adventurous, downtempo deep cuts around Bonobo, no Skrillex."}`,
	},
}
