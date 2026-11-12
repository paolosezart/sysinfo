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
	SwapDevices []SwapDevice

	// RAM frequency is only exposed by some SoCs (e.g. via devfreq); zero when
	// the platform does not report it.
	RAMCurMHz float64
	RAMMinMHz float64
	RAMMaxMHz float64

	// RAMType/Speed/Bus come from SMBIOS or the board model; empty/zero when the
	// platform does not reveal them.
	RAMType     string
	RAMSpeedMTs int
	RAMBusBits  int
	RAMPeakGBs  float64
	RAMSource   string // where the values came from: SMBIOS or the board model
}

// SwapDevice is one entry from /proc/swaps.
type SwapDevice struct {
	Filename string
	Type     string // file / partition
	SizeKB   uint64
	UsedKB   uint64
	Priority int
	Location string // human hint, e.g. "файл подкачки" or the backing device
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
	return collectMemoryInfoFor("")
}

// collectMemoryInfoFor is collectMemoryInfo with the board model, which is only
// needed to resolve the RAM type and speed when the platform does not report it.
func collectMemoryInfoFor(model string) MemoryInfo {
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
	mi.SwapDevices = collectSwapDevices()
	mi.RAMCurMHz, mi.RAMMinMHz, mi.RAMMaxMHz = ramFrequency()
	if spec, source, ok := ramInfo(model); ok {
		mi.RAMType = spec.Type
		mi.RAMSpeedMTs = spec.SpeedMTs
		mi.RAMBusBits = spec.BusBits
		mi.RAMPeakGBs = spec.peakGBs()
		mi.RAMSource = source
	}
	return mi
}

// collectSwapDevices parses /proc/swaps so the user can see exactly which swap
// backends exist (a swap file, a partition, zram) and where they live.
func collectSwapDevices() []SwapDevice {
	return parseSwapFile("/proc/swaps")
}

// parseSwapFile reads a /proc/swaps-formatted file.
func parseSwapFile(path string) []SwapDevice {
	b, err := readFile(path)
	if err != nil {
		return nil
	}
	var out []SwapDevice
	lines := strings.Split(string(b), "\n")
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue // header row
		}
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		sw := SwapDevice{
			Filename: f[0],
			Type:     f[1],
			SizeKB:   parseUint(f[2]),
			UsedKB:   parseUint(f[3]),
		}
		if len(f) >= 5 {
			sw.Priority = int(parseUint(f[4]))
		}
		sw.Location = swapLocation(sw)
		out = append(out, sw)
	}
	return out
}

// swapLocation describes where a swap backend lives in human terms.
func swapLocation(sw SwapDevice) string {
	switch sw.Type {
	case "partition":
		if strings.HasPrefix(sw.Filename, "/dev/zram") {
			return L("ZRAM (сжатие в ОЗУ)", "ZRAM (compressed in RAM)")
		}
		return L("раздел диска ", "disk partition ") + sw.Filename
	case "file":
		// Try to find which filesystem the swap file lives on.
		if dev := deviceForPath(sw.Filename); dev != "" {
			return L("файл на ", "file on ") + dev
		}
		return L("файл подкачки", "swap file")
	}
	return sw.Type
}

// ramFrequency tries to read the RAM/DDR clock. Some SoCs (Rockchip, some
// Allwinner boards) expose a devfreq device named "dmc" or "ddr"; most
// Raspberry Pi models do not, in which case all values stay zero.
func ramFrequency() (cur, min, max float64) {
	candidates := []string{
		"/sys/class/devfreq/dmc",
		"/sys/class/devfreq/ddr",
		"/sys/class/devfreq/*.dmc",
	}
	for _, c := range candidates {
		dirs, _ := filepath.Glob(c)
		for _, d := range dirs {
			if v := hzToMHz(readFileTrim(filepath.Join(d, "cur_freq"))); v > 0 {
				cur = v
			}
			if v := hzToMHz(readFileTrim(filepath.Join(d, "min_freq"))); v > 0 {
				min = v
			}
			if v := hzToMHz(readFileTrim(filepath.Join(d, "max_freq"))); v > 0 {
				max = v
			}
			if cur > 0 {
				return
			}
		}
	}
	return
}

// hzToMHz converts a raw hertz value (as reported by devfreq) to MHz.
func hzToMHz(s string) float64 {
	if s == "" {
		return 0
	}
	return parseFloat(s) / 1e6
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
