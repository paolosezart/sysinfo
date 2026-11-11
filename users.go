package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SessionInfo describes a single logged-in user session.
type SessionInfo struct {
	User      string `json:"user"`
	Line      string `json:"line"`             // tty / pts device
	ConnType  string `json:"connection"`       // local / ssh / sftp / scp
	Remote    string `json:"remote,omitempty"` // client address for remote sessions
	RemoteDNS string `json:"remote_dns,omitempty"`
	LoginTime string `json:"login_time"` // preformatted, locale independent
	Client    string `json:"client,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	PID       int    `json:"pid,omitempty"`
}

// utmp record layout (Linux, /usr/include/bits/utmp.h): 384 bytes. The trailing
// padding is dropped because every field we read precedes it.
const (
	utmpRecordSize  = 384
	utLineOffset    = 8
	utLineLen       = 32
	utUserOffset    = 44
	utUserLen       = 32
	utHostOffset    = 76
	utHostLen       = 256
	utSessionOffset = 336
	utTVSecOffset   = 340
)

const (
	utEmpty       = 0
	utUserProcess = 7
)

type utmpRecord struct {
	typ       int16
	pid       int32
	line      string
	user      string
	host      string
	session   int64
	loginTime time.Time
}

// collectSessions reads the login records (utmp) and enriches each with the
// connection type, client address and, where the kernel allows it, the SSH
// client identification.
func collectSessions() []SessionInfo {
	return sessionsFromPath(utmpFile())
}

// sessionsFromPath builds the session list from a utmp file, sorted by login
// time. Split out from collectSessions so it can be tested against a fixture.
func sessionsFromPath(path string) []SessionInfo {
	records, err := readUtmp(path)
	if err != nil {
		return []SessionInfo{}
	}

	out := make([]SessionInfo, 0, len(records))
	for _, r := range records {
		if r.typ != utUserProcess || r.user == "" {
			continue
		}
		s := SessionInfo{
			User:      r.user,
			Line:      r.line,
			SessionID: strconv.FormatInt(r.session, 10),
			PID:       int(r.pid),
		}
		if !r.loginTime.IsZero() {
			s.LoginTime = r.loginTime.Format("02.01.2006 15:04:05")
		}
		s.ConnType = sessionConnType(r)
		s.Remote, s.Client = classifySession(r)
		if s.Remote != "" {
			s.RemoteDNS = reverseDNS(s.Remote)
		}
		out = append(out, s)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].LoginTime != out[j].LoginTime {
			return out[i].LoginTime < out[j].LoginTime
		}
		return out[i].User < out[j].User
	})
	return out
}

// classifySession returns the client address and, for SSH sessions, the client
// identification string. The client version is not part of utmp; sshd keeps it
// in the session's environment (SSH_CLIENT_VERSION), readable only when we run
// with the same privileges as the login process.
func classifySession(r utmpRecord) (remote, client string) {
	client = sshClientFromEnv(r.pid)

	// The utmp host field carries the origin for both local (:0) and remote
	// logins; prefer the explicit SSH_CONNECTION peer when present.
	host := r.host
	if h := sshHostFromEnv(r.pid); h != "" {
		host = h
	}
	if host == "" {
		host = sshHostFromClient(client)
	}
	if host == ":0" || host == "localhost" {
		host = ""
	}
	return host, client
}

// sessionConnType maps a utmp record to a connection type. The kernel keeps no
// protocol tag, so the classification is heuristic: a pts device with a client
// address is a remote (SSH) login, everything else is local. sftp and scp ride
// the same SSH transport and cannot be told apart from utmp alone.
func sessionConnType(r utmpRecord) string {
	if strings.HasPrefix(r.line, "pts/") || (r.host != "" && r.host != ":0") {
		return "ssh"
	}
	return "local"
}

// sshClientFromEnv reads the SSH client identification (e.g. "OpenSSH_9.2p1
// Debian-3") that sshd stores in the login session's environment. Best effort:
// only readable with root or matching ownership.
func sshClientFromEnv(pid int32) string {
	b := procEnviron(pid)
	if b == nil {
		return ""
	}
	return sshClientFromEnviron(b)
}

func sshClientFromEnviron(environ []byte) string {
	for _, kv := range bytes.Split(environ, []byte{0}) {
		if v, ok := strings.CutPrefix(string(kv), "SSH_CLIENT_VERSION="); ok {
			return v
		}
	}
	return ""
}

// sshHostFromClient extracts an address embedded in an "SSH-2.0-<software>"
// client string, if one is present.
func sshHostFromClient(client string) string {
	if !strings.HasPrefix(client, "SSH-") {
		return ""
	}
	f := strings.Fields(client)
	if len(f) >= 2 {
		return f[len(f)-1]
	}
	return ""
}

// sshHostFromEnv resolves the peer address from SSH_CONNECTION, which sshd
// exports to the session ("client_ip client_port server_ip server_port").
func sshHostFromEnv(pid int32) string {
	b := procEnviron(pid)
	if b == nil {
		return ""
	}
	for _, kv := range bytes.Split(b, []byte{0}) {
		v, ok := strings.CutPrefix(string(kv), "SSH_CONNECTION=")
		if !ok {
			continue
		}
		if f := strings.Fields(v); len(f) > 0 {
			return f[0]
		}
	}
	return ""
}

func procEnviron(pid int32) []byte {
	if pid <= 0 {
		return nil
	}
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(int(pid)), "environ"))
	if err != nil {
		return nil
	}
	return b
}

// utmpFile returns the login record file, honouring the common paths.
func utmpFile() string {
	for _, p := range []string{"/run/utmp", "/var/run/utmp"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/run/utmp"
}

// readUtmp parses the fixed-width utmp records. Records of every type are
// returned; callers filter. A trailing partial record is ignored.
func readUtmp(path string) ([]utmpRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []utmpRecord
	for off := 0; off+utmpRecordSize <= len(b); off += utmpRecordSize {
		rec := b[off : off+utmpRecordSize]
		t := int16(binary.LittleEndian.Uint16(rec[0:]))
		if t == utEmpty {
			continue
		}
		r := utmpRecord{
			typ:     t,
			pid:     int32(binary.LittleEndian.Uint32(rec[4:])),
			line:    cstr(rec[utLineOffset : utLineOffset+utLineLen]),
			user:    cstr(rec[utUserOffset : utUserOffset+utUserLen]),
			host:    cstr(rec[utHostOffset : utHostOffset+utHostLen]),
			session: int64(binary.LittleEndian.Uint32(rec[utSessionOffset:])),
		}
		if sec := int64(int32(binary.LittleEndian.Uint32(rec[utTVSecOffset:]))); sec > 0 {
			r.loginTime = time.Unix(sec, 0)
		}
		out = append(out, r)
	}
	return out, nil
}

// reverseDNS resolves an address to a host name, bounded by cmdTimeout so a
// slow resolver cannot stall the report.
func reverseDNS(addr string) string {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, addr)
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
}

// cstr trims a fixed-width NUL-terminated C string.
func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}
