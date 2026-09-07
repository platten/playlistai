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

type inference struct {
	audio, text  *ort.DynamicAdvancedSession
	tokenizer    *audio.RobertaTokenizer
	textUnpadded bool
}

// Run serves framed audio/text inference in an isolated child process.
func Run(dir string) error {
	manifest, err := audio.ReadRuntimeBundle(dir)
	if err != nil {
		return err
	}
	if err := audio.CheckPreprocessing(manifest.File(dir, "preprocessing")); err != nil {
		return err
	}
	ort.SetSharedLibraryPath(manifest.File(dir, "runtime"))
	if err := ort.InitializeEnvironment(); err != nil {
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
	audioOutput, textOutput := "embedding", "embedding"
	if manifest.Version == 2 {
		audioOutput, textOutput = manifest.ONNXOutputNames[0], manifest.ONNXOutputNames[1]
	}
	a, err := ort.NewDynamicAdvancedSession(manifest.File(dir, "audio_model"), []string{"input_features"}, []string{audioOutput}, options)
	if err != nil {
		return err
	}
	defer func() { _ = a.Destroy() }()
	textInputs := []string{"input_ids", "attention_mask"}
	if manifest.TextUnpadded {
		textInputs = []string{"input_ids"}
	}
	t, err := ort.NewDynamicAdvancedSession(manifest.File(dir, "text_model"), textInputs, []string{textOutput}, options)
	if err != nil {
		return err
	}
	defer func() { _ = t.Destroy() }()
	tokenizer, err := audio.LoadTokenizer(manifest.File(dir, "vocabulary"), manifest.File(dir, "merges"))
	if err != nil {
		return err
	}
	i := inference{audio: a, text: t, tokenizer: tokenizer, textUnpadded: manifest.TextUnpadded}
	for {
		var request audio.WorkerRequest
		if err := audio.ReadFrame(os.Stdin, &request, 16<<20); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		response := audio.WorkerResponse{Protocol: audio.WorkerProtocol, Model: manifest.Model}
		if request.Protocol != audio.WorkerProtocol {
			return fmt.Errorf("protocol mismatch")
		}
		switch {
		case request.Health:
			err = i.health(manifest.File(dir, "health"))
		case len(request.Audio) > 0:
			response.Vector, err = i.audioEmbedding(request.Audio)
		default:
			response.Vector, err = i.textEmbedding(request.Text)
		}
		clear(request.Audio)
		if err != nil {
			response.Error = "inference unavailable"
		}
		if err := audio.WriteFrame(os.Stdout, response); err != nil {
			return err
		}
		clear(response.Vector)
	}
}

func (i *inference) audioEmbedding(samples []float32) ([]float32, error) {
	if len(samples) != audio.SegmentSamples {
		return nil, fmt.Errorf("invalid audio shape")
	}
	mel, err := audio.LogMel(samples)
	if err != nil {
		return nil, err
	}
	defer clear(mel)
	input, err := ort.NewTensor(ort.NewShape(1, 1, audio.MelFrames, audio.MelBins), mel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = input.Destroy() }()
	return infer(i.audio, []ort.Value{input})
}
func (i *inference) textEmbedding(text string) ([]float32, error) {
	ids, mask, err := i.tokenizer.Encode(text)
	if err != nil {
		return nil, err
	}
	defer clear(ids)
	defer clear(mask)
	if i.textUnpadded {
		count := 0
		for _, value := range mask {
			count += int(value)
		}
		ids = ids[:count]
	}
	x, err := ort.NewTensor(ort.NewShape(1, int64(len(ids))), ids)
	if err != nil {
		return nil, err
	}
	defer func() { _ = x.Destroy() }()
	if i.textUnpadded {
		return infer(i.text, []ort.Value{x})
	}
	y, err := ort.NewTensor(ort.NewShape(1, 77), mask)
	if err != nil {
		return nil, err
	}
	defer func() { _ = y.Destroy() }()
	return infer(i.text, []ort.Value{x, y})
}
func infer(session *ort.DynamicAdvancedSession, inputs []ort.Value) ([]float32, error) {
	output, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 512))
	if err != nil {
		return nil, err
	}
	defer func() { clear(output.GetData()); _ = output.Destroy() }()
	if err := session.Run(inputs, []ort.Value{output}); err != nil {
		return nil, err
	}
	vector := append([]float32(nil), output.GetData()...)
	var norm float64
	for _, x := range vector {
		norm += float64(x) * float64(x)
	}
	if math.IsNaN(norm) || math.IsInf(norm, 0) || norm <= 1e-12 {
		return nil, fmt.Errorf("invalid output norm")
	}
	for j := range vector {
		vector[j] /= float32(math.Sqrt(norm))
	}
	return vector, nil
}

// Health compares actual inference to pinned reference outputs. Synthetic PCM
// is generated in memory; the bundle carries no music audio.
func (i *inference) health(path string) error {
	var fixture struct {
		Text           string    `json:"text"`
		TextEmbedding  []float32 `json:"textEmbedding"`
		ToneHz         float64   `json:"toneHz"`
		AudioEmbedding []float32 `json:"audioEmbedding"`
		TokenIDs       []int64   `json:"tokenIds"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return err
	}
	ids, _, err := i.tokenizer.Encode(fixture.Text)
	if err != nil {
		return err
	}
	if len(ids) != len(fixture.TokenIDs) {
		return fmt.Errorf("tokenizer health mismatch")
	}
	for j := range ids {
		if ids[j] != fixture.TokenIDs[j] {
			return fmt.Errorf("tokenizer health mismatch")
		}
	}
	text, err := i.textEmbedding(fixture.Text)
	if err != nil {
		return err
	}
	if !parity(text, fixture.TextEmbedding) {
		return fmt.Errorf("text health mismatch")
	}
	if fixture.ToneHz <= 0 || fixture.ToneHz >= audio.SampleRate/2 {
		return fmt.Errorf("invalid health tone")
	}
	pcm := make([]float32, audio.SegmentSamples)
	defer clear(pcm)
	for j := range pcm {
		pcm[j] = float32(0.1 * math.Sin(2*math.Pi*fixture.ToneHz*float64(j)/audio.SampleRate))
	}
	vector, err := i.audioEmbedding(pcm)
	if err != nil {
		return err
	}
	if !parity(vector, fixture.AudioEmbedding) {
		return fmt.Errorf("audio health mismatch")
	}
	return nil
}
func parity(a, b []float32) bool {
	if len(a) != 512 || len(b) != 512 {
		return false
	}
	for j := range a {
		if math.IsNaN(float64(a[j])) || math.IsNaN(float64(b[j])) || math.IsInf(float64(b[j]), 0) || math.Abs(float64(a[j]-b[j])) > 0.0001 {
			return false
		}
	}
	return true
}
