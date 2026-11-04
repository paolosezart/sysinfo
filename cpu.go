package main

import (
	"path/filepath"
	"runtime"
	"strings"
)

// CPUInfo describes the processor and its frequency range.
type CPUInfo struct {
	Model        string
	Vendor       string
	Architecture string
	Cores        int
	Threads      int
	BogoMIPS     string
	CurFreqMHz   float64
	MinFreqMHz   float64
	MaxFreqMHz   float64
	Governor     string
	Features     []string
}

func collectCPUInfo() CPUInfo {
	ci := CPUInfo{Architecture: readFileTrim("/proc/sys/kernel/arch")}

	model, vendor, cores, threads, bogo, features := parseCPUInfo("/proc/cpuinfo")
	ci.Model = model
	ci.Vendor = vendor
	ci.Cores = cores
	ci.Threads = threads
	ci.BogoMIPS = bogo
	ci.Features = features
	if ci.Architecture == "" {
		ci.Architecture = runtime.GOARCH
	}

	// Frequency: prefer the live per-CPU cpufreq sysfs values. On a Pi the
	// policy files may live under cpufreq/policy0.
	base := "/sys/devices/system/cpu/cpu0/cpufreq"
	if _, err := filepath.Glob(base + "/*"); err != nil {
		base = "/sys/devices/system/cpu/cpufreq/policy0"
	}
	ci.CurFreqMHz = khzToMHz(readFileTrim(filepath.Join(base, "scaling_cur_freq")))
	if ci.CurFreqMHz == 0 {
		ci.CurFreqMHz = khzToMHz(readFileTrim(filepath.Join(base, "cpuinfo_cur_freq")))
	}
	// Fallback: /proc/cpuinfo reports "cpu MHz" on most x86 and some ARM.
	if ci.CurFreqMHz == 0 {
		ci.CurFreqMHz = parseFloat(cpuInfoField("/proc/cpuinfo", "cpu MHz"))
	}
	ci.MinFreqMHz = khzToMHz(readFileTrim(filepath.Join(base, "cpuinfo_min_freq")))
	ci.MaxFreqMHz = khzToMHz(readFileTrim(filepath.Join(base, "cpuinfo_max_freq")))
	ci.Governor = readFileTrim(filepath.Join(base, "scaling_governor"))

	// Raspberry Pi 5 exposes the real-time clock via vcgencmd; use it when the
	// cpufreq interface is unavailable.
	if ci.CurFreqMHz == 0 && commandExists("vcgencmd") {
		ci.CurFreqMHz = vcgencmdClockMHz("arm")
	}
	return ci
}

func khzToMHz(s string) float64 {
	if s == "" {
		return 0
	}
	return parseFloat(s) / 1000.0
}

// parseCPUInfo extracts the fields we display from /proc/cpuinfo, handling
// both x86 (model name) and ARM (model / Processor / Hardware) layouts.
func parseCPUInfo(path string) (model, vendor string, cores, threads int, bogo string, features []string) {
	b, err := readFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	seenThreads := map[string]bool{}
	seenSockets := map[string]bool{}
	sockets := 0
	perSocketCores := 0
	for _, line := range lines {
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		switch key {
		case "processor":
			seenThreads[val] = true
		case "model name", "Processor":
			if model == "" {
				model = val
			}
		case "Model":
			// Raspberry Pi 4/5 style "Model : Raspberry Pi 4 Model B Rev 1.4"
			if model == "" {
				model = val
			}
		case "Hardware":
			if model == "" {
				model = val
			}
		case "vendor_id":
			vendor = val
		case "CPU implementer":
			if vendor == "" {
				vendor = val
			}
		case "BogoMIPS":
			if bogo == "" {
				bogo = val
			}
		case "Features", "flags":
			if len(features) == 0 {
				features = strings.Fields(val)
			}
		case "cpu cores":
			if perSocketCores == 0 {
				perSocketCores = int(parseUint(val))
			}
		case "physical id":
			// Count distinct physical sockets.
			if !seenSockets[val] {
				seenSockets[val] = true
				sockets++
			}
		}
	}
	threads = len(seenThreads)
	cores = perSocketCores
	if sockets > 1 {
		cores = sockets * perSocketCores
	}
	if cores == 0 {
		cores = threads
	}
	if threads == 0 {
		threads = cores
	}
	return
}

// cpuInfoField returns the first value for key in a "key : value" file.
func cpuInfoField(path, key string) string {
	b, err := readFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		if strings.TrimSpace(line[:idx]) == key {
			return strings.TrimSpace(line[idx+1:])
		}
	}
	return ""
}

func vcgencmdClockMHz(clock string) float64 {
	out, err := runCmd(cmdTimeout, "vcgencmd", "measure_clock", clock)
	if err != nil {
		return 0
	}
	// Format: frequency(48)=1500000000
	idx := strings.Index(out, "=")
	if idx < 0 {
		return 0
	}
	hz := parseFloat(out[idx+1:])
	return hz / 1e6
}
