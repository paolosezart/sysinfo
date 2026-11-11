package main

import (
	"bufio"
	"bytes"
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

// usb.ids copies that distros ship (hwdata / usbutils).
var usbSystemPaths = []string{
	"/usr/share/misc/usb.ids",
	"/usr/share/hwdata/usb.ids",
	"/var/lib/usbutils/usb.ids",
	"/usr/share/usb.ids",
}

// fileExists reports whether path exists and is not a directory.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
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

// loadOUI reads the MAC vendor database, preferring the freshest local copy:
// the user cache (may have been updated by --oui), then a distro-installed
// copy, then the copy embedded in the binary. That last fallback means vendor
// names keep working with no network and an empty cache.
func loadOUI() {
	ouiTable = map[string]string{}
	if path, ok := ouiLocalPath(); ok {
		if parseOUIFile(path, ouiTable) {
			return
		}
	}
	parseOUILines(embeddedOUI(), ouiTable)
}

// ouiLocalPath returns the first existing on-disk OUI database.
func ouiLocalPath() (string, bool) {
	if p := cachePath("oui.txt"); fileExists(p) {
		return p, true
	}
	for _, p := range ouiSystemPaths {
		if fileExists(p) {
			return p, true
		}
	}
	return "", false
}

// parseOUIFile loads an OUI database from disk. It returns false when the file
// cannot be opened so the caller can fall back to the embedded copy.
func parseOUIFile(path string, dst map[string]string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		parseOUILine(sc.Text(), dst)
	}
	return true
}

// parseOUILines loads an OUI database from an in-memory string.
func parseOUILines(data string, dst map[string]string) {
	for _, line := range strings.Split(data, "\n") {
		parseOUILine(line, dst)
	}
}

// parseOUILine understands the three shapes we may encounter: the IEEE
// oui.txt ("00-00-0C   (hex)   Name"), our compact tab form ("00000C\tName")
// and the Wireshark manuf format ("00:00:0C\tShort\tName").
func parseOUILine(line string, dst map[string]string) {
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	// IEEE oui.txt
	if strings.Contains(line, "(hex)") {
		parts := strings.SplitN(line, "(hex)", 2)
		key := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(parts[0]), "-", ""))
		name := strings.TrimSpace(parts[1])
		if len(key) >= 6 && name != "" {
			dst[key[:6]] = name
		}
		return
	}
	// Compact form: "00000C\tName" (embedded) or Wireshark "00:00:0C\tA\tB".
	if i := strings.IndexByte(line, '\t'); i == 6 {
		key := strings.ToUpper(line[:6])
		name := strings.TrimSpace(line[i+1:])
		if name != "" {
			dst[key] = name
		}
		return
	}
	if strings.Count(line, ":") >= 2 {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			key := strings.ToUpper(strings.ReplaceAll(fields[0], ":", ""))
			if len(key) >= 6 {
				dst[key[:6]] = fields[len(fields)-1]
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

// loadUSBIDs reads the USB ID database, preferring an on-disk copy (user cache
// or a distro install) and falling back to the copy embedded in the binary so
// lookups keep working offline with an empty cache.
func loadUSBIDs() {
	usbVendors = map[string]string{}
	usbProducts = map[string]map[string]string{}
	if path, ok := usbLocalPath(); ok {
		if parseUSBIDsFile(path) {
			return
		}
	}
	parseUSBIDs(embeddedUSBIDs())
}

// usbLocalPath returns the first existing on-disk usb.ids.
func usbLocalPath() (string, bool) {
	if p := cachePath("usb.ids"); fileExists(p) {
		return p, true
	}
	for _, p := range usbSystemPaths {
		if fileExists(p) {
			return p, true
		}
	}
	return "", false
}

// parseUSBIDsFile loads usb.ids from disk; false means "cannot open", so the
// caller falls back to the embedded copy.
func parseUSBIDsFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
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
	return true
}

// parseUSBIDs loads usb.ids content already held in memory (the embedded copy).
func parseUSBIDs(data string) {
	currentVendor := ""
	for _, line := range strings.Split(data, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line[0] != '\t' && line[0] != ' ' {
			fields := strings.SplitN(line, " ", 2)
			if len(fields) == 2 && len(fields[0]) == 4 {
				currentVendor = strings.ToLower(fields[0])
				usbVendors[currentVendor] = strings.TrimSpace(fields[1])
				usbProducts[currentVendor] = map[string]string{}
			}
			continue
		}
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
	fmt.Println(L("Проверка базы USB-вендоров...", "Checking the USB vendor database..."))
	if err := refreshDB(usbIDsURL, "usb.ids", usbDatabaseLooksOK); err != nil {
		return offlineFallback(err)
	}
	return nil
}

func updateOUIDB() error {
	fmt.Println(L("Проверка базы MAC-вендоров (IEEE OUI)...", "Checking the IEEE OUI MAC vendor database..."))
	var lastErr error
	for _, u := range ouiURLs {
		err := refreshDB(u, "oui.txt", looksLikeOUIDB)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return offlineFallback(lastErr)
}

// offlineFallback keeps --vendor/--oui useful without a network: the binary
// already ships a database, so a failed refresh is a warning, not an error.
func offlineFallback(err error) error {
	fmt.Fprintln(os.Stderr, L("Не удалось обновить базу: ", "Could not refresh the database: ")+err.Error())
	fmt.Println(L(
		"Используется встроенная база — расшифровка вендоров работает без интернета.",
		"Using the built-in database — vendor lookups work without a network."))
	return nil
}

// refreshDB downloads a database into the cache unless the local copy is
// already current. Freshness is decided with an HTTP conditional request
// (If-None-Match / If-Modified-Since) against the validators stored next to
// the file, so an unchanged upstream costs one cheap round trip and no body.
//
// A 304, or a server that offers no validators and the same content, leaves
// the cache untouched and reports "already up to date".
func refreshDB(url, name string, valid func(string) bool) error {
	dst := cachePath(name)

	etag, lastMod := readValidators(dst)
	status, err := downloadTo(url, dst, etag, lastMod)
	if err != nil {
		return err
	}
	switch status {
	case dlNotModified:
		fmt.Println(L("Уже актуально: ", "Already up to date: ") + dst)
		return nil
	case dlUnchanged:
		fmt.Println(L("Уже актуально: ", "Already up to date: ") + dst)
		return nil
	}

	if !valid(dst) {
		os.Remove(dst)
		os.Remove(validatorsPath(dst))
		return fmt.Errorf("%s did not return a valid database", url)
	}
	fmt.Println(L("Обновлено: ", "Updated: ") + dst)
	return nil
}

// dlStatus describes what a conditional download actually did.
type dlStatus int

const (
	dlUpdated     dlStatus = iota // new content was written
	dlNotModified                 // server answered 304
	dlUnchanged                   // server had no validators but content matched
)

// validatorsPath is the sidecar that stores the HTTP validators for a file.
func validatorsPath(dst string) string { return dst + ".http" }

// readValidators returns the ETag and Last-Modified values stored for dst.
func readValidators(dst string) (etag, lastMod string) {
	b, err := os.ReadFile(validatorsPath(dst))
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "etag: "); ok {
			etag = v
		}
		if v, ok := strings.CutPrefix(line, "last-modified: "); ok {
			lastMod = v
		}
	}
	return etag, lastMod
}

// writeValidators stores the validators returned with the content we saved.
func writeValidators(dst, etag, lastMod string) {
	if etag == "" && lastMod == "" {
		return
	}
	var b strings.Builder
	if etag != "" {
		b.WriteString("etag: " + etag + "\n")
	}
	if lastMod != "" {
		b.WriteString("last-modified: " + lastMod + "\n")
	}
	os.WriteFile(validatorsPath(dst), []byte(b.String()), 0o644)
}

// downloadTo fetches a URL into dst atomically (temp file + rename), sending
// the supplied validators so an unchanged resource is not transferred again.
func downloadTo(url, dst, etag, lastMod string) (dlStatus, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return dlUpdated, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return dlUpdated, err
	}
	req.Header.Set("User-Agent", "sysinfo/1.3 (+https://github.com/paolosezart/sysinfo)")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastMod != "" {
		req.Header.Set("If-Modified-Since", lastMod)
	}
	resp, err := client.Do(req)
	if err != nil {
		return dlUpdated, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return dlNotModified, nil
	}
	if resp.StatusCode != http.StatusOK {
		return dlUpdated, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return dlUpdated, err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return dlUpdated, err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return dlUpdated, err
	}

	// Some mirrors answer 200 without validators. Compare content so an
	// unchanged database does not get rewritten on every run.
	if etag == "" && lastMod == "" && sameFileContent(dst, tmp) {
		os.Remove(tmp)
		return dlUnchanged, nil
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return dlUpdated, err
	}
	writeValidators(dst, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"))
	return dlUpdated, nil
}

// sameFileContent reports whether two files have identical contents.
func sameFileContent(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil || fa.Size() != fb.Size() {
		return false
	}
	ba, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	bb, err := os.ReadFile(b)
	if err != nil {
		return false
	}
	return bytes.Equal(ba, bb)
}

// usbDatabaseLooksOK guards against an HTML error page saved as usb.ids.
func usbDatabaseLooksOK(path string) bool {
	return dbHeaderOK(path, "list of usb id")
}

// looksLikeOUIDB guards against servers that answer with an HTML page instead
// of the database (which happens behind captive portals and some mirrors).
func looksLikeOUIDB(path string) bool {
	return dbHeaderOK(path, "(hex)", "organization")
}

// dbHeaderOK checks the first few KiB of a downloaded database for expected
// markers and rejects obvious HTML error pages.
func dbHeaderOK(path string, markers ...string) bool {
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
	for _, m := range markers {
		if strings.Contains(head, m) {
			return true
		}
	}
	return false
}
