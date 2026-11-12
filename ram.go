package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

// ramSpec is the RAM type, data rate and bus width of a board. The kernel does
// not report any of this on most ARM boards, so it is resolved from the board
// model or from SMBIOS on x86.
type ramSpec struct {
	Type     string // LPDDR4, LPDDR4X, DDR4, ...
	SpeedMTs int    // data rate in megatransfers per second
	BusBits  int    // bus width in bits
}

// ramBandwidthMbit returns the peak bandwidth in megabits per second for a data
// rate (in MT/s) and a bus width, or 0 when either is unknown. Each transfer
// moves one bus-width word, so the rate in transfers/s times the width in bits
// is already a bit rate.
func ramBandwidthMbit(rateMTs, busBits int) float64 {
	if rateMTs == 0 || busBits == 0 {
		return 0
	}
	return float64(rateMTs) * float64(busBits)
}

// ramClockMHz returns the real (physical) clock for a DDR data rate. Every
// LPDDR/DDR generation transfers on both edges, so the clock is half the rate.
func ramClockMHz(rateMTs int) float64 {
	return float64(rateMTs) / 2
}

// formatRateClock renders a DDR data rate as "3200 MT/s (1600 MHz)", i.e. the
// effective rate followed by the real bus clock.
func formatRateClock(rateMTs int) string {
	return fmt.Sprintf("%d MT/s (%.0f %s)", rateMTs, ramClockMHz(rateMTs), L("МГц", "MHz"))
}

// formatBandwidth renders a bandwidth given in megabits per second, switching to
// gigabits per second once it is large enough.
func formatBandwidth(mbit float64) string {
	switch {
	case mbit <= 0:
		return ""
	case mbit >= 1000:
		return fmt.Sprintf("%.1f %s", mbit/1000, L("Гбит/с", "Gbit/s"))
	default:
		return fmt.Sprintf("%.0f %s", mbit, L("Мбит/с", "Mbit/s"))
	}
}

// formatBandwidthBytes renders a bandwidth given in megabits per second as bytes
// per second, switching to gigabytes once it is large enough.
func formatBandwidthBytes(mbit float64) string {
	if mbit <= 0 {
		return ""
	}
	mb := mbit / 8
	if mb >= 1000 {
		return fmt.Sprintf("%.1f %s", mb/1000, L("ГБ/с", "GB/s"))
	}
	return fmt.Sprintf("%.0f %s", mb, L("МБ/с", "MB/s"))
}

// numStr formats a float, dropping the trailing ".0" for whole numbers.
func numStr(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	if strings.HasSuffix(s, ".0") {
		s = s[:len(s)-2]
	}
	return s
}

// pairStr renders "base" or "base / oc" when oc is greater than zero.
func pairStr(base, oc float64) string {
	if oc > 0 {
		return numStr(base) + " / " + numStr(oc)
	}
	return numStr(base)
}

// formatRAMType renders the memory type and bus width, e.g. "LPDDR4 (шина 32 бит)".
// The bus width is shown here because it explains the bandwidth figure below.
func formatRAMType(spec ramSpec) string {
	if spec.Type == "" {
		return ""
	}
	if spec.BusBits > 0 {
		return fmt.Sprintf("%s (%s %d %s)", spec.Type,
			L("шина", "bus"), spec.BusBits, L("бит", "bit"))
	}
	return spec.Type
}

// formatRAMFrequency renders the effective data rate with the real clock, e.g.
// "3200 MT/s (1600 МГц)". With an overclock the factory and OC values are shown
// as "base / oc" so the factory spec is never lost.
func formatRAMFrequency(spec ramSpec, ocMTs int) string {
	if spec.SpeedMTs <= 0 {
		return ""
	}
	rate := pairStr(float64(spec.SpeedMTs), float64(ocMTs)) + " MT/s"
	clock := pairStr(ramClockMHz(spec.SpeedMTs), ramClockMHz(ocMTs))
	return fmt.Sprintf("%s (%s %s)", rate, clock, L("МГц", "MHz"))
}

// formatRAMSpeed renders the peak bandwidth in bits with bytes in parentheses,
// e.g. "102.4 Гбит/с (12.8 ГБ/с)". With an overclock both figures are shown.
func formatRAMSpeed(spec ramSpec, ocMTs int) string {
	bw := ramBandwidthMbit(spec.SpeedMTs, spec.BusBits)
	if bw <= 0 {
		return ""
	}
	bits, bytes := formatBandwidth(bw), formatBandwidthBytes(bw)
	if obw := ramBandwidthMbit(ocMTs, spec.BusBits); obw > 0 {
		bits += " / " + formatBandwidth(obw)
		bytes += " / " + formatBandwidthBytes(obw)
	}
	return fmt.Sprintf("%s (%s)", bits, bytes)
}

// knownRAM returns the RAM specification for a recognised board model. Raspberry
// Pi boards have soldered RAM with a fixed type and rate, so the model is enough
// to name them. The rates follow the default sdram_freq column of the Raspberry
// Pi config.txt documentation.
func knownRAM(model string) (ramSpec, bool) {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "raspberry pi 5"), strings.Contains(m, "raspberry pi 500"),
		strings.Contains(m, "compute module 5"):
		return ramSpec{"LPDDR4X", 4267, 32}, true
	case strings.Contains(m, "raspberry pi 4"), strings.Contains(m, "raspberry pi 400"),
		strings.Contains(m, "compute module 4"):
		return ramSpec{"LPDDR4", 3200, 32}, true
	// Check the "+" variants before the plain 3/zero models. The kernel reports
	// the Pi 3B+ as "Model B Plus", not "B+".
	case strings.Contains(m, "raspberry pi 3 model b plus"), strings.Contains(m, "raspberry pi 3b+"),
		strings.Contains(m, "raspberry pi 3 model a plus"), strings.Contains(m, "raspberry pi 3a+"):
		// The config.txt default table lists sdram_freq=500 (clock) for these
		// boards, i.e. an effective LPDDR2-1000 rate. The product page instead
		// states 900 MHz; the two official pages disagree, so keep 1000 to stay
		// consistent with the sdram_freq option we actually parse.
		return ramSpec{"LPDDR2", 1000, 32}, true
	case strings.Contains(m, "raspberry pi 3"), strings.Contains(m, "compute module 3"):
		return ramSpec{"LPDDR2", 900, 32}, true
	case strings.Contains(m, "raspberry pi 2"):
		return ramSpec{"LPDDR2", 900, 32}, true
	case strings.Contains(m, "raspberry pi zero"):
		return ramSpec{"LPDDR2", 900, 32}, true
	case strings.Contains(m, "raspberry pi 1"), strings.Contains(m, "compute module 1"):
		return ramSpec{"LPDDR2", 800, 32}, true
	}
	return ramSpec{}, false
}

// dmiMemoryType maps the SMBIOS Memory Device type byte to a name.
func dmiMemoryType(code byte) string {
	switch code {
	case 0x01:
		return "Other"
	case 0x02:
		return "Unknown"
	case 0x0F:
		return "SDRAM"
	case 0x11:
		return "SDRAM"
	case 0x12:
		return "DDR"
	case 0x13:
		return "DDR2"
	case 0x18:
		return "DDR3"
	case 0x1A:
		return "DDR4"
	case 0x1B:
		return "LPDDR"
	case 0x1C:
		return "LPDDR2"
	case 0x1D:
		return "LPDDR3"
	case 0x1E:
		return "LPDDR4"
	case 0x22:
		return "DDR5"
	case 0x23:
		return "LPDDR5"
	}
	return ""
}

// dmiRAM reads the installed memory type and speed from SMBIOS (type 17). This
// only works on x86/EFI machines that expose the DMI tables; the tables are
// usually root-readable, so a normal user simply gets no result.
func dmiRAM() (ramSpec, bool) {
	b, err := os.ReadFile("/sys/firmware/dmi/tables/DMI")
	if err != nil || len(b) < 8 {
		return ramSpec{}, false
	}
	for _, s := range smbiosStructures(b) {
		if s[0] != 17 || len(s) < 0x17 {
			continue
		}
		spec := ramSpec{
			Type:     dmiMemoryType(s[0x12]),
			SpeedMTs: int(binary.LittleEndian.Uint16(s[0x15:0x17])),
			BusBits:  int(binary.LittleEndian.Uint16(s[0x0A:0x0C])),
		}
		// Prefer the configured speed (offset 0x20) when present, since the
		// rated speed may be higher than what the system actually runs at.
		if len(s) >= 0x22 {
			if cs := int(binary.LittleEndian.Uint16(s[0x20:0x22])); cs > 0 {
				spec.SpeedMTs = cs
			}
		}
		if spec.Type != "" {
			return spec, true
		}
	}
	return ramSpec{}, false
}

// smbiosStructures splits the raw SMBIOS table into individual structures. Each
// one starts with a type/length/handle header followed by a formatted area of
// exactly length bytes, then a double NUL terminating the string set.
func smbiosStructures(b []byte) [][]byte {
	var out [][]byte
	for i := 0; i+4 <= len(b); {
		typ, length := b[i], int(b[i+1])
		if typ == 0x7f { // end-of-table
			break
		}
		if length < 4 || i+length > len(b) {
			break
		}
		out = append(out, b[i:i+length])
		// Skip the formatted area, then the string set up to a double NUL.
		j := i + length
		for j+1 < len(b) && !(b[j] == 0 && b[j+1] == 0) {
			j++
		}
		i = j + 2
	}
	return out
}

// ramInfo resolves the RAM specification, preferring SMBIOS (real installed
// hardware) and falling back to the board model table for Raspberry Pi.
func ramInfo(model string) (ramSpec, string, bool) {
	if spec, ok := dmiRAM(); ok {
		return spec, L("SMBIOS", "SMBIOS"), true
	}
	if spec, ok := knownRAM(model); ok {
		return spec, L("модель платы", "board model"), true
	}
	return ramSpec{}, "", false
}
