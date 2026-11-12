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
	// TimeZoneOffset is the current UTC offset, e.g. "GMT+03:00".
	TimeZoneOffset string
	// InstallTime is when the root filesystem was created (best effort).
	InstallTime *time.Time
	// CurrentTime is the local clock at collection time.
	CurrentTime time.Time
	// InternetTime and ClockDrift are set only when the network time check
	// succeeds; ClockDrift is local minus server.
	InternetTime *time.Time
	ClockDrift   string
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
	si.TimeZoneOffset = zoneOffset(time.Now())
	si.CurrentTime = time.Now()
	if t, ok := fileBtime("/"); ok && !t.IsZero() {
		si.InstallTime = &t
	} else if fi, err := os.Stat("/etc/machine-id"); err == nil {
		t := fi.ModTime()
		si.InstallTime = &t
	}

	return si
}

// applyInternetTime fills the internet time fields, best effort. It is kept out
// of collectSystemInfo so the report never blocks on the network unless asked.
func applyInternetTime(si *SystemInfo, budget time.Duration) {
	if t, drift, ok := internetTime(budget); ok {
		si.InternetTime = &t
		si.ClockDrift = formatDrift(drift)
	}
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
