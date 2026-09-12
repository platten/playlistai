//go:build !cgo

package nlu

func loadNativeModel(WorkerConfig, modelSettings) (nativeModel, error) {
	return nil, ErrNativeUnavailable
}
