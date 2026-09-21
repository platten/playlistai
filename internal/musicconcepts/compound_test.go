package musicconcepts

import "testing"

func TestCompoundGenreLeadsRequireReviewedDiscovery(t *testing.T) {
	got := CompoundGenreLeads("genre", "ambient electronica")
	if len(got) == 0 || got[0][0] != "ambient" || got[0][1] != "electronic" {
		t.Fatalf("reviewed discovery pair missing: %v", got)
	}
	for _, value := range []string{"melodic house", "unreviewed electric", "ambient"} {
		if got := CompoundGenreLeads("genre", value); len(got) > 0 {
			t.Fatalf("unreviewed partial category %q: %v", value, got)
		}
	}
}
