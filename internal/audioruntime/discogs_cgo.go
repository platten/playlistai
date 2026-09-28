//go:build cgo

package audioruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/platten/playlistai/internal/audio"
)

// RunDiscogs serves the original EffNet encoder and three classification heads.
func RunDiscogs(modelDir, runtimeDir string) error {
	if _, err := audio.ReadDiscogsModel(context.Background(), modelDir); err != nil {
		return err
	}
	bundle, err := audio.ReadRuntimeBundle(runtimeDir)
	if err != nil {
		return err
	}
	ort.SetSharedLibraryPath(bundle.File(runtimeDir, "runtime"))
	if err = ort.InitializeEnvironment(); err != nil {
		return err
	}
	defer func() { _ = ort.DestroyEnvironment() }()
	options, err := ort.NewSessionOptions()
	if err != nil {
		return err
	}
	defer func() { _ = options.Destroy() }()
	for _, e := range []error{options.SetIntraOpNumThreads(2), options.SetInterOpNumThreads(1), options.SetCpuMemArena(false), options.SetMemPattern(false)} {
		if e != nil {
			return e
		}
	}
	encoder, err := ort.NewDynamicAdvancedSession(modelDir+string(os.PathSeparator)+"encoder.onnx", []string{"melspectrogram"}, []string{"activations", "embeddings"}, options)
	if err != nil {
		return err
	}
	defer func() { _ = encoder.Destroy() }()
	heads := make([]*ort.DynamicAdvancedSession, 3)
	for i, name := range []string{"instrument", "vocal", "mood"} {
		heads[i], err = ort.NewDynamicAdvancedSession(modelDir+string(os.PathSeparator)+name+".onnx", []string{"embeddings"}, []string{"activations"}, options)
		if err != nil {
			for _, h := range heads {
				if h != nil {
					_ = h.Destroy()
				}
			}
			return err
		}
	}
	defer func() {
		for _, h := range heads {
			_ = h.Destroy()
		}
	}()
	for {
		var request audio.DiscogsWorkerRequest
		if err := audio.ReadFrame(os.Stdin, &request, 32<<20); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if request.Protocol != audio.DiscogsWorkerProtocol {
			return fmt.Errorf("discogs protocol mismatch")
		}
		response := audio.DiscogsWorkerResponse{Protocol: audio.DiscogsWorkerProtocol}
		if request.Health {
			tone := make([]float32, 8*16000)
			for i := range tone {
				tone[i] = float32(.25 * math.Sin(2*math.Pi*440*float64(i)/16000))
			}
			response.Scores, err = discogsScores(encoder, heads, tone)
			clear(tone)
			if err == nil && (math.Abs(float64(response.Scores[0][0])-.000004270) > .0001 || math.Abs(float64(response.Scores[1][0])-.00783490) > .001 || math.Abs(float64(response.Scores[2][0])-.98369294) > .001 || math.Abs(float64(response.Scores[3][0])-.13318640) > .001) {
				err = fmt.Errorf("discogs native parity check failed")
			}
			response.Scores = nil
		} else {
			response.Scores, err = discogsScores(encoder, heads, request.Audio)
		}
		clear(request.Audio)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			response.Error = "Discogs inference unavailable"
		}
		if err := audio.WriteFrame(os.Stdout, response); err != nil {
			return err
		}
		for _, row := range response.Scores {
			clear(row)
		}
	}
}

func discogsScores(encoder *ort.DynamicAdvancedSession, heads []*ort.DynamicAdvancedSession, pcm []float32) ([][]float32, error) {
	mel, count, err := audio.DiscogsMelPatches(pcm)
	if err != nil {
		return nil, err
	}
	defer clear(mel)
	input, err := ort.NewTensor(ort.NewShape(int64(count), 128, 96), mel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = input.Destroy() }()
	style, err := ort.NewEmptyTensor[float32](ort.NewShape(int64(count), 400))
	if err != nil {
		return nil, err
	}
	defer func() { clear(style.GetData()); _ = style.Destroy() }()
	embedding, err := ort.NewEmptyTensor[float32](ort.NewShape(int64(count), 1280))
	if err != nil {
		return nil, err
	}
	defer func() { clear(embedding.GetData()); _ = embedding.Destroy() }()
	if err := encoder.Run([]ort.Value{input}, []ort.Value{style, embedding}); err != nil {
		return nil, err
	}
	out := make([][]float32, 4)
	out[0], err = discogsAverage(style.GetData(), count, 400)
	if err != nil {
		return nil, err
	}
	for i, dimension := range []int{40, 2, 2} {
		result, e := ort.NewEmptyTensor[float32](ort.NewShape(int64(count), int64(dimension)))
		if e != nil {
			return nil, e
		}
		e = heads[i].Run([]ort.Value{embedding}, []ort.Value{result})
		if e == nil {
			out[i+1], e = discogsAverage(result.GetData(), count, dimension)
		}
		clear(result.GetData())
		_ = result.Destroy()
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func discogsAverage(values []float32, patches, classes int) ([]float32, error) {
	if patches <= 0 || len(values) != patches*classes {
		return nil, fmt.Errorf("invalid Discogs output")
	}
	out := make([]float32, classes)
	for patch := 0; patch < patches; patch++ {
		for class := range out {
			v := values[patch*classes+class]
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || v < 0 || v > 1 {
				return nil, fmt.Errorf("nonfinite Discogs output")
			}
			out[class] += v / float32(patches)
		}
	}
	return out, nil
}
