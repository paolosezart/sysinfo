package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
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
	Line      string `json:"line,omitempty"`   // tty / pts device
	ConnType  string `json:"connection"`       // local / ssh
	Type      string `json:"type,omitempty"`   // tty / x11 / wayland / unspecified
	Detail    string `json:"detail,omitempty"` // desktop name or session service
	Remote    string `json:"remote,omitempty"` // client address for remote sessions
	RemoteDNS string `json:"remote_dns,omitempty"`
	LoginTime string `json:"login_time"` // preformatted, locale independent
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
// Offsets are identical to the struct above except for ut_session/ut_tv:
//   - compact: ut_session is int32_t and ut_tv is {uint32 tv_sec; int32 tv_usec}
//   - wide:    ut_session is a 64-bit long and ut_tv is a struct timeval
type utmpLayout struct {
	size       int
	sessionOff int
	tvSecOff   int
	wide       bool
}

// Both layouts were verified against the glibc struct definition (bits/utmp.h)
// with a C offsetof() probe: compact is the x86_64 native layout, wide mirrors
// the aarch64/s390x/loong64 one (long and struct timeval share size/alignment
// on x86_64 and aarch64, so compiling the wide struct on x86_64 reproduces it).
var (
	utmpCompact = utmpLayout{size: 384, sessionOff: 336, tvSecOff: 340}
	utmpWide    = utmpLayout{size: 400, sessionOff: 336, tvSecOff: 344, wide: true}
)

func utmpLayoutFor(goarch string) utmpLayout {
	switch goarch {
	case "arm64", "s390x", "loong64":
		return utmpWide
	default:
		return utmpCompact
	}
}

func (l utmpLayout) alternate() utmpLayout {
	if l.wide {
		return utmpCompact
	}
	return utmpWide
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
	// systemd 257 removed utmp (no /run/utmp); on those systems logind is the
	// only source. Prefer it when the file is gone, and fall back to it whenever
	// utmp yields nothing, so a modern box still lists its sessions.
	if _, err := os.Stat(utmpFile()); err != nil {
		if logindAvailable() {
			return collectLogindSessions()
		}
	}
	sessions := sessionsFromPath(utmpFile())
	if len(sessions) == 0 && logindAvailable() {
		if ld := collectLogindSessions(); len(ld) > 0 {
			return ld
		}
	}
	return sessions
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
		if s.ConnType == "local" {
			s.Type = "tty"
		}
		s.Remote = classifySession(r)
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

// classifySession returns the client address of a utmp session. utmp has no
// protocol tag and no client identification: the sshd version string is not
// exported to the session environment (OpenSSH sets only SSH_CLIENT,
// SSH_CONNECTION and SSH_TTY), so the client field that older versions tried to
// read never existed. Only the peer address is real, and it comes from
// SSH_CONNECTION when readable, otherwise from the utmp host field.
func classifySession(r utmpRecord) (remote string) {
	// The utmp host field carries the origin for both local (:0) and remote
	// logins; prefer the explicit SSH_CONNECTION peer when present.
	host := r.host
	if h := sshHostFromEnv(r.pid); h != "" {
		host = h
	}
	if host == ":0" || host == "localhost" {
		host = ""
	}
	return host
}

// sessionConnType maps a utmp record to a connection type. The kernel keeps no
// protocol tag, so the classification is heuristic: a pts device or a non-local
// host means a remote (SSH) login, everything else is local. sftp and scp are
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

// sshHostFromEnv resolves the peer address from SSH_CONNECTION, which sshd
// exports to the session ("client_ip client_port server_ip server_port").
func sshHostFromEnv(pid int32) string {
	return sshPeerFromEnviron(procEnviron(pid))
}

// sshPeerFromEnviron extracts the client address from an SSH_CONNECTION value
// inside a NUL-separated environment blob.
func sshPeerFromEnviron(environ []byte) string {
	if environ == nil {
		return ""
	}
	for _, kv := range bytes.Split(environ, []byte{0}) {
		v, ok := bytes.CutPrefix(kv, []byte("SSH_CONNECTION="))
		if !ok {
			continue
		}
		if f := bytes.Fields(v); len(f) > 0 {
			return string(f[0])
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

// utmpFile returns the login record file, honouring the common paths. An
// explicit SYSINFO_UTMP overrides the default, which makes the parser testable
// against a saved copy on any machine.
func utmpFile() string {
	if p := os.Getenv("SYSINFO_UTMP"); p != "" {
		return p
	}
	for _, p := range []string{"/run/utmp", "/var/run/utmp"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/run/utmp"
}

// readUtmp parses the login records (utmp). The record size depends on the
// architecture, so both layouts are tried and scored; the better one wins. This
// keeps a misidentified GOARCH (or a binary run under an unusual libc) from
// silently reporting "no sessions".
func readUtmp(path string) ([]utmpRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	records, _ := parseUtmp(b, utmpRec)
	pScore := layoutScore(b, utmpRec)

	if alt := utmpRec.alternate(); alt.size != utmpRec.size {
		if aRecords, aValid := parseUtmp(b, alt); layoutScoreN(b, alt, aValid) > pScore {
			records = aRecords
		}
	}
	return records, nil
}

// layoutScore ranks a layout for a given utmp image. A correct layout divides
// the file into whole records, so that dominates; the number of usable login
// records breaks the remaining ties.
func layoutScore(b []byte, lay utmpLayout) int {
	_, valid := parseUtmp(b, lay)
	return layoutScoreN(b, lay, valid)
}

func layoutScoreN(b []byte, lay utmpLayout, valid int) int {
	score := valid * 2
	if len(b)%lay.size == 0 {
		score += 1000
	}
	return score
}

// readUtmpWith parses utmp records using an explicit layout, so the parser can
// be exercised for every architecture regardless of the host it runs on.
func readUtmpWith(path string, lay utmpLayout) ([]utmpRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	records, _ := parseUtmp(b, lay)
	return records, nil
}

// parseUtmp walks the buffer with the given stride and returns the records plus
// the number of usable login records, used to pick the right layout.
func parseUtmp(b []byte, lay utmpLayout) ([]utmpRecord, int) {
	var out []utmpRecord
	valid := 0
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
		if usableLogin(r) {
			valid++
		}
		out = append(out, r)
	}
	return out, valid
}

// usableLogin reports whether a record looks like a real USER_PROCESS entry
// rather than the debris of a wrong stride.
func usableLogin(r utmpRecord) bool {
	return r.typ == utUserProcess && r.user != "" && printable(r.user) && printable(r.line)
}

func printable(s string) bool {
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// utmpSec reads the login timestamp. The wide layout stores tv_sec as a signed
// 64-bit value in a struct timeval; the compact one as a signed 32-bit value.
func utmpSec(rec []byte, lay utmpLayout) int64 {
	if lay.wide {
		return int64(binary.LittleEndian.Uint64(rec[lay.tvSecOff:]))
	}
	return int64(int32(binary.LittleEndian.Uint32(rec[lay.tvSecOff:])))
}

// debugUtmp prints why the session list is (or is not) populated: which source
// was used, the file it read, its size, and how many records each utmp layout
// yields. Written for the "no active sessions" report so the cause is visible
// on the target machine.
func debugUtmp(w io.Writer) {
	fmt.Fprintf(w, "version: %s\n", versionLine())
	fmt.Fprintf(w, "arch: %s, long bit: %d\n", runtime.GOARCH, strconv.IntSize)
	path := utmpFile()
	fmt.Fprintf(w, "utmp file: %s\n", path)
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(w, "  read error: %v\n", err)
	} else {
		fmt.Fprintf(w, "  size: %d bytes\n", len(b))
		for _, lay := range []utmpLayout{utmpCompact, utmpWide} {
			records, valid := parseUtmp(b, lay)
			name := "compact"
			if lay.wide {
				name = "wide"
			}
			fmt.Fprintf(w, "  layout %-7s size=%d ut_session@%d ut_tv@%d: %d records, %d login records, %d trailing bytes\n",
				name, lay.size, lay.sessionOff, lay.tvSecOff, len(records), valid, len(b)%lay.size)
		}
		fmt.Fprintf(w, "  chosen layout size %d\n", utmpRec.size)
	}
	fmt.Fprintf(w, "  utmp sessions: %d\n", len(sessionsFromPath(path)))

	fmt.Fprintf(w, "logind available: %v\n", logindAvailable())
	if ids := logindSessionIDs(); len(ids) > 0 {
		fmt.Fprintf(w, "  logind sessions: %s\n", strings.Join(ids, ", "))
	}

	sessions := collectSessions()
	fmt.Fprintf(w, "sessions used in report: %d\n", len(sessions))
	for _, s := range sessions {
		detail := ""
		if s.Detail != "" {
			detail = " detail=" + strconv.Quote(s.Detail)
		}
		fmt.Fprintf(w, "    %s [%s] type=%s line=%q remote=%q%s pid=%d\n",
			s.User, s.ConnType, orNone(s.Type), s.Line, s.Remote, detail, s.PID)
	}
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
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
