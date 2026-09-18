//go:build cgo

package audioruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"

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
	threads := 2
	if raw := os.Getenv("PLAYLISTAI_MERT_INTRA_THREADS"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 1 || parsed > 256 {
			return fmt.Errorf("invalid MERT intra-operation thread budget")
		}
		threads = parsed
	}
	for _, err := range []error{
		options.SetIntraOpNumThreads(threads), options.SetInterOpNumThreads(1),
		options.SetExecutionMode(ort.ExecutionModeSequential),
		options.AddSessionConfigEntry("session.intra_op.allow_spinning", "0"),
		options.AddSessionConfigEntry("session.inter_op.allow_spinning", "0"),
		options.AddSessionConfigEntry("session.force_spinning_stop", "1"),
		options.SetCpuMemArena(false), options.SetMemPattern(false),
	} {
		if err != nil {
			return err
		}
	}
	device := os.Getenv("PLAYLISTAI_MERT_DEVICE")
	if device == "" {
		device = "cpu"
	}
	if device == "cpu" {
		if m.Backend() != "cpu" {
			return fmt.Errorf("MERT bundle requires CUDA but worker requested CPU")
		}
	} else {
		deviceIndex, ok := audio.MERTCUDADeviceIndex(device)
		if !ok || m.Backend() != "cuda" {
			return fmt.Errorf("MERT CUDA device and bundle backend do not match")
		}
		cudaOptions, cudaErr := ort.NewCUDAProviderOptions()
		if cudaErr != nil {
			return fmt.Errorf("initialize CUDA execution provider: %w", cudaErr)
		}
		if cudaErr = cudaOptions.Update(map[string]string{"device_id": strconv.Itoa(deviceIndex), "do_copy_in_default_stream": "1"}); cudaErr == nil {
			cudaErr = options.AppendExecutionProviderCUDA(cudaOptions)
		}
		destroyErr := cudaOptions.Destroy()
		if cudaErr != nil || destroyErr != nil {
			return fmt.Errorf("configure CUDA execution provider: %w", errors.Join(cudaErr, destroyErr))
		}
	}
	session, err := ort.NewDynamicAdvancedSession(m.File(dir, "audio_model"), []string{"input_values", "attention_mask"}, []string{"embedding"}, options)
	if err != nil {
		return err
	}
	defer func() { _ = session.Destroy() }()
	for {
		var request audio.MERTWorkerRequest
		request, err = audio.ReadMERTRequest(os.Stdin)
		if err != nil {
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
			err = mertHealth(session, m.File(dir, "health"), m.Backend())
		} else {
			response.Vector, response.Timings, err = mertEmbedding(session, request.Audio)
		}
		clear(request.Audio)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			response.Error = "MERT inference unavailable"
		}
		err = audio.WriteFrame(os.Stdout, response)
		clear(response.Vector)
		if err != nil {
			return err
		}
	}
}
func mertEmbedding(session *ort.DynamicAdvancedSession, samples []float32) ([]float32, audio.MERTWorkerTimings, error) {
	var timings audio.MERTWorkerTimings
	preprocessStarted := time.Now()
	pcm, mask, err := audio.MERTInput(samples)
	if err != nil {
		return nil, timings, err
	}
	defer clear(pcm)
	defer clear(mask)
	input, err := ort.NewTensor(ort.NewShape(1, audio.MERTSegmentSamples), pcm)
	if err != nil {
		return nil, timings, err
	}
	defer func() { _ = input.Destroy() }()
	attention, err := ort.NewTensor(ort.NewShape(1, audio.MERTSegmentSamples), mask)
	if err != nil {
		return nil, timings, err
	}
	defer func() { _ = attention.Destroy() }()
	output, err := ort.NewEmptyTensor[float32](ort.NewShape(1, audio.MERTDimension))
	if err != nil {
		return nil, timings, err
	}
	defer func() { clear(output.GetData()); _ = output.Destroy() }()
	timings.Preprocessing = time.Since(preprocessStarted)
	executionStarted := time.Now()
	if err = session.Run([]ort.Value{input, attention}, []ort.Value{output}); err != nil {
		timings.Execution = time.Since(executionStarted)
		return nil, timings, err
	}
	timings.Execution = time.Since(executionStarted)
	vector := append([]float32(nil), output.GetData()...)
	// Graph output must already be normalized. Do not hide graph/export errors.
	if !audio.MERTParity(vector, vector) {
		clear(vector)
		return nil, timings, fmt.Errorf("invalid MERT output")
	}
	return vector, timings, nil
}

type mertHealthFixture struct {
	Fixtures []struct {
		Name      string    `json:"name"`
		ToneHz    float64   `json:"toneHz"`
		Samples   int       `json:"samples"`
		Embedding []float32 `json:"embedding"`
	} `json:"fixtures"`
}

func mertHealth(session *ort.DynamicAdvancedSession, path, backend string) error {
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
	maximumAllowedError := 0.0001
	if backend == "cuda" {
		// CUDA kernels can choose different floating-point reduction orders than
		// the pinned CPU/PyTorch reference while retaining essentially identical
		// direction. Keep a bounded component gate as well as cosine parity.
		maximumAllowedError = 0.003
	}
	observedMaximumError, observedMinimumCosine := 0.0, 1.0
	validParity := true
	for _, test := range fixture.Fixtures {
		if test.Samples < 400 || test.Samples > audio.MERTSegmentSamples || math.IsNaN(test.ToneHz) || test.ToneHz < 0 || test.ToneHz >= audio.MERTSampleRate/2 {
			return fmt.Errorf("invalid MERT health tone")
		}
		pcm := make([]float32, test.Samples)
		for j := range pcm {
			pcm[j] = float32(0.1 * math.Sin(2*math.Pi*test.ToneHz*float64(j)/audio.MERTSampleRate))
		}
		vector, _, err := mertEmbedding(session, pcm)
		clear(pcm)
		maximumError, cosine, valid := audio.MERTParityMetrics(vector, test.Embedding)
		clear(vector)
		if err != nil {
			return err
		}
		if maximumError > observedMaximumError {
			observedMaximumError = maximumError
		}
		if cosine < observedMinimumCosine {
			observedMinimumCosine = cosine
		}
		validParity = validParity && valid
	}
	if !validParity || observedMaximumError > maximumAllowedError || observedMinimumCosine < 0.9999 {
		return fmt.Errorf("MERT health parity mismatch: maximum_error=%g minimum_cosine=%g", observedMaximumError, observedMinimumCosine)
	}
	return nil
}
