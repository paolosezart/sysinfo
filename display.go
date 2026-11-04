package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DisplayInfo describes connected monitors, parsed from EDID blobs.
type DisplayInfo struct {
	Connectors []Connector
	Monitors   []Monitor
}

// Connector is a DRM connector (HDMI/DisplayPort/DSI) and its connection state.
type Connector struct {
	Name    string
	Status  string // connected / disconnected
	Enabled string
}

// Monitor holds the useful fields decoded from an EDID block.
type Monitor struct {
	Connector       string
	Manufacturer    string
	ProductName     string
	Serial          string
	Year            int
	Week            int
	EDIDVersion     string
	PreferredWidth  int
	PreferredHeight int
	RefreshHz       float64
	SizeCmW         float64
	SizeCmH         float64
}

func collectDisplayInfo() DisplayInfo {
	di := DisplayInfo{}
	cards, _ := filepath.Glob("/sys/class/drm/card*-*")
	for _, c := range cards {
		base := filepath.Base(c)
		// Skip the version file and the card node itself.
		if !strings.Contains(base, "-") {
			continue
		}
		conn := Connector{
			Name:    connectorName(base),
			Status:  readFileTrim(filepath.Join(c, "status")),
			Enabled: readFileTrim(filepath.Join(c, "enabled")),
		}
		di.Connectors = append(di.Connectors, conn)

		if conn.Status != "connected" {
			continue
		}
		edidPath := filepath.Join(c, "edid")
		if data, err := os.ReadFile(edidPath); err == nil && len(data) >= 128 {
			if m := parseEDID(data); m != nil {
				m.Connector = conn.Name
				di.Monitors = append(di.Monitors, *m)
			}
		}
	}
	return di
}

// connectorName turns "card0-HDMI-A-1" into "HDMI-A-1".
func connectorName(base string) string {
	if idx := strings.Index(base, "-"); idx >= 0 {
		return base[idx+1:]
	}
	return base
}

// parseEDID decodes manufacturer, product name, serial and the preferred
// timing from a raw EDID block.
func parseEDID(b []byte) *Monitor {
	if len(b) < 128 || b[0] != 0x00 || b[1] != 0xFF || b[2] != 0xFF || b[3] != 0xFF {
		return nil
	}
	m := &Monitor{}
	m.Manufacturer = decodeManufacturer(b[8], b[9])
	m.ProductName = readEDIDString(b, 0xFC)
	m.Serial = readEDIDString(b, 0xFF)
	if m.Serial == "" {
		m.Serial = readEDIDString(b, 0xFE)
	}
	m.Week = int(b[16])
	m.Year = int(b[17]) + 1990
	m.EDIDVersion = fmt.Sprintf("%d.%d", b[18], b[19])

	// Detailed timing descriptor #1 (bytes 54..71) holds the preferred mode.
	dt := b[54:72]
	if dt[0] != 0 || dt[1] != 0 {
		pixelClock := (int(dt[1])<<8 | int(dt[0])) * 10 // kHz
		hActive := int(dt[2]) | int(dt[4]>>4)<<8
		vActive := int(dt[5]) | int(dt[7]>>4)<<8
		hBlank := int(dt[3]) | int(dt[4]&0x0F)<<8
		vBlank := int(dt[6]) | int(dt[7]&0x0F)<<8
		m.PreferredWidth = hActive
		m.PreferredHeight = vActive
		if hActive > 0 && vActive > 0 && pixelClock > 0 {
			htotal := hActive + hBlank
			vtotal := vActive + vBlank
			if htotal > 0 && vtotal > 0 {
				m.RefreshHz = float64(pixelClock) * 1000 / (float64(htotal) * float64(vtotal))
			}
		}
	}

	// Physical size in cm (bytes 21, 22).
	if b[21] != 0 && b[22] != 0 {
		m.SizeCmW = float64(b[21]) / 10
		m.SizeCmH = float64(b[22]) / 10
	}
	return m
}

// decodeManufacturer expands the 3-letter PNP ID packed into two bytes.
func decodeManufacturer(b1, b2 byte) string {
	v := uint16(b1)<<8 | uint16(b2)
	c1 := byte((v>>10)&0x1F) + 'A' - 1
	c2 := byte((v>>5)&0x1F) + 'A' - 1
	c3 := byte(v&0x1F) + 'A' - 1
	return string([]byte{c1, c2, c3})
}

// readEDIDString scans the four 18-byte descriptor blocks for one with the
// given tag (0xFC = name, 0xFF = serial) and returns its text.
func readEDIDString(b []byte, tag byte) string {
	for off := 54; off+18 <= 126; off += 18 {
		d := b[off : off+18]
		if d[0] != 0 || d[1] != 0 || d[2] != 0 || d[3] != tag {
			continue
		}
		s := string(d[5:18])
		s = strings.TrimRight(s, "\x00 ")
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}
