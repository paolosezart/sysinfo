package main

import (
	"context"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// systemd 257 dropped utmp entirely (build flag -UTMP, so /run/utmp no longer
// exists). On such systems the login list lives in systemd-logind and is read
// through loginctl. `loginctl list-sessions` gives the roster, and
// `loginctl show-session <id>...` prints each session's properties as
// "Key=Value" lines, one block per session. We use the plain text form rather
// than --json because only the list-* verbs gained JSON output in v257.

// logindSession holds the properties we use from `loginctl show-session`.
type logindSession struct {
	ID         string
	User       string
	TTY        string
	Display    string
	Remote     bool
	RemoteHost string
	Service    string
	Type       string
	Class      string
	State      string
	Desktop    string
	Leader     int
	Timestamp  string
}

// collectLogindSessions returns the login sessions known to systemd-logind.
func collectLogindSessions() []SessionInfo {
	ids := parseLogindSessionIDs(mustLoginctl("--no-legend", "--no-pager", "list-sessions"))
	if len(ids) == 0 {
		return []SessionInfo{}
	}
	args := append([]string{"show-session"}, ids...)
	return parseLogindSessions(mustLoginctl(args...))
}

// parseLogindSessions parses the blank-line separated "Key=Value" blocks of
// `loginctl show-session`. Kept pure so it can be tested without systemd.
func parseLogindSessions(out []byte) []SessionInfo {
	sessions := make([]SessionInfo, 0, 4)
	for _, block := range strings.Split(string(out), "\n\n") {
		s := parseLogindBlock(block)
		if si, ok := logindToSession(s); ok {
			sessions = append(sessions, si)
		}
	}
	sortSessions(sessions)
	return sessions
}

// parseLogindBlock reads one session's "Key=Value" lines.
func parseLogindBlock(block string) logindSession {
	var s logindSession
	for _, line := range strings.Split(block, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "Id":
			s.ID = value
		case "Name":
			s.User = value
		case "TTY":
			s.TTY = value
		case "Display":
			s.Display = value
		case "Remote":
			s.Remote = value == "yes"
		case "RemoteHost":
			s.RemoteHost = value
		case "Service":
			s.Service = value
		case "Type":
			s.Type = value
		case "Class":
			s.Class = value
		case "State":
			s.State = value
		case "Desktop":
			s.Desktop = value
		case "Leader":
			s.Leader, _ = strconv.Atoi(value)
		case "Timestamp":
			s.Timestamp = value
		}
	}
	return s
}

// logindToSession converts a logind record to the report model. Only real user
// logins are kept: the greeter, lock-screen and manager sessions would
// otherwise appear as phantom users. Both "active" (foreground on its seat) and
// "online" (logged in elsewhere, e.g. a second tty) count as logged in, which
// matches what `who` shows.
func logindToSession(s logindSession) (SessionInfo, bool) {
	if s.User == "" || s.Class != "user" {
		return SessionInfo{}, false
	}
	if s.State != "active" && s.State != "online" {
		return SessionInfo{}, false
	}

	si := SessionInfo{
		User:      s.User,
		Line:      s.TTY,
		SessionID: s.ID,
		PID:       s.Leader,
		Type:      s.Type,
	}
	if t, ok := parseLogindTimestamp(s.Timestamp); ok {
		si.LoginTime = t.Format("02.01.2006 15:04:05")
	}
	if s.Remote && s.RemoteHost != "" {
		si.ConnType = "ssh"
		si.Remote = s.RemoteHost
		si.RemoteDNS = reverseDNS(s.RemoteHost)
		si.Detail = s.Service
	} else {
		si.ConnType = "local"
		si.Detail = sessionDesktop(s)
	}
	return si, true
}

// sessionDesktop describes a local session for display: the desktop name for a
// graphical login, otherwise the service that owns the session (e.g. "lightdm",
// "login"). Empty for the bare "unspecified" service so nothing noisy is shown.
func sessionDesktop(s logindSession) string {
	if s.Desktop != "" {
		return s.Desktop
	}
	if s.Service == "systemd" || s.Service == "unspecified" {
		return ""
	}
	return s.Service
}

// parseLogindTimestamp reads the "Timestamp=" value, which loginctl prints in a
// human format (e.g. "Wed 2026-11-11 21:11:00 UTC"). Several layouts are tried
// because the exact one depends on locale and build.
func parseLogindTimestamp(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == "[not set]" {
		return time.Time{}, false
	}
	layouts := []string{
		"Mon 2006-01-02 15:04:05 MST",
		"Mon 2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05",
		time.RFC3339,
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// logindSessionIDs lists the active session identifiers.
func logindSessionIDs() []string {
	return parseLogindSessionIDs(mustLoginctl("--no-legend", "--no-pager", "list-sessions"))
}

// parseLogindSessionIDs extracts the session id from each row of
// `loginctl list-sessions` output. A real row is "<id> <uid> <user> ...", so we
// require the first two fields to be numeric; this skips blank lines and the
// "N sessions listed." legend, making the parser safe even without --no-legend.
func parseLogindSessionIDs(out []byte) []string {
	var ids []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || !isDigits(fields[0]) || !isDigits(fields[1]) {
			continue
		}
		ids = append(ids, fields[0])
	}
	return ids
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// logindAvailable reports whether loginctl can actually be reached (systemd is
// PID 1). In containers it is present but answers with an error.
func logindAvailable() bool {
	out, err := runLoginctl("--no-legend", "--no-pager", "list-sessions")
	if err != nil {
		return false
	}
	return !strings.Contains(string(out), "System has not been booted")
}

// mustLoginctl runs loginctl, returning nil on any error (missing binary,
// timeout, not booted with systemd). Callers treat nil as "no data".
func mustLoginctl(args ...string) []byte {
	out, err := runLoginctl(args...)
	if err != nil {
		return nil
	}
	return out
}

func runLoginctl(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "loginctl", args...).Output()
}

// sortSessions orders sessions by login time, then by user, matching the utmp
// path so both sources render identically.
func sortSessions(s []SessionInfo) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].LoginTime != s[j].LoginTime {
			return s[i].LoginTime < s[j].LoginTime
		}
		return s[i].User < s[j].User
	})
}
