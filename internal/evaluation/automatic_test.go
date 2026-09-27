package evaluation

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func automaticFixtureCorpus() AutomaticCorpus {
	c := AutomaticCorpus{Version: AutomaticCorpusVersion, Seed: "fixture", AnnotationsSHA256: automaticSHA([]byte("labels")), AudioHashesSHA256: automaticSHA([]byte("audio")), LowAudioHashesSHA256: automaticSHA([]byte("low"))}
	for i := 0; i < 600; i++ {
		id := i + 100000
		split, label := "development", "instrumental"
		if i >= 300 {
			split = "heldout"
		}
		if i%300 >= 150 {
			label = "voice"
		}
		labels := map[string]string{"voice_instrumental": label}
		for _, facet := range AutomaticEvaluationFacets()[1:] {
			labels[facet] = "target"
			if label == "voice" {
				labels[facet] = "other"
			}
		}
		c.Tracks = append(c.Tracks, AutomaticCorpusTrack{ID: fmt.Sprintf("track_%d", id), ArtistID: fmt.Sprintf("artist_%d", id), AlbumID: fmt.Sprintf("album_%d", id), AudioPath: fmt.Sprintf("%02d/%d.mp3", id%100, id), AudioSHA256: automaticSHA([]byte(fmt.Sprintf("full:%d", id))), LowAudioSHA256: automaticSHA([]byte(fmt.Sprintf("low:%d", id))), Duration: 100, Split: split, Labels: labels})
	}
	return c
}

func automaticFixtureMeasurements(c AutomaticCorpus) AutomaticMeasurements {
	m := AutomaticMeasurements{Version: AutomaticEvaluationVersion, CorpusSHA256: AutomaticCorpusSHA256(c), ProducerSourceSHA256: automaticSHA([]byte("source")), PolicySHA256: automaticSHA([]byte("policy")), FeatureSnapshotSHA256: automaticSHA([]byte("features")), PolicyFrozen: true}
	var pool, positives, negatives []string
	artists := map[string]string{}
	for _, track := range c.Tracks {
		if track.Split != "heldout" {
			continue
		}
		pool = append(pool, track.ID)
		artists[track.ID] = track.ArtistID
		if track.Labels["voice_instrumental"] == "instrumental" {
			positives = append(positives, track.ID)
		} else {
			negatives = append(negatives, track.ID)
		}
	}
	for _, facet := range AutomaticEvaluationFacets() {
		count, value := 1, "target"
		if facet == "voice_instrumental" {
			count, value = 8, "instrumental"
		}
		for repeat := 0; repeat < count; repeat++ {
			for _, variant := range []string{"metadata", "audio", "combined"} {
				for _, cache := range []string{"cold_provider", "warm_installed"} {
					ms, facts := int64(1000), 0
					r := AutomaticRun{TaskID: fmt.Sprintf("%s-%d", facet, repeat), Split: "heldout", Variant: variant, CacheCondition: cache, Seed: fmt.Sprint(repeat), CandidateIDs: pool, CandidateSHA256: AutomaticCandidateSHA256(pool), Facet: facet, Value: value, Requested: 10, Retrieved: append(append([]string(nil), positives...), negatives...), StrongAdmissions: positives, Milliseconds: &ms, FactualViolations: &facts, FactualChecker: "fixture-independent-checker"}
					for _, id := range positives[repeat*10 : repeat*10+10] {
						r.Output = append(r.Output, AutomaticOutput{ID: id, ArtistID: artists[id]})
					}
					m.Runs = append(m.Runs, r)
				}
			}
		}
	}
	return m
}

func cloneAutomaticMeasurements(m AutomaticMeasurements) AutomaticMeasurements {
	raw, _ := json.Marshal(m)
	var out AutomaticMeasurements
	_ = json.Unmarshal(raw, &out)
	return out
}

func TestAutomaticEvaluationGatesAndPairedAblations(t *testing.T) {
	c := automaticFixtureCorpus()
	m := automaticFixtureMeasurements(c)
	for _, tc := range []struct {
		name    string
		change  func(*AutomaticMeasurements)
		state   string
		invalid bool
	}{
		{"complete synthetic fixture", nil, "pass", false},
		{"one operation beyond two minutes", func(m *AutomaticMeasurements) {
			for i := range m.Runs {
				if m.Runs[i].Variant == "combined" {
					value := int64(120001)
					m.Runs[i].Milliseconds = &value
					break
				}
			}
		}, "fail", false},
		{"baseline failure allowed", func(m *AutomaticMeasurements) {
			for i := range m.Runs {
				if m.Runs[i].Variant == "metadata" {
					m.Runs[i].Output = nil
				}
			}
		}, "pass", false},
		{"missing all observations", func(m *AutomaticMeasurements) { m.Runs = nil }, "insufficient", false},
		{"missing paired baseline", func(m *AutomaticMeasurements) { m.Runs = m.Runs[1:] }, "insufficient", false},
		{"unfrozen policy", func(m *AutomaticMeasurements) { m.PolicyFrozen = false }, "insufficient", false},
		{"different ablation seed", func(m *AutomaticMeasurements) { m.Runs[0].Seed = "changed" }, "", true},
		{"different candidate pool", func(m *AutomaticMeasurements) {
			m.Runs[0].CandidateIDs[0] = "invented"
			m.Runs[0].CandidateSHA256 = AutomaticCandidateSHA256(m.Runs[0].CandidateIDs)
		}, "", true},
		{"duplicate task", func(m *AutomaticMeasurements) { m.Runs = append(m.Runs, m.Runs[0]) }, "", true},
		{"missing timing", func(m *AutomaticMeasurements) { m.Runs[4].Milliseconds = nil }, "insufficient", false},
		{"missing baseline observation", func(m *AutomaticMeasurements) { m.Runs[0].Milliseconds = nil }, "insufficient", false},
		{"fixed pool order is not retrieval recall", func(m *AutomaticMeasurements) {
			for i := range m.Runs {
				m.Runs[i].RetrievalScope = "fixed_pool_order"
			}
		}, "insufficient", false},
		{"prepared engine is not whole application timing", func(m *AutomaticMeasurements) {
			for i := range m.Runs {
				m.Runs[i].TimingScope = "prepared_engine"
			}
		}, "insufficient", false},
		{"slow cold run", func(m *AutomaticMeasurements) {
			for i := range m.Runs {
				if m.Runs[i].Variant == "combined" && m.Runs[i].CacheCondition == "cold_provider" {
					n := int64(120001)
					m.Runs[i].Milliseconds = &n
				}
			}
		}, "fail", false},
		{"wrong output artist", func(m *AutomaticMeasurements) { m.Runs[4].Output[0].ArtistID = "wrong" }, "fail", false},
		{"duplicate output", func(m *AutomaticMeasurements) { m.Runs[4].Output[1] = m.Runs[4].Output[0] }, "fail", false},
		{"no factual checker", func(m *AutomaticMeasurements) { m.Runs[4].FactualChecker = "" }, "insufficient", false},
		{"unknown admission", func(m *AutomaticMeasurements) {
			m.Runs[4].StrongAdmissions = append(m.Runs[4].StrongAdmissions, "unknown")
		}, "insufficient", false},
		{"repeated samples do not increase vocal n", func(m *AutomaticMeasurements) {
			for i := range m.Runs {
				if m.Runs[i].Facet == "voice_instrumental" {
					m.Runs[i].Output = append([]AutomaticOutput(nil), m.Runs[0].Output...)
				}
			}
		}, "fail", false},
		{"no output is not success", func(m *AutomaticMeasurements) {
			for i := range m.Runs {
				if m.Runs[i].Variant == "combined" {
					m.Runs[i].Output = nil
					m.Runs[i].Error = "deadline"
				}
			}
		}, "fail", false},
		{"voice only cannot pass general fit", func(m *AutomaticMeasurements) {
			var kept []AutomaticRun
			for _, r := range m.Runs {
				if r.Facet == "voice_instrumental" {
					kept = append(kept, r)
				}
			}
			m.Runs = kept
		}, "insufficient", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := cloneAutomaticMeasurements(m)
			if tc.change != nil {
				tc.change(&candidate)
			}
			report, err := EvaluateAutomatic(c, candidate)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid pairing accepted")
				}
				return
			}
			if err != nil || report.State != tc.state {
				t.Fatalf("state=%s err=%v results=%+v", report.State, err, report.Results)
			}
		})
	}
}

func TestAutomaticUnknownFacetCannotBecomeNegativeOrPass(t *testing.T) {
	c := automaticFixtureCorpus()
	delete(c.Tracks[300].Labels, "genre_dortmund")
	m := automaticFixtureMeasurements(c)
	report, err := EvaluateAutomatic(c, m)
	if err != nil || report.State != "insufficient" {
		t.Fatalf("unknown label: %s %v", report.State, err)
	}
	for _, result := range report.Results {
		if result.Split == "heldout" && result.Variant == "combined" {
			for _, facet := range result.Facets {
				if facet.Facet == "genre_dortmund" && facet.Gates[1].State != "insufficient" {
					t.Fatal("partial genre pool used as exhaustive negatives")
				}
			}
		}
	}
}

func TestAutomaticReportsErrorsWithoutDiscardingUsableObservations(t *testing.T) {
	c := automaticFixtureCorpus()
	m := automaticFixtureMeasurements(c)
	for i := range m.Runs {
		if m.Runs[i].Variant == "combined" {
			m.Runs[i].Error = "usable result retained after generation error"
		}
	}
	report, err := EvaluateAutomatic(c, m)
	if err != nil || report.State != "pass" {
		t.Fatalf("usable observations lost: %s %v", report.State, err)
	}
	for _, result := range report.Results {
		want := 0
		if result.Split == "heldout" && result.Variant == "combined" {
			want = result.Runs
		}
		if result.RunsWithErrors != want {
			t.Fatalf("%s/%s: error runs %d want %d", result.Split, result.Variant, result.RunsWithErrors, want)
		}
	}
}

func TestClopperPearsonUpper95(t *testing.T) {
	for _, tc := range []struct {
		k, n int
		want float64
	}{{0, 59, 1 - math.Pow(.05, 1.0/59)}, {0, 58, 1 - math.Pow(.05, 1.0/58)}, {1, 1, 1}, {1, 2, math.Sqrt(.95)}} {
		got, err := ClopperPearsonUpper95(tc.k, tc.n)
		if err != nil || math.Abs(got-tc.want) > 1e-12 {
			t.Fatalf("%d/%d=%f want %f err=%v", tc.k, tc.n, got, tc.want, err)
		}
	}
	if v, _ := ClopperPearsonUpper95(0, 59); v > .05 {
		t.Fatal("59 clean observations should clear 5%")
	}
	if v, _ := ClopperPearsonUpper95(0, 58); v <= .05 {
		t.Fatal("58 clean observations cannot clear 5%")
	}
	if _, err := ClopperPearsonUpper95(0, 0); err == nil {
		t.Fatal("empty observations have no bound")
	}
}

func TestAutomaticCorpusSourceSelectionAndLeakage(t *testing.T) {
	fixture := automaticFixtureCorpus()
	var rows, full, low []string
	for _, track := range fixture.Tracks {
		rows = append(rows, fmt.Sprintf("%s\t%s\t%s\t%s\t100\tvoice_instrumental---%s,%s,%s\tgenre_dortmund---rock,rock\tmood_relaxed---relaxed,relaxed,relaxed", track.ID, track.ArtistID, track.AlbumID, track.AudioPath, track.Labels["voice_instrumental"], track.Labels["voice_instrumental"], track.Labels["voice_instrumental"]))
		full = append(full, track.AudioSHA256+" "+track.AudioPath)
		low = append(low, track.LowAudioSHA256+" "+strings.TrimSuffix(track.AudioPath, ".mp3")+".low.mp3")
	}
	build := func(rows []string) (AutomaticCorpus, error) {
		return BuildAutomaticCorpus([]byte("TRACK_ID\tARTIST_ID\tALBUM_ID\tPATH\tDURATION\tANNOTATIONS\n"+strings.Join(rows, "\n")+"\n"), []byte(strings.Join(full, "\n")), []byte(strings.Join(low, "\n")), "frozen")
	}
	a, err := build(rows)
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	b, err := build(rows)
	if err != nil || !reflect.DeepEqual(a.Tracks, b.Tracks) {
		t.Fatal("provider row reorder changed deterministic cohort")
	}
	for _, track := range a.Tracks {
		if track.Labels["genre_dortmund"] != "" || track.Labels["mood_relaxed"] != "relaxed" {
			t.Fatal("nonunanimous labels accepted or unanimous lost")
		}
	}
	a.Tracks[1].ArtistID = a.Tracks[0].ArtistID
	if a.Validate() == nil {
		t.Fatal("artist leakage accepted")
	}
	a = b
	a.Tracks[1].AudioSHA256 = a.Tracks[0].AudioSHA256
	if a.Validate() == nil {
		t.Fatal("audio duplicate leakage accepted")
	}
	rows[0] = strings.Replace(rows[0], "voice,voice,voice", "voice,voice", 1)
	rows[0] = strings.Replace(rows[0], "instrumental,instrumental,instrumental", "instrumental,instrumental", 1)
	if _, err := build(rows); err == nil {
		t.Fatal("insufficient exactly-three labels passed")
	}
}
