package main

import (
	"strings"
	"testing"
)

// logindShowOutput mirrors `loginctl show-session 1 2 3 4` on a Raspberry Pi
// running Debian 13 / systemd 257, where /run/utmp no longer exists. Blocks are
// separated by a blank line. Values are the ones the user's `who`/`w` reported.
const logindShowOutput = `Id=1
User=1000
Name=raspi
Timestamp=Wed 2026-11-11 20:06:12 MSK
TimestampMonotonic=12345678
VTNr=1
Seat=seat0
TTY=tty1
Display=:0
Remote=no
RemoteHost=
RemoteUser=
Service=lightdm
Desktop=LXDE-pi
Scope=session-1.scope
Leader=1234
Audit=1
Type=x11
Class=user
Active=yes
State=active
IdleHint=no
IdleSinceHint=0
IdleSinceHintMonotonic=0
LockedHint=no

Id=2
User=1000
Name=raspi
Timestamp=Wed 2026-11-11 20:11:03 MSK
TimestampMonotonic=12645678
VTNr=0
Seat=
TTY=pts/0
Display=
Remote=yes
RemoteHost=192.168.100.234
RemoteUser=
Service=sshd
Desktop=
Scope=session-2.scope
Leader=2345
Audit=2
Type=tty
Class=user
Active=yes
State=active
IdleHint=no
IdleSinceHint=0
IdleSinceHintMonotonic=0
LockedHint=no

Id=3
User=1000
Name=raspi
Timestamp=Wed 2026-11-11 20:11:05 MSK
TimestampMonotonic=12646678
VTNr=0
Seat=
TTY=pts/1
Display=
Remote=yes
RemoteHost=192.168.100.234
RemoteUser=
Service=sshd
Desktop=
Scope=session-3.scope
Leader=2346
Audit=3
Type=tty
Class=user
Active=no
State=online
IdleHint=no
IdleSinceHint=0
IdleSinceHintMonotonic=0
LockedHint=no

Id=4
User=1000
Name=raspi
Timestamp=Wed 2026-11-11 21:11:00 MSK
TimestampMonotonic=16246678
VTNr=0
Seat=
TTY=pts/2
Display=
Remote=yes
RemoteHost=192.168.100.234
RemoteUser=
Service=sshd
Desktop=
Scope=session-4.scope
Leader=2347
Audit=4
Type=tty
Class=user
Active=yes
State=active
IdleHint=no
IdleSinceHint=0
IdleSinceHintMonotonic=0
LockedHint=no`

const logindListOutput = `  1 1000 raspi   seat0  1234 lightdm tty1
  2 1000 raspi          2345 sshd    pts/0
  3 1000 raspi          2346 sshd    pts/1
  4 1000 raspi          2347 sshd    pts/2
4 sessions listed.
`

func TestParseLogindSessionIDs(t *testing.T) {
	ids := parseLogindSessionIDs([]byte(logindListOutput))
	want := []string{"1", "2", "3", "4"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], want[i])
		}
	}
}

func TestParseLogindSessions(t *testing.T) {
	got := parseLogindSessions([]byte(logindShowOutput))
	if len(got) != 4 {
		t.Fatalf("got %d sessions, want 4: %+v", len(got), got)
	}

	// Sorted by login time: tty1 (20:06), pts/0 (20:11), pts/1 (20:11), pts/2 (21:11).
	byLine := map[string]SessionInfo{}
	for _, s := range got {
		byLine[s.Line] = s
	}

	tty1 := byLine["tty1"]
	if tty1.ConnType != "local" {
		t.Errorf("tty1 ConnType = %q, want local", tty1.ConnType)
	}
	if tty1.User != "raspi" || tty1.SessionID != "1" || tty1.PID != 1234 {
		t.Errorf("tty1 = %+v", tty1)
	}
	if tty1.LoginTime != "11.11.2026 20:06:12" {
		t.Errorf("tty1 LoginTime = %q, want 11.11.2026 20:06:12", tty1.LoginTime)
	}

	pts0 := byLine["pts/0"]
	if pts0.ConnType != "ssh" {
		t.Errorf("pts/0 ConnType = %q, want ssh", pts0.ConnType)
	}
	if pts0.Remote != "192.168.100.234" {
		t.Errorf("pts/0 Remote = %q, want 192.168.100.234", pts0.Remote)
	}

	// A session in state "online" is still a logged-in user and must be listed.
	pts1 := byLine["pts/1"]
	if pts1.User != "raspi" || pts1.SessionID != "3" {
		t.Errorf("online session pts/1 = %+v", pts1)
	}
}

// The manager and greeter sessions are not user logins and must be filtered out.
func TestLogindFiltersNonUserSessions(t *testing.T) {
	cases := []logindSession{
		{ID: "c1", User: "root", Class: "manager", State: "active", TTY: ""},
		{ID: "c2", User: "lightdm", Class: "greeter", State: "active", TTY: "tty7"},
		{ID: "c3", User: "raspi", Class: "user", State: "closing"},
		{ID: "c4", User: "", Class: "user", State: "active"},
	}
	for _, c := range cases {
		if _, ok := logindToSession(c); ok {
			t.Errorf("logindToSession(%+v) accepted, want rejected", c)
		}
	}

	// A background session (e.g. lingering user services) is a real login.
	if _, ok := logindToSession(logindSession{ID: "b1", User: "raspi", Class: "user", State: "active", TTY: "pts/9"}); !ok {
		t.Error("active user session was rejected")
	}
}

func TestParseLogindTimestamp(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"Wed 2026-11-11 21:11:00 MSK", "11.11.2026 21:11:00", true},
		{"Wed 2026-11-11 21:11:00 UTC", "11.11.2026 21:11:00", true},
		{"2026-11-11 21:11:00 MSK", "11.11.2026 21:11:00", true},
		{"", "", false},
		{"[not set]", "", false},
	}
	for _, c := range cases {
		got, ok := parseLogindTimestamp(c.in)
		if ok != c.ok {
			t.Errorf("parseLogindTimestamp(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && got.Format("02.01.2006 15:04:05") != c.want {
			t.Errorf("parseLogindTimestamp(%q) = %q, want %q", c.in, got.Format("02.01.2006 15:04:05"), c.want)
		}
	}
}

// A partially written or empty output must not panic or invent sessions.
func TestParseLogindSessionsDegenerate(t *testing.T) {
	if got := parseLogindSessions(nil); len(got) != 0 {
		t.Errorf("empty input gave %d sessions", len(got))
	}
	if got := parseLogindSessions([]byte("garbage\nno equals here\n\n\n")); len(got) != 0 {
		t.Errorf("garbage input gave %d sessions", len(got))
	}
}

// versionLine must never be empty and must carry the injected version.
func TestVersionLine(t *testing.T) {
	old := version
	version = "9.9.9-test"
	defer func() { version = old }()

	line := versionLine()
	for _, want := range []string{"sysinfo", "9.9.9-test", "linux", "go"} {
		if !strings.Contains(line, want) {
			t.Errorf("versionLine() = %q, missing %q", line, want)
		}
	}
}
