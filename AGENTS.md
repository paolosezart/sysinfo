# AGENTS.md

## Project
`sysinfo` — Linux/Raspberry Pi system summary tool (Go, no external deps).
Ported from Windows `systeminfo`, focused on Raspberry Pi 4B (arm64).

## Environment notes
- Go is NOT on PATH by default. Use: `export PATH=$PATH:/home/openhands/go-sdk/go/bin`
- `/usr/local` is root-owned; install toolchains under `$HOME`.
- `file` is not installed; inspect ELF headers with `od`/Python instead.
- `qemu-user-static` IS available: run arm64 binaries with
  `qemu-aarch64-static dist/sysinfo-linux-arm64 ...`

## Build & test
```sh
make release     # builds arm64, armv7, amd64 into dist/ + SHA256SUMS
make test
go vet ./...
```

## Vendor databases
- `data/usb.ids.gz` and `data/oui.txt.gz` are embedded into the binary
  (`embed.go`) so vendor lookups work with no network and an empty cache.
- `--vendor` / `--oui` refresh the cache with a conditional request
  (`If-None-Match` / `If-Modified-Since`); an unchanged upstream is skipped.
- Regenerate the embedded databases (compact OUI + usb.ids) with:
  ```sh
  curl -sL https://standards-oui.ieee.org/oui/oui.txt |
    awk -F'[(]hex[)]' '/\(hex\)/{gsub("-","",$1); sub(/^[ \t]+/,"",$2); printf "%s\t%s\n", toupper(substr($1,1,6)), $2}' |
    gzip -9 > data/oui.txt.gz
  curl -sL https://raw.githubusercontent.com/gentoo/hwids/master/usb.ids |
    gzip -9 > data/usb.ids.gz
  ```
  Then bump `sysinfo/1.3` in the `User-Agent` if the format changed.

## Data sources (learned the hard way)
- SD speed class is NOT in the CSD. It lives in the SD Status register (`ssr`),
  bits 440..447 -> the top byte of `raw_ssr[2]`, i.e. hex chars 16..18 of
  `/sys/block/mmcblk*/device/ssr`. Value 0/1/2/3/4 = class 0/2/4/6/10.
- The active MMC bus mode is not in sysfs. Read the debugfs `ios` file
  (`/sys/kernel/debug/mmc<N>/ios`, "timing spec: <n> (<name>)").
- EDID bytes 21/22 are already centimetres — do not divide by 10.
- `ethtool` reports duplex "Unknown! (255)" for a down link; only full/half
  are meaningful.
- ARM `CPU implementer`/`CPU part` are raw hex codes; map them to vendor/core
  names (`armImplementerName`, `armCoreName`) instead of showing `0x41`.
- `/proc/cpuinfo` "Model" on ARM is the *board* name (already in SYSTEM), so
  prefer `CPU part` for the CPU block.
- Virtual netdevs (bridge/veth/tunnel/VPN) are the ones the kernel places under
  `/sys/devices/virtual/net/`; physical NICs resolve to a bus path. Only the
  former should skip the LAN-port/cable/speed lines.
- `operstate` can be "unknown" or missing (common for veth); fall back to
  carrier/address presence before calling a link "down".
- Logged-in sessions come from `utmp` (`/run/utmp`). The record layout is
  architecture dependent: fields up to `ut_exit` are fixed (`ut_type`@0,
  `ut_pid`@4, `ut_line`@8, `ut_user`@44, `ut_host`@76), but `ut_session`/`ut_tv`
  are 32-bit only where glibc sets `__WORDSIZE_TIME64_COMPAT32=1`.
  amd64/armv7/386/riscv64/ppc64/sparc64/mips keep the 384-byte record
  (`ut_session`@336, `ut_tv.tv_sec`@340); arm64/s390x/loong64 use the wide
  400-byte record where `ut_session` is a 64-bit long and `ut_tv` a 16-byte
  `struct timeval` — there `ut_session` stays at 336 but `ut_tv.tv_sec` moves to
  344. (Both verified with a C `offsetof()` probe; do not guess these.) Parsing
  aarch64 with the 384-byte stride silently yields nothing, so the size must be
  chosen per `GOARCH` (`utmpLayoutFor`) with a fallback to the other layout
  (`readUtmp` scores both, preferring the one that divides the file evenly).
  Filter `ut_type == USER_PROCESS` (7). `--debug-utmp` prints the file, size and
  per-layout record counts on the target machine; `SYSINFO_UTMP=<path>` overrides
  the file for testing against a saved copy.
- Debian 13 (trixie) / systemd 257 builds systemd with `-UTMP`, so `/run/utmp`
  does not exist at all and the utmp path yields nothing. The session list then
  comes from systemd-logind via `loginctl`: `list-sessions` gives the roster and
  `show-session <id>...` prints each session's properties as `Key=Value` blocks.
  `collectSessions` prefers logind when `/run/utmp` is missing, and also falls
  back to it when utmp parses to zero sessions, so either source works.
  `show-session --json` does NOT exist in v257 (only the `list-*` verbs gained
  JSON), so the text form is parsed. Keep only `Class=user` and
  `State=active|online`; the manager/greeter/lock-screen sessions would otherwise
  show as phantom users.
- `sftp`/`scp` never appear in `utmp`: they are SSH subsystems that run without
  a pty, so sshd records no login entry for them (OpenSSH `mm_pty_allocate` ->
  `mm_record_login` runs only for interactive PTY sessions). Only the
  interactive SSH login is listed; it cannot be told apart from sftp/scp by
  this data. Do not claim a protocol the data cannot show.
- The SSH client version is not in utmp. sshd exports `SSH_CLIENT_VERSION` /
  `SSH_CONNECTION` to the session, so it is only readable from that process's
  `/proc/<pid>/environ` (root, or same uid). Treat the client field as
  best-effort and never fail the report when it is empty.

## Conventions
- Output is bilingual (ru/en); every user-facing string goes through `L(ru, en)`
  in `i18n.go`. Units are stored language-neutral ("MHz", "Mbps") and localized
  at render time via `unitLocal`.
- Keep labels human-readable (no kernel codes).
- All optional tools (`vcgencmd`, `iw`, `ethtool`, `resolvectl`) degrade
  gracefully — never fatal if missing.
- Parsers of /proc and /sys are kept as standalone functions so they can be
  unit-tested against fixtures (see `sysinfo_test.go`, `extra_test.go`).
- Vendor names come from `usb.ids` and IEEE OUI; downloads set a User-Agent
  (some mirrors reject the default Go client) and are validated for content.
