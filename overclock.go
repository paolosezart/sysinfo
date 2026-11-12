package main

import (
	"fmt"
	"strings"
)

// OverclockInfo describes the CPU/GPU/SDRAM frequency and voltage settings from
// config.txt, plus the values the firmware reports as active at runtime.
type OverclockInfo struct {
	ConfigPath string
	// Settings present in config.txt (what was requested).
	ARMfreqMHz       int
	GPUfreqMHz       int
	CorefreqMHz      int
	SDRAMfreqMHz     int
	OverVoltage      int  // raw [-16,8] value
	OverVoltageSet   bool // whether over_voltage was specified
	OverVoltageDelta int  // microvolts, Pi 4/5
	ForceTurbo       bool
	ArmBoost         bool
	TempLimitC       int
	// Live values from the firmware (vcgencmd get_config), when available.
	LiveARMfreqMHz   int
	LiveCorefreqMHz  int
	LiveSDRAMfreqMHz int
	LiveOverVoltage  int
	HasLive          bool
}

// collectOverclock reads config.txt settings and, when vcgencmd is present, the
// firmware's active values. It returns nil when there is no config.txt, so the
// block is skipped on non-Raspberry-Pi hosts.
func collectOverclock(ct *configTxt, model string) *OverclockInfo {
	if ct == nil {
		return nil
	}
	oc := &OverclockInfo{ConfigPath: ct.Path}
	oc.ARMfreqMHz = int(parseUint(ct.setting("arm_freq")))
	oc.GPUfreqMHz = int(parseUint(ct.setting("gpu_freq")))
	oc.CorefreqMHz = int(parseUint(ct.setting("core_freq")))
	oc.SDRAMfreqMHz = int(parseUint(ct.setting("sdram_freq")))
	if v, ok := ct.Settings["over_voltage"]; ok {
		oc.OverVoltageSet = true
		oc.OverVoltage = atoiDefault(v, 0)
	}
	oc.OverVoltageDelta = atoiDefault(ct.setting("over_voltage_delta"), 0)
	oc.ForceTurbo = ct.setting("force_turbo") == "1"
	oc.ArmBoost = ct.setting("arm_boost") == "1"
	oc.TempLimitC = atoiDefault(ct.setting("temp_limit"), 0)

	if commandExists("vcgencmd") {
		if live := vcgencmdGetConfigInt(); len(live) > 0 {
			oc.HasLive = true
			oc.LiveARMfreqMHz = live["arm_freq"]
			oc.LiveCorefreqMHz = live["core_freq"]
			oc.LiveSDRAMfreqMHz = live["sdram_freq"]
			oc.LiveOverVoltage = live["over_voltage"]
		}
	}
	return oc
}

// overVoltageVolts converts a raw over_voltage value to the implied upper
// voltage limit in volts. The documented range [-16,8] maps to [0.95V,1.55V] on
// Raspberry Pi 2 and newer, so the neutral value 0 sits at 1.35V. Raspberry Pi 1
// uses [0.8V,1.4V], i.e. 1.2V at zero.
func overVoltageVolts(v int, model string) float64 {
	base := 1.35
	if strings.Contains(strings.ToLower(model), "raspberry pi 1") {
		base = 1.2
	}
	return base + float64(v)*0.025
}

// atoiDefault parses a signed integer, returning def when it is not a number.
func atoiDefault(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n, err := parseInt(s)
	if err != nil {
		return def
	}
	return n
}

// vcgencmdGetConfigInt returns the non-zero integer config values the firmware
// currently applies, parsed from `vcgencmd get_config int`.
func vcgencmdGetConfigInt() map[string]int {
	out, err := runCmd(cmdTimeout, "vcgencmd", "get_config", "int")
	if err != nil {
		return nil
	}
	m := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			m[strings.TrimSpace(k)] = atoiDefault(v, 0)
		}
	}
	return m
}

// overclockVoltageLine renders the voltage setting in a readable form.
func overclockVoltageLine(oc *OverclockInfo, model string) string {
	switch {
	case oc.OverVoltageDelta != 0:
		return fmt.Sprintf("%+d %s (%.3f %s)", oc.OverVoltageDelta, L("мкВ", "uV"),
			float64(oc.OverVoltageDelta)/1e6, L("В", "V"))
	case oc.OverVoltageSet:
		return fmt.Sprintf("%+d (≈%.2f %s)", oc.OverVoltage,
			overVoltageVolts(oc.OverVoltage, model), L("В", "V"))
	}
	return ""
}
