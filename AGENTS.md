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
