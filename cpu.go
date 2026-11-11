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
	part := ""
	boardLevel := false
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
			// Raspberry Pi 4/5 style "Model : Raspberry Pi 4 Model B Rev 1.4".
			// This is the board name, not the core, so it is only a fallback.
			if model == "" {
				model, boardLevel = val, true
			}
		case "Hardware":
			if model == "" {
				model, boardLevel = val, true
			}
		case "CPU part":
			if part == "" {
				part = val
			}
		case "vendor_id":
			vendor = val
		case "CPU implementer":
			if vendor == "" {
				vendor = armImplementerName(val)
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
	// On ARM the "Model" line is the board name, which the SYSTEM block already
	// shows. Prefer the actual core when /proc/cpuinfo exposes "CPU part".
	if boardLevel {
		if core := armCoreName(part); core != "" {
			model = core
		}
	}
	return
}

// armCoreName maps an ARM "CPU part" number to the marketing core name. The
// values are ARM_CPU_PART_* from the kernel's asm/cputype.h.
func armCoreName(part string) string {
	switch strings.ToLower(strings.TrimSpace(part)) {
	case "0xb76":
		return "ARM1176"
	case "0xc05":
		return "Cortex-A5"
	case "0xc07":
		return "Cortex-A7"
	case "0xc08":
		return "Cortex-A8"
	case "0xc09":
		return "Cortex-A9"
	case "0xc0d":
		return "Cortex-A12"
	case "0xc0f":
		return "Cortex-A15"
	case "0xc0e":
		return "Cortex-A17"
	case "0xd01":
		return "Cortex-A32"
	case "0xd02":
		return "Cortex-A34"
	case "0xd03":
		return "Cortex-A53"
	case "0xd04":
		return "Cortex-A35"
	case "0xd05":
		return "Cortex-A55"
	case "0xd06":
		return "Cortex-A65"
	case "0xd07":
		return "Cortex-A57"
	case "0xd08":
		return "Cortex-A72"
	case "0xd09":
		return "Cortex-A73"
	case "0xd0a":
		return "Cortex-A75"
	case "0xd0b":
		return "Cortex-A76"
	case "0xd0c":
		return "Neoverse-N1"
	case "0xd0d":
		return "Cortex-A77"
	case "0xd41":
		return "Cortex-A78"
	case "0xd44":
		return "Cortex-X1"
	case "0xd46":
		return "Cortex-A510"
	case "0xd47":
		return "Cortex-A710"
	case "0xd48":
		return "Cortex-X2"
	case "0xd49":
		return "Neoverse-N2"
	case "0xd4d":
		return "Cortex-A715"
	case "0xd4e":
		return "Cortex-X3"
	case "0xd80":
		return "Cortex-A520"
	case "0xd81":
		return "Cortex-A720"
	case "0xd82":
		return "Cortex-X4"
	}
	return ""
}

// armImplementerName maps the "CPU implementer" byte from /proc/cpuinfo to the
// vendor that designed the core. Pi boards report 0x41 (ARM) etc.
func armImplementerName(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "0x41":
		return "ARM"
	case "0x42":
		return "Broadcom"
	case "0x43":
		return "Cavium"
	case "0x44":
		return "DEC"
	case "0x46":
		return "Fujitsu"
	case "0x48":
		return "HiSilicon"
	case "0x49":
		return "Infineon"
	case "0x4d":
		return "Motorola/Freescale"
	case "0x4e":
		return "NVIDIA"
	case "0x50":
		return "Ampere"
	case "0x51":
		return "Qualcomm"
	case "0x53":
		return "Samsung"
	case "0x56":
		return "Marvell"
	case "0x61":
		return "Apple"
	case "0x69":
		return "Intel"
	case "0x70":
		return "Phytium"
	case "0xc0":
		return "Ampere"
	}
	// Unknown implementer: keep the raw code so nothing is silently dropped.
	return code
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
