package main

import (
	"context"
	"fmt"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/discoveryasset"
	"github.com/platten/playlistai/internal/intent/lexicon"
	"github.com/platten/playlistai/internal/intent/recognition"
	"github.com/platten/playlistai/internal/musicconcepts"
	"github.com/platten/playlistai/internal/ports"
)

type stageTimings struct {
	ParseMilliseconds   int64 `json:"parseMilliseconds"`
	PrepareMilliseconds int64 `json:"prepareMilliseconds"`
	BuildMilliseconds   int64 `json:"buildMilliseconds"`
}

type evidenceCoverage struct {
	SelectedTracks      int   `json:"selectedTracks"`
	GroundedSelected    int   `json:"groundedSelected"`
	CloseSelected       int   `json:"closeSelected"`
	CLAPSelected        int   `json:"clapSelected"`
	LibraryCLAPSelected int   `json:"libraryClapSelected"`
	DSPSelected         int   `json:"dspSelected"`
	MERTSelected        int   `json:"mertSelected"`
	MERTSearchable      int64 `json:"mertSearchable"`
}

type cacheState struct {
	CLAPHits        int  `json:"clapHits"`
	CLAPNewAnalyses int  `json:"clapNewAnalyses"`
	BudgetExhausted bool `json:"budgetExhausted"`
	Stopped         bool `json:"stopped"`
}

func populateEvidenceReport(r *result) {
	for _, notice := range r.Playlist.Notices {
		r.ProviderNotices = append(r.ProviderNotices, notice.Code+": "+notice.Detail)
	}
	if r.Playlist.Intent.Knowledge != nil {
		r.ProviderNotices = append(r.ProviderNotices, r.Playlist.Intent.Knowledge.Notices...)
	}
	r.EvidenceCoverage.SelectedTracks = len(r.Playlist.Tracks)
	selected := make(map[string]bool, len(r.Playlist.Tracks))
	for _, track := range r.Playlist.Tracks {
		selected[track.ID] = true
	}
	for _, track := range r.Playlist.Tracks {
		if hasGroundedMusicalEvidence(r.Playlist, track.ID) {
			r.EvidenceCoverage.GroundedSelected++
		}
		for _, assessment := range r.Playlist.Assessments {
			if assessment.TrackID == track.ID && assessment.FitTier == "close" {
				r.EvidenceCoverage.CloseSelected++
				break
			}
		}
	}
	if snapshot := r.Playlist.AudioEvidence; snapshot != nil {
		r.CacheState.CLAPHits = snapshot.CacheHits
		r.CacheState.CLAPNewAnalyses = snapshot.NewAnalyses
		r.CacheState.BudgetExhausted = snapshot.BudgetExhausted
		r.CacheState.Stopped = snapshot.Stopped
		for _, assessment := range snapshot.Assessments {
			if selected[assessment.TrackID] && assessment.Eligible && assessment.AnalysisID != "" {
				r.EvidenceCoverage.CLAPSelected++
			}
		}
		for _, assessment := range snapshot.LibraryAssessments {
			if selected[assessment.TrackID] && assessment.Eligible {
				r.EvidenceCoverage.LibraryCLAPSelected++
			}
		}
	}
	if snapshot := r.Playlist.EnhancedAudio; snapshot != nil {
		input := snapshot.Input()
		for id := range input.DSP {
			if selected[id] {
				r.EvidenceCoverage.DSPSelected++
			}
		}
		for id := range input.Representations {
			if selected[id] {
				r.EvidenceCoverage.MERTSelected++
			}
		}
		if input.MERTSearch != nil {
			r.EvidenceCoverage.MERTSearchable = input.MERTSearch.SearchableTracks
		}
	}
}

// hasGroundedMusicalEvidence accepts only request-comparison evidence. Generic
// retrieval, taste, novelty and MERT-neighbor scores cannot turn an unsupported
// recording into benchmark padding. A positive catalog-metadata/DSP/semantic
// comparison may support an honestly labeled close result without being
// misreported as CLAP or categorical proof.
func hasGroundedMusicalEvidence(playlist core.Playlist, id string) bool {
	if playlist.AudioEvidence != nil {
		for _, assessment := range playlist.AudioEvidence.Assessments {
			if assessment.TrackID == id && assessment.Eligible && assessment.AnalysisID != "" {
				return true
			}
		}
		for _, assessment := range playlist.AudioEvidence.LibraryAssessments {
			if assessment.TrackID == id && assessment.Eligible {
				return true
			}
		}
	}
	for _, assessment := range playlist.Assessments {
		if assessment.TrackID == id && assessment.State == core.EvidenceMatch && assessment.FitTier == "strong" {
			return true
		}
	}
	for _, reason := range playlist.Rationale {
		if reason.TrackID != id {
			continue
		}
		for _, evidence := range reason.Evidence {
			if !evidence.Available || evidence.Score <= 0 {
				continue
			}
			switch evidence.Component {
			case "library_metadata", "library_dsp_preference", "dsp_soft_preference", "semantic_text_match", "acousticbrainz_intent":
				return true
			}
		}
	}
	return false
}

func selectedCases(cases []promptCase, prompt string) []promptCase {
	if prompt == "" {
		return cases
	}
	selected := make([]promptCase, 0, 1)
	for _, c := range cases {
		if c.Prompt == prompt {
			selected = append(selected, c)
		}
	}
	return selected
}

func prepareIntentInput(ctx context.Context, prompt string, discovery *discoveryasset.Manager) ports.IntentInput {
	source := lexicon.Extract(prompt)
	var lookup recognition.IdentityLookup
	snapshot := "unavailable"
	if discovery != nil {
		if packs, release, err := discovery.Pin(ctx); err == nil {
			defer release()
			for _, pack := range packs {
				lookup = recognition.Combine(lookup, pack)
			}
			if lookup != nil {
				identity := lookup.SnapshotIdentity()
				snapshot = identity.IndexVersion + ":" + identity.Snapshot
			}
		}
	}
	source = recognition.Apply(ctx, prompt, source, lookup, nil)
	return ports.IntentInput{
		Prompt: prompt, SourceFacts: &source,
		RecognitionIdentity: fmt.Sprintf("%s|%s|embedded|%s", recognition.Version, musicconcepts.Version, snapshot),
	}
}
