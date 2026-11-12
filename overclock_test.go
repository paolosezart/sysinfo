package main

import "testing"

func TestOverVoltageVolts(t *testing.T) {
	cases := []struct {
		v     int
		model string
		want  float64
	}{
		{0, "Raspberry Pi 4 Model B", 1.35},
		{2, "Raspberry Pi 4 Model B", 1.40},
		{8, "Raspberry Pi 4 Model B", 1.55},
		{-16, "Raspberry Pi 4 Model B", 0.95},
		{0, "Raspberry Pi 1 Model B", 1.20},
		{8, "Raspberry Pi 1 Model B", 1.40},
	}
	for _, c := range cases {
		if got := overVoltageVolts(c.v, c.model); got < c.want-0.001 || got > c.want+0.001 {
			t.Errorf("overVoltageVolts(%d, %q) = %v, want %v", c.v, c.model, got, c.want)
		}
	}
}

func TestAtoiDefault(t *testing.T) {
	if got := atoiDefault("42", 0); got != 42 {
		t.Errorf("atoiDefault(42) = %d, want 42", got)
	}
	if got := atoiDefault("-3", 0); got != -3 {
		t.Errorf("atoiDefault(-3) = %d, want -3", got)
	}
	if got := atoiDefault("", 7); got != 7 {
		t.Errorf("atoiDefault(empty) = %d, want 7", got)
	}
	if got := atoiDefault("abc", 5); got != 5 {
		t.Errorf("atoiDefault(abc) = %d, want 5", got)
	}
}

func TestCollectOverclockFromConfig(t *testing.T) {
	ct := &configTxt{
		Path: "/boot/firmware/config.txt",
		Settings: map[string]string{
			"arm_freq":     "1800",
			"gpu_freq":     "600",
			"core_freq":    "600",
			"sdram_freq":   "3200",
			"over_voltage": "2",
			"force_turbo":  "1",
			"arm_boost":    "1",
			"temp_limit":   "80",
		},
		Overlays: map[string]bool{},
	}
	oc := collectOverclock(ct, "Raspberry Pi 4 Model B")
	if oc == nil {
		t.Fatal("collectOverclock returned nil for a valid config")
	}
	if oc.ARMfreqMHz != 1800 || oc.CorefreqMHz != 600 || oc.SDRAMfreqMHz != 3200 {
		t.Errorf("frequencies parsed incorrectly: %+v", oc)
	}
	if !oc.OverVoltageSet || oc.OverVoltage != 2 {
		t.Errorf("over_voltage = %d (set=%v), want 2/true", oc.OverVoltage, oc.OverVoltageSet)
	}
	if !oc.ForceTurbo || !oc.ArmBoost {
		t.Errorf("force_turbo/arm_boost not detected: %+v", oc)
	}
	if oc.TempLimitC != 80 {
		t.Errorf("temp_limit = %d, want 80", oc.TempLimitC)
	}
}

func TestCollectOverclockNilConfig(t *testing.T) {
	if oc := collectOverclock(nil, "Raspberry Pi 4 Model B"); oc != nil {
		t.Errorf("collectOverclock(nil) = %+v, want nil", oc)
	}
}

func TestOverclockVoltageLine(t *testing.T) {
	oc := &OverclockInfo{OverVoltageSet: true, OverVoltage: 2}
	if got := overclockVoltageLine(oc, "Raspberry Pi 4"); got == "" {
		t.Error("overclockVoltageLine returned empty for a set over_voltage")
	}
	delta := &OverclockInfo{OverVoltageDelta: 50000}
	if got := overclockVoltageLine(delta, "Raspberry Pi 5"); got == "" {
		t.Error("overclockVoltageLine returned empty for over_voltage_delta")
	}
	if got := overclockVoltageLine(&OverclockInfo{}, "Raspberry Pi 4"); got != "" {
		t.Errorf("overclockVoltageLine with no voltage = %q, want empty", got)
	}
}
