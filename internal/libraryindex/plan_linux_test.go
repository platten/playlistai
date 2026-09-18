//go:build linux

package libraryindex

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestCountPhysicalCoresIntersectsAllowedLogicalCPUs(t *testing.T) {
	read := func(path string) ([]byte, error) {
		parts := strings.Split(filepath.ToSlash(path), "/")
		if len(parts) < 2 {
			return nil, errors.New("bad topology path")
		}
		cpu := parts[len(parts)-3]
		field := parts[len(parts)-1]
		var id int
		if _, err := fmt.Sscanf(cpu, "cpu%d", &id); err != nil {
			return nil, err
		}
		switch field {
		case "physical_package_id":
			return []byte("0\n"), nil
		case "core_id":
			return []byte(fmt.Sprintf("%d\n", id/2)), nil
		default:
			return nil, errors.New("unknown field")
		}
	}
	if got := countPhysicalCores([]int{0, 1, 2, 3, 8, 9}, read); got != 3 {
		t.Fatalf("physical cores=%d want 3", got)
	}
}

func TestCountPhysicalCoresKeepsPackagesDistinctAndFallsBackOnMissingData(t *testing.T) {
	read := func(path string) ([]byte, error) {
		cpuOne := strings.Contains(filepath.ToSlash(path), "/cpu1/")
		if strings.HasSuffix(path, "physical_package_id") {
			if cpuOne {
				return []byte("1"), nil
			}
			return []byte("0"), nil
		}
		return []byte("0"), nil
	}
	if got := countPhysicalCores([]int{0, 1}, read); got != 2 {
		t.Fatalf("multi-package physical cores=%d want 2", got)
	}
	if got := countPhysicalCores([]int{0}, func(string) ([]byte, error) { return nil, errors.New("missing") }); got != 0 {
		t.Fatalf("incomplete topology returned %d cores", got)
	}
}
