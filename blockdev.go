package main

import (
	"bufio"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// hexToBytes decodes a hex string (possibly space separated) into bytes.
func hexToBytes(s string) ([]byte, error) {
	return hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
}

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

	// SD/MMC bus speed mode (mmcblk*): hs, ddr50, sdr104 ...
	if mode := readFileTrim(filepath.Join(dir, "device/timing_spec")); mode != "" {
		busSpeed = sdTimingName(mode)
	}

	// SD speed class from the CSD, when the kernel exposes it.
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
	case "sdr50":
		return "SDR50 (50 " + L("МБ/с", "MB/s") + ")"
	case "sdr104":
		return "SDR104 (UHS-I, 104 " + L("МБ/с", "MB/s") + ")"
	}
	return spec
}

// sdSpeedClass maps the CSD speed-class bits to a human label. Many kernels do
// not expose the CSD, in which case this returns "".
func sdSpeedClass(dir string) string {
	csd := readFileTrim(filepath.Join(dir, "device/csd"))
	if csd == "" {
		return ""
	}
	// The CSD is 16 hex bytes; speed class lives in the last byte (CCC/class).
	b, err := hexToBytes(csd)
	if err != nil || len(b) < 16 {
		return ""
	}
	switch b[15] {
	case 0x00, 0x2B:
		return L("Class 0/2", "Class 0/2")
	case 0x2D:
		return L("Class 4 (4 МБ/с)", "Class 4 (4 MB/s)")
	case 0x32:
		return L("Class 6 (6 МБ/с)", "Class 6 (6 MB/s)")
	case 0x3B:
		return L("Class 10 (10 МБ/с)", "Class 10 (10 MB/s)")
	}
	return ""
}
