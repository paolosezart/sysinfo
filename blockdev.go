package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// deviceForPath returns the block device backing a path, by finding the longest
// mount point in /proc/mounts that is a prefix of the path.
func deviceForPath(path string) string {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return ""
	}
	defer f.Close()

	bestMount, bestDev := "", ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		dev, mount := fields[0], unescapeMount(fields[1])
		if !strings.HasPrefix(dev, "/dev/") {
			continue
		}
		if path == mount || strings.HasPrefix(path, strings.TrimSuffix(mount, "/")+"/") {
			if len(mount) > len(bestMount) {
				bestMount, bestDev = mount, dev
			}
		}
	}
	return bestDev
}

// blockDeviceMeta resolves the model name, bus speed and (for SD cards) the
// speed class of the block device backing a partition.
func blockDeviceMeta(device string) (model, busSpeed, sdClass string) {
	base := blockBaseName(device)
	if base == "" {
		return
	}
	dir := filepath.Join("/sys/block", base)

	// Model: USB/SATA devices expose vendor+model; SD cards expose "name".
	if n := readFileTrim(filepath.Join(dir, "device/name")); n != "" {
		model = n
	} else {
		v := readFileTrim(filepath.Join(dir, "device/vendor"))
		m := readFileTrim(filepath.Join(dir, "device/model"))
		model = strings.TrimSpace(v + " " + m)
	}
	if model == "" {
		model = readFileTrim(filepath.Join(dir, "device/product_name"))
	}

	// SD/MMC bus speed mode (mmcblk*). The block device exposes no timing
	// attribute in sysfs; the active mode lives in debugfs.
	if base != "" && strings.HasPrefix(base, "mmcblk") {
		busSpeed = mmcBusSpeed(dir)
	}

	// SD speed class from the SD Status register, when the kernel exposes it.
	sdClass = sdSpeedClass(dir)
	return
}

// blockBaseName turns "/dev/mmcblk0p2" into "mmcblk0" and "/dev/sda1" into "sda".
func blockBaseName(device string) string {
	name := strings.TrimPrefix(device, "/dev/")
	name = strings.TrimPrefix(name, "mapper/")
	// Strip a trailing partition suffix (p1, 1) from the whole-disk name.
	if strings.HasPrefix(name, "mmcblk") || strings.HasPrefix(name, "nvme") {
		if idx := strings.LastIndex(name, "p"); idx > 0 {
			if _, err := strconv.Atoi(name[idx+1:]); err == nil {
				return name[:idx]
			}
		}
		return name
	}
	trimmed := strings.TrimRight(name, "0123456789")
	if trimmed != "" {
		return trimmed
	}
	return name
}

// sdTimingName maps an MMC timing spec to a human bus-mode name.
func sdTimingName(spec string) string {
	switch strings.ToLower(spec) {
	case "legacy", "ds":
		return L("Default Speed (12.5 МБ/с)", "Default Speed (12.5 MB/s)")
	case "hs":
		return L("High Speed (25 МБ/с)", "High Speed (25 MB/s)")
	case "hs200":
		return "HS200 (200 " + L("МБ/с", "MB/s") + ")"
	case "hs400":
		return "HS400 (400 " + L("МБ/с", "MB/s") + ")"
	case "ddr50":
		return "DDR50 (50 " + L("МБ/с", "MB/s") + ")"
	case "ddr52":
		return "DDR52 (52 " + L("МБ/с", "MB/s") + ")"
	case "sdr12":
		return "SDR12 (12.5 " + L("МБ/с", "MB/s") + ")"
	case "sdr25":
		return "SDR25 (25 " + L("МБ/с", "MB/s") + ")"
	case "sdr50":
		return "SDR50 (50 " + L("МБ/с", "MB/s") + ")"
	case "sdr104":
		return "SDR104 (UHS-I, 104 " + L("МБ/с", "MB/s") + ")"
	}
	return spec
}

// mmcBusSpeed reads the active bus timing from debugfs, where the kernel exposes
// it as "timing spec: <n> (<name>)". The block device has no such attribute in
// sysfs, so this is the only place the current mode is reported.
func mmcBusSpeed(dir string) string {
	idx := strings.TrimPrefix(filepath.Base(dir), "mmcblk")
	if idx == "" {
		return ""
	}
	ios := readFileTrim(filepath.Join("/sys/kernel/debug/mmc"+idx, "ios"))
	if ios == "" {
		return ""
	}
	for _, line := range strings.Split(ios, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "timing spec:") {
			continue
		}
		// Format: "timing spec:\t2 (sd high-speed)".
		start := strings.Index(line, "(")
		end := strings.LastIndex(line, ")")
		if start < 0 || end <= start {
			break
		}
		return sdTimingName(normalizeTiming(line[start+1 : end]))
	}
	return ""
}

// normalizeTiming maps a debugfs timing label to the short form sdTimingName uses.
func normalizeTiming(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "legacy":
		return "legacy"
	case "sd high-speed", "mmc high-speed":
		return "hs"
	case "sd uhs sdr12":
		return "sdr12"
	case "sd uhs sdr25":
		return "sdr25"
	case "sd uhs sdr50":
		return "sdr50"
	case "sd uhs sdr104":
		return "sdr104"
	case "sd uhs ddr50":
		return "ddr50"
	case "mmc ddr52":
		return "ddr52"
	case "mmc hs200":
		return "hs200"
	case "mmc hs400", "mmc hs400 enhanced strobe":
		return "hs400"
	}
	return s
}

// sdSpeedClass reads the SD Status register (ssr) from sysfs and maps the
// speed-class field to a human label. The CSD does not carry the speed class,
// so when the kernel does not expose ssr this returns "".
func sdSpeedClass(dir string) string {
	ssr := strings.ReplaceAll(readFileTrim(filepath.Join(dir, "device/ssr")), " ", "")
	// The kernel prints the 512-bit register as 16 big-endian u32 values with no
	// separators. The speed-class byte is bits 440..447, the top byte of raw_ssr[2]
	// (hex characters 16..18). Mapping: 0=class 0, 1=class 2 ... 4=class 10.
	if len(ssr) < 18 {
		return ""
	}
	switch parseUintHex(ssr[16:18]) {
	case 1:
		return L("Class 2 (2 МБ/с)", "Class 2 (2 MB/s)")
	case 2:
		return L("Class 4 (4 МБ/с)", "Class 4 (4 MB/s)")
	case 3:
		return L("Class 6 (6 МБ/с)", "Class 6 (6 MB/s)")
	case 4:
		return L("Class 10 (10 МБ/с)", "Class 10 (10 MB/s)")
	}
	return ""
}
