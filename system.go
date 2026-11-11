package main

import (
	"os"
	"runtime"
	"strings"
	"time"
)

// SystemInfo describes the host and operating system, mirroring the header
// block of Windows' systeminfo but adapted for Linux boards such as the Pi.
type SystemInfo struct {
	Hostname      string
	OSName        string
	KernelVersion string
	Arch          string
	BoardModel    string
	BoardVendor   string
	BoardRevision string
	Serial        string
	BootTime      time.Time
	Uptime        time.Duration
	TimeZone      string
}

func collectSystemInfo() SystemInfo {
	si := SystemInfo{
		Arch: runtime.GOARCH,
	}

	si.Hostname, _ = os.Hostname()

	si.OSName = osReleasePrettyName()
	si.KernelVersion = kernelVersion()
	si.BoardModel, si.BoardVendor, si.BoardRevision, si.Serial = boardInfo()

	if up := readFileTrim("/proc/uptime"); up != "" {
		fields := strings.Fields(up)
		if len(fields) > 0 {
			si.Uptime = time.Duration(parseFloat(fields[0]) * float64(time.Second))
		}
	}
	si.BootTime = time.Now().Add(-si.Uptime)
	si.TimeZone = timeZoneName()

	return si
}

// timeZoneName resolves the IANA zone name (e.g. "Europe/Moscow"). time.Now()'s
// "MST" format only yields a numeric offset on many systems, which is far less
// useful, so prefer the zoneinfo link and the /etc/timezone file.
func timeZoneName() string {
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		if idx := strings.Index(link, "zoneinfo/"); idx >= 0 {
			return link[idx+len("zoneinfo/"):]
		}
	}
	if tz := readFileTrim("/etc/timezone"); tz != "" {
		return tz
	}
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	return time.Now().Format("MST")
}

func osReleasePrettyName() string {
	m := readKeyValueFile("/etc/os-release", "=")
	if v := m["PRETTY_NAME"]; v != "" {
		return strings.Trim(v, "\"")
	}
	if v := m["NAME"]; v != "" {
		return strings.Trim(v, "\"")
	}
	return "Linux"
}

func kernelVersion() string {
	if v := readFileTrim("/proc/sys/kernel/osrelease"); v != "" {
		return v
	}
	// Fall back to /proc/version ("Linux version 6.1.21-v8+ ...").
	if v := readFileTrim("/proc/version"); v != "" {
		fields := strings.Fields(v)
		if len(fields) >= 3 {
			return fields[2]
		}
	}
	return ""
}

// boardInfo reads the device-tree model (Raspberry Pi and most ARM boards),
// falling back to DMI on x86 machines.
func boardInfo() (model, vendor, revision, serial string) {
	// Device tree (Raspberry Pi).
	model = readFileTrim("/sys/firmware/devicetree/base/model")
	if model == "" {
		model = readFileTrim("/proc/device-tree/model")
	}
	model = strings.TrimRight(model, "\x00")

	serial = strings.TrimRight(readFileTrim("/sys/firmware/devicetree/base/serial-number"), "\x00")

	// Raspberry Pi revision code (e.g. c03114) -> also useful for older boards.
	if cpuinfo := readFileTrim("/proc/cpuinfo"); cpuinfo != "" {
		for _, line := range strings.Split(cpuinfo, "\n") {
			if strings.HasPrefix(line, "Revision") {
				if parts := strings.SplitN(line, ":", 2); len(parts) == 2 {
					revision = strings.TrimSpace(parts[1])
				}
				break
			}
		}
	}

	// DMI fallback (x86).
	if model == "" {
		model = readFileTrim("/sys/class/dmi/id/product_name")
		if model == "" || model == "System Product Name" {
			if pv := readFileTrim("/sys/class/dmi/id/product_version"); pv != "" && pv != "To Be Filled By O.E.M." {
				model = pv
			}
		}
	}
	vendor = readFileTrim("/sys/class/dmi/id/sys_vendor")
	if vendor == "" || vendor == "System manufacturer" {
		vendor = readFileTrim("/sys/class/dmi/id/board_vendor")
	}
	if vendor == "" {
		vendor = raspberryVendor(model)
	}
	return model, vendor, revision, serial
}

func raspberryVendor(model string) string {
	if strings.Contains(strings.ToLower(model), "raspberry") {
		return "Raspberry Pi Foundation"
	}
	return ""
}
