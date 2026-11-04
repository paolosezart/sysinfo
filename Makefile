BINARY := sysinfo
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w

.PHONY: all build arm64 arm64-pi3 armhf amd64 test vet fmt clean release

all: arm64

## Build for Raspberry Pi 4/5 and other 64-bit ARM boards (linux/arm64)
arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 .

## Build for older 32-bit Raspberry Pi (linux/armv7)
armhf:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-armv7 .

## Build for a regular x86-64 Linux machine
amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w *.go

clean:
	rm -rf dist

## Build every target and checksum the artifacts
release: clean arm64 armhf amd64
	cd dist && sha256sum $(BINARY)-linux-* > SHA256SUMS && cat SHA256SUMS
