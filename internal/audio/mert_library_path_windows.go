//go:build windows

package audio

func prependMERTLibraryPath(environment []string, directory string) []string {
	return prependMERTEnvironmentPath(environment, "PATH", directory, true)
}
