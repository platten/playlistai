//go:build cgo

package nlu

import (
	"fmt"
	"path/filepath"

	ort "github.com/yalue/onnxruntime_go"
)

type onnxModel struct {
	session        *ort.DynamicAdvancedSession
	kind           ModelKind
	dimension      int
	releaseLibrary func()
}

func loadNativeModel(config WorkerConfig, settings modelSettings) (nativeModel, error) {
	releaseLibrary, err := prepareRuntime(config.RuntimeLibrary)
	if err != nil {
		return nil, err
	}
	ort.SetSharedLibraryPath(config.RuntimeLibrary)
	if err = ort.InitializeEnvironment(ort.WithLogLevelError()); err != nil {
		releaseLibrary()
		return nil, err
	}
	cleanup := func() { _ = ort.DestroyEnvironment(); releaseLibrary() }
	if err = ort.DisableTelemetry(); err != nil {
		cleanup()
		return nil, err
	}
	options, err := ort.NewSessionOptions()
	if err != nil {
		cleanup()
		return nil, err
	}
	defer func() { _ = options.Destroy() }()
	for _, err := range []error{options.SetIntraOpNumThreads(2), options.SetInterOpNumThreads(1), options.SetCpuMemArena(false), options.SetMemPattern(false)} {
		if err != nil {
			cleanup()
			return nil, err
		}
	}
	inputs, output, dimension := []string{"input_ids", "attention_mask", "token_type_ids"}, "last_hidden_state", EmbeddingDimension
	if config.Kind == DistilBERT {
		if settings.head == nil {
			cleanup()
			return nil, fmt.Errorf("nlu: trained head required")
		}
		inputs, output, dimension = []string{"input_ids", "attention_mask"}, settings.head.OutputName, len(settings.head.Labels)
	}
	session, err := ort.NewDynamicAdvancedSession(filepath.Join(config.ModelDir, "model.onnx"), inputs, []string{output}, options)
	if err != nil {
		cleanup()
		return nil, err
	}
	return &onnxModel{session: session, kind: config.Kind, dimension: dimension, releaseLibrary: releaseLibrary}, nil
}

func (m *onnxModel) Infer(encoding Encoding) ([]float32, error) {
	length := len(encoding.IDs)
	if length < 2 || len(encoding.AttentionMask) != length || len(encoding.TypeIDs) != length {
		return nil, fmt.Errorf("nlu: invalid tensor inputs")
	}
	ids, err := ort.NewTensor(ort.NewShape(1, int64(length)), encoding.IDs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = ids.Destroy() }()
	mask, err := ort.NewTensor(ort.NewShape(1, int64(length)), encoding.AttentionMask)
	if err != nil {
		return nil, err
	}
	defer func() { _ = mask.Destroy() }()
	inputs := []ort.Value{ids, mask}
	if m.kind == MiniLM {
		types, err := ort.NewTensor(ort.NewShape(1, int64(length)), encoding.TypeIDs)
		if err != nil {
			return nil, err
		}
		defer func() { _ = types.Destroy() }()
		inputs = append(inputs, types)
	}
	output, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(length), int64(m.dimension)))
	if err != nil {
		return nil, err
	}
	defer func() { clear(output.GetData()); _ = output.Destroy() }()
	if err = m.session.Run(inputs, []ort.Value{output}); err != nil {
		return nil, err
	}
	return append([]float32(nil), output.GetData()...), nil
}

func (m *onnxModel) Close() error {
	err := m.session.Destroy()
	if envErr := ort.DestroyEnvironment(); err == nil {
		err = envErr
	}
	m.releaseLibrary()
	return err
}
