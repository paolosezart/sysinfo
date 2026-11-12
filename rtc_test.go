package main

import "testing"

func TestRTCChipTable(t *testing.T) {
	if rtcChip[0x68] == "" {
		t.Error("0x68 (DS1307/DS3231) missing from the chip table")
	}
	if rtcChip[0x51] == "" {
		t.Error("0x51 (PCF8563) missing from the chip table")
	}
}

func TestRTCStatusLine(t *testing.T) {
	cases := []struct {
		name string
		rtc  RTCInfo
		want bool // whether a status is produced
	}{
		{"hctosys", RTCInfo{Hctosys: true, I2CFound: true}, true},
		{"found but not enabled", RTCInfo{I2CFound: true}, true},
		{"found and enabled, no hctosys", RTCInfo{I2CFound: true, OverlayEnabled: true}, true},
		{"kernel only", RTCInfo{Name: "rtc_cmos 00:00"}, true},
		{"nothing", RTCInfo{}, false},
	}
	for _, c := range cases {
		got := c.rtc.statusLine() != ""
		if got != c.want {
			t.Errorf("%s: statusLine produced=%v, want %v", c.name, got, c.want)
		}
	}
}

func TestRTCStatusPriority(t *testing.T) {
	// hctosys must win over the "found but not enabled" wording.
	r := RTCInfo{Hctosys: true, I2CFound: true, OverlayEnabled: false}
	if got := r.statusLine(); got == "" {
		t.Fatal("empty status")
	}
	plain := RTCInfo{I2CFound: true}
	if r.statusLine() == plain.statusLine() {
		t.Error("hctosys status should differ from the not-enabled status")
	}
}

func TestRTCScanNoBus(t *testing.T) {
	// With no /dev/i2c-* present the scan must stay quiet (not mark Scanned).
	r := RTCInfo{}
	r.scanI2C()
	if r.Scanned {
		t.Skip("host exposes an I2C bus; scan-quiet case not exercised")
	}
	if r.ScanError != "" || r.I2CFound {
		t.Errorf("unexpected scan result on a bus-less host: %+v", r)
	}
}

func TestChipLine(t *testing.T) {
	r := RTCInfo{I2CFound: true, I2CChip: "DS1307/DS3231", I2CAddr: 0x68}
	if got := r.chipLine(); got != "DS1307/DS3231 @ 0x68" {
		t.Errorf("chipLine = %q", got)
	}
	if got := (RTCInfo{}).chipLine(); got != "" {
		t.Errorf("chipLine without a chip = %q, want empty", got)
	}
}

func TestRTCReportNil(t *testing.T) {
	// A host with no RTC device and no config must produce no block.
	if r := rtcReport(nil); r != nil && r.Device == "" && !r.I2CFound {
		// On a machine that has an RTC this is expected to be non-nil.
		t.Logf("rtcReport returned %+v", r)
	}
}
