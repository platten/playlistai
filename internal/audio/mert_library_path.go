package audio

import (
	"os"
	"strings"
)

// prependMERTEnvironmentPath replaces inherited loader-path entries instead of
// appending a duplicate environment key. Some native loaders use the first
// duplicate while others use the last, so retaining both is not deterministic.
func prependMERTEnvironmentPath(environment []string, name, directory string, caseInsensitive bool) []string {
	result := make([]string, 0, len(environment)+1)
	existing := ""
	for _, entry := range environment {
		key, value, found := strings.Cut(entry, "=")
		matches := found && key == name
		if caseInsensitive {
			matches = found && strings.EqualFold(key, name)
		}
		if matches {
			if existing == "" {
				existing = value
			}
			continue
		}
		result = append(result, entry)
	}
	value := directory
	if existing != "" {
		value += string(os.PathListSeparator) + existing
	}
	return append(result, name+"="+value)
}
