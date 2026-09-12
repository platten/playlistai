//go:build cgo

package audioruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/platten/playlistai/internal/audio"
)

// RunMERT executes only audio-only MERT graphs in the compiled desktop worker.
func RunMERT(dir string) error {
	m, err := audio.ReadMERTBundle(dir)
	if err != nil {
		return err
	}
	releaseRuntime, err := prepareMERTRuntime(dir, m)
	if err != nil {
		return err
	}
	defer releaseRuntime()
	ort.SetSharedLibraryPath(m.File(dir, "runtime"))
	if err = ort.InitializeEnvironment(); err != nil {
		return err
	}
	defer func() { _ = ort.DestroyEnvironment() }()
	options, err := ort.NewSessionOptions()
	if err != nil {
		return err
	}
	defer func() { _ = options.Destroy() }()
	for _, err := range []error{options.SetIntraOpNumThreads(2), options.SetInterOpNumThreads(1), options.SetCpuMemArena(false), options.SetMemPattern(false)} {
		if err != nil {
			return err
		}
	}
	session, err := ort.NewDynamicAdvancedSession(m.File(dir, "audio_model"), []string{"input_values", "attention_mask"}, []string{"embedding"}, options)
	if err != nil {
		return err
	}
	defer func() { _ = session.Destroy() }()
	for {
		var request audio.MERTWorkerRequest
		if err = audio.ReadFrame(os.Stdin, &request, 1<<20); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if request.Protocol != audio.MERTWorkerProtocol {
			clear(request.Audio)
			return fmt.Errorf("MERT protocol mismatch")
		}
		response := audio.MERTWorkerResponse{Protocol: audio.MERTWorkerProtocol, Model: m.Model}
		if request.Health {
			err = mertHealth(session, m.File(dir, "health"))
		} else {
			response.Vector, err = mertEmbedding(session, request.Audio)
		}
		clear(request.Audio)
		if err != nil {
			response.Error = "MERT inference unavailable"
		}
		err = audio.WriteFrame(os.Stdout, response)
		clear(response.Vector)
		if err != nil {
			return err
		}
	}
}
func mertEmbedding(session *ort.DynamicAdvancedSession, samples []float32) ([]float32, error) {
	pcm, mask, err := audio.MERTInput(samples)
	if err != nil {
		return nil, err
	}
	defer clear(pcm)
	defer clear(mask)
	input, err := ort.NewTensor(ort.NewShape(1, audio.MERTSegmentSamples), pcm)
	if err != nil {
		return nil, err
	}
	defer func() { _ = input.Destroy() }()
	attention, err := ort.NewTensor(ort.NewShape(1, audio.MERTSegmentSamples), mask)
	if err != nil {
		return nil, err
	}
	defer func() { _ = attention.Destroy() }()
	output, err := ort.NewEmptyTensor[float32](ort.NewShape(1, audio.MERTDimension))
	if err != nil {
		return nil, err
	}
	defer func() { clear(output.GetData()); _ = output.Destroy() }()
	if err = session.Run([]ort.Value{input, attention}, []ort.Value{output}); err != nil {
		return nil, err
	}
	vector := append([]float32(nil), output.GetData()...)
	// Graph output must already be normalized. Do not hide graph/export errors.
	if !audio.MERTParity(vector, vector) {
		clear(vector)
		return nil, fmt.Errorf("invalid MERT output")
	}
	return vector, nil
}

type mertHealthFixture struct {
	Fixtures []struct {
		Name      string    `json:"name"`
		ToneHz    float64   `json:"toneHz"`
		Samples   int       `json:"samples"`
		Embedding []float32 `json:"embedding"`
	} `json:"fixtures"`
}

func mertHealth(session *ort.DynamicAdvancedSession, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var fixture mertHealthFixture
	if err = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&fixture); err != nil {
		return err
	}
	if len(fixture.Fixtures) < 3 || len(fixture.Fixtures) > 16 {
		return fmt.Errorf("invalid MERT health fixtures")
	}
	for _, test := range fixture.Fixtures {
		if test.Samples < 400 || test.Samples > audio.MERTSegmentSamples || math.IsNaN(test.ToneHz) || test.ToneHz < 0 || test.ToneHz >= audio.MERTSampleRate/2 {
			return fmt.Errorf("invalid MERT health tone")
		}
		pcm := make([]float32, test.Samples)
		for j := range pcm {
			pcm[j] = float32(0.1 * math.Sin(2*math.Pi*test.ToneHz*float64(j)/audio.MERTSampleRate))
		}
		vector, err := mertEmbedding(session, pcm)
		clear(pcm)
		parity := audio.MERTParity(vector, test.Embedding)
		clear(vector)
		if err != nil {
			return err
		}
		if !parity {
			return fmt.Errorf("MERT health parity mismatch")
		}
	}
	return nil
}
