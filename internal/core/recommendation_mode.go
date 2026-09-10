package core

// RecommendationMode is a user-selected execution policy, never an LLM choice.
// Empty legacy records use the desktop default; standalone ranking treats empty
// as AcousticBrainz-first. New desktop requests pin the selected mode in history.
type RecommendationMode string

const (
	AcousticBrainzFirst RecommendationMode = "acousticbrainz_first"
	CLAPFirst           RecommendationMode = "clap_first"
	DeejAIOnly          RecommendationMode = "deejai_only"
)

func (m RecommendationMode) Valid() bool {
	return m == "" || m == AcousticBrainzFirst || m == CLAPFirst || m == DeejAIOnly
}
