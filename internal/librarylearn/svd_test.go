package librarylearn

import (
	"context"
	"math"
	"testing"
)

func TestSparseSVDMatchesAnalyticalReference(t *testing.T) {
	rootHalf := 1 / math.Sqrt(2)
	rows := []SparseRow{
		{ArtistID: "a", Values: []SparseValue{{Column: 0, Value: 1}}},
		{ArtistID: "b", Values: []SparseValue{{Column: 1, Value: 1}}},
		{ArtistID: "c", Values: []SparseValue{{Column: 0, Value: rootHalf}, {Column: 1, Value: rootHalf}}},
	}
	model := fitSparseSVD(context.Background(), rows, 2, MetadataOptions{SVDDim: 2, SVDIterations: 3})
	if model.Outcome != SVDAvailable || model.Dimension != 2 {
		t.Fatalf("SVD outcome=%+v", model)
	}
	if math.Abs(model.SingularValues[0]-math.Sqrt(2)) > 1e-10 || math.Abs(model.SingularValues[1]-1) > 1e-10 {
		t.Fatalf("singular values=%v, want [%v 1]", model.SingularValues, math.Sqrt(2))
	}
	// A full-rank orthonormal right projection preserves row cosine. This
	// compares observable similarity rather than relying on eigenvector signs.
	left, right := model.Project(rows[0]), model.Project(rows[2])
	var dot, ln, rn float64
	for i := range left {
		dot += left[i] * right[i]
		ln += left[i] * left[i]
		rn += right[i] * right[i]
	}
	if got := dot / math.Sqrt(ln*rn); math.Abs(got-rootHalf) > 1e-10 {
		t.Fatalf("projected cosine=%v, want %v", got, rootHalf)
	}
}

func TestSparseSVDResourceOutcome(t *testing.T) {
	rows := []SparseRow{
		{ArtistID: "a", Values: []SparseValue{{Column: 0, Value: 1}}},
		{ArtistID: "b", Values: []SparseValue{{Column: 1, Value: 1}}},
	}
	model := fitSparseSVD(context.Background(), rows, 2, MetadataOptions{SVDDim: 2, MaxScratchBytes: 1})
	if model.Outcome != SVDResourceLimited {
		t.Fatalf("resource outcome=%+v", model)
	}
}
