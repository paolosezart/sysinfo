package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSwapDevices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "swaps")
	content := `Filename				Type		Size		Used		Priority
/swapfile                               file		8392700		0		10
/dev/zram0                              partition	2097148		0		100
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Exercise the parser directly via a path-injected variant.
	devs := parseSwapFile(path)
	if len(devs) != 2 {
		t.Fatalf("got %d swap devices, want 2", len(devs))
	}
	if devs[0].Filename != "/swapfile" || devs[0].Type != "file" || devs[0].Priority != 10 {
		t.Errorf("first device = %+v", devs[0])
	}
	if devs[0].SizeKB != 8392700 {
		t.Errorf("size = %d, want 8392700", devs[0].SizeKB)
	}
	if devs[1].Filename != "/dev/zram0" || devs[1].Priority != 100 {
		t.Errorf("second device = %+v", devs[1])
	}
}

func TestHexLEToIPv4(t *testing.T) {
	// 0x0164A8C0 little-endian is 192.168.100.1
	if got := hexLEToIPv4("0164A8C0"); got != "192.168.100.1" {
		t.Errorf("got %q, want 192.168.100.1", got)
	}
	if got := hexLEToIPv4("00000000"); got != "0.0.0.0" {
		t.Errorf("got %q, want 0.0.0.0", got)
	}
	if got := hexLEToIPv4("bad"); got != "" {
		t.Errorf("got %q, want empty for bad input", got)
	}
}

func TestArpLookup(t *testing.T) {
	// arpLookup reads /proc/net/arp; just ensure it never panics and returns a
	// MAC-shaped string or empty.
	got := arpLookup("192.0.2.123")
	if got != "" && len(got) != 17 {
		t.Errorf("unexpected MAC %q", got)
	}
}

func TestBlockBaseName(t *testing.T) {
	cases := map[string]string{
		"/dev/mmcblk0p2": "mmcblk0",
		"/dev/mmcblk0p1": "mmcblk0",
		"/dev/mmcblk0":   "mmcblk0",
		"/dev/sda1":      "sda",
		"/dev/sda":       "sda",
		"/dev/nvme0n1p3": "nvme0n1",
		"/dev/nvme0n1":   "nvme0n1",
	}
	for in, want := range cases {
		if got := blockBaseName(in); got != want {
			t.Errorf("blockBaseName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUsbKindFromClasses(t *testing.T) {
	// A combo dongle exposing both keyboard and mouse HID interfaces.
	combo := usbKindFromClasses([]int{3, 3}, map[int]int{3: 1})
	if combo == "" {
		t.Fatal("expected a kind for keyboard interface")
	}
	kb := usbKindFromClasses([]int{3}, map[int]int{3: 1})
	if kb == "" {
		t.Error("keyboard interface should map to a kind")
	}
	storage := usbKindFromClasses([]int{8}, nil)
	if storage == "" {
		t.Error("storage class should map to a kind")
	}
	if usbKindFromClasses(nil, nil) != "" {
		t.Error("no classes should map to empty")
	}
}

func TestFormatLinkSpeed(t *testing.T) {
	cases := map[float64]string{
		12:    formatLinkSpeed(12),
		480:   formatLinkSpeed(480),
		5000:  formatLinkSpeed(5000),
		10000: formatLinkSpeed(10000),
	}
	if cases[12] == "" || cases[480] == "" || cases[5000] == "" || cases[10000] == "" {
		t.Error("link speed strings must not be empty")
	}
}

func TestLangFromLocale(t *testing.T) {
	cases := map[string]string{
		"ru_RU.UTF-8": "ru",
		"ru":          "ru",
		"en_US.utf8":  "en",
		"de_DE":       "en",
		"C":           "en",
		"POSIX":       "en",
		"":            "",
	}
	for in, want := range cases {
		if got := langFromLocale(in); got != want {
			t.Errorf("langFromLocale(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLanguageName(t *testing.T) {
	langCode = "ru"
	if languageName("ru") != "русский" {
		t.Errorf("ru name = %q", languageName("ru"))
	}
	langCode = "en"
	if languageName("ru") != "Russian" {
		t.Errorf("en name = %q", languageName("ru"))
	}
	langCode = "ru"
}

func TestUnitLocal(t *testing.T) {
	langCode = "en"
	if unitLocal("MHz") != "MHz" || unitLocal("Mbps") != "Mbps" {
		t.Error("english units wrong")
	}
	langCode = "ru"
	if unitLocal("MHz") != "МГц" || unitLocal("Mbps") != "Мбит/с" {
		t.Error("russian units wrong")
	}
	langCode = "ru"
}

func TestSDTimingName(t *testing.T) {
	langCode = "en"
	if sdTimingName("sdr104") == "" || sdTimingName("hs") == "" {
		t.Error("timing names must not be empty")
	}
	if sdTimingName("unknownmode") != "unknownmode" {
		t.Error("unknown timing should pass through")
	}
	langCode = "ru"
}

func TestSDBase(t *testing.T) {
	// A CSD with speed class 10 in the last byte (0x3B).
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "device"), "csd", "0000000000000000000000000000003b")
	if got := sdSpeedClass(dir); got == "" {
		t.Error("expected a speed class from CSD ending in 3b")
	}
}
