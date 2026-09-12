package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/platten/playlistai/internal/core"
)

// readEnhancedEvidence freezes only saved derived evidence. It does not wire an
// analysis provider, mutate stores, load MERT, or obtain any preview audio.
func readEnhancedEvidence(path, catalogVersion string) (*core.EnhancedAudioSnapshot, string, error) {
	if path == "" {
		return nil, "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = f.Close() }()
	const limit = 64 << 20
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, "", err
	}
	if len(b) > limit {
		return nil, "", fmt.Errorf("enhanced evidence exceeds 64 MiB")
	}
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil, "", fmt.Errorf("enhanced evidence must be an object")
	}
	var input core.EnhancedAudioInput
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		return nil, "", err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return nil, "", fmt.Errorf("enhanced evidence must contain exactly one object")
	}
	if input.CatalogVersion != "" && input.CatalogVersion != catalogVersion {
		return nil, "", fmt.Errorf("enhanced evidence belongs to another catalog")
	}
	if input.PolicyVersion != "" && input.PolicyVersion != core.EnhancedAudioPolicyVersion {
		return nil, "", fmt.Errorf("enhanced evidence uses an unsupported policy")
	}
	snapshot, err := core.NewEnhancedAudioSnapshot(input)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(b)
	return snapshot, hex.EncodeToString(digest[:]), nil
}
