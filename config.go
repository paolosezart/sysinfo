package main

import (
	"os"
	"path/filepath"
	"strings"
)

// configTxtPaths lists the boot configuration locations in priority order.
// Raspberry Pi OS Bookworm and later mount the boot partition at /boot/firmware;
// older images used /boot. Checking both means the file is found either way.
var configTxtPaths = []string{
	"/boot/firmware/config.txt",
	"/boot/config.txt",
}

// findConfigTxt returns the first existing config.txt, or false when the host is
// not a Raspberry Pi (or the boot partition is not mounted).
func findConfigTxt() (string, bool) {
	for _, p := range configTxtPaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	return "", false
}

// configTxt is the parsed form of config.txt: the settings that apply to this
// board (after resolving conditionals and includes) plus the active device-tree
// overlays.
type configTxt struct {
	Path     string
	Settings map[string]string
	Overlays map[string]bool
}

// setting returns a setting's value, or "" when it is absent.
func (c *configTxt) setting(key string) string {
	if c == nil {
		return ""
	}
	return c.Settings[key]
}

// hasOverlay reports whether a dtoverlay is enabled (parameters ignored).
func (c *configTxt) hasOverlay(name string) bool {
	if c == nil {
		return false
	}
	return c.Overlays[name]
}

// collectConfigTxt finds and parses config.txt for the given board model. It
// returns nil when no config.txt exists, so callers can skip the whole block on
// non-Raspberry-Pi hosts.
func collectConfigTxt(model string) *configTxt {
	path, ok := findConfigTxt()
	if !ok {
		return nil
	}
	ct := &configTxt{Path: path, Settings: map[string]string{}, Overlays: map[string]bool{}}
	ct.load(path, boardConfigTags(model), 0)
	return ct
}

// load reads one config.txt file, following includes. depth guards against an
// include cycle.
func (c *configTxt) load(path string, tags map[string]bool, depth int) {
	if depth > 4 {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	active := true
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// The firmware treats "#" as a comment start anywhere on the line.
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			active = filterApplies(line[1:len(line)-1], tags)
			continue
		}
		if !active {
			continue
		}
		key, val := splitConfigLine(line)
		switch {
		case key == "":
			continue
		case key == "include" || strings.HasPrefix(key, "include "):
			// The include directive is space-separated ("include extra.txt"),
			// not "key=value", so the filename may end up inside key.
			file := strings.TrimSpace(strings.TrimPrefix(key, "include"))
			if file == "" {
				file = val
			}
			if file != "" {
				c.load(filepath.Join(filepath.Dir(path), file), tags, depth+1)
			}
		case key == "dtoverlay":
			name := val
			if i := strings.IndexByte(name, ','); i >= 0 {
				name = name[:i]
			}
			if name != "" {
				c.Overlays[name] = true
			}
			c.Settings[key] = val
		default:
			c.Settings[key] = val
		}
	}
}

// splitConfigLine splits a "property=value" statement. A bare key yields an
// empty value.
func splitConfigLine(line string) (key, val string) {
	if i := strings.IndexByte(line, '='); i >= 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
	}
	return strings.TrimSpace(line), ""
}

// modelTags is the set of conditional-filter names that identify a board.
var modelTags = map[string]bool{
	"pi1": true, "pi2": true, "pi3": true, "pi3+": true, "pi4": true, "pi5": true,
	"pi400": true, "pi500": true, "pi0": true, "pi0w": true, "pi02": true,
	"cm0": true, "cm1": true, "cm3": true, "cm3+": true, "cm4": true, "cm4s": true, "cm5": true,
}

// filterApplies decides whether a "[...]" conditional filter selects this board.
// Model filters are matched against the board tags. Filters this tool cannot
// evaluate (EDID, HDMI, gpio, board-type) are treated as active so a setting is
// never silently dropped.
func filterApplies(spec string, tags map[string]bool) bool {
	unknown := false
	for _, p := range strings.Split(spec, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		switch {
		case p == "":
		case p == "all":
			return true
		case p == "none":
			return false
		case tags[p]:
			return true
		case modelTags[p]:
			// A known model filter that does not match this board.
		default:
			unknown = true
		}
	}
	return unknown
}

// boardConfigTags maps a board model string to the conditional-filter names the
// firmware would enable for it.
func boardConfigTags(model string) map[string]bool {
	t := map[string]bool{}
	m := strings.ToLower(model)
	has := func(s string) bool { return strings.Contains(m, s) }
	switch {
	case has("compute module 5"):
		t["cm5"], t["pi5"] = true, true
	case has("raspberry pi 500"):
		t["pi500"], t["pi5"] = true, true
	case has("raspberry pi 5"):
		t["pi5"] = true
	case has("compute module 4"):
		t["cm4"], t["pi4"] = true, true
	case has("raspberry pi 400"):
		t["pi400"], t["pi4"] = true, true
	case has("raspberry pi 4"):
		t["pi4"] = true
	case has("compute module 3+"):
		t["cm3+"], t["pi3+"], t["pi3"] = true, true, true
	case has("compute module 3"):
		t["cm3"], t["pi3"] = true, true
	case has("raspberry pi 3 model b plus"), has("raspberry pi 3 model a plus"),
		has("raspberry pi 3b+"), has("raspberry pi 3a+"):
		t["pi3+"], t["pi3"] = true, true
	case has("raspberry pi 3"):
		t["pi3"] = true
	case has("raspberry pi zero 2"):
		t["pi02"], t["pi0w"], t["pi0"] = true, true, true
	case has("raspberry pi zero w"):
		t["pi0w"], t["pi0"] = true, true
	case has("raspberry pi zero"):
		t["pi0"] = true
	case has("raspberry pi 2"):
		t["pi2"] = true
	case has("raspberry pi"):
		t["pi1"] = true
	}
	return t
}
