//go:build !windows

package discoveryasset

import "os"

func atomicReplaceFile(source, target string) error { return os.Rename(source, target) }
func syncDirectory(name string) error {
	f, e := os.Open(name)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
