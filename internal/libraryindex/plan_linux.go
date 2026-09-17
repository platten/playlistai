//go:build linux

package libraryindex

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func detectHostCapacity() hostCapacity {
	capacity := hostCapacity{GOMAXPROCS: runtimeCPUSlots(), CPUSource: "gomaxprocs"}
	capacity.CPUSlots = capacity.GOMAXPROCS
	var affinity unix.CPUSet
	if unix.SchedGetaffinity(0, &affinity) == nil && affinity.Count() > 0 && affinity.Count() < capacity.CPUSlots {
		capacity.CPUSlots = affinity.Count()
		capacity.CPUSource = "affinity"
	}
	cgroup := currentCgroupFiles()
	if raw, err := os.ReadFile(cgroup.cpuMax); err == nil {
		fields := strings.Fields(string(raw))
		if len(fields) == 2 && fields[0] != "max" {
			quota, qerr := strconv.ParseFloat(fields[0], 64)
			period, perr := strconv.ParseFloat(fields[1], 64)
			if qerr == nil && perr == nil && quota > 0 && period > 0 {
				capacity.CPUQuota = quota / period
				// Floor fractional quotas so planned native thread counts do not
				// exceed the sustained entitlement; any positive quota gets one slot.
				slots := max(1, int(capacity.CPUQuota))
				if slots < capacity.CPUSlots {
					capacity.CPUSlots = slots
					capacity.CPUSource = cgroup.cpuSource
				}
			}
		}
	} else if quotaRaw, quotaErr := os.ReadFile(cgroup.cpuQuota); quotaErr == nil {
		periodRaw, periodErr := os.ReadFile(cgroup.cpuPeriod)
		quota, qerr := strconv.ParseFloat(strings.TrimSpace(string(quotaRaw)), 64)
		period, perr := strconv.ParseFloat(strings.TrimSpace(string(periodRaw)), 64)
		if periodErr == nil && qerr == nil && perr == nil && quota > 0 && period > 0 {
			capacity.CPUQuota = quota / period
			slots := max(1, int(capacity.CPUQuota))
			if slots < capacity.CPUSlots {
				capacity.CPUSlots = slots
				capacity.CPUSource = "cgroup-v1"
			}
		}
	}
	capacity.AvailableRAM = procMemAvailable()
	if raw, err := os.ReadFile(cgroup.memoryMax); err == nil && strings.TrimSpace(string(raw)) != "max" {
		if limit, parseErr := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64); parseErr == nil && limit > 0 {
			current := int64(0)
			if used, readErr := os.ReadFile(cgroup.memoryCurrent); readErr == nil {
				current, _ = strconv.ParseInt(strings.TrimSpace(string(used)), 10, 64)
			}
			remaining := max(int64(0), limit-current)
			if capacity.AvailableRAM == 0 || remaining < capacity.AvailableRAM {
				capacity.AvailableRAM = remaining
			}
		}
	}
	var limits unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_NOFILE, &limits) == nil {
		capacity.OpenFileSoft = int(min(uint64(^uint(0)>>1), limits.Cur))
	}
	return capacity
}

type cgroupFileSet struct {
	cpuMax, cpuQuota, cpuPeriod string
	memoryMax, memoryCurrent    string
	cpuSource                   string
}

func currentCgroupFiles() cgroupFileSet {
	files := cgroupFileSet{
		cpuMax: "/sys/fs/cgroup/cpu.max", cpuSource: "cgroup-v2",
		cpuQuota: "/sys/fs/cgroup/cpu/cpu.cfs_quota_us", cpuPeriod: "/sys/fs/cgroup/cpu/cpu.cfs_period_us",
		memoryMax: "/sys/fs/cgroup/memory.max", memoryCurrent: "/sys/fs/cgroup/memory.current",
	}
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return files
	}
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		relative := strings.TrimPrefix(filepath.Clean("/"+parts[2]), "/")
		if parts[1] == "" {
			root := filepath.Join("/sys/fs/cgroup", relative)
			files.cpuMax = filepath.Join(root, "cpu.max")
			files.memoryMax = filepath.Join(root, "memory.max")
			files.memoryCurrent = filepath.Join(root, "memory.current")
			return files
		}
		controllers := "," + parts[1] + ","
		if strings.Contains(controllers, ",cpu,") || strings.Contains(controllers, ",cpuacct,") {
			root := filepath.Join("/sys/fs/cgroup/cpu", relative)
			files.cpuQuota = filepath.Join(root, "cpu.cfs_quota_us")
			files.cpuPeriod = filepath.Join(root, "cpu.cfs_period_us")
		}
		if strings.Contains(controllers, ",memory,") {
			root := filepath.Join("/sys/fs/cgroup/memory", relative)
			files.memoryMax = filepath.Join(root, "memory.limit_in_bytes")
			files.memoryCurrent = filepath.Join(root, "memory.usage_in_bytes")
		}
	}
	return files
}

func procMemAvailable() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			return kb << 10
		}
	}
	return 0
}
