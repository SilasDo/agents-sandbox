package sysinfo

import (
	"testing"
)

func TestParseMemInfo(t *testing.T) {
	data := []byte("MemTotal:       16384000 kB\nMemFree:          123456 kB\nMemAvailable:    8000000 kB\n")
	totalKB, ok := parseMemInfo(data)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if totalKB != 16384000 {
		t.Errorf("expected 16384000, got %d", totalKB)
	}
}

func TestParseMemInfoMissing(t *testing.T) {
	data := []byte("MemFree: 123 kB\n")
	got, ok := parseMemInfo(data)
	if ok {
		t.Error("expected ok=false when MemTotal missing")
	}
	if got != 0 {
		t.Errorf("expected 0 KiB on failure, got %d", got)
	}
}

func TestParseMemInfoEmpty(t *testing.T) {
	got, ok := parseMemInfo(nil)
	if ok {
		t.Error("expected ok=false for empty input")
	}
	if got != 0 {
		t.Errorf("expected 0 KiB on failure, got %d", got)
	}
}

func TestParseMemInfoIgnoresNonMemTotalLines(t *testing.T) {
	data := []byte("MemFree: 123 kB\nMemTotal:       16384000 kB\n")
	totalKB, ok := parseMemInfo(data)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if totalKB != 16384000 {
		t.Errorf("expected 16384000, got %d", totalKB)
	}
}

func TestParseMemInfoMissingValue(t *testing.T) {
	data := []byte("MemTotal:\n")
	got, ok := parseMemInfo(data)
	if ok {
		t.Error("expected ok=false when MemTotal has no value field")
	}
	if got != 0 {
		t.Errorf("expected 0 KiB on failure, got %d", got)
	}
}

func TestParseMemInfoInvalidNumber(t *testing.T) {
	data := []byte("MemTotal: notanumber kB\n")
	got, ok := parseMemInfo(data)
	if ok {
		t.Error("expected ok=false when MemTotal value is not an integer")
	}
	if got != 0 {
		t.Errorf("expected 0 KiB on failure, got %d", got)
	}
}

func TestParseMemInfoTwoFields(t *testing.T) {
	data := []byte("MemTotal:\t16384000\n")
	totalKB, ok := parseMemInfo(data)
	if !ok {
		t.Fatal("expected ok=true for a two-field MemTotal line")
	}
	if totalKB != 16384000 {
		t.Errorf("expected 16384000, got %d", totalKB)
	}
}

func TestNumCPUsAtLeastOne(t *testing.T) {
	cpus := NumCPUs()
	if cpus < 1 {
		t.Errorf("expected at least 1 CPU, got %d", cpus)
	}
}
