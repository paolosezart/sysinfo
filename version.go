package main

import (
	"fmt"
	"runtime"
)

// version is injected at build time:
//
//	go build -ldflags "-X main.version=$(git describe --tags --always)"
//
// It stays "dev" for a plain `go build`/`go run` so a local binary is never
// mistaken for a release.
var version = "dev"

// versionLine is the single source of truth for `--version` and the header the
// report prints, so the two can never disagree.
func versionLine() string {
	return fmt.Sprintf("sysinfo %s (%s/%s, %s)", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
