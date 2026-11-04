package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// readFileTrim reads a file and trims surrounding whitespace.
func readFileTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readFileFirstLine returns the first non-empty line of a file.
func readFileFirstLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// runCmd executes a command with a timeout and returns trimmed stdout.
func runCmd(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// commandExists reports whether a binary is available in PATH.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// unitLocal translates a neutral unit token (stored in the data model) into the
// active language for display. Keeping units neutral keeps JSON stable and
// language-independent.
func unitLocal(u string) string {
	switch u {
	case "V":
		return L("В", "V")
	case "MHz":
		return L("МГц", "MHz")
	case "GHz":
		return L("ГГц", "GHz")
	case "Mbps":
		return L("Мбит/с", "Mbps")
	case "Gbps":
		return L("Гбит/с", "Gbps")
	case "cm":
		return L("см", "cm")
	case "RPM":
		return L("об/мин", "RPM")
	case "C":
		return L("°C", "°C")
	}
	return u
}

// langCode is defined in i18n.go; L() selects between the two variants.

// humanBytes formats a byte count using binary units (KiB, MiB, GiB...).
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d %s", n, L("Б", "B"))
	}
	units := []string{"КиБ", "МиБ", "ГиБ", "ТиБ", "ПиБ"}
	if langCode == "en" {
		units = []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	}
	val := float64(n)
	i := -1
	for val >= unit && i < len(units)-1 {
		val /= unit
		i++
	}
	return fmt.Sprintf("%.2f %s", val, units[i])
}

// humanBytesShort is like humanBytes but omits decimals for whole values.
func humanBytesShort(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d %s", n, L("Б", "B"))
	}
	units := []string{"КиБ", "МиБ", "ГиБ", "ТиБ", "ПиБ"}
	if langCode == "en" {
		units = []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	}
	val := float64(n)
	i := -1
	for val >= unit && i < len(units)-1 {
		val /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", val, units[i])
}

// parseUint parses an unsigned integer, returning 0 on error.
func parseUint(s string) uint64 {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseFloat parses a float, returning 0 on error.
func parseFloat(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

// parseUintHex parses a base-16 unsigned integer, returning 0 on error.
func parseUintHex(s string) uint64 {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 16, 64)
	if err != nil {
		return 0
	}
	return v
}

// readKeyValueFile parses "key: value" files such as /proc/cpuinfo.
func readKeyValueFile(path string, sep string) map[string]string {
	m := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	for _, line := range strings.Split(string(b), "\n") {
		idx := strings.Index(line, sep)
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+len(sep):])
		if key != "" && val != "" {
			if _, exists := m[key]; !exists {
				m[key] = val
			}
		}
	}
	return m
}

// humanDuration formats a duration in a compact human form.
func humanDuration(d time.Duration) string {
	total := int64(d.Seconds())
	days := total / 86400
	hours := (total % 86400) / 3600
	mins := (total % 3600) / 60
	secs := total % 60
	parts := []string{}
	if days > 0 {
		parts = append(parts, fmt.Sprintf(L("%d д", "%dd"), days))
	}
	if hours > 0 || days > 0 {
		parts = append(parts, fmt.Sprintf(L("%d ч", "%dh"), hours))
	}
	if mins > 0 || hours > 0 || days > 0 {
		parts = append(parts, fmt.Sprintf(L("%d мин", "%dm"), mins))
	}
	parts = append(parts, fmt.Sprintf(L("%d с", "%ds"), secs))
	return strings.Join(parts, " ")
}
