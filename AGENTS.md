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
- A remote session is decided by `Remote=yes` + `RemoteHost`, not by the
  `Service=sshd` name, so non-sshd remote paths are not missed. logind `Type`
  (`x11`/`wayland`/`tty`/`unspecified`) is the only way to tell a graphical
  desktop login from a plain console; utmp cannot. The desktop name comes from
  the `Desktop=` property (e.g. `LXDE-pi`), otherwise the owning `Service=`.
- A session whose `Remote=yes` has no `RemoteHost` is still remote: keep it as
  SSH with an empty address rather than dropping it.
- `sftp`/`scp` never appear in `utmp`: they are SSH subsystems that run without
  a pty, so sshd records no login entry for them (OpenSSH `mm_pty_allocate` ->
  `mm_record_login` runs only for interactive PTY sessions). Only the
  interactive SSH login is listed; it cannot be told apart from sftp/scp by
  this data. Do not claim a protocol the data cannot show.
- There is NO SSH client version/name in the session environment. OpenSSH
  exports only `SSH_CLIENT`, `SSH_CONNECTION` and `SSH_TTY`; a
  `SSH_CLIENT_VERSION` variable does not exist (verified against the OpenSSH
  tree — 0 hits). An earlier revision read it and always got "". Do not
  reintroduce it. The only real remote datum is the peer address, from
  `SSH_CONNECTION` (`/proc/<pid>/environ`, root or same uid) or the utmp host.
- The client version IS in the log, but only at LogLevel DEBUG: kex.c logs
  `debug("Remote protocol version %d.%d, remote software version %.100s")`, and
  `debug()` is SYSLOG_LEVEL_DEBUG1, one step ABOVE VERBOSE (see log.h). So a
  stock `LogLevel INFO` sshd never writes it; `VERBOSE` is not enough either.
  `--ssh-client` reads it anyway, best effort. The version line carries no
  address, so it is paired with the address from the VERBOSE `Connection from
  <ip> port <n> on ...` line (sshd-session.c) by the shared `[pid]` syslog
  prefix. Key the result by address, NOT by PID: the logging PID is the sshd
  session process, not the logind `Leader` PID.
- Idle time comes from the tty device atime, the same source `w` uses
  (`stat` on `/dev/pts/N` or `/dev/ttyN`, `st.Atim`). logind's IdleHint /
  IdleSinceHint are only populated for graphical sessions (`SESSION_TYPE_IS_GRAPHICAL`
  in logind-session.c) and stay `0`/`no` for tty/ssh, so they are useless here.
  `os.FileInfo` does not expose atime — read `syscall.Stat_t.Atim`.
- Install time comes from the root filesystem's birth time (`statx` with
  `STATX_BTIME`, field `btime`). ext4/xfs/btrfs record it; older filesystems
  return no btime (mask bit clear) and then `/etc/machine-id`'s mtime is the
  fallback (systemd writes it on first boot). `statx` is NOT wrapped by the
  `syscall` package: call it via `Syscall6` and get `__NR_statx` from a per-arch
  table (`statxTrapFor`). The numbers DIFFER per arch — s390x is 379, not 291,
  and ppc64 is 383; a wrong number fails silently. `struct statx` is a fixed
  256-byte layout, same on every arch (`btime` at offset 0x50).
- Internet time: query NTP (UDP/123, 48-byte packet, transmit timestamp at
  bytes 40..48, NTP epoch offset 2208988800) and fall back to an HTTP `Date`
  header. Estimate drift against the midpoint of the request/response window,
  not the send time, or half the round trip shows up as skew. `http.Client`
  follows the `Date` header, which has 1-second resolution — good enough for a
  drift check, but never claim sub-second precision from it. Bound the whole
  check with one budget so an offline host cannot stall the report.
- A remote session with an empty `TTY` is a non-interactive SSH connection
  (`sftp`, `scp`, `rsync`, `ssh -N`); sshd shows it as `user@notty`. It has no
  utmp entry, only a logind session. Label it "SSH without terminal" — the data
  cannot say which protocol it is.
- `config.txt` lives at `/boot/firmware/config.txt` on Raspberry Pi OS Bookworm
  and later, and at `/boot/config.txt` on older images; check both, in that
  order. The file is plain `key=value`, but the `include` directive is
  space-separated ("include extra.txt") and there are `[filter]` sections.
  Resolve model filters against the board tags (`boardConfigTags`); keep settings
  under filters we cannot evaluate (EDID/HDMI/gpio/board-type) rather than
  dropping them. `#` starts a comment anywhere on a line, not just at column 0.
- RAM type/speed is not in sysfs on ARM. Raspberry Pi RAM is soldered, so the
  board model fixes it: the `sdram_freq` default column in the config.txt docs
  gives the data rate (Pi 4 = 3200, Pi 5 = 4267), and the bus is 32-bit for
  every Pi, including the Pi 5 (the BCM2712 doc states a 32-bit LPDDR4X
  interface at "up to 17 GB/s", which matches 4267 x 32 = 136.5 Gbit/s).
  On x86 read SMBIOS type 17 from
  `/sys/firmware/dmi/tables/DMI` (type@0x12, configured speed@0x20, bus
  width@0x0A); the tables are often root-only, so a normal user just gets no
  value. The line shows the effective rate with the real clock (rate / 2), the
  peak bandwidth in bits with bytes in parentheses, and the bus width; a
  `sdram_freq` in config.txt is shown as "base / oc" so the factory spec is
  never lost.
- The I2C RTC chip cannot be seen through sysfs once a driver binds the address,
  so probe `/dev/i2c-*` directly with ioctl `I2C_RDWR` (0x0707) using a
  zero-length write (START + address + STOP); a NAK from a missing chip is
  ENXIO/EREMOTEIO. Common addresses: 0x68 (DS1307/DS3231, the ZS-042 module),
  0x51 (PCF8563), 0x6f (MCP7940x). Reading `/dev/i2c-*` usually needs the `i2c`
  group. `/sys/class/rtc/rtc0/hctosys` is the key status bit: it says whether the
  system clock was set from the RTC at boot (the thing users actually care about).
  It is a kernel sysfs attribute, NOT a command, so the report must never tell the
  user to run "hctosys". The kernel calls `rtc_hctosys()` only when the RTC
  registers and its name equals `CONFIG_RTC_HCTOSYS_DEVICE` (default "rtc0"); a
  fitted-but-unused clock usually means the driver was not in the initramfs, and
  `dmesg | grep "setting system clock"` is the confirmation. Keep the wording
  plain and add the check command rather than a bare "check the setup".

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
