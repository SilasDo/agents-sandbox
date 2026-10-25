//go:build linux

package sysinfo

import (
	"os"
)

// procMeminfoPath is overridable so tests can exercise the read and parse
// error paths without a real /proc/meminfo.
var procMeminfoPath = "/proc/meminfo"

func TotalMemoryGiB() int {
	data, err := os.ReadFile(procMeminfoPath)
	// gomutants:disable-next-line BRANCH_IF reason="read error yields empty data; parse returns 0 too"
	if err != nil {
		return 0
	}
	totalKB, ok := parseMemInfo(data)
	// gomutants:disable-next-line BRANCH_IF reason="parseMemInfo returns 0 whenever ok is false"
	if !ok {
		return 0
	}
	return totalKB / (kiB * kiB)
}
