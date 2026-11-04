package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Data files are cached under $XDG_CACHE_HOME/sysinfo (or ~/.cache/sysinfo).
func cacheDir() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return filepath.Join(d, "sysinfo")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/sysinfo"
	}
	return filepath.Join(home, ".cache", "sysinfo")
}

func cachePath(name string) string { return filepath.Join(cacheDir(), name) }

// System-wide databases that may already be installed by the distro. We read
// these before downloading anything.
var ouiSystemPaths = []string{
	"/usr/share/ieee-data/oui.txt",
	"/usr/share/misc/oui.txt",
	"/usr/share/hwdata/oui.txt",
	"/var/lib/ieee-data/oui.txt",
	"/usr/share/arp-scan/ieee-oui.txt",
}

// usb.ids sources (the classic community database).
const usbIDsURL = "https://raw.githubusercontent.com/gentoo/hwids/master/usb.ids"

// IEEE OUI sources. The canonical text list is published by the IEEE; the
// ieee-data Debian package mirrors it.
var ouiURLs = []string{
	"https://standards-oui.ieee.org/oui/oui.txt",
	"https://linuxnet.ca/ieee/oui.txt",
	"https://gitlab.com/wireshark/wireshark/-/raw/master/manuf",
}

// ---- vendor name caches (loaded lazily) ----

var (
	ouiOnce     sync.Once
	ouiTable    map[string]string
	usbIDsOnce  sync.Once
	usbVendors  map[string]string
	usbProducts map[string]map[string]string
)

// lookupMACVendor resolves the first three bytes of a MAC to a vendor name.
// Returns "" when no database is available or the prefix is unknown.
func lookupMACVendor(mac string) string {
	if mac == "" {
		return ""
	}
	ouiOnce.Do(loadOUI)
	prefix := strings.ToUpper(strings.ReplaceAll(mac, ":", ""))
	if len(prefix) < 6 {
		return ""
	}
	return ouiTable[prefix[:6]]
}

func loadOUI() {
	ouiTable = map[string]string{}
	path := cachePath("oui.txt")
	if _, err := os.Stat(path); err != nil {
		// Try the distro-provided copy.
		for _, p := range ouiSystemPaths {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// Standard oui.txt: "00-00-0C   (hex)        CISCO SYSTEMS, INC."
		if strings.Contains(line, "(hex)") {
			parts := strings.SplitN(line, "(hex)", 2)
			key := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(parts[0]), "-", ""))
			name := strings.TrimSpace(parts[1])
			if len(key) >= 6 && name != "" {
				ouiTable[key[:6]] = name
			}
			continue
		}
		// Wireshark manuf: "00:00:0C\tCisco\tCisco Systems, Inc"
		if strings.Count(line, ":") >= 2 && !strings.HasPrefix(line, "#") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				key := strings.ToUpper(strings.ReplaceAll(fields[0], ":", ""))
				if len(key) >= 6 {
					ouiTable[key[:6]] = fields[len(fields)-1]
				}
			}
		}
	}
}

// usbIDsLookup resolves a VID:PID pair to vendor and product names.
func usbIDsLookup(vid, pid string) (vendor, product string) {
	usbIDsOnce.Do(loadUSBIDs)
	if usbVendors == nil {
		return "", ""
	}
	vendor = usbVendors[strings.ToLower(vid)]
	if pm := usbProducts[strings.ToLower(vid)]; pm != nil {
		product = pm[strings.ToLower(pid)]
	}
	return
}

func loadUSBIDs() {
	usbVendors = map[string]string{}
	usbProducts = map[string]map[string]string{}
	f, err := os.Open(cachePath("usb.ids"))
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	currentVendor := ""
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// A line starting at column 0 is a vendor: "1234  Vendor Name".
		if line[0] != '\t' && line[0] != ' ' {
			fields := strings.SplitN(line, " ", 2)
			if len(fields) == 2 && len(fields[0]) == 4 {
				currentVendor = strings.ToLower(fields[0])
				usbVendors[currentVendor] = strings.TrimSpace(fields[1])
				usbProducts[currentVendor] = map[string]string{}
			}
			continue
		}
		// Indented lines are products: "\t5678  Product Name".
		if currentVendor != "" {
			fields := strings.SplitN(strings.TrimSpace(line), " ", 2)
			if len(fields) == 2 && len(fields[0]) == 4 {
				usbProducts[currentVendor][strings.ToLower(fields[0])] = strings.TrimSpace(fields[1])
			}
		}
	}
}

// ---- download commands ----

func updateVendorDB() error {
	fmt.Println(L("Загрузка базы USB-вендоров...", "Downloading the USB vendor database..."))
	if err := downloadTo(usbIDsURL, cachePath("usb.ids")); err != nil {
		return err
	}
	fmt.Println(L("Готово: ", "Done: ") + cachePath("usb.ids"))
	return nil
}

func updateOUIDB() error {
	fmt.Println(L("Загрузка базы MAC-вендоров (IEEE OUI)...", "Downloading the IEEE OUI MAC vendor database..."))
	var lastErr error
	for _, u := range ouiURLs {
		if err := downloadTo(u, cachePath("oui.txt")); err != nil {
			lastErr = err
			continue
		}
		if !looksLikeOUIDB(cachePath("oui.txt")) {
			lastErr = fmt.Errorf("%s did not return an OUI database", u)
			os.Remove(cachePath("oui.txt"))
			continue
		}
		fmt.Println(L("Готово: ", "Done: ") + cachePath("oui.txt"))
		return nil
	}
	return lastErr
}

// looksLikeOUIDB guards against servers that answer with an HTML page instead
// of the database (which happens behind captive portals and some mirrors).
func looksLikeOUIDB(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	head := strings.ToLower(string(buf[:n]))
	if strings.Contains(head, "<!doctype html") || strings.Contains(head, "<html") {
		return false
	}
	return strings.Contains(head, "(hex)") || strings.Contains(head, "organization")
}

// downloadTo fetches a URL into dst atomically (temp file + rename).
func downloadTo(url, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "sysinfo/1.1 (+https://github.com/paolosezart/sysinfo)")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// ouiDatabaseAvailable reports whether a MAC vendor database can be read.
func ouiDatabaseAvailable() bool {
	if _, err := os.Stat(cachePath("oui.txt")); err == nil {
		return true
	}
	for _, p := range ouiSystemPaths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
