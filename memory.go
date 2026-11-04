package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// readFile is a thin indirection so helpers can be unit-tested later.
func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

// MemoryInfo describes RAM and swap, including zram compressed swap details.
type MemoryInfo struct {
	TotalKB     uint64
	FreeKB      uint64
	AvailableKB uint64
	BuffersKB   uint64
	CachedKB    uint64
	UsedKB      uint64
	SwapTotalKB uint64
	SwapFreeKB  uint64
	SwapUsedKB  uint64
	Zram        []ZramDevice
}

// ZramDevice holds the human-oriented view of a zram compressed block device.
type ZramDevice struct {
	Name              string
	Algorithm         string
	DisksizeBytes     uint64 // configured size of the compressed block device
	OriginalBytes     uint64 // uncompressed data currently stored
	CompressedBytes   uint64 // physical memory actually occupied by the data
	MemUsedTotalBytes uint64 // total memory used including metadata
	CompressionRatio  float64
	UsedPercent       float64 // share of the disk size currently holding data
}

func collectMemoryInfo() MemoryInfo {
	mi := MemoryInfo{}
	vals := readMemInfo("/proc/meminfo")
	mi.TotalKB = vals["MemTotal"]
	mi.FreeKB = vals["MemFree"]
	mi.AvailableKB = vals["MemAvailable"]
	mi.BuffersKB = vals["Buffers"]
	mi.CachedKB = vals["Cached"]
	mi.SwapTotalKB = vals["SwapTotal"]
	mi.SwapFreeKB = vals["SwapFree"]

	// "Used" as the kernel reports it: everything that is not available.
	if mi.TotalKB > mi.AvailableKB {
		mi.UsedKB = mi.TotalKB - mi.AvailableKB
	}
	if mi.SwapTotalKB > mi.SwapFreeKB {
		mi.SwapUsedKB = mi.SwapTotalKB - mi.SwapFreeKB
	}

	mi.Zram = collectZram()
	return mi
}

func readMemInfo(path string) map[string]uint64 {
	m := map[string]uint64{}
	b, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	for _, line := range strings.Split(string(b), "\n") {
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		rest := strings.TrimSpace(line[idx+1:])
		rest = strings.TrimSuffix(rest, "kB")
		rest = strings.TrimSpace(rest)
		v, err := strconv.ParseUint(rest, 10, 64)
		if err != nil {
			continue
		}
		m[key] = v
	}
	return m
}

// collectZram finds every zram device and reads its statistics from sysfs.
// Layout: /sys/block/zram0/ with mm_stat / comp_algorithm / disksize.
func collectZram() []ZramDevice {
	var out []ZramDevice
	entries, err := filepath.Glob("/sys/block/zram*")
	if err != nil {
		return out
	}
	for _, dir := range entries {
		out = append(out, parseZramDevice(dir))
	}
	return out
}

// parseZramDevice reads a single zram sysfs directory. Kept separate so it can
// be exercised against fixture data.
func parseZramDevice(dir string) ZramDevice {
	z := ZramDevice{Name: filepath.Base(dir)}

	z.Algorithm = readFileFirstLine(filepath.Join(dir, "comp_algorithm"))
	// comp_algorithm lists all algorithms with the active one in [brackets].
	if strings.Contains(z.Algorithm, "[") {
		if s := extractBracket(z.Algorithm); s != "" {
			z.Algorithm = s
		}
	}
	z.DisksizeBytes = parseUint(readFileTrim(filepath.Join(dir, "disksize")))

	// mm_stat: orig_data_size compr_data_size mem_used_total mem_limit
	//          mem_used_max same_pages pages_compacted huge_pages ...
	if stat := readFileTrim(filepath.Join(dir, "mm_stat")); stat != "" {
		f := strings.Fields(stat)
		if len(f) >= 3 {
			z.OriginalBytes = parseUint(f[0])
			z.CompressedBytes = parseUint(f[1])
			z.MemUsedTotalBytes = parseUint(f[2])
		}
	}
	if z.OriginalBytes == 0 {
		z.OriginalBytes = parseUint(readFileTrim(filepath.Join(dir, "orig_data_size")))
	}
	if z.CompressedBytes == 0 {
		z.CompressedBytes = parseUint(readFileTrim(filepath.Join(dir, "compr_data_size")))
	}
	if z.MemUsedTotalBytes == 0 {
		z.MemUsedTotalBytes = parseUint(readFileTrim(filepath.Join(dir, "mem_used_total")))
	}

	if z.CompressedBytes > 0 {
		z.CompressionRatio = float64(z.OriginalBytes) / float64(z.CompressedBytes)
	}
	if z.DisksizeBytes > 0 {
		z.UsedPercent = float64(z.OriginalBytes) / float64(z.DisksizeBytes) * 100
	}
	return z
}

func extractBracket(s string) string {
	start := strings.Index(s, "[")
	end := strings.Index(s, "]")
	if start < 0 || end < 0 || end <= start+1 {
		return ""
	}
	return s[start+1 : end]
}
