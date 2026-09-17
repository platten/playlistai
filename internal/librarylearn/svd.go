package librarylearn

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"
)

const sparseSVDVersion = "implicit-subspace-jacobi/v1"

type TruncatedSVD struct {
	Version        string
	Outcome        SVDOutcome
	Reason         string
	Dimension      int
	Columns        int
	SingularValues []float64
	// Components is row-major Dimension x Columns. The source artist-by-genre
	// matrix is never densified.
	Components []float64
}

func (s TruncatedSVD) Project(row SparseRow) []float64 {
	if s.Outcome != SVDAvailable || s.Dimension <= 0 {
		return nil
	}
	out := make([]float64, s.Dimension)
	for component := range s.Dimension {
		base := component * s.Columns
		for _, value := range row.Values {
			out[component] += value.Value * s.Components[base+value.Column]
		}
	}
	return out
}

func fitSparseSVD(ctx context.Context, rows []SparseRow, columns int, options MetadataOptions) TruncatedSVD {
	out := TruncatedSVD{Version: sparseSVDVersion, Outcome: SVDInsufficientStructure, Columns: columns}
	if len(rows) < 2 || columns < 2 {
		out.Reason = "at least two artists and two observed genres are required"
		return out
	}
	dimension := options.SVDDim
	if dimension <= 0 {
		dimension = 64
	}
	dimension = min(dimension, min(len(rows), columns))
	oversample := min(8, min(len(rows), columns)-dimension)
	width := dimension + max(0, oversample)
	budget := options.MaxScratchBytes
	if budget <= 0 {
		budget = 256 << 20
	}
	// q + next + final components + small Rayleigh matrices. Reduce the rank
	// rather than allocating beyond the caller's resource plan.
	for width > 1 && svdScratchBytes(columns, dimension, width) > budget {
		dimension--
		width = dimension
	}
	if dimension < 1 || svdScratchBytes(columns, dimension, width) > budget {
		out.Outcome = SVDResourceLimited
		out.Reason = "SVD scratch budget cannot hold one implicit component"
		return out
	}
	if ctx.Err() != nil {
		out.Outcome = SVDResourceLimited
		out.Reason = ctx.Err().Error()
		return out
	}
	q := make([]float64, columns*width)
	for column := range width {
		for genre := range columns {
			q[genre*width+column] = deterministicUnit(genre, column)
		}
	}
	if orthonormalize(q, columns, width) == 0 {
		out.Reason = "metadata matrix initialization had no independent directions"
		return out
	}
	iterations := options.SVDIterations
	if iterations <= 0 {
		iterations = 4
	}
	next := make([]float64, columns*width)
	for range iterations {
		if ctx.Err() != nil {
			out.Outcome = SVDResourceLimited
			out.Reason = ctx.Err().Error()
			return out
		}
		clear(next)
		multiplyGram(next, rows, width, q)
		if orthonormalize(next, columns, width) == 0 {
			out.Reason = "metadata matrix has no nonzero latent direction"
			return out
		}
		q, next = next, q
	}
	rayleigh := make([]float64, width*width)
	projection := make([]float64, width)
	for rowIndex, row := range rows {
		if rowIndex&1023 == 0 && ctx.Err() != nil {
			out.Outcome = SVDResourceLimited
			out.Reason = ctx.Err().Error()
			return out
		}
		clear(projection)
		for _, value := range row.Values {
			base := value.Column * width
			for j := range width {
				projection[j] += value.Value * q[base+j]
			}
		}
		for i := range width {
			for j := i; j < width; j++ {
				rayleigh[i*width+j] += projection[i] * projection[j]
			}
		}
	}
	for i := range width {
		for j := i + 1; j < width; j++ {
			rayleigh[j*width+i] = rayleigh[i*width+j]
		}
	}
	values, vectors := jacobiEigen(rayleigh, width)
	order := make([]int, width)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		if values[order[i]] != values[order[j]] {
			return values[order[i]] > values[order[j]]
		}
		return order[i] < order[j]
	})
	maximum := max(0, values[order[0]])
	meaningful := 0
	for _, index := range order {
		if meaningful == dimension || values[index] <= maximum*1e-10 || values[index] <= 1e-14 {
			break
		}
		meaningful++
	}
	if meaningful == 0 {
		out.Reason = "weighted genre matrix has zero numerical rank"
		return out
	}
	out.Dimension = meaningful
	out.SingularValues = make([]float64, meaningful)
	out.Components = make([]float64, meaningful*columns)
	for component := range meaningful {
		eigenColumn := order[component]
		out.SingularValues[component] = math.Sqrt(max(0, values[eigenColumn]))
		for genre := range columns {
			var value float64
			for basis := range width {
				value += q[genre*width+basis] * vectors[basis*width+eigenColumn]
			}
			out.Components[component*columns+genre] = value
		}
		canonicalizeComponentSign(out.Components[component*columns : (component+1)*columns])
	}
	out.Outcome = SVDAvailable
	out.Reason = ""
	return out
}

func svdScratchBytes(columns, dimension, width int) int64 {
	return int64(columns)*int64(2*width+dimension)*8 + int64(width*width*3)*8
}

func deterministicUnit(row, column int) float64 {
	var input [24]byte
	copy(input[:8], "paisvd1")
	binary.LittleEndian.PutUint64(input[8:16], uint64(row))
	binary.LittleEndian.PutUint64(input[16:], uint64(column))
	digest := sha256.Sum256(input[:])
	value := binary.LittleEndian.Uint64(digest[:8]) >> 11
	return 2*(float64(value)/float64(uint64(1)<<53)) - 1
}

func multiplyGram(out []float64, rows []SparseRow, width int, q []float64) {
	tmp := make([]float64, width)
	for _, row := range rows {
		clear(tmp)
		for _, value := range row.Values {
			base := value.Column * width
			for j := range width {
				tmp[j] += value.Value * q[base+j]
			}
		}
		for _, value := range row.Values {
			base := value.Column * width
			for j := range width {
				out[base+j] += value.Value * tmp[j]
			}
		}
	}
}

// orthonormalize uses deterministic modified Gram-Schmidt with reorthogonal-
// ization. Matrices are row-major rows x columns.
func orthonormalize(matrix []float64, rows, columns int) int {
	independent := 0
	for column := range columns {
		for range 2 {
			for previous := 0; previous < column; previous++ {
				var dot float64
				for row := range rows {
					dot += matrix[row*columns+column] * matrix[row*columns+previous]
				}
				for row := range rows {
					matrix[row*columns+column] -= dot * matrix[row*columns+previous]
				}
			}
		}
		var squared float64
		for row := range rows {
			value := matrix[row*columns+column]
			squared += value * value
		}
		if squared <= 1e-24 {
			for row := range rows {
				matrix[row*columns+column] = 0
			}
			continue
		}
		scale := 1 / math.Sqrt(squared)
		for row := range rows {
			matrix[row*columns+column] *= scale
		}
		independent++
	}
	return independent
}

// jacobiEigen returns eigenvalues and a row-major matrix whose columns are the
// corresponding eigenvectors. It is used only on the bounded projected matrix.
func jacobiEigen(input []float64, n int) ([]float64, []float64) {
	a := append([]float64(nil), input...)
	v := make([]float64, n*n)
	for i := range n {
		v[i*n+i] = 1
	}
	for sweep := 0; sweep < 50; sweep++ {
		largest := 0.0
		for p := range n {
			for q := p + 1; q < n; q++ {
				apq := a[p*n+q]
				largest = max(largest, math.Abs(apq))
				if math.Abs(apq) <= 1e-15 {
					continue
				}
				app, aqq := a[p*n+p], a[q*n+q]
				angle := .5 * math.Atan2(2*apq, aqq-app)
				c, s := math.Cos(angle), math.Sin(angle)
				for k := range n {
					if k == p || k == q {
						continue
					}
					akp, akq := a[k*n+p], a[k*n+q]
					a[k*n+p], a[p*n+k] = c*akp-s*akq, c*akp-s*akq
					a[k*n+q], a[q*n+k] = s*akp+c*akq, s*akp+c*akq
				}
				a[p*n+p] = c*c*app - 2*s*c*apq + s*s*aqq
				a[q*n+q] = s*s*app + 2*s*c*apq + c*c*aqq
				a[p*n+q], a[q*n+p] = 0, 0
				for k := range n {
					vkp, vkq := v[k*n+p], v[k*n+q]
					v[k*n+p], v[k*n+q] = c*vkp-s*vkq, s*vkp+c*vkq
				}
			}
		}
		if largest <= 1e-13 {
			break
		}
	}
	values := make([]float64, n)
	for i := range n {
		values[i] = a[i*n+i]
	}
	return values, v
}

func canonicalizeComponentSign(component []float64) {
	pivot := 0
	for i := 1; i < len(component); i++ {
		if math.Abs(component[i]) > math.Abs(component[pivot]) {
			pivot = i
		}
	}
	if len(component) > 0 && component[pivot] < 0 {
		for i := range component {
			component[i] = -component[i]
		}
	}
}
