package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// timePtr returns a pointer to t, for the optional time fields in the model.
func timePtr(t time.Time) *time.Time { return &t }

// zoneOffset renders the current UTC offset as "GMT+03:00", the form the report
// shows next to the system clock.
func zoneOffset(now time.Time) string {
	_, off := now.Zone()
	sign := "+"
	if off < 0 {
		sign = "-"
		off = -off
	}
	return fmt.Sprintf("GMT%s%02d:%02d", sign, off/3600, (off%3600)/60)
}

// formatDrift renders a clock deviation with an explicit sign. Sub-second
// deviations are shown in milliseconds because NTP usually lands there.
func formatDrift(d time.Duration) string {
	sign := "+"
	if d < 0 {
		sign = "-"
		d = -d
	}
	if d < time.Second {
		return fmt.Sprintf("%s%d %s", sign, d.Milliseconds(), L("мс", "ms"))
	}
	return fmt.Sprintf("%s%.2f %s", sign, d.Seconds(), L("с", "s"))
}

// internetTimeBudget bounds the whole internet time check so a run without a
// route to the network cannot stall the report.
const internetTimeBudget = 5 * time.Second

// internetTime queries a public time source and returns the server time (UTC),
// the local clock's drift (local minus server) and whether it succeeded. NTP is
// tried first, then an HTTP Date header, which is coarser (one-second
// resolution) but survives networks that block UDP.
func internetTime(budget time.Duration) (time.Time, time.Duration, bool) {
	deadline := time.Now().Add(budget)
	for _, host := range []string{"pool.ntp.org", "time.google.com"} {
		if t, d, ok := ntpTime(host, remaining(deadline)); ok {
			return t, d, true
		}
	}
	for _, url := range []string{"https://www.google.com", "https://www.cloudflare.com"} {
		if t, d, ok := httpTime(url, remaining(deadline)); ok {
			return t, d, true
		}
	}
	return time.Time{}, 0, false
}

// remaining caps a single attempt by what is left of the overall budget.
func remaining(deadline time.Time) time.Duration {
	left := time.Until(deadline)
	if left <= 0 {
		return time.Millisecond
	}
	return left
}

// ntpEpochOffset is the number of seconds between the NTP epoch (1900) and the
// Unix epoch (1970).
const ntpEpochOffset = 2208988800

// ntpTime queries an NTP server by host name on the standard port.
func ntpTime(host string, timeout time.Duration) (time.Time, time.Duration, bool) {
	return ntpQuery(net.JoinHostPort(host, "123"), timeout)
}

// ntpQuery sends one SNTP request (RFC 5905) to addr and reads the transmit
// timestamp, which sits at bytes 40..48 of the reply. The drift is estimated
// against the midpoint of the request/response window, so it does not include
// half the round trip.
func ntpQuery(addr string, timeout time.Duration) (time.Time, time.Duration, bool) {
	conn, err := net.DialTimeout("udp", addr, timeout)
	if err != nil {
		return time.Time{}, 0, false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	req := make([]byte, 48)
	req[0] = 0x1b // LI=0, VN=3, mode=3 (client)
	t1 := time.Now()
	if _, err := conn.Write(req); err != nil {
		return time.Time{}, 0, false
	}
	resp := make([]byte, 48)
	n, err := conn.Read(resp)
	t4 := time.Now()
	if err != nil || n < 48 {
		return time.Time{}, 0, false
	}
	secs := binary.BigEndian.Uint32(resp[40:44])
	if secs == 0 {
		return time.Time{}, 0, false // Kiss-o'-Death or unsynchronised server
	}
	frac := binary.BigEndian.Uint32(resp[44:48])
	server := time.Unix(int64(secs)-ntpEpochOffset, int64(float64(frac)*1e9/4294967296.0)).UTC()

	mid := t1.Add(t4.Sub(t1) / 2)
	return server, mid.Sub(server), true
}

// httpTime reads the server clock from an HTTP Date header. HEAD avoids
// downloading a body.
func httpTime(url string, timeout time.Duration) (time.Time, time.Duration, bool) {
	client := &http.Client{Timeout: timeout}
	t1 := time.Now()
	resp, err := client.Head(url)
	if err != nil {
		return time.Time{}, 0, false
	}
	defer resp.Body.Close()
	t4 := time.Now()
	server, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		return time.Time{}, 0, false
	}
	mid := t1.Add(t4.Sub(t1) / 2)
	return server.UTC(), mid.Sub(server), true
}

// statxBtimeMask is STATX_BTIME: the caller asks for the birth time, and the
// returned mask says whether the filesystem provided it.
const statxBtimeMask = 0x800

// atFDCWD is the special dirfd value meaning "relative to the working
// directory". The kernel defines it as -100.
const atFDCWD = ^uintptr(0) - 99

// statxTrap returns the statx syscall number for the running architecture.
func statxTrap() uintptr { return statxTrapFor(runtime.GOARCH) }

// statxTrapFor returns the statx syscall number, which is architecture specific
// and not exported by the syscall package. Zero means unsupported there. The
// numbers come from each arch's unistd_*.h (__NR_statx).
func statxTrapFor(arch string) uintptr {
	switch arch {
	case "amd64":
		return 332
	case "386":
		return 383
	case "arm":
		return 397
	case "arm64", "riscv64", "loong64":
		return 291
	case "s390x":
		return 379
	case "ppc64", "ppc64le":
		return 383
	}
	return 0
}

// statxTimestamp mirrors struct statx_timestamp: a 64-bit seconds field, a
// 32-bit nanoseconds field and 32 bits of padding.
type statxTimestamp struct {
	Sec  int64
	Nsec uint32
	_    int32
}

// statxBuf mirrors struct statx. The layout is architecture independent (the
// kernel defines it with fixed-width types), and the trailing spare area is
// kept so the struct is exactly 256 bytes.
type statxBuf struct {
	Mask           uint32
	Blksize        uint32
	Attributes     uint64
	Nlink          uint32
	Uid            uint32
	Gid            uint32
	Mode           uint16
	_              uint16
	Ino            uint64
	Size           uint64
	Blocks         uint64
	AttributesMask uint64
	Atime          statxTimestamp
	Btime          statxTimestamp
	Ctime          statxTimestamp
	Mtime          statxTimestamp
	RdevMajor      uint32
	RdevMinor      uint32
	DevMajor       uint32
	DevMinor       uint32
	_              [14]uint64
}

// fileBtime returns a file's birth (creation) time via statx. Filesystems that
// record it (ext4, xfs, btrfs) report it; older kernels and filesystems without
// the concept report false, so callers must have a fallback.
func fileBtime(path string) (time.Time, bool) {
	trap := statxTrap()
	if trap == 0 {
		return time.Time{}, false
	}
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return time.Time{}, false
	}
	var st statxBuf
	_, _, errno := syscall.Syscall6(trap, atFDCWD, uintptr(unsafe.Pointer(p)), 0,
		statxBtimeMask, uintptr(unsafe.Pointer(&st)), 0)
	if errno != 0 || st.Mask&statxBtimeMask == 0 || st.Btime.Sec == 0 {
		return time.Time{}, false
	}
	return time.Unix(st.Btime.Sec, int64(st.Btime.Nsec)), true
}
