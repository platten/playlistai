//go:build windows

package localaudio

import "io/fs"

// Windows does not represent executability with Unix permission bits and has
// no portable directory-fsync operation. Payload identity and bytes are still
// pinned and checksum-verified before atomic promotion.
func executableModeValid(fs.FileInfo) bool { return true }

func syncDirectory(string) error { return nil }
