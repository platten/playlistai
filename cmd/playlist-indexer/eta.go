package main

import (
	"fmt"
	"time"
)

const (
	// The estimate follows recent throughput so it adapts to MERT outages,
	// slow sources and cache-hit stretches instead of the whole-run average.
	etaWindow     = 10 * time.Minute
	etaMinimumAge = 10 * time.Second
)

type etaSample struct {
	at       time.Time
	finished int64
}

// analysisETA estimates completion from finished-file throughput over a
// sliding window of durable progress snapshots.
type analysisETA struct {
	samples []etaSample
}

func (e *analysisETA) observe(now time.Time, finished int64) {
	if n := len(e.samples); n > 0 && (finished < e.samples[n-1].finished || finished == 0) {
		// A reset counter invalidates the history, and time before the first
		// finished file (scan, warm-up) is not analysis throughput.
		e.samples = e.samples[:0]
	}
	e.samples = append(e.samples, etaSample{at: now, finished: finished})
	cutoff := now.Add(-etaWindow)
	drop := 0
	// Keep one sample at or before the cutoff so the window stays full.
	for drop+1 < len(e.samples) && !e.samples[drop+1].at.After(cutoff) {
		drop++
	}
	if drop > 0 {
		e.samples = append(e.samples[:0], e.samples[drop:]...)
	}
}

// estimate returns the remaining duration and files per minute. ok is false
// until the window spans etaMinimumAge with at least one finished file.
func (e *analysisETA) estimate(remaining int64) (time.Duration, float64, bool) {
	if len(e.samples) < 2 || remaining <= 0 {
		return 0, 0, false
	}
	first, last := e.samples[0], e.samples[len(e.samples)-1]
	span := last.at.Sub(first.at)
	done := last.finished - first.finished
	if span < etaMinimumAge || done <= 0 {
		return 0, 0, false
	}
	perSecond := float64(done) / span.Seconds()
	return time.Duration(float64(remaining) / perSecond * float64(time.Second)), perSecond * 60, true
}

// suffix renders the status-line fragment for the given remaining work.
func (e *analysisETA) suffix(remaining int64) string {
	if remaining <= 0 {
		return ""
	}
	left, perMinute, ok := e.estimate(remaining)
	if !ok {
		return " • ETA estimating…"
	}
	return fmt.Sprintf(" • ETA %s (%s files/min)", formatETA(left), formatRate(perMinute))
}

func formatETA(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Round(time.Minute)/time.Minute))
	case d < 48*time.Hour:
		d = d.Round(time.Minute)
		return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	default:
		d = d.Round(time.Hour)
		return fmt.Sprintf("%dd%02dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
	}
}

func formatRate(perMinute float64) string {
	if perMinute < 10 {
		return fmt.Sprintf("%.1f", perMinute)
	}
	return fmt.Sprintf("%.0f", perMinute)
}
