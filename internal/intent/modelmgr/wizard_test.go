package modelmgr

import (
	"reflect"
	"testing"
)

func TestWizardIncludesSmallestDownloadWithOneRecommendation(t *testing.T) {
	models := []Model{
		{ID: "large", SizeApprox: 8 << 30, Recommended: true},
		{ID: "medium", SizeApprox: 4 << 30, Recommended: true},
		{ID: "small", SizeApprox: 2 << 30, Recommended: true},
		{ID: "smallest", SizeApprox: 1 << 30},
		{ID: "unknown"},
	}
	original := append([]Model(nil), models...)
	for _, tc := range []struct {
		name        string
		hw          Hardware
		ids         []string
		recommended string
	}{
		{"cpu", Hardware{}, []string{"smallest"}, "smallest"},
		{"large gpu", Hardware{GPUAvailable: true, AvailableVRAMBytes: 9 << 30, ReserveBytes: 1 << 30}, []string{"large", "smallest"}, "large"},
		{"small gpu", Hardware{GPUAvailable: true, AvailableVRAMBytes: 3 << 30, ReserveBytes: 1 << 30}, []string{"small", "smallest"}, "small"},
		{"only smallest fits", Hardware{GPUAvailable: true, AvailableVRAMBytes: 2 << 30, ReserveBytes: 1 << 30}, []string{"smallest"}, "smallest"},
		{"none fits", Hardware{GPUAvailable: true, AvailableVRAMBytes: 1 << 30, ReserveBytes: 1 << 30}, []string{"smallest"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := WizardModels(models, tc.hw)
			if len(got) != len(tc.ids) {
				t.Fatalf("choices: %+v", got)
			}
			for i, model := range got {
				if model.ID != tc.ids[i] || model.Recommended != (model.ID == tc.recommended) {
					t.Fatalf("unexpected choice/badge: %+v", model)
				}
			}
			if !reflect.DeepEqual(models, original) {
				t.Fatal("wizard mutated the shared catalog")
			}
		})
	}
}

func TestWizardSmallestRecommendedModelIsNotDuplicated(t *testing.T) {
	models := []Model{{ID: "only", SizeApprox: 1 << 30, Recommended: true}}
	got := WizardModels(models, Hardware{})
	if len(got) != 1 || got[0].ID != "only" || !got[0].Recommended {
		t.Fatalf("choices: %+v", got)
	}
	if got := WizardModels(nil, Hardware{}); len(got) != 0 {
		t.Fatalf("empty catalog: %+v", got)
	}
}

func TestSettingsBadgesUseActualHardwareAndPinnedWeightSize(t *testing.T) {
	models := []Model{{ID: "large", Size: 8 << 30, SizeApprox: 1 << 30, Recommended: true}, {ID: "medium", Size: 4 << 30, Recommended: true}, {ID: "smallest", Size: 1 << 30}}
	original := append([]Model(nil), models...)
	for _, tc := range []struct {
		name string
		hw   Hardware
		want string
	}{
		{"CPU", Hardware{}, "smallest"},
		{"fits with headroom", Hardware{GPUAvailable: true, TotalVRAMBytes: 6 << 30, AvailableVRAMBytes: 6 << 30, ReserveBytes: 1 << 30}, "medium"},
		{"free memory cannot exceed total", Hardware{GPUAvailable: true, TotalVRAMBytes: 3 << 30, AvailableVRAMBytes: 20 << 30, ReserveBytes: 1 << 30}, "smallest"},
		{"busy GPU", Hardware{GPUAvailable: true, TotalVRAMBytes: 12 << 30, AvailableVRAMBytes: 1 << 30, ReserveBytes: 1 << 30}, ""},
		{"unknown free memory", Hardware{GPUAvailable: true, TotalVRAMBytes: 12 << 30}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CatalogForHardware(models, tc.hw)
			if len(got) != len(models) {
				t.Fatal("manual choices lost")
			}
			for _, m := range got {
				if m.Recommended != (m.ID == tc.want) {
					t.Fatalf("unexpected badge %+v; want %q", m, tc.want)
				}
			}
			if !reflect.DeepEqual(models, original) {
				t.Fatal("static catalog mutated")
			}
		})
	}
}
