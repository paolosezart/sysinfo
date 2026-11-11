package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
)

// Databases embedded into the binary so vendor names resolve with no network
// and an empty cache. They are gzipped to keep the binary small; --vendor and
// --oui refresh the on-disk copies when a newer release is available.
//
//go:embed data/usb.ids.gz
var usbIDsGz []byte

//go:embed data/oui.txt.gz
var ouiGz []byte

// gunzip returns the decompressed contents, or "" if the payload is corrupt.
func gunzip(b []byte) string {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return ""
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		return ""
	}
	return string(out)
}

func embeddedUSBIDs() string { return gunzip(usbIDsGz) }
func embeddedOUI() string    { return gunzip(ouiGz) }
