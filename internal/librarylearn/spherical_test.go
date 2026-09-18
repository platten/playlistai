package librarylearn

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
)

func sphericalFixture() []DenseVector {
	var out []DenseVector
	for i := range 48 {
		angle := float64(i%16) * .01
		base := float64(i/16) * 2 * math.Pi / 3
		out = append(out, DenseVector{ID: string(rune('A' + i)), Values: []float32{float32(math.Cos(base + angle)), float32(math.Sin(base + angle))}})
	}
	return out
}

func TestSphericalWorkerInvariant(t *testing.T) {
	input := sphericalFixture()
	var models []SphericalModel
	var assignments [][]ClusterAssignment
	for _, workers := range []int{1, 2, 4} {
		model, err := FitSpherical(context.Background(), input, SphericalOptions{Clusters: 3, BatchSize: 17, LogicalBlock: 5, MaxEpochs: 8, Workers: workers, Seed: 9, InputGeneration: "frozen"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := AssignSpherical(context.Background(), input, model, workers, 7)
		if err != nil {
			t.Fatal(err)
		}
		models, assignments = append(models, model), append(assignments, got)
	}
	for i := 1; i < len(models); i++ {
		if !reflect.DeepEqual(models[0], models[i]) || !reflect.DeepEqual(assignments[0], assignments[i]) {
			t.Fatal("spherical result changed with physical workers")
		}
	}
}

func TestSphericalCheckpointResume(t *testing.T) {
	input := sphericalFixture()
	options := SphericalOptions{Clusters: 3, BatchSize: 11, LogicalBlock: 4, MaxEpochs: 6, Workers: 3, Seed: 17, InputGeneration: "snapshot"}
	want, err := FitSpherical(context.Background(), input, options, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("persisted boundary")
	var saved SphericalCheckpoint
	_, err = FitSpherical(context.Background(), input, options, nil, func(checkpoint SphericalCheckpoint) error {
		saved = checkpoint
		return stop
	})
	if !errors.Is(err, stop) || saved.Cursor == 0 {
		t.Fatalf("checkpoint not captured: %+v %v", saved, err)
	}
	options.Workers = 1
	got, err := FitSpherical(context.Background(), input, options, &saved, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("resuming with a different worker count changed the model")
	}
}

func TestSphericalCheckpointsAreJSONSerializable(t *testing.T) {
	input := sphericalFixture()
	checkpoints := 0
	_, err := FitSpherical(context.Background(), input, SphericalOptions{
		Clusters: 3, BatchSize: 11, LogicalBlock: 4, MaxEpochs: 2, Workers: 2,
	}, nil, func(checkpoint SphericalCheckpoint) error {
		checkpoints++
		_, err := json.Marshal(checkpoint)
		return err
	})
	if err != nil {
		t.Fatalf("serialize checkpoint: %v", err)
	}
	if checkpoints < 2 {
		t.Fatalf("checkpoints = %d, want multiple batches", checkpoints)
	}
}

func TestSphericalEmptyClusterAndCancellation(t *testing.T) {
	input := []DenseVector{{ID: "a", Values: []float32{1, 0}}, {ID: "b", Values: []float32{1, 0}}, {ID: "c", Values: []float32{0, 1}}}
	model, err := FitSpherical(context.Background(), input, SphericalOptions{Clusters: 3, MaxEpochs: 3, Workers: 2}, nil, nil)
	if err != nil || len(model.Centroids) != 6 {
		t.Fatalf("empty cluster handling: %+v %v", model, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FitSpherical(ctx, input, SphericalOptions{Clusters: 2}, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestStreamingAssignmentMatchesBoundedAssignment(t *testing.T) {
	input := sphericalFixture()
	model, err := FitSpherical(context.Background(), input, SphericalOptions{Clusters: 3, MaxEpochs: 3, Workers: 2}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := AssignSpherical(context.Background(), input, model, 1, 7)
	if err != nil {
		t.Fatal(err)
	}
	var got []ClusterAssignment
	err = AssignSphericalSource(context.Background(), &SliceVectorSource{Rows: input}, model, AssignmentOptions{Workers: 4, LogicalBlock: 5, BatchSize: 11}, func(batch []ClusterAssignment) error {
		got = append(got, batch...)
		return nil
	})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("streaming assignment differs: %v", err)
	}
}
