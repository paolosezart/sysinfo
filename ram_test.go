package main

import (
	"encoding/binary"
	"testing"
)

func TestKnownRAM(t *testing.T) {
	cases := []struct {
		model string
		typ   string
		mts   int
		bus   int
	}{
		{"Raspberry Pi 4 Model B Rev 1.4", "LPDDR4", 3200, 32},
		{"Raspberry Pi 400 Rev 1.0", "LPDDR4", 3200, 32},
		{"Raspberry Pi 5 Model B Rev 1.0", "LPDDR4X", 4267, 64},
		{"Raspberry Pi 3 Model B Plus Rev 1.3", "LPDDR2", 1000, 32},
		{"Raspberry Pi 3 Model B Rev 1.2", "LPDDR2", 900, 32},
		{"Raspberry Pi Zero W Rev 1.1", "LPDDR2", 900, 32},
		{"Compute Module 4 Rev 1.0", "LPDDR4", 3200, 32},
	}
	for _, c := range cases {
		spec, ok := knownRAM(c.model)
		if !ok {
			t.Errorf("knownRAM(%q) not recognised", c.model)
			continue
		}
		if spec.Type != c.typ || spec.SpeedMTs != c.mts || spec.BusBits != c.bus {
			t.Errorf("knownRAM(%q) = %+v, want %s/%d/%d", c.model, spec, c.typ, c.mts, c.bus)
		}
	}
	if _, ok := knownRAM("Generic x86 Board"); ok {
		t.Error("knownRAM should not recognise a non-Pi board")
	}
}

func TestRamSpecPeakGBs(t *testing.T) {
	cases := []struct {
		spec ramSpec
		want float64
	}{
		{ramSpec{"LPDDR4", 3200, 32}, 12.8},
		{ramSpec{"LPDDR4X", 4267, 64}, 34.136},
		{ramSpec{"LPDDR2", 900, 32}, 3.6},
		{ramSpec{"DDR4", 2666, 64}, 21.328},
	}
	for _, c := range cases {
		if got := c.spec.peakGBs(); got < c.want-0.01 || got > c.want+0.01 {
			t.Errorf("%+v peakGBs = %v, want %v", c.spec, got, c.want)
		}
	}
	if got := (ramSpec{}).peakGBs(); got != 0 {
		t.Errorf("empty spec peakGBs = %v, want 0", got)
	}
}

func TestDMIMemoryType(t *testing.T) {
	cases := map[byte]string{
		0x1A: "DDR4",
		0x1E: "LPDDR4",
		0x22: "DDR5",
		0x18: "DDR3",
		0x99: "",
	}
	for code, want := range cases {
		if got := dmiMemoryType(code); got != want {
			t.Errorf("dmiMemoryType(%#x) = %q, want %q", code, got, want)
		}
	}
}

// buildSMBIOSTable assembles a type-17 (Memory Device) structure followed by the
// end-of-table marker, exercising the real parser.
func buildSMBIOSTable(typ byte, speed, bus uint16) []byte {
	body := make([]byte, 0x22)
	body[0] = 17 // structure type
	body[1] = byte(len(body))
	binary.LittleEndian.PutUint16(body[0x0A:0x0C], bus)
	body[0x12] = typ
	binary.LittleEndian.PutUint16(body[0x15:0x17], speed)
	binary.LittleEndian.PutUint16(body[0x20:0x22], speed)
	// String set terminator (double NUL), then the end-of-table structure.
	return append(body, 0x00, 0x00, 0x7f, 0x04, 0x00, 0x00, 0x00, 0x00)
}

func TestParseSMBIOSMemory(t *testing.T) {
	raw := buildSMBIOSTable(0x1A /* DDR4 */, 2666, 64)
	structs := smbiosStructures(raw)
	var found bool
	for _, s := range structs {
		if s[0] != 17 {
			continue
		}
		found = true
		spec := ramSpec{
			Type:     dmiMemoryType(s[0x12]),
			SpeedMTs: int(binary.LittleEndian.Uint16(s[0x15:0x17])),
			BusBits:  int(binary.LittleEndian.Uint16(s[0x0A:0x0C])),
		}
		if spec.Type != "DDR4" || spec.SpeedMTs != 2666 || spec.BusBits != 64 {
			t.Errorf("parsed spec = %+v, want DDR4/2666/64", spec)
		}
	}
	if !found {
		t.Fatal("no type-17 structure parsed from the SMBIOS table")
	}
}
