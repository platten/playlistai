// Package multichannel implements exact multi-query retrieval, transparent
// personalized ranking, and deterministic playlist sequencing.
package multichannel

const AlgorithmVersion = "multichannel/v1"

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

	RetrievalWeight       float64
	ListenerWeight        float64
	NegativePenalty       float64
	ExposurePenalty       float64
	NoveltyWeight         float64
	ExplorationChance     float64
	ExplorationPickPool   int
	JourneyPositionWeight float64
}

func DefaultConfig() Config {
	return Config{
		SeedAudioBudget: 32, SeedCooccurrenceBudget: 32,
		TasteClusterBudget: 24, MaxTasteClusters: 4,
		ExplorationPool: 160, ExplorationBudget: 24, ExplorationMinScore: .10,
		MaxCandidates: 512, ReciprocalRankConstant: 60,
		RetrievalWeight: .15, ListenerWeight: .30, NegativePenalty: .65,
		ExposurePenalty: .30, NoveltyWeight: .20,
		ExplorationChance: .35, ExplorationPickPool: 12, JourneyPositionWeight: .35,
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
	if c.ExplorationPickPool <= 0 {
		c.ExplorationPickPool = d.ExplorationPickPool
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
	return c
}
