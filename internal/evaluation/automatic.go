package evaluation

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
)

const AutomaticEvaluationVersion = "automatic-evaluation/v2"

// These are independent published taxonomies applicable to supported musical
// facets. Their granularity does not establish arbitrary subgenres or textures.
func AutomaticEvaluationFacets() []string {
	return []string{"voice_instrumental", "genre_dortmund", "mood_acoustic", "mood_electronic", "mood_relaxed", "danceability"}
}

// AutomaticMeasurements contains observations from an external, real feature
// producer. Labels are read exclusively from the separately frozen corpus.
type AutomaticMeasurements struct {
	Version               string         `json:"version"`
	CorpusSHA256          string         `json:"corpusSHA256"`
	ProducerSourceSHA256  string         `json:"producerSourceSHA256"`
	PolicySHA256          string         `json:"policySHA256"`
	FeatureSnapshotSHA256 string         `json:"featureSnapshotSHA256"`
	PolicyFrozen          bool           `json:"policyFrozen"`
	Runs                  []AutomaticRun `json:"runs"`
}

type AutomaticRun struct {
	TaskID            string            `json:"taskId"`
	Split             string            `json:"split"`
	Variant           string            `json:"variant"`
	CacheCondition    string            `json:"cacheCondition"`
	Seed              string            `json:"seed"`
	CandidateIDs      []string          `json:"candidateIds"`
	CandidateSHA256   string            `json:"candidateSHA256"`
	Facet             string            `json:"facet"`
	Value             string            `json:"value"`
	Requested         int               `json:"requested"`
	Retrieved         []string          `json:"retrieved"`
	RetrievalScope    string            `json:"retrievalScope,omitempty"`
	StrongAdmissions  []string          `json:"strongAdmissions"`
	Output            []AutomaticOutput `json:"output"`
	Milliseconds      *int64            `json:"milliseconds"`
	TimingScope       string            `json:"timingScope,omitempty"`
	FactualViolations *int              `json:"factualViolations"`
	FactualChecker    string            `json:"factualChecker"`
	Error             string            `json:"error,omitempty"`
}

type AutomaticOutput struct {
	ID       string `json:"id"`
	ArtistID string `json:"artistId"`
}

type AutomaticGate struct {
	Name        string   `json:"name"`
	State       string   `json:"state"` // pass, fail, insufficient
	Value       *float64 `json:"value"`
	Target      float64  `json:"target"`
	Numerator   int      `json:"numerator"`
	Denominator int      `json:"denominator"`
	Unknown     int      `json:"unknown"`
}

type AutomaticVariantResult struct {
	Split          string                 `json:"split"`
	Variant        string                 `json:"variant"`
	Runs           int                    `json:"runs"`
	RunsWithErrors int                    `json:"runsWithErrors"`
	State          string                 `json:"state"`
	Gates          []AutomaticGate        `json:"gates"`
	Facets         []AutomaticFacetResult `json:"facets"`
}

type AutomaticFacetResult struct {
	Facet string          `json:"facet"`
	Runs  int             `json:"runs"`
	Gates []AutomaticGate `json:"gates"`
}

type AutomaticReport struct {
	Version            string                   `json:"version"`
	CorpusSHA256       string                   `json:"corpusSHA256"`
	MeasurementsSHA256 string                   `json:"measurementsSHA256"`
	PolicySHA256       string                   `json:"policySHA256"`
	PolicyFrozen       bool                     `json:"policyFrozen"`
	State              string                   `json:"state"`
	Results            []AutomaticVariantResult `json:"results"`
	Limitations        []string                 `json:"limitations"`
}

type automaticTotals struct {
	runs, strong, strongGood, strongUnknown, recallHits, relevant, recallUnknown int
	runsWithErrors                                                               int
	requested, good, outputUnknown, violations, factsUnknown                     int
	latency                                                                      map[string][]int64
	latencyUnknown                                                               map[string]int
	admissions                                                                   map[string]bool
	vocalOutputs                                                                 map[string]bool
	vocalUnknown                                                                 int
}

// EvaluateAutomatic does not synthesize predictions, human grades, or provider
// evidence. Missing or unpaired measurements cannot produce a passing report.
func EvaluateAutomatic(c AutomaticCorpus, m AutomaticMeasurements) (AutomaticReport, error) {
	measurementJSON, _ := json.Marshal(m)
	report := AutomaticReport{Version: AutomaticEvaluationVersion, CorpusSHA256: AutomaticCorpusSHA256(c), MeasurementsSHA256: automaticSHA(measurementJSON), PolicySHA256: m.PolicySHA256, PolicyFrozen: m.PolicyFrozen, State: "insufficient", Limitations: []string{
		"Automated benchmark only; existing blind listening packets and human grades are untouched.",
		"Only unanimous published taxonomy labels are targets. Missing labels are unknown, not negative examples.",
		"Vocal confidence uses distinct returned no-vocals recordings per variant/split, not repeated cache/seed occurrences.",
		"Artist and encoded-audio checksum separation does not prove perceptual deduplication or absence from pretrained model training data.",
		"Latency includes only supplied measured end-to-end runs; installed assets and cache conditions must be documented by the producer.",
		"Known identity and duplicates are checked here; other factual constraints require the named external checker and explicit measured count.",
		"Runs with errors are reported separately. Usable observations still count toward the stated gates; a passing gate does not establish error-free execution.",
	}}
	if err := c.Validate(); err != nil {
		return report, err
	}
	if m.Version != AutomaticEvaluationVersion || m.CorpusSHA256 != report.CorpusSHA256 || !automaticValidSHA(m.ProducerSourceSHA256) || !automaticValidSHA(m.PolicySHA256) || !automaticValidSHA(m.FeatureSnapshotSHA256) {
		return report, errors.New("automatic evaluation: missing or mismatched corpus/source/policy/features identity")
	}
	tracks := map[string]AutomaticCorpusTrack{}
	pools := map[string][]string{}
	for _, track := range c.Tracks {
		tracks[track.ID] = track
		pools[track.Split] = append(pools[track.Split], track.ID)
	}
	totals := map[string]*automaticTotals{}
	facets := map[string]*automaticTotals{}
	groups := map[string]map[string]AutomaticRun{}
	for _, run := range m.Runs {
		if err := validateAutomaticRun(run, pools); err != nil {
			return report, err
		}
		group := run.Split + "\x00" + run.TaskID
		if groups[group] == nil {
			groups[group] = map[string]AutomaticRun{}
		}
		key := run.Variant + "\x00" + run.CacheCondition
		if _, exists := groups[group][key]; exists {
			return report, errors.New("automatic evaluation: duplicate task/variant/cache run")
		}
		for _, previous := range groups[group] {
			if run.Seed != previous.Seed || run.CandidateSHA256 != previous.CandidateSHA256 || run.Facet != previous.Facet || run.Value != previous.Value || run.Requested != previous.Requested {
				return report, errors.New("automatic evaluation: ablations/cache repeats differ in candidates, seed, or task")
			}
		}
		groups[group][key] = run
		key = run.Split + "\x00" + run.Variant
		if totals[key] == nil {
			totals[key] = &automaticTotals{latency: map[string][]int64{}, latencyUnknown: map[string]int{}, admissions: map[string]bool{}, vocalOutputs: map[string]bool{}}
		}
		accumulateAutomatic(totals[key], run, tracks)
		key += "\x00" + run.Facet
		if facets[key] == nil {
			facets[key] = &automaticTotals{latency: map[string][]int64{}, latencyUnknown: map[string]int{}, admissions: map[string]bool{}, vocalOutputs: map[string]bool{}}
		}
		accumulateAutomatic(facets[key], run, tracks)
	}
	paired := map[string]bool{"development": true, "heldout": true}
	for _, group := range groups {
		for _, run := range group {
			if len(group) != 6 || run.Milliseconds == nil || run.TimingScope != "" && run.TimingScope != "whole_application" || run.FactualViolations == nil || run.FactualChecker == "" {
				paired[run.Split] = false
			}
		}
	}
	heldoutPass := m.PolicyFrozen
	anyFail := false
	for _, split := range []string{"development", "heldout"} {
		for _, variant := range []string{"metadata", "audio", "combined"} {
			t := totals[split+"\x00"+variant]
			if t == nil {
				t = &automaticTotals{}
			}
			result := automaticResult(split, variant, t, paired[split])
			for _, facet := range AutomaticEvaluationFacets() {
				f := facets[split+"\x00"+variant+"\x00"+facet]
				if f == nil {
					f = &automaticTotals{}
				}
				gates := []AutomaticGate{
					automaticRatioGate("strong_admission_precision", f.strongGood, f.strong, f.strongUnknown, .90),
					automaticRatioGate("recall_at_200", f.recallHits, f.relevant, f.recallUnknown, .90),
					automaticRatioGate("good_per_requested_slot", f.good, f.requested, f.outputUnknown, .80),
				}
				result.Facets = append(result.Facets, AutomaticFacetResult{Facet: facet, Runs: f.runs, Gates: gates})
				for _, gate := range gates {
					if gate.State == "fail" {
						result.State = "fail"
					} else if gate.State == "insufficient" && result.State != "fail" {
						result.State = "insufficient"
					}
				}
			}
			report.Results = append(report.Results, result)
			if split == "heldout" && variant == "combined" {
				heldoutPass = heldoutPass && result.State == "pass"
				anyFail = anyFail || result.State == "fail"
			}
		}
	}
	if anyFail {
		report.State = "fail"
	} else if heldoutPass {
		report.State = "pass"
	}
	return report, nil
}

func validateAutomaticRun(r AutomaticRun, pools map[string][]string) error {
	if r.TaskID == "" || r.Seed == "" || r.Facet == "" || r.Value == "" || r.Requested < 1 || r.Requested > 300 || (r.Split != "development" && r.Split != "heldout") || (r.Variant != "metadata" && r.Variant != "audio" && r.Variant != "combined") || (r.CacheCondition != "cold_provider" && r.CacheCondition != "warm_installed") {
		return errors.New("automatic evaluation: invalid task identity/variant/cache condition")
	}
	if len(r.CandidateIDs) != 300 || r.CandidateSHA256 != AutomaticCandidateSHA256(r.CandidateIDs) || r.CandidateSHA256 != AutomaticCandidateSHA256(pools[r.Split]) {
		return errors.New("automatic evaluation: candidate pool must be exactly the frozen 300-recording split")
	}
	if (r.Milliseconds != nil && *r.Milliseconds <= 0) || (r.FactualViolations != nil && *r.FactualViolations < 0) {
		return errors.New("automatic evaluation: invalid measured time or violation count")
	}
	return nil
}

func accumulateAutomatic(t *automaticTotals, run AutomaticRun, tracks map[string]AutomaticCorpusTrack) {
	t.runs++
	if run.Error != "" {
		t.runsWithErrors++
	}
	if run.Milliseconds == nil || run.TimingScope != "" && run.TimingScope != "whole_application" {
		t.latencyUnknown[run.CacheCondition]++
	} else {
		t.latency[run.CacheCondition] = append(t.latency[run.CacheCondition], *run.Milliseconds)
	}
	if run.FactualViolations == nil || run.FactualChecker == "" {
		t.factsUnknown++
	} else {
		t.violations += *run.FactualViolations
	}
	label := func(id string) string {
		track, ok := tracks[id]
		if !ok || track.Split != run.Split {
			return ""
		}
		return track.Labels[run.Facet]
	}
	strongSeen := map[string]bool{}
	for _, id := range run.StrongAdmissions {
		if strongSeen[id] {
			t.violations++
			continue
		}
		strongSeen[id] = true
		key := run.Facet + "\x00" + run.Value + "\x00" + id
		if t.admissions[key] {
			continue
		}
		t.admissions[key] = true
		t.strong++
		if known := label(id); known == "" {
			t.strongUnknown++
		} else if known == run.Value {
			t.strongGood++
		}
	}
	poolKnown, relevant := true, 0
	for _, id := range run.CandidateIDs {
		known := label(id)
		poolKnown = poolKnown && known != ""
		if known == run.Value {
			relevant++
		}
	}
	if run.RetrievalScope != "" && run.RetrievalScope != "retrieval" {
		poolKnown = false
	}
	if !poolKnown || relevant == 0 {
		t.recallUnknown++
	} else {
		t.relevant += relevant
	}
	retrieved := map[string]bool{}
	for index, id := range run.Retrieved {
		if retrieved[id] || label(id) == "" {
			if _, exists := tracks[id]; !exists || retrieved[id] || tracks[id].Split != run.Split {
				t.violations++
			}
			continue
		}
		retrieved[id] = true
		if poolKnown && index < 200 && label(id) == run.Value {
			t.recallHits++
		}
	}
	t.requested += run.Requested
	good := 0
	output, duplicates := map[string]bool{}, map[string]bool{}
	for _, item := range run.Output {
		track, exists := tracks[item.ID]
		identityOK := exists && track.Split == run.Split && item.ArtistID == track.ArtistID
		duplicate := output[item.ID] || exists && (duplicates[track.AudioSHA256] || duplicates[track.LowAudioSHA256])
		if !identityOK || duplicate {
			t.violations++
		}
		if duplicate {
			continue
		}
		output[item.ID] = true
		duplicates[track.AudioSHA256], duplicates[track.LowAudioSHA256] = true, true
		known := label(item.ID)
		if known == "" {
			t.outputUnknown++
		} else if identityOK && known == run.Value {
			good++
		}
		if run.Facet == "voice_instrumental" && run.Value == "instrumental" {
			if known == "" || !identityOK {
				t.vocalUnknown++
			} else {
				t.vocalOutputs[item.ID] = known == "voice"
			}
		}
	}
	t.good += min(good, run.Requested)
	if len(run.Output) > run.Requested {
		t.violations += len(run.Output) - run.Requested
	}
}

func automaticRatioGate(name string, good, total, unknown int, target float64) AutomaticGate {
	g := AutomaticGate{Name: name, State: "insufficient", Target: target, Numerator: good, Denominator: total, Unknown: unknown}
	if total > 0 {
		value := float64(good) / float64(total)
		g.Value = &value
		if unknown == 0 {
			g.State = "fail"
			if value >= target {
				g.State = "pass"
			}
		}
	}
	return g
}

func automaticResult(split, variant string, t *automaticTotals, paired bool) AutomaticVariantResult {
	r := AutomaticVariantResult{Split: split, Variant: variant, Runs: t.runs, RunsWithErrors: t.runsWithErrors, State: "pass"}
	pairGate := AutomaticGate{Name: "complete_paired_ablation_runs", State: "insufficient", Target: 1, Denominator: t.runs}
	if paired && t.runs > 0 {
		pairGate.State = "pass"
		value := 1.0
		pairGate.Value = &value
	}
	r.Gates = append(r.Gates, pairGate)
	r.Gates = append(r.Gates,
		automaticRatioGate("strong_admission_precision", t.strongGood, t.strong, t.strongUnknown, .90),
		automaticRatioGate("recall_at_200", t.recallHits, t.relevant, t.recallUnknown, .90),
		automaticRatioGate("good_per_requested_slot", t.good, t.requested, t.outputUnknown, .80))
	vocals := 0
	for _, vocal := range t.vocalOutputs {
		if vocal {
			vocals++
		}
	}
	g := AutomaticGate{Name: "vocal_leakage_upper_95", State: "insufficient", Target: .05, Numerator: vocals, Denominator: len(t.vocalOutputs), Unknown: t.vocalUnknown}
	if len(t.vocalOutputs) > 0 {
		upper, _ := ClopperPearsonUpper95(vocals, len(t.vocalOutputs))
		g.Value = &upper
		if t.vocalUnknown == 0 {
			g.State = "fail"
			if upper <= .05 {
				g.State = "pass"
			}
		}
	}
	r.Gates = append(r.Gates, g)
	g = AutomaticGate{Name: "known_factual_identity_duplicate_violations", State: "insufficient", Target: 0, Numerator: t.violations, Denominator: t.runs, Unknown: t.factsUnknown}
	if t.runs > 0 {
		value := float64(t.violations)
		g.Value = &value
		if t.violations > 0 {
			g.State = "fail"
		} else if t.factsUnknown == 0 {
			g.State = "pass"
		}
	}
	r.Gates = append(r.Gates, g)
	for _, cache := range []string{"cold_provider", "warm_installed"} {
		times := t.latency[cache]
		g = AutomaticGate{Name: "p95_milliseconds_" + cache, State: "insufficient", Target: 120000, Denominator: len(times), Unknown: t.latencyUnknown[cache]}
		if len(times) > 0 {
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			value := float64(times[int(math.Ceil(.95*float64(len(times))))-1])
			g.Value = &value
			if g.Unknown == 0 {
				g.State = "fail"
				if value <= g.Target {
					g.State = "pass"
				}
			}
		}
		r.Gates = append(r.Gates, g)
		// A tail above the hard deadline cannot hide behind a passing p95.
		g.Name = "maximum_milliseconds_" + cache
		if len(times) > 0 {
			value := float64(times[len(times)-1])
			g.Value = &value
			if g.Unknown == 0 {
				g.State = "fail"
				if value <= g.Target {
					g.State = "pass"
				}
			}
		}
		r.Gates = append(r.Gates, g)
	}
	for _, gate := range r.Gates {
		if gate.State == "fail" {
			r.State = "fail"
		} else if gate.State == "insufficient" && r.State != "fail" {
			r.State = "insufficient"
		}
	}
	if !paired && r.State != "fail" {
		r.State = "insufficient"
	}
	return r
}

// ClopperPearsonUpper95 returns the exact one-sided 95% binomial upper bound.
// Zero observations have no bound. Repeating a recording does not increase n.
func ClopperPearsonUpper95(failures, n int) (float64, error) {
	if n <= 0 || failures < 0 || failures > n {
		return 0, fmt.Errorf("automatic evaluation: invalid binomial observations %d/%d", failures, n)
	}
	if failures == n {
		return 1, nil
	}
	if failures == 0 {
		return -math.Expm1(math.Log(.05) / float64(n)), nil
	}
	lo, hi := 0.0, 1.0
	for range 80 {
		p := (lo + hi) / 2
		cdf := 0.0
		for k := 0; k <= failures; k++ {
			ln, _ := math.Lgamma(float64(n + 1))
			lk, _ := math.Lgamma(float64(k + 1))
			lr, _ := math.Lgamma(float64(n - k + 1))
			cdf += math.Exp(ln - lk - lr + float64(k)*math.Log(p) + float64(n-k)*math.Log1p(-p))
		}
		if cdf > .05 {
			lo = p
		} else {
			hi = p
		}
	}
	return (lo + hi) / 2, nil
}
