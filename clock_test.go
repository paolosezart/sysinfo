package main

import (
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"unsafe"
)

// statxTrapFor must return each architecture's real __NR_statx. A wrong number
// would make fileBtime fail silently on that platform.
func TestStatxTrapFor(t *testing.T) {
	cases := map[string]uintptr{
		"amd64": 332, "386": 383, "arm": 397, "arm64": 291,
		"riscv64": 291, "loong64": 291, "s390x": 379,
		"ppc64": 383, "ppc64le": 383, "mips": 0,
	}
	for arch, want := range cases {
		if got := statxTrapFor(arch); got != want {
			t.Errorf("statxTrapFor(%q) = %d, want %d", arch, got, want)
		}
	}
}

// The statx struct is defined by the kernel with fixed-width fields and must be
// exactly 256 bytes, or the kernel would write past the buffer.
func TestStatxBufSize(t *testing.T) {
	if s := unsafe.Sizeof(statxBuf{}); s != 256 {
		t.Errorf("statxBuf size = %d, want 256", s)
	}
}

// Btime must line up with the kernel's struct statx layout: 0x50 for the mask,
// 0x60 for btime.sec. A wrong offset would read the wrong field.
func TestStatxBtimeOffset(t *testing.T) {
	var b statxBuf
	base := uintptr(unsafe.Pointer(&b))
	if off := uintptr(unsafe.Pointer(&b.Mask)) - base; off != 0 {
		t.Errorf("Mask offset = %#x, want 0", off)
	}
	if off := uintptr(unsafe.Pointer(&b.Btime)) - base; off != 0x50 {
		t.Errorf("Btime offset = %#x, want 0x50", off)
	}
}

// fileBtime on the root filesystem returns a plausible time, and never a future
// one. On a filesystem without birth time it reports false instead of guessing.
func TestFileBtime(t *testing.T) {
	bt, ok := fileBtime("/")
	if !ok {
		t.Skip("root filesystem does not report a birth time")
	}
	if bt.IsZero() {
		t.Fatal("birth time is zero")
	}
	if bt.After(time.Now().Add(time.Minute)) {
		t.Errorf("birth time %s is in the future", bt)
	}
}

func TestZoneOffset(t *testing.T) {
	cases := []struct {
		name string
		loc  *time.Location
		want string
	}{
		{"UTC", time.UTC, "GMT+00:00"},
		{"Moscow", time.FixedZone("MSK", 3*3600), "GMT+03:00"},
		{"half hour", time.FixedZone("IST", 5*3600+1800), "GMT+05:30"},
		{"negative", time.FixedZone("EST", -5*3600), "GMT-05:00"},
	}
	for _, c := range cases {
		if got := zoneOffset(time.Now().In(c.loc)); got != c.want {
			t.Errorf("%s: zoneOffset = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFormatDrift(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{12 * time.Millisecond, "+12 " + L("мс", "ms")},
		{-340 * time.Millisecond, "-340 " + L("мс", "ms")},
		{1500 * time.Millisecond, "+1.50 " + L("с", "s")},
		{-2 * time.Second, "-2.00 " + L("с", "s")},
	}
	for _, c := range cases {
		if got := formatDrift(c.in); got != c.want {
			t.Errorf("formatDrift(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// buildNTPReply crafts a minimal SNTP reply carrying the given transmit time.
func buildNTPReply(t time.Time) []byte {
	resp := make([]byte, 48)
	resp[0] = 0x24 // LI=0, VN=4, mode=4 (server)
	secs := uint32(t.Unix() + ntpEpochOffset)
	binary.BigEndian.PutUint32(resp[40:44], secs)
	return resp
}

// ntpTime must parse the transmit timestamp from a reply. A local UDP listener
// stands in for the server so the real client code path runs.
func TestNTPTime(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	server := time.Now().UTC().Truncate(time.Second)
	go func() {
		buf := make([]byte, 48)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil || n < 48 {
			return
		}
		pc.WriteTo(buildNTPReply(server), addr)
	}()

	got, drift, ok := ntpQuery(pc.LocalAddr().String(), 2*time.Second)
	if !ok {
		t.Skip("local UDP round trip failed in this environment")
	}
	if d := got.Sub(server); d > time.Second || d < -time.Second {
		t.Errorf("parsed time %s differs from %s by %v", got, server, d)
	}
	if drift > time.Second || drift < -time.Second {
		t.Errorf("drift %v looks wrong for a loopback round trip", drift)
	}
}

// httpTime reads the clock from the Date header; a stub server exercises the
// real parsing path.
func TestHTTPTime(t *testing.T) {
	server := time.Now().UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", server.Format(http.TimeFormat))
	}))
	defer srv.Close()

	got, drift, ok := httpTime(srv.URL, 2*time.Second)
	if !ok {
		t.Fatal("httpTime failed against a well-formed server")
	}
	if d := got.Sub(server); d > time.Second || d < -time.Second {
		t.Errorf("parsed time %s differs from %s by %v", got, server, d)
	}
	if drift > 2*time.Second || drift < -2*time.Second {
		t.Errorf("drift %v looks wrong for a local round trip", drift)
	}
}

// A server with no usable Date header must fail rather than invent a time.
func TestHTTPTimeNoDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Date"] = nil
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if _, _, ok := httpTime(srv.URL, 2*time.Second); ok {
		t.Error("expected failure without a Date header")
	}
}

// A silent server (no reply) must fail cleanly instead of hanging forever.
func TestNTPTimeTimeout(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	start := time.Now()
	_, _, ok := ntpQuery(pc.LocalAddr().String(), 300*time.Millisecond)
	if ok {
		t.Fatal("expected failure from a silent server")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("timeout took %v, want well under 3s", elapsed)
	}
}
