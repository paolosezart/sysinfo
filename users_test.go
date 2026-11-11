package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// buildUtmpRecord lays out one Linux utmp record with the fields the parser
// reads, using the given layout. It mirrors /usr/include/bits/utmp.h.
func buildUtmpRecord(lay utmpLayout, typ int16, pid int32, line, user, host string, session uint32, tvSec uint32) []byte {
	rec := make([]byte, lay.size)
	binary.LittleEndian.PutUint16(rec[0:], uint16(typ))
	binary.LittleEndian.PutUint32(rec[4:], uint32(pid))
	copy(rec[utLineOffset:utLineOffset+utLineLen], line)
	copy(rec[utUserOffset:utUserOffset+utUserLen], user)
	copy(rec[utHostOffset:utHostOffset+utHostLen], host)
	if lay.wide {
		binary.LittleEndian.PutUint64(rec[lay.sessionOff:], uint64(session))
		binary.LittleEndian.PutUint64(rec[lay.tvSecOff:], uint64(tvSec))
	} else {
		binary.LittleEndian.PutUint32(rec[lay.sessionOff:], session)
		binary.LittleEndian.PutUint32(rec[lay.tvSecOff:], tvSec)
	}
	return rec
}

// The two layouts glibc produces: compact (amd64, armv7, riscv64, …) and wide
// (aarch64, s390x, loong64). Offsets mirror bits/utmp.h.
var (
	compactLayout = utmpLayout{size: 384, sessionOff: 336, tvSecOff: 340}
	wideLayout    = utmpLayout{size: 400, sessionOff: 336, tvSecOff: 344, wide: true}
)

func TestUtmpLayoutFor(t *testing.T) {
	cases := []struct {
		arch string
		want utmpLayout
	}{
		{"amd64", compactLayout},
		{"arm", compactLayout},
		{"386", compactLayout},
		{"riscv64", compactLayout},
		{"ppc64le", compactLayout},
		{"arm64", wideLayout},
		{"s390x", wideLayout},
		{"loong64", wideLayout},
	}
	for _, c := range cases {
		if got := utmpLayoutFor(c.arch); got != c.want {
			t.Errorf("utmpLayoutFor(%q) = %+v, want %+v", c.arch, got, c.want)
		}
	}
}

// The exact byte offsets matter: ut_session/ut_tv sit at 336/340 in the compact
// layout but ut_tv moves to 344 in the wide one. Pin them so a future edit
// cannot quietly reintroduce the "no sessions" bug.
func TestUtmpOffsets(t *testing.T) {
	if utmpCompact.sessionOff != 336 || utmpCompact.tvSecOff != 340 || utmpCompact.size != 384 {
		t.Errorf("compact layout = %+v", utmpCompact)
	}
	if utmpWide.sessionOff != 336 || utmpWide.tvSecOff != 344 || utmpWide.size != 400 {
		t.Errorf("wide layout = %+v", utmpWide)
	}
	if utmpRec != utmpLayoutFor(runtime.GOARCH) {
		t.Errorf("utmpRec = %+v, want %+v", utmpRec, utmpLayoutFor(runtime.GOARCH))
	}
}

// A wide (aarch64) file must be read even when the running binary picked the
// compact layout, and vice versa: the parser falls back to the other stride
// when the primary one yields no usable login records.
func TestReadUtmpAutoDetectsLayout(t *testing.T) {
	for _, lay := range []utmpLayout{compactLayout, wideLayout} {
		buf := buildUtmpRecord(lay, utUserProcess, 1842, "pts/0", "raspi", "192.168.100.42", 3, 1700000200)
		path := filepath.Join(t.TempDir(), "utmp")
		if err := os.WriteFile(path, buf, 0o644); err != nil {
			t.Fatal(err)
		}
		sessions := sessionsFromPath(path)
		if len(sessions) != 1 || sessions[0].User != "raspi" {
			t.Errorf("layout %d: sessionsFromPath = %+v, want 1 raspi session", lay.size, sessions)
		}
	}
}

// The fields up to ut_host are identical in both layouts, so a wrong stride can
// still yield a plausible-looking first record; the trailing-bytes check is what
// distinguishes them. A wide (aarch64) image must therefore score higher under
// the wide layout and be parsed with the right session id and timestamp.
func TestLayoutScorePicksDivisibleLayout(t *testing.T) {
	buf := buildUtmpRecord(wideLayout, utUserProcess, 1842, "pts/0", "raspi", "192.168.100.42", 3, 1700000200)
	if layoutScore(buf, wideLayout) <= layoutScore(buf, compactLayout) {
		t.Fatalf("wide score %d <= compact score %d", layoutScore(buf, wideLayout), layoutScore(buf, compactLayout))
	}

	path := filepath.Join(t.TempDir(), "utmp")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	sessions := sessionsFromPath(path)
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].SessionID != "3" {
		t.Errorf("SessionID = %q, want 3 (wrong stride would misread it)", sessions[0].SessionID)
	}
	want := time.Unix(1700000200, 0).Format("02.01.2006 15:04:05")
	if sessions[0].LoginTime != want {
		t.Errorf("LoginTime = %q, want %q", sessions[0].LoginTime, want)
	}
}

func TestReadUtmp(t *testing.T) {
	for _, lay := range []utmpLayout{compactLayout, wideLayout} {
		var buf []byte
		// A BOOT_TIME record must be ignored by the session filter.
		buf = append(buf, buildUtmpRecord(lay, 2, 0, "", "", "", 0, 1700000000)...)
		buf = append(buf, buildUtmpRecord(lay, utUserProcess, 812, "tty1", "raspi", "", 1, 1700000100)...)
		buf = append(buf, buildUtmpRecord(lay, utUserProcess, 1842, "pts/0", "raspi", "192.168.100.42", 3, 1700000200)...)
		// A trailing partial record must not panic or be parsed.
		buf = append(buf, []byte{0x07, 0x00}...)

		path := filepath.Join(t.TempDir(), "utmp")
		if err := os.WriteFile(path, buf, 0o644); err != nil {
			t.Fatal(err)
		}

		recs, err := readUtmpWith(path, lay)
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) != 3 {
			t.Fatalf("layout %d: got %d records, want 3", lay.size, len(recs))
		}
		if recs[1].user != "raspi" || recs[1].line != "tty1" || recs[1].pid != 812 {
			t.Errorf("layout %d: record 1 = %+v", lay.size, recs[1])
		}
		if recs[2].host != "192.168.100.42" || recs[2].session != 3 {
			t.Errorf("layout %d: record 2 = %+v", lay.size, recs[2])
		}
		if got := recs[1].loginTime.Unix(); got != 1700000100 {
			t.Errorf("layout %d: loginTime = %d, want 1700000100", lay.size, got)
		}
	}
}

func TestSessionsFromPath(t *testing.T) {
	var buf []byte
	buf = append(buf, buildUtmpRecord(compactLayout, 2, 0, "", "", "", 0, 1700000000)...)
	buf = append(buf, buildUtmpRecord(compactLayout, utUserProcess, 812, "tty1", "raspi", "", 1, 1700000100)...)
	buf = append(buf, buildUtmpRecord(compactLayout, utUserProcess, 1842, "pts/0", "alice", "203.0.113.9", 3, 1700000200)...)

	path := filepath.Join(t.TempDir(), "utmp")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}

	sessions := sessionsFromPath(path)
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}
	// Sorted by login time: the local tty precedes the SSH session.
	if sessions[0].User != "raspi" || sessions[0].ConnType != "local" || sessions[0].Line != "tty1" {
		t.Errorf("session 0 = %+v", sessions[0])
	}
	if sessions[1].User != "alice" || sessions[1].ConnType != "ssh" || sessions[1].Remote != "203.0.113.9" {
		t.Errorf("session 1 = %+v", sessions[1])
	}
	want := time.Unix(1700000100, 0).Format("02.01.2006 15:04:05")
	if sessions[0].LoginTime != want {
		t.Errorf("login time = %q, want %q", sessions[0].LoginTime, want)
	}
}

func TestSessionConnType(t *testing.T) {
	cases := []struct {
		rec  utmpRecord
		want string
	}{
		{utmpRecord{typ: utUserProcess, line: "tty1"}, "local"},
		{utmpRecord{typ: utUserProcess, line: "pts/0", host: "10.0.0.5"}, "ssh"},
		{utmpRecord{typ: utUserProcess, line: "pts/1"}, "ssh"},
		{utmpRecord{typ: utUserProcess, line: ":0", host: ":0"}, "local"},
	}
	for _, c := range cases {
		if got := sessionConnType(c.rec); got != c.want {
			t.Errorf("sessionConnType(%+v) = %q, want %q", c.rec, got, c.want)
		}
	}
}

func TestClassifySession(t *testing.T) {
	// A pts login with a host in utmp yields that host as the client address.
	remote := classifySession(utmpRecord{line: "pts/0", host: "203.0.113.7", pid: 0})
	if remote != "203.0.113.7" {
		t.Errorf("remote = %q, want 203.0.113.7", remote)
	}

	// A local X session has no remote host.
	if remote := classifySession(utmpRecord{line: ":0", host: ":0"}); remote != "" {
		t.Errorf("local remote = %q, want empty", remote)
	}
}

// SSH_CONNECTION is the only peer address sshd exports; the client version is
// not available anywhere, so nothing may be invented for it.
func TestSSHPeerFromEnviron(t *testing.T) {
	env := []byte("PATH=/usr/bin\x00SSH_CONNECTION=10.0.0.9 50123 10.0.0.1 22\x00SSH_TTY=/dev/pts/0\x00")
	if got := sshPeerFromEnviron(env); got != "10.0.0.9" {
		t.Errorf("peer = %q, want 10.0.0.9", got)
	}
	if got := sshPeerFromEnviron([]byte("PATH=/usr/bin\x00")); got != "" {
		t.Errorf("peer = %q, want empty", got)
	}
	if got := sshPeerFromEnviron(nil); got != "" {
		t.Errorf("peer(nil) = %q, want empty", got)
	}
}

// idleSince turns an access time into the human idle string, and refuses to
// report a negative gap when the clock is skewed.
func TestIdleSince(t *testing.T) {
	now := time.Date(2026, 11, 12, 4, 24, 38, 0, time.Local)
	cases := []struct {
		name  string
		atime time.Time
		want  string
	}{
		{"two seconds", now.Add(-2 * time.Second), "2 с"},
		{"eight hours", now.Add(-8*time.Hour - 13*time.Minute), "8 ч 13 мин 0 с"},
		{"future atime", now.Add(5 * time.Minute), ""},
		{"zero atime", time.Time{}, ""},
	}
	for _, c := range cases {
		if got := idleSince(c.atime, now); got != c.want {
			t.Errorf("%s: idleSince = %q, want %q", c.name, got, c.want)
		}
	}
}

// ttyDevicePath maps utmp/logind line names onto device nodes.
func TestTtyDevicePath(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"tty1", "/dev/tty1", true},
		{"pts/0", "/dev/pts/0", true},
		{"/dev/pts/3", "/dev/pts/3", true},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := ttyDevicePath(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ttyDevicePath(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// A remote session with no tty is a non-interactive SSH connection and must be
// labelled distinctly from an interactive one.
func TestSessionLabel(t *testing.T) {
	cases := []struct {
		s    SessionInfo
		want string
	}{
		{SessionInfo{ConnType: "ssh", Line: "pts/0"}, "SSH"},
		{SessionInfo{ConnType: "ssh", Line: ""}, L("SSH без терминала", "SSH, no terminal")},
		{SessionInfo{ConnType: "local", Type: "x11"}, L("рабочий стол", "desktop")},
		{SessionInfo{ConnType: "local", Type: "wayland"}, L("рабочий стол", "desktop")},
		{SessionInfo{ConnType: "local", Type: "tty"}, L("локальный терминал", "local terminal")},
	}
	for _, c := range cases {
		if got := sessionLabel(c.s); got != c.want {
			t.Errorf("sessionLabel(%+v) = %q, want %q", c.s, got, c.want)
		}
	}
}

// mergeSSHClientVersions pairs a logged client version with the peer address.
// The version line has no address, so the shared sshd PID prefix is what links
// it to the "Connection from <ip>" line.
func TestMergeSSHClientVersions(t *testing.T) {
	log := strings.Join([]string{
		`ноя 12 04:24:20 host sshd-session[646547]: Connection from 192.168.100.234 port 9807 on 192.168.100.10 port 22`,
		`ноя 12 04:24:20 host sshd-session[646547]: debug1: Remote protocol version 2.0, remote software version OpenSSH_9.6p1 Ubuntu-3ubuntu13`,
		`ноя 12 04:24:24 host sshd-session[646623]: Connection from 10.8.0.5 port 51000 on 192.168.100.10 port 22`,
		`ноя 12 04:24:24 host sshd-session[646623]: debug1: Remote protocol version 2.0, remote software version PuTTY_Release_0.80`,
		`ноя 12 04:24:30 host sshd-session[646700]: debug1: Remote protocol version 2.0, remote software version NoAddressLine`,
	}, "\n")

	got := map[string]string{}
	mergeSSHClientVersions(got, log)

	if got["192.168.100.234"] != "OpenSSH_9.6p1 Ubuntu-3ubuntu13" {
		t.Errorf("192.168.100.234 = %q", got["192.168.100.234"])
	}
	if got["10.8.0.5"] != "PuTTY_Release_0.80" {
		t.Errorf("10.8.0.5 = %q", got["10.8.0.5"])
	}
	if len(got) != 2 {
		t.Errorf("got %d entries, want 2: %+v", len(got), got)
	}
}

// applyClientVersions must only touch SSH sessions that have a peer address.
func TestApplyClientVersions(t *testing.T) {
	sessions := []SessionInfo{
		{User: "raspi", ConnType: "ssh", Remote: "192.168.100.234"},
		{User: "raspi", ConnType: "local", Type: "x11", Remote: "192.168.100.234"},
		{User: "admin", ConnType: "ssh", Remote: "10.0.0.9"},
	}
	applyClientVersions(sessions, map[string]string{"192.168.100.234": "OpenSSH_9.6p1"})

	if sessions[0].Client != "OpenSSH_9.6p1" {
		t.Errorf("ssh session Client = %q, want OpenSSH_9.6p1", sessions[0].Client)
	}
	if sessions[1].Client != "" {
		t.Errorf("local session got a client version: %q", sessions[1].Client)
	}
	if sessions[2].Client != "" {
		t.Errorf("unknown address got a client version: %q", sessions[2].Client)
	}
}

// connectionFromIP and remoteSoftwareVersion read the two halves of a session's
// log pair.
func TestSSHLogMarkers(t *testing.T) {
	conn := `ноя 12 04:24:20 host sshd-session[1]: Connection from 192.168.100.234 port 9807 on 192.168.100.10 port 22`
	if got := connectionFromIP(conn); got != "192.168.100.234" {
		t.Errorf("connectionFromIP = %q", got)
	}
	ver := `ноя 12 04:24:20 host sshd-session[1]: debug1: Remote protocol version 2.0, remote software version OpenSSH_9.6p1`
	if got := remoteSoftwareVersion(ver); got != "OpenSSH_9.6p1" {
		t.Errorf("remoteSoftwareVersion = %q", got)
	}
	if got := connectionFromIP("no marker here"); got != "" {
		t.Errorf("connectionFromIP(no marker) = %q", got)
	}
	if got := remoteSoftwareVersion("no marker here"); got != "" {
		t.Errorf("remoteSoftwareVersion(no marker) = %q", got)
	}
}

// syslogPID reads the bracketed PID from a log line prefix.
func TestSyslogPID(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"ноя 12 04:24:20 host sshd-session[646547]: hello", 646547},
		{"2026-11-12T04:24:20 host sshd[123]: hi", 123},
		{"no pid here", 0},
		{"unclosed[42", 0},
		{"nonnumeric[x]:", 0},
	}
	for _, c := range cases {
		if got := syslogPID(c.in); got != c.want {
			t.Errorf("syslogPID(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// sshLogSince returns the earliest SSH login time, and "" when there is no SSH
// session to look up.
func TestSSHLogSince(t *testing.T) {
	sessions := []SessionInfo{
		{ConnType: "local", Type: "x11", LoginTime: "11.11.2026 20:06:11"},
		{ConnType: "ssh", LoginTime: "11.11.2026 20:11:22", PID: 1},
		{ConnType: "ssh", LoginTime: "11.11.2026 19:05:00", PID: 2},
	}
	got := sshLogSince(sessions)
	want := time.Date(2026, 11, 11, 19, 5, 0, 0, time.Local).Format("2006-01-02 15:04:05")
	if got != want {
		t.Errorf("sshLogSince = %q, want %q", got, want)
	}
	if got := sshLogSince([]SessionInfo{{ConnType: "local"}}); got != "" {
		t.Errorf("sshLogSince with no SSH sessions = %q, want empty", got)
	}
}

func TestCstr(t *testing.T) {
	b := make([]byte, 8)
	copy(b, "abc")
	b[3] = 0
	copy(b[4:], "xy")
	if got := cstr(b); got != "abc" {
		t.Errorf("cstr = %q, want abc", got)
	}
}
