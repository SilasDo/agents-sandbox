package sysinfo

import (
	"runtime"
	"strconv"
	"strings"
)

const (
	// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT reason="unreachable below 255 CPUs"
	maxCPUs = 255
	kiB     = 1024
)

func NumCPUs() uint8 {
	// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT reason="NumCPU is always >=1; no seam to force 0"
	n := min(max(runtime.NumCPU(), 1), maxCPUs)
	return uint8(n)
}

func parseMemInfo(data []byte) (int, bool) {
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kb, err := strconv.Atoi(fields[1])
		if err != nil {
			return 0, false
		}
		return kb, true
	}
	return 0, false
}
