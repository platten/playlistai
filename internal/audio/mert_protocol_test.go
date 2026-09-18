package audio

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"
)

func TestMERTRequestBinaryRoundTrip(t *testing.T) {
	requests := []MERTWorkerRequest{
		{Protocol: MERTWorkerProtocol, Health: true},
		{Protocol: MERTWorkerProtocol, Audio: make([]float32, MERTSegmentSamples)},
	}
	requests[1].Audio[0] = math.Float32frombits(0x80000000)
	requests[1].Audio[399] = 0.25
	requests[1].Audio[len(requests[1].Audio)-1] = -1.5
	for _, request := range requests {
		var wire bytes.Buffer
		if err := WriteMERTRequest(&wire, request); err != nil {
			t.Fatal(err)
		}
		if maximum := mertRequestHeaderBytes + MERTSegmentSamples*4; wire.Len() > maximum {
			t.Fatalf("wire bytes=%d maximum=%d", wire.Len(), maximum)
		}
		got, err := ReadMERTRequest(&wire)
		if err != nil {
			t.Fatal(err)
		}
		if got.Protocol != request.Protocol || got.Health != request.Health || len(got.Audio) != len(request.Audio) {
			t.Fatalf("round trip metadata=%+v", got)
		}
		for index := range got.Audio {
			if math.Float32bits(got.Audio[index]) != math.Float32bits(request.Audio[index]) {
				t.Fatalf("sample %d bits=%08x want=%08x", index, math.Float32bits(got.Audio[index]), math.Float32bits(request.Audio[index]))
			}
		}
		clear(got.Audio)
	}
}

func TestMERTRequestBinaryRejectsInvalidAndTruncatedFrames(t *testing.T) {
	valid := func(flags, samples uint32) []byte {
		header := make([]byte, mertRequestHeaderBytes)
		binary.LittleEndian.PutUint32(header[0:4], mertRequestMagic)
		binary.LittleEndian.PutUint32(header[4:8], MERTWorkerProtocol)
		binary.LittleEndian.PutUint32(header[8:12], flags)
		binary.LittleEndian.PutUint32(header[12:16], samples)
		return header
	}
	for name, payload := range map[string][]byte{
		"empty":           nil,
		"bad magic":       append([]byte{0, 0, 0, 0}, valid(0, 400)[4:]...),
		"unknown flags":   valid(2, 0),
		"health with pcm": valid(mertRequestHealth, 400),
		"too short":       valid(0, 399),
		"too long":        valid(0, MERTSegmentSamples+1),
		"truncated pcm":   append(valid(0, 400), make([]byte, 399*4)...),
	} {
		t.Run(name, func(t *testing.T) {
			if request, err := ReadMERTRequest(bytes.NewReader(payload)); err == nil || request.Audio != nil {
				t.Fatalf("invalid request accepted: %+v err=%v", request, err)
			}
		})
	}
	if err := WriteMERTRequest(io.Discard, MERTWorkerRequest{Protocol: MERTWorkerProtocol, Audio: make([]float32, 399)}); err == nil {
		t.Fatal("short request was written")
	}
}

type shortMERTWriter struct{ maximum int }

func (w shortMERTWriter) Write(value []byte) (int, error) {
	return min(w.maximum, len(value)), nil
}

func TestMERTRequestBinaryHandlesShortWrites(t *testing.T) {
	if err := WriteMERTRequest(shortMERTWriter{maximum: 7}, MERTWorkerRequest{Protocol: MERTWorkerProtocol, Audio: make([]float32, 400)}); err != nil {
		t.Fatal(err)
	}
	if err := WriteMERTRequest(shortMERTWriter{}, MERTWorkerRequest{Protocol: MERTWorkerProtocol, Health: true}); err != io.ErrShortWrite {
		t.Fatalf("zero write error=%v", err)
	}
}
