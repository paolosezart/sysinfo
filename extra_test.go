package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	// SD Status register: the speed class is bits 440..447, i.e. the top byte of
	// raw_ssr[2] (hex characters 16..18). 4 = class 10.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	ssr := strings.Repeat("0", 128)
	ssr = ssr[:16] + "04" + ssr[18:]
	writeFixture(t, filepath.Join(dir, "device"), "ssr", ssr)
	if got := sdSpeedClass(dir); got == "" {
		t.Error("expected a speed class from an ssr with class 10")
	}
}

func TestKeyboardLayouts(t *testing.T) {
	// RPiOS stores "us,ru" with the variant list ",phonetic": the first layout has
	// no variant, so the rendered string must not contain an empty "(...)".
	got := keyboardLayouts(LocaleInfo{Layouts: "us,ru", Variant: ",phonetic"})
	if got != "us, ru (phonetic)" {
		t.Errorf("keyboardLayouts = %q, want %q", got, "us, ru (phonetic)")
	}
	if got := keyboardLayouts(LocaleInfo{Layouts: "us"}); got != "us" {
		t.Errorf("keyboardLayouts = %q, want us", got)
	}
}

func TestARMImplementerName(t *testing.T) {
	if got := armImplementerName("0x41"); got != "ARM" {
		t.Errorf("0x41 = %q, want ARM", got)
	}
	// Unknown codes must be preserved rather than dropped.
	if got := armImplementerName("0xff"); got != "0xff" {
		t.Errorf("0xff = %q, want the raw code back", got)
	}
}

func TestMMCBusSpeed(t *testing.T) {
	// The debugfs "timing spec" line is the only source of the active bus mode.
	if got := normalizeTiming("sd uhs SDR104"); got != "sdr104" {
		t.Errorf("normalizeTiming = %q, want sdr104", got)
	}
	if got := sdTimingName("sdr104"); got == "" {
		t.Error("sdr104 should map to a name")
	}
}

// ---- embedded databases ----

func TestEmbeddedUSBIDsParsed(t *testing.T) {
	vendors, products := map[string]string{}, map[string]map[string]string{}
	old := usbVendors
	oldP := usbProducts
	usbVendors, usbProducts = vendors, products
	parseUSBIDs(embeddedUSBIDs())
	usbVendors, usbProducts = old, oldP

	if vendors["046d"] == "" {
		t.Fatalf("embedded usb.ids missing vendor 046d, got %d vendors", len(vendors))
	}
	if products["046d"] == nil || products["046d"]["c534"] == "" {
		t.Error("embedded usb.ids missing product 046d:c534")
	}
}

func TestEmbeddedOUIParsed(t *testing.T) {
	table := map[string]string{}
	parseOUILines(embeddedOUI(), table)
	if len(table) < 10000 {
		t.Fatalf("embedded OUI looks truncated: %d entries", len(table))
	}
	if table["286FB9"] == "" {
		t.Error("embedded OUI missing 28:6F:B9")
	}
}

// TestOfflineFallbackUsesEmbedded verifies that with no cache and no network
// the lookups still resolve from the copy baked into the binary.
func TestOfflineFallbackUsesEmbedded(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Point the system paths at nonexistent files so only the embedded copy remains.
	oldSys := ouiSystemPaths
	ouiSystemPaths = []string{filepath.Join(t.TempDir(), "nope")}
	defer func() { ouiSystemPaths = oldSys }()

	table := map[string]string{}
	if path, ok := ouiLocalPath(); ok {
		t.Fatalf("expected no local OUI, found %s", path)
	}
	parseOUILines(embeddedOUI(), table)
	if table["286FB9"] == "" {
		t.Error("offline fallback failed to resolve a MAC vendor")
	}
}

// ---- conditional download ----

func TestRefreshDBSkipsWhenNotModified(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	body := "OUI/MA-L  Organization\n00-00-0C   (hex)   Cisco Systems, Inc\n"
	etag := `"v1"`
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		io.WriteString(w, body)
	}))
	defer srv.Close()

	dst := cachePath("oui.txt")
	if err := refreshDB(srv.URL, "oui.txt", looksLikeOUIDB); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if !looksLikeOUIDB(dst) {
		t.Fatal("downloaded file not recognized as an OUI database")
	}
	if got, _ := readValidators(dst); got != etag {
		t.Fatalf("etag not stored, got %q", got)
	}

	// Second call must be a conditional request answered with 304.
	if err := refreshDB(srv.URL, "oui.txt", looksLikeOUIDB); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if hits != 2 {
		t.Fatalf("expected 2 server hits, got %d", hits)
	}
	data, _ := os.ReadFile(dst)
	if string(data) != body {
		t.Error("cached content was clobbered by a 304")
	}
}

func TestRefreshDBRejectsHTML(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<!DOCTYPE html><html><body>captive portal</body></html>")
	}))
	defer srv.Close()

	err := refreshDB(srv.URL, "oui.txt", looksLikeOUIDB)
	if err == nil {
		t.Fatal("expected an error for an HTML response")
	}
	if fileExists(cachePath("oui.txt")) {
		t.Error("invalid download must not be left in the cache")
	}
}

func TestParseOUILineFormats(t *testing.T) {
	table := map[string]string{}
	parseOUILine("00-00-0C   (hex)              CISCO SYSTEMS, INC.", table)
	parseOUILine("286FB9\tNokia Shanghai Bell Co., Ltd.", table)
	parseOUILine("38:E2:CA\tKatun\tKatun Corporation", table)
	parseOUILine("# comment", table)
	if table["00000C"] == "" || table["286FB9"] == "" || table["38E2CA"] == "" {
		t.Errorf("failed to parse one of the OUI formats: %v", table)
	}
	if len(table) != 3 {
		t.Errorf("expected 3 entries, got %d", len(table))
	}
}
