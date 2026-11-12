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

// knownRAM returns the RAM specification for a recognised board model. Raspberry
// Pi boards have soldered RAM with a fixed type and rate, so the model is enough
// to name them. The rates follow the default sdram_freq column of the Raspberry
// Pi config.txt documentation.
func knownRAM(model string) (ramSpec, bool) {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "raspberry pi 5"), strings.Contains(m, "raspberry pi 500"),
		strings.Contains(m, "compute module 5"):
		return ramSpec{"LPDDR4X", 4267, 64}, true
	case strings.Contains(m, "raspberry pi 4"), strings.Contains(m, "raspberry pi 400"),
		strings.Contains(m, "compute module 4"):
		return ramSpec{"LPDDR4", 3200, 32}, true
	// Check the "+" variants before the plain 3/zero models. The kernel reports
	// the Pi 3B+ as "Model B Plus", not "B+".
	case strings.Contains(m, "raspberry pi 3 model b plus"), strings.Contains(m, "raspberry pi 3b+"),
		strings.Contains(m, "raspberry pi 3 model a plus"), strings.Contains(m, "raspberry pi 3a+"):
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
