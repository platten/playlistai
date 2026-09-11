package catalog

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestRejectUnsupportedVectorShape(t *testing.T) {
	for _, tc := range []struct {
		name               string
		count, dim, spaces uint32
		payload            int
	}{{"single space", 2, 2, 1, 4}, {"extra spaces", 1, 1, 3, 3}, {"overflow", 1 << 31, 1 << 31, 4, 0}, {"zero dimension", 1, 0, 2, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			raw := make([]byte, vecHeaderSize+tc.payload)
			copy(raw, vecMagic)
			for i, value := range []uint32{vecFormatVer, tc.count, tc.dim, tc.spaces, vecQuantInt8} {
				binary.LittleEndian.PutUint32(raw[8+i*4:], value)
			}
			path := filepath.Join(t.TempDir(), "vectors.i8")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			v, err := openVectors(path)
			if err == nil {
				_ = v.close()
				t.Fatal("accepted unsupported vector shape")
			}
		})
	}
}
