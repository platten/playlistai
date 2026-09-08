//go:build production

// Package build exposes the version already used by the installer configuration.
package build

import (
	_ "embed"
	"regexp"
)

//go:embed config.yml
var configuration string

var Version = configVersion()

func configVersion() string {
	match := regexp.MustCompile(`(?m)^  version: "([0-9]+\.[0-9]+\.[0-9]+)"`).FindStringSubmatch(configuration)
	if len(match) != 2 {
		panic("build/config.yml must contain info.version")
	}
	return match[1]
}
