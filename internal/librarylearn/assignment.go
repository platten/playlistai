package librarylearn

import (
	"context"
	"errors"
)

type DenseVectorSource interface {
	Next(context.Context) (DenseVector, bool, error)
}

type SliceVectorSource struct {
	Rows  []DenseVector
	index int
}

func (s *SliceVectorSource) Next(ctx context.Context) (DenseVector, bool, error) {
	if err := ctx.Err(); err != nil {
		return DenseVector{}, false, err
	}
	if s.index >= len(s.Rows) {
		return DenseVector{}, false, nil
	}
	row := s.Rows[s.index]
	s.index++
	return row, true, nil
}

type AssignmentOptions struct {
	Workers      int
	LogicalBlock int
	BatchSize    int
}

type AssignmentSink func([]ClusterAssignment) error

// AssignSphericalSource incrementally assigns a canonical frozen vector stream.
// The sink is called only for complete stable-ID-ordered batches, bounding
// memory independently of corpus size.
func AssignSphericalSource(ctx context.Context, source DenseVectorSource, model SphericalModel, options AssignmentOptions, sink AssignmentSink) error {
	if source == nil || sink == nil || model.Dimension <= 0 || model.Clusters <= 0 {
		return errors.New("librarylearn: invalid streaming assignment")
	}
	options.Workers = max(1, options.Workers)
	options.LogicalBlock = max(1, options.LogicalBlock)
	if options.BatchSize <= 0 {
		options.BatchSize = 4096
	}
	lastID := ""
	batch := make([]DenseVector, 0, options.BatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		assignments, err := AssignSpherical(ctx, batch, model, options.Workers, options.LogicalBlock)
		if err != nil {
			return err
		}
		if err := sink(assignments); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	for {
		row, ok, err := source.Next(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return flush()
		}
		if row.ID == "" || lastID != "" && row.ID <= lastID {
			return errors.New("librarylearn: assignment source IDs must be unique and strictly increasing")
		}
		lastID = row.ID
		batch = append(batch, row)
		if len(batch) == options.BatchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
}
