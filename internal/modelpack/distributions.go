package modelpack

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// Distribution identifies an immutable reviewed manifest on the model host.
type Distribution struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	SHA256        string `json:"sha256"`
	DownloadBytes int64  `json:"downloadBytes"`
}

//go:embed distributions.json
var distributionJSON []byte

func recommended(name string) (Distribution, error) {
	var values []Distribution
	if err := json.Unmarshal(distributionJSON, &values); err != nil {
		return Distribution{}, fmt.Errorf("invalid embedded model registry: %w", err)
	}
	for _, d := range values {
		if d.Name == name {
			if !httpsURL(d.URL) || !validHash(d.SHA256) || d.DownloadBytes <= 0 {
				return Distribution{}, fmt.Errorf("invalid embedded model distribution")
			}
			return d, nil
		}
	}
	return Distribution{}, fmt.Errorf("no hosted model pack for %s", name)
}

func RecommendedMERT(goos, goarch string) (Distribution, error) {
	return recommended("mert-" + goos + "-" + goarch)
}

func RecommendedIntent() Distribution {
	d, err := recommended("intent-encoders-v1")
	if err != nil {
		panic(err)
	}
	return d
}
