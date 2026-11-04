package main

import (
	"bufio"
	"os"
	"sort"
	"strings"
	"syscall"
)

// Filesystem describes one real, user-meaningful mounted filesystem.
type Filesystem struct {
	Device     string
	MountPoint string
	FSType     string
	TotalBytes uint64
	UsedBytes  uint64
	FreeBytes  uint64
	UsedPct    float64
	Model      string // block device model/vendor, when readable
	BusSpeed   string // e.g. "SDR104 (UHS-I)" for SD cards
	SDClass    string // e.g. "Class 10 (10 МБ/с)" when known
}

// pseudoFS are virtual or otherwise uninteresting filesystems for a
// "how much disk space do I have" overview.
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "tmpfs": true, "devtmpfs": true,
	"devpts": true, "cgroup": true, "cgroup2": true, "pstore": true,
	"securityfs": true, "debugfs": true, "tracefs": true, "configfs": true,
	"fusectl": true, "mqueue": true, "hugetlbfs": true, "bpf": true,
	"binfmt_misc": true, "autofs": true, "ramfs": true, "squashfs": true,
	"overlay": true, "fuse.sysboxfs": true, "efivarfs": true,
}

// collectFilesystems parses /proc/mounts and reports only real block-device
// filesystems, deduplicating bind mounts by device+mountpoint and skipping
// the /etc, /var/lib noise that makes `df -h` hard to read.
func collectFilesystems() []Filesystem {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []Filesystem
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		device, mount, fstype := fields[0], fields[1], fields[2]
		if pseudoFS[fstype] {
			continue
		}
		if !strings.HasPrefix(device, "/dev/") && !strings.HasPrefix(device, "/dev/root") {
			continue
		}
		mount = unescapeMount(mount)
		// Skip bind mounts and system plumbing that users do not care about.
		if isSystemMount(mount) {
			continue
		}
		key := device + "|" + mount
		if seen[key] {
			continue
		}
		seen[key] = true

		var st syscall.Statfs_t
		if err := syscall.Statfs(mount, &st); err != nil {
			continue
		}
		bs := uint64(st.Bsize)
		total := st.Blocks * bs
		free := st.Bavail * bs
		used := total - st.Bfree*bs
		fs := Filesystem{
			Device:     device,
			MountPoint: mount,
			FSType:     fstype,
			TotalBytes: total,
			FreeBytes:  free,
			UsedBytes:  used,
		}
		if total > 0 {
			fs.UsedPct = float64(used) / float64(total) * 100
		}
		fs.Model, fs.BusSpeed, fs.SDClass = blockDeviceMeta(device)
		out = append(out, fs)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].MountPoint < out[j].MountPoint })
	return out
}

// isSystemMount reports whether a mount point is system plumbing that only
// clutters a "how much disk space do I have" overview. Real partitions such as
// /boot, /boot/firmware and /home are kept; nested mounts under /etc, /var,
// /usr, /run, /dev and the like are skipped.
func isSystemMount(mount string) bool {
	trimmed := strings.TrimPrefix(mount, "/")
	if trimmed == "" {
		return false // root
	}
	top := trimmed
	if idx := strings.Index(trimmed, "/"); idx >= 0 {
		top = trimmed[:idx]
	}

	// Always keep user-facing mount trees, including /boot/firmware.
	switch top {
	case "mnt", "media", "data", "srv", "boot", "home", "opt":
		return false
	}

	// Everything else at depth 1 is a real top-level filesystem (keep it).
	if !strings.Contains(trimmed, "/") {
		return false
	}

	// Deeper mounts under these trees are bind mounts or pseudo-filesystems.
	switch top {
	case "etc", "var", "usr", "sys", "proc", "run", "dev", "snap", "tmp":
		return true
	}
	return false
}

// unescapeMount decodes the octal escapes (\040 for space) used in /proc/mounts.
func unescapeMount(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			var v int
			ok := true
			for j := 1; j <= 3; j++ {
				c := s[i+j]
				if c < '0' || c > '7' {
					ok = false
					break
				}
				v = v*8 + int(c-'0')
			}
			if ok {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
