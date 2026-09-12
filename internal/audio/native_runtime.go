package audio

import (
	"context"
	"fmt"
)

// NativeRuntimeArtifact shares the pinned CPU runtime across isolated audio and
// text workers. Their model spaces and sessions remain separate.
func NativeRuntimeArtifact(platform string) (BundleArtifact, error) {
	a, ok := recommendedRuntimes[platform]
	if !ok {
		return BundleArtifact{}, fmt.Errorf("no native ONNX runtime for %s", platform)
	}
	return a, nil
}

// UnpackNativeRuntime extracts only the checksum-pinned regular library member.
func UnpackNativeRuntime(ctx context.Context, dir string, artifact BundleArtifact) error {
	return unpackRuntime(ctx, dir, artifact)
}
