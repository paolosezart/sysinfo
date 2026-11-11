package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseThrottled(t *testing.T) {
	cases := []struct {
		in                  string
		under, freq, thr, s bool
		occurredAny         bool
	}{
		{"0x0", false, false, false, false, false},
		{"throttled=0x0", false, false, false, false, false},
		{"0x50000", false, false, false, false, true}, // freq capped + throttled occurred
		{"0x50005", true, false, true, false, true},   // + undervoltage & throttled now
		{"0x80008", false, false, false, true, true},  // soft temp limit now + occurred
	}
	for _, c := range cases {
		got := parseThrottled(c.in)
		if got == nil {
			t.Fatalf("%q: nil result", c.in)
		}
		if got.UnderVoltageNow != c.under || got.FreqCappedNow != c.freq ||
			got.ThrottledNow != c.thr || got.SoftTempLimitNow != c.s {
			t.Errorf("%q: now-bits = %+v, want under=%v freq=%v thr=%v soft=%v",
				c.in, got, c.under, c.freq, c.thr, c.s)
		}
		if got.healthy() == c.occurredAny {
			t.Errorf("%q: healthy()=%v, want %v", c.in, got.healthy(), !c.occurredAny)
		}
	}
	if parseThrottled("0xZZ") != nil {
		t.Error("expected nil for invalid hex")
	}
}

func TestParseZramDevice(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "disksize", "1073741824\n")
	writeFixture(t, dir, "comp_algorithm", "lzo lz4 [zstd] lz4hc\n")
	// orig compr mem_used_total mem_limit mem_used_max same_pages pages_compacted
	writeFixture(t, dir, "mm_stat", "268435456 67108864 75497472 1073741824 75497472 1024 0 0\n")

	z := parseZramDevice(dir)
	if z.Algorithm != "zstd" {
		t.Errorf("algorithm = %q, want zstd", z.Algorithm)
	}
	if z.OriginalBytes != 268435456 {
		t.Errorf("orig = %d", z.OriginalBytes)
	}
	if z.CompressedBytes != 67108864 {
		t.Errorf("compr = %d", z.CompressedBytes)
	}
	if z.MemUsedTotalBytes != 75497472 {
		t.Errorf("mem_used_total = %d", z.MemUsedTotalBytes)
	}
	if z.CompressionRatio != 4.0 {
		t.Errorf("ratio = %v, want 4.0", z.CompressionRatio)
	}
	if z.UsedPercent < 24.9 || z.UsedPercent > 25.1 {
		t.Errorf("used%% = %v, want ~25", z.UsedPercent)
	}
}

func TestExtractBracket(t *testing.T) {
	if got := extractBracket("lzo lz4 [zstd]"); got != "zstd" {
		t.Errorf("got %q", got)
	}
	if got := extractBracket("zstd"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFreqToBandChannel(t *testing.T) {
	cases := []struct {
		freq     int
		band, ch string
	}{
		{2412, "2.4", "1"},
		{2437, "2.4", "6"},
		{2484, "2.4", "14"},
		{5180, "5", "36"},
		{5745, "5", "149"},
		{5955, "6", "1"},
		{0, "", ""},
	}
	for _, c := range cases {
		b, ch := freqToBandChannel(c.freq)
		if b != c.band || ch != c.ch {
			t.Errorf("freq %d: got (%q,%q), want (%q,%q)", c.freq, b, ch, c.band, c.ch)
		}
	}
}

func TestSignalPercent(t *testing.T) {
	cases := map[int]int{-30: 100, -50: 100, -75: 50, -100: 0, -120: 0}
	for dbm, want := range cases {
		if got := signalPercent(dbm); got != want {
			t.Errorf("signalPercent(%d) = %d, want %d", dbm, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{
		512:        "512 Б",
		1024:       "1.00 КиБ",
		1048576:    "1.00 МиБ",
		1073741824: "1.00 ГиБ",
		1610612736: "1.50 ГиБ",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestUnescapeMount(t *testing.T) {
	if got := unescapeMount("/mnt/my\\040drive"); got != "/mnt/my drive" {
		t.Errorf("got %q", got)
	}
	if got := unescapeMount("/plain"); got != "/plain" {
		t.Errorf("got %q", got)
	}
}

// buildEDID assembles a minimal but valid EDID block with a product name and
// a 1920x1080@60 preferred timing, matching a real monitor dump.
func buildEDID() []byte {
	b := make([]byte, 128)
	b[0], b[1], b[2], b[3] = 0x00, 0xFF, 0xFF, 0xFF
	// Manufacturer "DEL" -> 0x10AC: D=4, E=5, L=12 -> 00100 00101 01100
	// packed = (4<<10)|(5<<5)|12 = 4096+160+12 = 4268 = 0x10AC
	b[8], b[9] = 0x10, 0xAC
	b[16] = 10 // week
	b[17] = 30 // year = 2020
	b[18], b[19] = 1, 4
	b[21], b[22] = 34, 19 // 34x19 cm (a 15.8" panel)

	// Detailed timing descriptor #1: 1920x1080@60, pixel clock 148500 kHz.
	// pixel clock (LSB then MSB, in 10 kHz units): 14850 = 0x3A02
	dt := b[54:72]
	dt[0], dt[1] = 0x02, 0x3A
	dt[2] = 0x80 // hActive LSB = 1920 & 0xFF = 0x80
	dt[3] = 0x18 // hBlank LSB = 280 & 0xFF = 0x18
	dt[4] = 0x71 // hActive high nibble (1920>>8=7) <<4 | hBlank high nibble (280>>8=1)
	dt[5] = 0x38 // vActive LSB = 1080 & 0xFF = 0x38
	dt[6] = 0x2D // vBlank LSB = 45 & 0xFF = 0x2D
	dt[7] = 0x40 // vActive high (1080>>8=4) <<4 | vBlank high (45>>8=0)

	// Descriptor block with tag 0xFC = product name.
	name := b[54+18 : 54+36]
	name[3] = 0xFC
	copy(name[5:], "DELL U2419H")

	// Serial descriptor, tag 0xFF.
	ser := b[54+36 : 54+54]
	ser[3] = 0xFF
	copy(ser[5:], "ABC123")
	return b
}

func TestParseEDID(t *testing.T) {
	m := parseEDID(buildEDID())
	if m == nil {
		t.Fatal("parseEDID returned nil")
	}
	if m.Manufacturer != "DEL" {
		t.Errorf("manufacturer = %q, want DEL", m.Manufacturer)
	}
	if m.ProductName != "DELL U2419H" {
		t.Errorf("product = %q", m.ProductName)
	}
	if m.Serial != "ABC123" {
		t.Errorf("serial = %q", m.Serial)
	}
	if m.Year != 2020 {
		t.Errorf("year = %d, want 2020", m.Year)
	}
	if m.PreferredWidth != 1920 || m.PreferredHeight != 1080 {
		t.Errorf("resolution = %dx%d, want 1920x1080", m.PreferredWidth, m.PreferredHeight)
	}
	if m.RefreshHz < 59 || m.RefreshHz > 61 {
		t.Errorf("refresh = %v, want ~60", m.RefreshHz)
	}
	if m.SizeCmW < 33 || m.SizeCmW > 35 || m.SizeCmH < 18 || m.SizeCmH > 20 {
		t.Errorf("size = %vx%v, want 34x19", m.SizeCmW, m.SizeCmH)
	}
}

func TestParseEDIDRejectsGarbage(t *testing.T) {
	if parseEDID([]byte{1, 2, 3}) != nil {
		t.Error("expected nil for short input")
	}
	if parseEDID(make([]byte, 128)) != nil {
		t.Error("expected nil for missing magic header")
	}
}

func TestIsSystemMount(t *testing.T) {
	keep := []string{"/", "/home", "/boot", "/mnt/usb", "/media/pi/disk", "/data"}
	skip := []string{"/etc/hosts", "/var/lib/docker", "/usr/src/linux", "/sys/kernel", "/run/lock", "/snap/core"}
	for _, m := range keep {
		if isSystemMount(m) {
			t.Errorf("%q should be kept", m)
		}
	}
	for _, m := range skip {
		if !isSystemMount(m) {
			t.Errorf("%q should be skipped", m)
		}
	}
}

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
