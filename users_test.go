package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
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
// (aarch64, s390x, loong64).
var (
	compactLayout = utmpLayout{size: 384, sessionOff: 336, tvSecOff: 340}
	wideLayout    = utmpLayout{size: 400, sessionOff: 344, tvSecOff: 352, wide: true}
)

func TestUtmpLayoutFor(t *testing.T) {
	cases := []struct {
		arch string
		want int
	}{
		{"amd64", 384},
		{"arm", 384},
		{"386", 384},
		{"riscv64", 384},
		{"ppc64le", 384},
		{"arm64", 400},
		{"s390x", 400},
		{"loong64", 400},
	}
	for _, c := range cases {
		if got := utmpLayoutFor(c.arch).size; got != c.want {
			t.Errorf("utmpLayoutFor(%q).size = %d, want %d", c.arch, got, c.want)
		}
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
	remote, client := classifySession(utmpRecord{line: "pts/0", host: "203.0.113.7", pid: 0})
	if remote != "203.0.113.7" {
		t.Errorf("remote = %q, want 203.0.113.7", remote)
	}
	if client != "" {
		t.Errorf("client = %q, want empty (no environ)", client)
	}

	// A local X session has no remote host.
	remote, _ = classifySession(utmpRecord{line: ":0", host: ":0"})
	if remote != "" {
		t.Errorf("local remote = %q, want empty", remote)
	}
}

func TestSSHClientFromEnviron(t *testing.T) {
	env := []byte("PATH=/usr/bin\x00SSH_CONNECTION=10.0.0.9 50123 10.0.0.1 22\x00SSH_CLIENT_VERSION=OpenSSH_9.6p1\x00")
	if got := sshClientFromEnviron(env); got != "OpenSSH_9.6p1" {
		t.Errorf("client = %q, want OpenSSH_9.6p1", got)
	}
	if got := sshClientFromEnviron([]byte("PATH=/usr/bin\x00")); got != "" {
		t.Errorf("client = %q, want empty", got)
	}
}

func TestSSHHostFromClient(t *testing.T) {
	if got := sshHostFromClient("SSH-2.0-OpenSSH_9.6p1 198.51.100.4"); got != "198.51.100.4" {
		t.Errorf("host = %q, want 198.51.100.4", got)
	}
	if got := sshHostFromClient("OpenSSH_9.6p1"); got != "" {
		t.Errorf("host = %q, want empty", got)
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
