//go:build linux

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func readBenchmarkHostDiagnostics(root, device string) benchmarkHostDiagnostics {
	diagnostics := benchmarkHostDiagnostics{Filesystem: "unknown", GPUDevice: device}
	var stat unix.Statfs_t
	if unix.Statfs(root, &stat) == nil {
		diagnostics.Filesystem = filesystemTypeName(stat.Type)
	}
	if file, err := os.Open("/proc/self/status"); err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			if value, found := strings.CutPrefix(scanner.Text(), "Cpus_allowed_list:"); found {
				diagnostics.Affinity = strings.TrimSpace(value)
				break
			}
		}
		_ = file.Close()
	}
	paths, _ := filepath.Glob("/proc/driver/nvidia/gpus/*/information")
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) > 1<<20 {
			continue
		}
		model, bus := "", ""
		for _, line := range strings.Split(string(raw), "\n") {
			if value, found := strings.CutPrefix(line, "Model:"); found {
				model = strings.TrimSpace(value)
			}
			if value, found := strings.CutPrefix(line, "Bus Location:"); found {
				bus = strings.TrimSpace(value)
			}
		}
		if model != "" {
			if bus != "" {
				model += " (" + bus + ")"
			}
			diagnostics.GPUIdentities = append(diagnostics.GPUIdentities, model)
		}
	}
	if raw, err := os.ReadFile("/sys/firmware/acpi/platform_profile"); err == nil {
		diagnostics.PowerProfile = strings.TrimSpace(string(raw))
	}
	return diagnostics
}

func filesystemTypeName(value int64) string {
	switch uint64(value) {
	case 0x2fc12fc1:
		return "zfs"
	case 0xef53:
		return "ext"
	case 0x9123683e:
		return "btrfs"
	case 0x58465342:
		return "xfs"
	case 0x6969:
		return "nfs"
	case 0xff534d42:
		return "cifs"
	default:
		return fmt.Sprintf("0x%x", uint64(value))
	}
}

func readBenchmarkARCSnapshot() benchmarkARCSnapshot {
	file, err := os.Open("/proc/spl/kstat/zfs/arcstats")
	if err != nil {
		return benchmarkARCSnapshot{}
	}
	defer file.Close()
	return parseBenchmarkARCSnapshot(file)
}

func parseBenchmarkARCSnapshot(reader io.Reader) benchmarkARCSnapshot {
	var snapshot benchmarkARCSnapshot
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		value, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "size":
			snapshot.SizeBytes = value
		case "hits":
			snapshot.Hits = value
		case "misses":
			snapshot.Misses = value
		}
	}
	return snapshot
}
