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

func TestRamBandwidthMbit(t *testing.T) {
	cases := []struct {
		rate, bus int
		want      float64
	}{
		{3200, 32, 102400}, // Pi 4: 12.8 GB/s = 102.4 Gbit/s
		{4267, 64, 273088}, // Pi 5
		{900, 32, 28800},
		{2666, 64, 170624},
	}
	for _, c := range cases {
		if got := ramBandwidthMbit(c.rate, c.bus); got != c.want {
			t.Errorf("ramBandwidthMbit(%d, %d) = %v, want %v", c.rate, c.bus, got, c.want)
		}
	}
	if got := ramBandwidthMbit(0, 32); got != 0 {
		t.Errorf("ramBandwidthMbit with no rate = %v, want 0", got)
	}
}

func TestFormatBandwidth(t *testing.T) {
	old := langCode
	langCode = "en"
	defer func() { langCode = old }()

	cases := []struct {
		mbit float64
		want string
	}{
		{102400, "102.4 Gbit/s"},
		{28800, "28.8 Gbit/s"},
		{500, "500 Mbit/s"},
		{0, ""},
	}
	for _, c := range cases {
		if got := formatBandwidth(c.mbit); got != c.want {
			t.Errorf("formatBandwidth(%v) = %q, want %q", c.mbit, got, c.want)
		}
	}
}

func TestCollectMemoryOCSpeed(t *testing.T) {
	// With no /proc/meminfo the byte counters stay zero, but the RAM spec is
	// still resolved from the board model plus the config.txt overclock.
	ct := &configTxt{
		Path:     "/boot/firmware/config.txt",
		Settings: map[string]string{"sdram_freq": "3400"},
		Overlays: map[string]bool{},
	}
	mi := collectMemoryInfoFor("Raspberry Pi 4 Model B Rev 1.4", ct)
	if mi.RAMType != "LPDDR4" || mi.RAMSpeedMTs != 3200 {
		t.Fatalf("base spec = %s/%d, want LPDDR4/3200", mi.RAMType, mi.RAMSpeedMTs)
	}
	if mi.RAMOCSpeedMTs != 3400 {
		t.Errorf("RAMOCSpeedMTs = %d, want 3400", mi.RAMOCSpeedMTs)
	}

	// A sdram_freq equal to the factory rate is not an overclock.
	ct.Settings["sdram_freq"] = "3200"
	if mi := collectMemoryInfoFor("Raspberry Pi 4 Model B Rev 1.4", ct); mi.RAMOCSpeedMTs != 0 {
		t.Errorf("RAMOCSpeedMTs = %d, want 0 when sdram_freq matches the base rate", mi.RAMOCSpeedMTs)
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
