package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"runtime"
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

// utmp record layout (Linux, glibc /usr/include/bits/utmp.h).
//
// glibc keeps the ut_session/ut_tv fields 32-bit only on targets where
// __WORDSIZE_TIME64_COMPAT32 is 1, so that 32- and 64-bit processes can share
// the file. On the remaining 64-bit targets (aarch64, s390x, loong64) ut_session
// is a 64-bit long and ut_tv a 16-byte struct timeval, which makes the record
// 400 bytes instead of 384. Everything up to and including ut_host has the same
// offset either way.
const (
	utLineOffset = 8
	utLineLen    = 32
	utUserOffset = 44
	utUserLen    = 32
	utHostOffset = 76
	utHostLen    = 256
)

const (
	utEmpty       = 0
	utUserProcess = 7
)

// utmpLayout describes the trailing (architecture-dependent) part of a record.
type utmpLayout struct {
	size       int
	sessionOff int
	tvSecOff   int
	wide       bool // ut_session is a 64-bit long and ut_tv is a struct timeval
}

func utmpLayoutFor(goarch string) utmpLayout {
	switch goarch {
	case "arm64", "s390x", "loong64":
		return utmpLayout{size: 400, sessionOff: 344, tvSecOff: 352, wide: true}
	default:
		return utmpLayout{size: 384, sessionOff: 336, tvSecOff: 340}
	}
}

var utmpRec = utmpLayoutFor(runtime.GOARCH)

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
// address is a remote (SSH) login, everything else is local. sftp and scp are
// not separate: both are SSH subsystems that run without a pty, so sshd records
// no utmp entry for them and they never appear here. Only the interactive SSH
// login (and its own PTY) is listed, which is why sftp/scp cannot be told apart
// from a plain SSH session by this data.
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
	return readUtmpWith(path, utmpRec)
}

// readUtmpWith parses utmp records using an explicit layout, so the parser can
// be exercised for every architecture regardless of the host it runs on.
func readUtmpWith(path string, lay utmpLayout) ([]utmpRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []utmpRecord
	for off := 0; off+lay.size <= len(b); off += lay.size {
		rec := b[off : off+lay.size]
		t := int16(binary.LittleEndian.Uint16(rec[0:]))
		if t == utEmpty {
			continue
		}
		r := utmpRecord{
			typ:  t,
			pid:  int32(binary.LittleEndian.Uint32(rec[4:])),
			line: cstr(rec[utLineOffset : utLineOffset+utLineLen]),
			user: cstr(rec[utUserOffset : utUserOffset+utUserLen]),
			host: cstr(rec[utHostOffset : utHostOffset+utHostLen]),
		}
		if sec := utmpSec(rec, lay); sec > 0 {
			r.loginTime = time.Unix(sec, 0)
		}
		if lay.wide {
			r.session = int64(binary.LittleEndian.Uint64(rec[lay.sessionOff:]))
		} else {
			r.session = int64(binary.LittleEndian.Uint32(rec[lay.sessionOff:]))
		}
		out = append(out, r)
	}
	return out, nil
}

// utmpSec reads the login timestamp. The wide layout stores tv_sec as a signed
// 64-bit value in a struct timeval; the compact one as a signed 32-bit value.
func utmpSec(rec []byte, lay utmpLayout) int64 {
	if lay.wide {
		return int64(binary.LittleEndian.Uint64(rec[lay.tvSecOff:]))
	}
	return int64(int32(binary.LittleEndian.Uint32(rec[lay.tvSecOff:])))
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
