package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.txt")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConfigParseBasic(t *testing.T) {
	p := writeConfig(t, `
# a comment
arm_freq=1800
gpu_freq=600   # trailing comment
dtoverlay=vc4-kms-v3d
dtoverlay=i2c-rtc,ds3231
over_voltage=2
`)
	ct := &configTxt{Settings: map[string]string{}, Overlays: map[string]bool{}}
	ct.load(p, nil, 0)

	if got := ct.setting("arm_freq"); got != "1800" {
		t.Errorf("arm_freq = %q, want 1800", got)
	}
	if got := ct.setting("gpu_freq"); got != "600" {
		t.Errorf("gpu_freq = %q, want 600 (trailing comment not stripped)", got)
	}
	if !ct.hasOverlay("vc4-kms-v3d") {
		t.Error("vc4-kms-v3d overlay not recorded")
	}
	if !ct.hasOverlay("i2c-rtc") {
		t.Error("i2c-rtc overlay not recorded (parameters should be ignored)")
	}
}

// A conditional filter for a different model must not leak its settings, while
// one matching this board must apply.
func TestConfigConditionalFilters(t *testing.T) {
	p := writeConfig(t, `
[pi3]
arm_freq=1200
[pi4]
arm_freq=1800
core_freq=500
[all]
over_voltage=1
`)
	tags := map[string]bool{"pi4": true}
	ct := &configTxt{Settings: map[string]string{}, Overlays: map[string]bool{}}
	ct.load(p, tags, 0)

	if got := ct.setting("arm_freq"); got != "1800" {
		t.Errorf("arm_freq = %q, want 1800 (the [pi4] value)", got)
	}
	if got := ct.setting("core_freq"); got != "500" {
		t.Errorf("core_freq = %q, want 500", got)
	}
	if got := ct.setting("over_voltage"); got != "1" {
		t.Errorf("over_voltage = %q, want 1 ([all] section)", got)
	}
}

// Unknown filters (EDID, gpio, board-type) cannot be evaluated, so their
// settings are kept rather than dropped.
func TestConfigUnknownFilterKept(t *testing.T) {
	p := writeConfig(t, `
[EDID=SomeMonitor]
hdmi_group=2
`)
	ct := &configTxt{Settings: map[string]string{}, Overlays: map[string]bool{}}
	ct.load(p, nil, 0)
	if got := ct.setting("hdmi_group"); got != "2" {
		t.Errorf("hdmi_group = %q, want 2 (unknown filter should not drop it)", got)
	}
}

func TestConfigInclude(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("arm_freq=2000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "config.txt")
	if err := os.WriteFile(main, []byte("include extra.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ct := &configTxt{Settings: map[string]string{}, Overlays: map[string]bool{}}
	ct.load(main, nil, 0)
	if got := ct.setting("arm_freq"); got != "2000" {
		t.Errorf("included arm_freq = %q, want 2000", got)
	}
}

func TestBoardConfigTags(t *testing.T) {
	cases := []struct {
		model string
		want  []string
	}{
		{"Raspberry Pi 4 Model B Rev 1.4", []string{"pi4"}},
		{"Raspberry Pi 400", []string{"pi4", "pi400"}},
		{"Raspberry Pi 5 Model B Rev 1.0", []string{"pi5"}},
		{"Raspberry Pi 3 Model B Plus Rev 1.3", []string{"pi3", "pi3+"}},
		{"Raspberry Pi Zero 2 W Rev 1.0", []string{"pi0", "pi0w", "pi02"}},
		{"Compute Module 4 Rev 1.0", []string{"pi4", "cm4"}},
	}
	for _, c := range cases {
		tags := boardConfigTags(c.model)
		for _, w := range c.want {
			if !tags[w] {
				t.Errorf("boardConfigTags(%q) missing %q (got %v)", c.model, w, tags)
			}
		}
	}
}

func TestFindConfigTxtNone(t *testing.T) {
	// The test environment has neither path, so this documents the miss case.
	if p, ok := findConfigTxt(); ok {
		t.Skipf("config.txt unexpectedly present at %s", p)
	}
	if ct := collectConfigTxt("Raspberry Pi 4 Model B"); ct != nil {
		t.Error("collectConfigTxt should return nil when no config.txt exists")
	}
}
