// Package multichannel implements exact multi-query retrieval, transparent
// personalized ranking, and deterministic playlist sequencing.
package multichannel

const AlgorithmVersion = "multichannel/v3"

type Config struct {
	SeedAudioBudget        int
	SeedCooccurrenceBudget int
	TasteClusterBudget     int
	MaxTasteClusters       int
	ExplorationPool        int
	ExplorationBudget      int
	ExplorationMinScore    float64
	MaxCandidates          int
	ReciprocalRankConstant float64

	RetrievalWeight         float64
	ListenerWeight          float64
	NegativePenalty         float64
	ExposurePenalty         float64
	NoveltyWeight           float64
	ExplorationChance       float64
	JourneyPositionWeight   float64
	ContinuationBudget      int
	SemanticBudget          int
	SemanticMinimumScore    float64
	SemanticWeight          float64
	SemanticNegativePenalty float64

	MMRMinimumLambda          float64
	SelectionMinimumRelevance float64
	SelectionRelevanceWindow  float64
	EmbeddingRedundancyWeight float64
	ArtistConcentrationWeight float64
	AlbumConcentrationWeight  float64
	SoftArtistSpacingMax      int
	TransitionRelevanceWeight float64
	LocalImprovementPasses    int
	LocalImprovementWindow    int
}

func DefaultConfig() Config {
	return Config{
		SeedAudioBudget: 32, SeedCooccurrenceBudget: 32,
		TasteClusterBudget: 24, MaxTasteClusters: 4,
		ExplorationPool: 160, ExplorationBudget: 24, ExplorationMinScore: .10,
		MaxCandidates: 512, ReciprocalRankConstant: 60,
		RetrievalWeight: .15, ListenerWeight: .30, NegativePenalty: .65,
		ExposurePenalty: .30, NoveltyWeight: .20,
		ExplorationChance: .35, JourneyPositionWeight: .35,
		ContinuationBudget: 16,
		SemanticBudget:     96, SemanticMinimumScore: .15, SemanticWeight: .35, SemanticNegativePenalty: .55,
		MMRMinimumLambda: .55, SelectionMinimumRelevance: .05, SelectionRelevanceWindow: .80,
		EmbeddingRedundancyWeight: .50, ArtistConcentrationWeight: .35, AlbumConcentrationWeight: .15,
		SoftArtistSpacingMax: 3, TransitionRelevanceWeight: .15,
		LocalImprovementPasses: 3, LocalImprovementWindow: 4,
	}
}

func (c Config) normalized() Config {
	d := DefaultConfig()
	if c.SeedAudioBudget <= 0 {
		c.SeedAudioBudget = d.SeedAudioBudget
	}
	if c.SeedCooccurrenceBudget <= 0 {
		c.SeedCooccurrenceBudget = d.SeedCooccurrenceBudget
	}
	if c.TasteClusterBudget <= 0 {
		c.TasteClusterBudget = d.TasteClusterBudget
	}
	if c.MaxTasteClusters <= 0 {
		c.MaxTasteClusters = d.MaxTasteClusters
	}
	if c.ExplorationPool <= 0 {
		c.ExplorationPool = d.ExplorationPool
	}
	if c.ExplorationBudget <= 0 {
		c.ExplorationBudget = d.ExplorationBudget
	}
	if c.MaxCandidates <= 0 {
		c.MaxCandidates = d.MaxCandidates
	}
	if c.ReciprocalRankConstant <= 0 {
		c.ReciprocalRankConstant = d.ReciprocalRankConstant
	}
	if c.ExplorationMinScore < -1 || c.ExplorationMinScore > 1 {
		c.ExplorationMinScore = d.ExplorationMinScore
	}
	if c.RetrievalWeight < 0 {
		c.RetrievalWeight = d.RetrievalWeight
	}
	if c.ListenerWeight < 0 {
		c.ListenerWeight = d.ListenerWeight
	}
	if c.NegativePenalty < 0 {
		c.NegativePenalty = d.NegativePenalty
	}
	if c.ExposurePenalty < 0 {
		c.ExposurePenalty = d.ExposurePenalty
	}
	if c.NoveltyWeight < 0 {
		c.NoveltyWeight = d.NoveltyWeight
	}
	if c.ExplorationChance < 0 || c.ExplorationChance > 1 {
		c.ExplorationChance = d.ExplorationChance
	}
	if c.JourneyPositionWeight < 0 {
		c.JourneyPositionWeight = d.JourneyPositionWeight
	}
	if c.ContinuationBudget <= 0 {
		c.ContinuationBudget = d.ContinuationBudget
	}
	if c.SemanticBudget <= 0 {
		c.SemanticBudget = d.SemanticBudget
	}
	if c.SemanticMinimumScore < -1 || c.SemanticMinimumScore > 1 {
		c.SemanticMinimumScore = d.SemanticMinimumScore
	}
	if c.SemanticWeight <= 0 {
		c.SemanticWeight = d.SemanticWeight
	}
	if c.SemanticNegativePenalty <= 0 {
		c.SemanticNegativePenalty = d.SemanticNegativePenalty
	}
	if c.MMRMinimumLambda <= 0 || c.MMRMinimumLambda > 1 {
		c.MMRMinimumLambda = d.MMRMinimumLambda
	}
	if c.SelectionMinimumRelevance < -2 || c.SelectionMinimumRelevance > 2 {
		c.SelectionMinimumRelevance = d.SelectionMinimumRelevance
	}
	if c.SelectionRelevanceWindow <= 0 {
		c.SelectionRelevanceWindow = d.SelectionRelevanceWindow
	}
	if c.EmbeddingRedundancyWeight < 0 {
		c.EmbeddingRedundancyWeight = d.EmbeddingRedundancyWeight
	}
	if c.ArtistConcentrationWeight < 0 {
		c.ArtistConcentrationWeight = d.ArtistConcentrationWeight
	}
	if c.AlbumConcentrationWeight < 0 {
		c.AlbumConcentrationWeight = d.AlbumConcentrationWeight
	}
	if c.SoftArtistSpacingMax < 0 {
		c.SoftArtistSpacingMax = d.SoftArtistSpacingMax
	}
	if c.TransitionRelevanceWeight < 0 {
		c.TransitionRelevanceWeight = d.TransitionRelevanceWeight
	}
	if c.LocalImprovementPasses < 0 {
		c.LocalImprovementPasses = d.LocalImprovementPasses
	}
	if c.LocalImprovementWindow <= 0 {
		c.LocalImprovementWindow = d.LocalImprovementWindow
	}
	return c
}
