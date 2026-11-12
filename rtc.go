package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// rtcChip maps an I2C address to the RTC chip usually found there. The list
// covers the modules sold for the Raspberry Pi (DS3231/DS1307 on the common
// ZS-042 board, PCF8563, PCF8523, RV-3028, MCP7940x).
var rtcChip = map[uint8]string{
	0x51: "PCF8563",
	0x68: "DS1307/DS3231",
	0x69: "DS1307/DS3231 (alt)",
	0x6f: "MCP7940x",
	0xa2: "RV-3028 (addr 0x51)",
}

// RTCInfo describes the hardware real-time clock and, importantly, whether the
// system is actually synchronised with it.
type RTCInfo struct {
	// Kernel RTC (from /sys/class/rtc).
	Device    string // e.g. rtc0
	Name      string // driver name, e.g. "rtc-ds1307 1-0068"
	Time      string // RTC time as the kernel sees it
	Date      string
	Hctosys   bool // was the system clock set from this RTC at boot?
	WakeAlarm string

	// I2C detection.
	I2CBus    string // e.g. "/dev/i2c-1"
	I2CChip   string // chip name at the RTC address
	I2CAddr   uint8  // detected address
	I2CBusy   bool   // address responded but is not a known chip
	I2CFound  bool   // a known RTC chip answered on the bus
	Scanned   bool   // an I2C bus was present and probed
	ScanError string // why the scan could not run (permission, no bus)

	// config.txt integration.
	OverlayEnabled bool // dtoverlay=i2c-rtc... is present
	OverlayParams  string
}

// KnownRTCAddresses are the I2C addresses probed for an RTC chip.
var knownRTCAddresses = []uint8{0x68, 0x51, 0x6f, 0x69}

// collectRTC gathers RTC information. It never fails: missing pieces simply stay
// empty so the block shows only what exists.
func collectRTC(ct *configTxt) RTCInfo {
	r := RTCInfo{}
	r.readKernelRTC()
	r.readConfig(ct)
	r.scanI2C()
	return r
}

// readKernelRTC reads the kernel's view of the real-time clock from sysfs.
func (r *RTCInfo) readKernelRTC() {
	dir := "/sys/class/rtc/rtc0"
	if _, err := os.Stat(dir); err != nil {
		return
	}
	r.Device = "rtc0"
	r.Name = readFileTrim(filepath.Join(dir, "name"))
	r.Time = readFileTrim(filepath.Join(dir, "time"))
	r.Date = readFileTrim(filepath.Join(dir, "date"))
	r.WakeAlarm = readFileTrim(filepath.Join(dir, "wakealarm"))
	r.Hctosys = readFileTrim(filepath.Join(dir, "hctosys")) == "1"
}

// readConfig notes whether config.txt enables an I2C RTC overlay.
func (r *RTCInfo) readConfig(ct *configTxt) {
	if ct == nil {
		return
	}
	for name := range ct.Overlays {
		if strings.HasPrefix(name, "i2c-rtc") {
			r.OverlayEnabled = true
			if v := ct.setting("dtoverlay"); strings.HasPrefix(v, name) {
				r.OverlayParams = v
			}
			return
		}
	}
}

// scanI2C probes the I2C buses for a known RTC chip. The sysfs "new_device"
// mechanism is not usable here because a bound driver hides the address, so this
// talks to the bus directly through the i2c-dev interface.
func (r *RTCInfo) scanI2C() {
	buses, _ := filepath.Glob("/dev/i2c-*")
	if len(buses) == 0 {
		return
	}
	r.Scanned = true
	var firstErr error
	for _, bus := range buses {
		for _, addr := range knownRTCAddresses {
			ok, err := i2cPing(bus, addr)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				break // bus unusable; try the next one
			}
			if ok {
				r.I2CBus = bus
				r.I2CAddr = addr
				r.I2CFound = true
				if name, known := rtcChip[addr]; known {
					r.I2CChip = name
				} else {
					r.I2CBusy = true
				}
				return
			}
		}
	}
	if firstErr != nil {
		r.ScanError = firstErr.Error()
	}
}

// i2c_msg mirrors struct i2c_msg from linux/i2c.h.
type i2cMsg struct {
	Addr  uint16
	Flags uint16
	Len   uint16
	Buf   *byte
}

// i2c_rdwr_ioctl_data mirrors struct i2c_rdwr_ioctl_data.
type i2cRdwrData struct {
	Msgs  *i2cMsg
	NMsgs uint32
}

const (
	i2cSlave = 0x0703 // I2C_SLAVE
	i2cRdwr  = 0x0707 // I2C_RDWR
)

// i2cPing reports whether a device acknowledges at addr. A zero-length write is
// used: the kernel sends START, the address and a STOP, and a NAK from a missing
// device surfaces as an error.
func i2cPing(bus string, addr uint8) (bool, error) {
	f, err := os.OpenFile(bus, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()

	msg := i2cMsg{Addr: uint16(addr), Flags: 0, Len: 0, Buf: nil}
	data := i2cRdwrData{Msgs: &msg, NMsgs: 1}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), i2cRdwr,
		uintptr(unsafe.Pointer(&data)))
	if errno != 0 {
		// ENXIO/EREMOTEIO mean no device answered; EACCES/EPERM mean the bus is
		// not readable by this user, which is a different (reported) condition.
		if errno == syscall.ENXIO || errno == syscall.EREMOTEIO || errno == syscall.EIO {
			return false, nil
		}
		return false, errno
	}
	return true, nil
}

// rtcReport returns the RTC block, or nil when there is nothing to show (no
// kernel RTC and no I2C clock), so the block stays hidden on hosts without one.
func rtcReport(ct *configTxt) *RTCInfo {
	r := collectRTC(ct)
	if r.Device == "" && !r.I2CFound && r.ScanError == "" && !r.OverlayEnabled {
		return nil
	}
	return &r
}

// statusLine renders the synchronisation status, which is the point of the
// block: a board may have a clock fitted but the system not configured to use
// it.
func (r RTCInfo) statusLine() string {
	switch {
	case r.Hctosys:
		return L("системные часы синхронизированы с RTC при загрузке",
			"system clock was set from the RTC at boot")
	case r.I2CFound && r.OverlayEnabled:
		return L("часы найдены и включены в config.txt, но ядро не взяло время из RTC при загрузке",
			"clock found and enabled in config.txt, but the kernel did not take the time from it at boot")
	case r.I2CFound:
		return L("часы найдены на шине I2C, но в config.txt не включены (dtoverlay=i2c-rtc)",
			"clock found on the I2C bus but not enabled in config.txt (dtoverlay=i2c-rtc)")
	case r.Name != "":
		return L("RTC обнаружен ядром", "RTC present as seen by the kernel")
	}
	return ""
}

// statusHint explains, for a fitted-but-unused clock, what the kernel flag means
// and how to check it. "hctosys" is a read-only sysfs attribute (not a command):
// it is 1 only when the RTC set the system clock at boot.
func (r RTCInfo) statusHint() string {
	if r.Hctosys || !r.I2CFound || !r.OverlayEnabled {
		return ""
	}
	return L(
		"hctosys — это флаг ядра (/sys/class/rtc/rtc0/hctosys), а не команда: 1, если время при загрузке взято из RTC. Обычно часы ставит NTP. Проверка: dmesg | grep -i \"setting system clock\"",
		"hctosys is a kernel flag (/sys/class/rtc/rtc0/hctosys), not a command: 1 when the boot time came from the RTC. NTP usually sets the clock instead. Check: dmesg | grep -i \"setting system clock\"")
}

// chipLine renders the detected chip and address, e.g. "DS1307/DS3231 @ 0x68".
func (r RTCInfo) chipLine() string {
	if !r.I2CFound {
		return ""
	}
	return fmt.Sprintf("%s @ 0x%02x", r.I2CChip, r.I2CAddr)
}
