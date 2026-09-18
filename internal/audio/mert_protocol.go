package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

const (
	mertRequestMagic       = uint32(0x3154524d) // "MRT1" in little-endian byte order.
	mertRequestHealth      = uint32(1)
	mertRequestHeaderBytes = 16
	mertRequestChunkBytes  = 16 << 10
)

// WriteMERTRequest writes the private, bounded parent/worker request protocol.
// The matched executable transports PCM directly as little-endian float32
// rather than allocating and reflecting through gob for every sample.
func WriteMERTRequest(writer io.Writer, request MERTWorkerRequest) error {
	if writer == nil || request.Protocol != MERTWorkerProtocol {
		return fmt.Errorf("audio MERT worker: invalid request protocol")
	}
	flags := uint32(0)
	if request.Health {
		flags = mertRequestHealth
		if len(request.Audio) != 0 {
			return fmt.Errorf("audio MERT worker: health request contains PCM")
		}
	} else if len(request.Audio) < 400 || len(request.Audio) > MERTSegmentSamples {
		return fmt.Errorf("audio MERT worker: invalid sample count")
	}
	var header [mertRequestHeaderBytes]byte
	binary.LittleEndian.PutUint32(header[0:4], mertRequestMagic)
	binary.LittleEndian.PutUint32(header[4:8], uint32(request.Protocol))
	binary.LittleEndian.PutUint32(header[8:12], flags)
	binary.LittleEndian.PutUint32(header[12:16], uint32(len(request.Audio))) //nolint:gosec // bounded by MERTSegmentSamples
	if err := writeMERTBytes(writer, header[:]); err != nil {
		return err
	}
	var encoded [mertRequestChunkBytes]byte
	for offset := 0; offset < len(request.Audio); {
		count := min(len(request.Audio)-offset, len(encoded)/4)
		for index, sample := range request.Audio[offset : offset+count] {
			binary.LittleEndian.PutUint32(encoded[index*4:index*4+4], math.Float32bits(sample))
		}
		if err := writeMERTBytes(writer, encoded[:count*4]); err != nil {
			clear(encoded[:])
			return err
		}
		offset += count
	}
	clear(encoded[:])
	return nil
}

// ReadMERTRequest decodes one private parent/worker request with a fixed maximum
// of one MERT segment. It never trusts a payload length supplied by the peer.
func ReadMERTRequest(reader io.Reader) (MERTWorkerRequest, error) {
	var request MERTWorkerRequest
	if reader == nil {
		return request, fmt.Errorf("audio MERT worker: missing request stream")
	}
	var header [mertRequestHeaderBytes]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return request, err
	}
	magic := binary.LittleEndian.Uint32(header[0:4])
	protocol := binary.LittleEndian.Uint32(header[4:8])
	flags := binary.LittleEndian.Uint32(header[8:12])
	samples := binary.LittleEndian.Uint32(header[12:16])
	if magic != mertRequestMagic || protocol != MERTWorkerProtocol || flags & ^mertRequestHealth != 0 {
		return request, fmt.Errorf("audio MERT worker: invalid request header")
	}
	request.Protocol = int(protocol)
	request.Health = flags&mertRequestHealth != 0
	if request.Health {
		if samples != 0 {
			return MERTWorkerRequest{}, fmt.Errorf("audio MERT worker: health request contains PCM")
		}
		return request, nil
	}
	if samples < 400 || samples > MERTSegmentSamples {
		return MERTWorkerRequest{}, fmt.Errorf("audio MERT worker: invalid sample count")
	}
	request.Audio = make([]float32, int(samples))
	success := false
	defer func() {
		if !success {
			clear(request.Audio)
		}
	}()
	var encoded [mertRequestChunkBytes]byte
	for offset := 0; offset < len(request.Audio); {
		count := min(len(request.Audio)-offset, len(encoded)/4)
		payload := encoded[:count*4]
		if _, err := io.ReadFull(reader, payload); err != nil {
			clear(encoded[:])
			return MERTWorkerRequest{}, err
		}
		for index := range count {
			request.Audio[offset+index] = math.Float32frombits(binary.LittleEndian.Uint32(payload[index*4 : index*4+4]))
		}
		offset += count
	}
	clear(encoded[:])
	success = true
	return request, nil
}

func writeMERTBytes(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}
