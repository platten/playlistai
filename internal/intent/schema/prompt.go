package schema

// SystemPrompt instructs the model to act purely as a translator.
const SystemPrompt = `You translate a music request into a JSON intent for a playlist engine.
You do not know any songs and must never invent track or song titles.
Only put artist names, band names, or genres in "seeds" — the values the user actually named.
If the user did not name anything to seed from, return "seeds": [].
"mode" is "journey" only when the user asks to travel FROM one thing TO another; otherwise "similar".
"creativity" 0..1: higher for "adventurous", "deep cuts", "surprise me"; lower for "safe", "familiar", "the hits".
"noise" 0..1: higher for "wandering", "unpredictable", "drifting"; lower for "cohesive", "smooth", "tight".
"count" is how many tracks (default 20). "lookback" 1..10 (default 3).
"exclude_artists" lists any artist the user said to avoid. "no_repeat_artist" defaults true.
"notes" is one short sentence paraphrasing the request for the user.
Reply with ONLY the JSON object, nothing else.`

// Example is a few-shot pair: a user prompt and the exact JSON to emit.
type Example struct {
	Prompt string
	JSON   string
}

// FewShot are appended as alternating user/assistant turns before the real prompt.
var FewShot = []Example{
	{
		Prompt: "upbeat instrumental tracks like Justice, leaning 90s, about 25 songs — keep it a little unpredictable",
		JSON:   `{"seeds":["Justice"],"mode":"similar","count":25,"creativity":0.6,"noise":0.35,"lookback":3,"exclude_artists":[],"no_repeat_artist":true,"notes":"Upbeat, instrumental, 90s-leaning electronic with a bit of drift."}`,
	},
	{
		Prompt: "something safe and familiar like Fleetwood Mac and the Eagles, a dozen tracks",
		JSON:   `{"seeds":["Fleetwood Mac","the Eagles"],"mode":"similar","count":12,"creativity":0.3,"noise":0.05,"lookback":3,"exclude_artists":[],"no_repeat_artist":true,"notes":"Familiar, easygoing classic rock."}`,
	},
	{
		Prompt: "a journey from Nick Drake to Aphex Twin",
		JSON:   `{"seeds":["Nick Drake","Aphex Twin"],"mode":"journey","count":10,"creativity":0.5,"noise":0.1,"lookback":3,"exclude_artists":[],"no_repeat_artist":true,"notes":"A gradual journey from folk to electronic."}`,
	},
	{
		Prompt: "keep it going but weirder, nothing by Skrillex",
		JSON:   `{"seeds":[],"mode":"similar","count":20,"creativity":0.75,"noise":0.3,"lookback":3,"exclude_artists":["Skrillex"],"no_repeat_artist":true,"notes":"More of the same, pushed further out, no Skrillex."}`,
	},
	{
		Prompt: "deep cuts adventurous set inspired by Bonobo, one track at a time, 30 songs",
		JSON:   `{"seeds":["Bonobo"],"mode":"similar","count":30,"creativity":0.85,"noise":0.2,"lookback":1,"exclude_artists":[],"no_repeat_artist":true,"notes":"Adventurous, downtempo deep cuts around Bonobo."}`,
	},
}
