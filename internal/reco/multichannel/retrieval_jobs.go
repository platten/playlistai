package multichannel

import (
	"context"
	"errors"

	"github.com/platten/playlistai/internal/core"
	"github.com/platten/playlistai/internal/ports"
	"github.com/platten/playlistai/internal/searchwork"
)

type retrievalJob struct {
	backend any
	run     func(map[string]*core.Candidate, *[]explorationOption) error
}

// Unsupported backends retain their serial invocation order. Opted-in searches
// write only their own slots; candidate evidence and exploration are committed
// by the coordinator after all workers have joined.
func runRetrievalJobs(ctx context.Context, jobs []retrievalJob, byID map[string]*core.Candidate, exploration *[]explorationOption) error {
	parallelSupported := false
	for _, job := range jobs {
		if capability, ok := job.backend.(ports.ConcurrentSearcher); ok && capability.ConcurrentSearch() {
			parallelSupported = true
			break
		}
	}
	if !parallelSupported || len(jobs) < 2 {
		var errs []error
		for _, job := range jobs {
			if ctx.Err() != nil {
				break
			}
			if err := job.run(byID, exploration); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(append(errs, ctx.Err())...)
	}
	type result struct {
		candidates  map[string]*core.Candidate
		exploration []explorationOption
		err         error
	}
	results := make([]result, len(jobs))
	run := func(index int) {
		slot := &results[index]
		slot.candidates = make(map[string]*core.Candidate)
		slot.err = jobs[index].run(slot.candidates, &slot.exploration)
	}
	var parallel []func()
	var serial []int
	for index, job := range jobs {
		if capability, ok := job.backend.(ports.ConcurrentSearcher); ok && capability.ConcurrentSearch() {
			parallel = append(parallel, func() { run(index) })
		} else {
			serial = append(serial, index)
		}
	}
	if len(serial) > 0 {
		parallel = append(parallel, func() {
			for _, index := range serial {
				if ctx.Err() != nil {
					return
				}
				run(index)
			}
		})
	}
	searchwork.Run(ctx, len(parallel), func(index int) { parallel[index]() })
	var errs []error
	for _, slot := range results {
		if slot.err != nil {
			errs = append(errs, slot.err)
		}
		for id, candidate := range slot.candidates {
			if existing := byID[id]; existing != nil {
				existing.Sources = append(existing.Sources, candidate.Sources...)
			} else {
				byID[id] = candidate
			}
		}
		*exploration = append(*exploration, slot.exploration...)
	}
	return errors.Join(append(errs, ctx.Err())...)
}
