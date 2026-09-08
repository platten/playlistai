package rules

import (
	"context"
	"testing"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
)

func TestCategoryJourneySeparatesGenresModifiersAndEntities(t *testing.T) {
	for _, tc := range []struct {
		prompt, start, end string
		stages             int
		rising             bool
	}{
		{"A journey from ambient to energetic electronic", "ambient", "electronic", 2, true},
		{"From energetic electronic to gentle jazz", "electronic", "jazz", 2, false},
		{"A journey from ambient to energetic electronic via mellow techno", "ambient", "electronic", 3, true},
		{"From genre vaporwave to genre gabber", "vaporwave", "gabber", 2, false},
	} {
		t.Run(tc.prompt, func(t *testing.T) {
			intent, err := New().Parse(context.Background(), ports.IntentInput{Prompt: tc.prompt})
			stages := core.JourneyCriteria(intent.EssentialCriteria)
			if err != nil || intent.Mode != core.ModeJourney || len(intent.References) != 0 || len(stages) != tc.stages || stages[0].Value != tc.start || stages[len(stages)-1].Value != tc.end {
				t.Fatalf("%+v %v", intent, err)
			}
			if tc.rising && (len(intent.Journey.EnergyTrajectory) != tc.stages || intent.Journey.EnergyTrajectory[0].Energy >= intent.Journey.EnergyTrajectory[len(intent.Journey.EnergyTrajectory)-1].Energy) {
				t.Fatal("energy destination lost")
			}
		})
	}
	for _, prompt := range []string{"From Radiohead to Muse", "From artist Ambient to artist Electronic", "From Artist - One to Other - Two"} {
		intent, _ := New().Parse(context.Background(), ports.IntentInput{Prompt: prompt})
		if len(intent.References) != 2 || len(core.JourneyCriteria(intent.EssentialCriteria)) != 0 {
			t.Fatalf("entities became genres: %+v", intent)
		}
	}
}
