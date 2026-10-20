// Package hostcapacity reads trusted host facts without changing instance specs.
package hostcapacity

import (
	"errors"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
)

var ErrUnavailable = errors.New("host compute capacity cannot be confirmed")

type Facts struct {
	CPUs            int
	AvailableMemory int64
}

// Fits checks a selected specification while preserving control-plane reserve.
func (f Facts) Fits(cpuMillicores, memoryBytes int64) bool {
	return cpuMillicores > 0 && memoryBytes > 0 && f.CPUs > 4 && f.AvailableMemory > 8<<30 &&
		cpuMillicores <= int64(f.CPUs-4)*1000 && memoryBytes <= f.AvailableMemory-(8<<30)
}

// Slots counts additional fixed instances after retaining 4 CPUs/8 GiB for the
// control plane. Existing workload memory is already absent from MemAvailable.
func (f Facts) Slots() int {
	cpu := (f.CPUs - 4) / 2
	memory := (f.AvailableMemory - (8 << 30)) / (4 << 30)
	if f.CPUs < 6 || f.AvailableMemory < 12<<30 {
		return 0
	}
	if memory < int64(cpu) {
		cpu = int(memory)
	}
	if cpu > 5 {
		cpu = 5
	}
	return cpu
}
func parseMemory(raw string) (int64, error) {
	var available int64
	found := false
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "MemAvailable:" {
			continue
		}
		if found || len(fields) != 3 || fields[2] != "kB" {
			return 0, ErrUnavailable
		}
		n, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || n < 0 || n > math.MaxInt64/1024 {
			return 0, ErrUnavailable
		}
		available, found = n*1024, true
	}
	if !found {
		return 0, ErrUnavailable
	}
	return available, nil
}
func Read() (Facts, error) {
	if runtime.GOOS != "linux" {
		return Facts{}, ErrUnavailable
	}
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil || len(raw) > 65536 {
		return Facts{}, ErrUnavailable
	}
	memory, err := parseMemory(string(raw))
	if err != nil {
		return Facts{}, err
	}
	return Facts{runtime.NumCPU(), memory}, nil
}
