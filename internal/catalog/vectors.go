package catalog

import (
	"encoding/binary"
	"fmt"
	"os"

	mmap "github.com/edsrzf/mmap-go"
)

// vectors.i8 layout (see python/catalogfmt.py):
//
//	offset 0   magic "PAIVEC1\0"            (8 bytes)
//	offset 8   uint32 LE  format version
//	offset 12  uint32 LE  track count       (N)
//	offset 16  uint32 LE  dim               (D, 100)
//	offset 20  uint32 LE  spaces            (2: audio, track)
//	offset 24  uint32 LE  quant code        (1 = int8, scale 1/127)
//	offset 32  N*spaces*D int8 values, row-major
const (
	vecMagic      = "PAIVEC1\x00"
	vecHeaderSize = 32
	vecFormatVer  = 1
	vecQuantInt8  = 1
	vecQuantScale = 127.0
)

type vectorStore struct {
	mm     mmap.MMap
	count  int
	dim    int
	spaces int
}

func openVectors(path string) (*vectorStore, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from validated config/dir
	if err != nil {
		return nil, err
	}
	defer f.Close()

	mm, err := mmap.Map(f, mmap.RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("mmap %s: %w", path, err)
	}

	if len(mm) < vecHeaderSize || string(mm[:8]) != vecMagic {
		_ = mm.Unmap()
		return nil, fmt.Errorf("%s: not a Playlist AI vectors file", path)
	}
	ver := binary.LittleEndian.Uint32(mm[8:12])
	count := int(binary.LittleEndian.Uint32(mm[12:16]))
	dim := int(binary.LittleEndian.Uint32(mm[16:20]))
	spaces := int(binary.LittleEndian.Uint32(mm[20:24]))
	quant := binary.LittleEndian.Uint32(mm[24:28])

	if ver != vecFormatVer {
		_ = mm.Unmap()
		return nil, fmt.Errorf("%s: format version %d, want %d", path, ver, vecFormatVer)
	}
	if quant != vecQuantInt8 {
		_ = mm.Unmap()
		return nil, fmt.Errorf("%s: unknown quantization %d", path, quant)
	}
	if dim <= 0 || spaces <= 0 || count < 0 {
		_ = mm.Unmap()
		return nil, fmt.Errorf("%s: bad header (count=%d dim=%d spaces=%d)", path, count, dim, spaces)
	}
	want := vecHeaderSize + count*spaces*dim
	if len(mm) != want {
		_ = mm.Unmap()
		return nil, fmt.Errorf("%s: size %d, expected %d", path, len(mm), want)
	}

	return &vectorStore{mm: mm, count: count, dim: dim, spaces: spaces}, nil
}

// at returns the dequantized sub-vectors for a row: space 0 (audio) and space 1
// (track). Returns nil, nil for an out-of-range row.
func (v *vectorStore) at(row int) (audio, track []float32) {
	if row < 0 || row >= v.count {
		return nil, nil
	}
	stride := v.spaces * v.dim
	off := vecHeaderSize + row*stride

	audio = make([]float32, v.dim)
	track = make([]float32, v.dim)
	for i := 0; i < v.dim; i++ {
		audio[i] = float32(int8(v.mm[off+i])) / vecQuantScale
		track[i] = float32(int8(v.mm[off+v.dim+i])) / vecQuantScale
	}
	return audio, track
}

func (v *vectorStore) close() error {
	if v.mm == nil {
		return nil
	}
	err := v.mm.Unmap()
	v.mm = nil
	return err
}
