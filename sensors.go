package main

import (
	"path/filepath"
	"strings"
)

// SensorInfo holds Raspberry Pi specific telemetry plus generic thermal zones.
type SensorInfo struct {
	IsRaspberryPi bool
	TemperatureC  float64
	Voltages      []SensorValue
	Clocks        []SensorValue
	Throttle      *ThrottleState
	ThermalZones  []ThermalZone
	Fans          []FanInfo
}

// SensorValue is a named numeric reading (voltage or clock).
type SensorValue struct {
	Name  string // human-readable label
	Value float64
	Unit  string
}

// ThrottleState is the decoded form of `vcgencmd get_throttled`.
type ThrottleState struct {
	Raw                   uint64
	UnderVoltageNow       bool
	FreqCappedNow         bool
	ThrottledNow          bool
	SoftTempLimitNow      bool
	UnderVoltageOccurred  bool
	FreqCappedOccurred    bool
	ThrottledOccurred     bool
	SoftTempLimitOccurred bool
}

// ThermalZone is a generic /sys/class/thermal zone.
type ThermalZone struct {
	Name  string
	TempC float64
}

// FanInfo describes a cooling device that reports an RPM.
type FanInfo struct {
	Name string
	RPM  int
}

// clockLabels maps vcgencmd clock domain codes to human-readable names.
var clockLabels = map[string]string{
	"arm":   "Ядро ARM",
	"core":  "Ядро GPU (core)",
	"h264":  "H.264",
	"isp":   "ISP (камера)",
	"v3d":   "3D-ускоритель (V3D)",
	"uart":  "UART",
	"pwm":   "PWM",
	"emmc":  "eMMC",
	"pixel": "Пиксельный такт",
	"vec":   "Векторный процессор",
	"hdmi":  "HDMI",
	"dpi":   "DPI",
	"hevc":  "HEVC",
}

// voltLabels maps vcgencmd voltage domains to human-readable names.
var voltLabels = map[string]string{
	"core":    "Ядро (core)",
	"sdram_c": "SDRAM (ядро)",
	"sdram_i": "SDRAM (I/O)",
	"sdram_p": "SDRAM (PHY)",
}

func collectSensors(si SystemInfo) SensorInfo {
	s := SensorInfo{}
	s.IsRaspberryPi = strings.Contains(strings.ToLower(si.BoardModel+si.BoardVendor), "raspberry")

	if commandExists("vcgencmd") {
		s.TemperatureC = vcgencmdTemp()
		for _, dom := range []string{"core", "sdram_c", "sdram_i", "sdram_p"} {
			if v := vcgencmdVolts(dom); v > 0 {
				label := voltLabels[dom]
				if label == "" {
					label = dom
				}
				s.Voltages = append(s.Voltages, SensorValue{Name: label, Value: v, Unit: "В"})
			}
		}
		for _, dom := range []string{"arm", "core", "v3d", "h264", "isp", "pixel"} {
			if v := vcgencmdClockMHz(dom); v > 0 {
				label := clockLabels[dom]
				if label == "" {
					label = dom
				}
				s.Clocks = append(s.Clocks, SensorValue{Name: label, Value: v, Unit: "МГц"})
			}
		}
		s.Throttle = vcgencmdThrottled()
	}

	if s.TemperatureC == 0 {
		s.TemperatureC = thermalZoneTemp()
	}
	s.ThermalZones = collectThermalZones()
	s.Fans = collectFans()
	return s
}

func vcgencmdTemp() float64 {
	out, err := runCmd(cmdTimeout, "vcgencmd", "measure_temp")
	if err != nil {
		return 0
	}
	// temp=45.6'C
	out = strings.TrimPrefix(out, "temp=")
	out = strings.TrimSuffix(out, "'C")
	return parseFloat(out)
}

func vcgencmdVolts(domain string) float64 {
	out, err := runCmd(cmdTimeout, "vcgencmd", "measure_volts", domain)
	if err != nil {
		return 0
	}
	// volt=0.8700V
	out = strings.TrimPrefix(out, "volt=")
	out = strings.TrimSuffix(out, "V")
	return parseFloat(out)
}

// vcgencmdThrottled decodes the bitfield returned by get_throttled.
// Bits 0-3: current conditions; bits 16-19: conditions seen since boot.
func vcgencmdThrottled() *ThrottleState {
	out, err := runCmd(cmdTimeout, "vcgencmd", "get_throttled")
	if err != nil {
		return nil
	}
	idx := strings.Index(out, "=")
	if idx < 0 {
		return nil
	}
	return parseThrottled(out[idx+1:])
}

// parseThrottled decodes the hexadecimal value from `get_throttled` output.
// It accepts either a bare hex value or the full "throttled=0x..." form.
func parseThrottled(raw string) *ThrottleState {
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "="); idx >= 0 {
		raw = raw[idx+1:]
	}
	raw = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(raw), "0x"), "0X")
	if raw == "" {
		return nil
	}
	var val uint64
	for _, c := range raw {
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint64(c-'A') + 10
		default:
			return nil
		}
		val = val<<4 | d
	}
	return &ThrottleState{
		Raw:                   val,
		UnderVoltageNow:       val&(1<<0) != 0,
		FreqCappedNow:         val&(1<<1) != 0,
		ThrottledNow:          val&(1<<2) != 0,
		SoftTempLimitNow:      val&(1<<3) != 0,
		UnderVoltageOccurred:  val&(1<<16) != 0,
		FreqCappedOccurred:    val&(1<<17) != 0,
		ThrottledOccurred:     val&(1<<18) != 0,
		SoftTempLimitOccurred: val&(1<<19) != 0,
	}
}

func thermalZoneTemp() float64 {
	zones, _ := filepath.Glob("/sys/class/thermal/thermal_zone*")
	for _, z := range zones {
		if t := parseFloat(readFileTrim(filepath.Join(z, "temp"))); t != 0 {
			return t / 1000.0
		}
	}
	return 0
}

func collectThermalZones() []ThermalZone {
	var out []ThermalZone
	zones, _ := filepath.Glob("/sys/class/thermal/thermal_zone*")
	for _, z := range zones {
		name := readFileTrim(filepath.Join(z, "type"))
		temp := parseFloat(readFileTrim(filepath.Join(z, "temp"))) / 1000.0
		if name == "" {
			name = filepath.Base(z)
		}
		out = append(out, ThermalZone{Name: humanThermalName(name), TempC: temp})
	}
	return out
}

// humanThermalName maps kernel thermal zone types to friendlier labels.
func humanThermalName(t string) string {
	lower := strings.ToLower(t)
	switch {
	case strings.Contains(lower, "cpu") || strings.Contains(lower, "soc"):
		return "Процессор (SoC)"
	case strings.Contains(lower, "gpu"):
		return "GPU"
	case strings.Contains(lower, "ddr"):
		return "Память (DDR)"
	case strings.Contains(lower, "wifi") || strings.Contains(lower, "wl"):
		return "Wi-Fi модуль"
	case strings.Contains(lower, "acpi"):
		return "ACPI"
	case strings.Contains(lower, "x86_pkg"):
		return "Пакет процессора"
	}
	return t
}

func collectFans() []FanInfo {
	var out []FanInfo
	hwmons, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, h := range hwmons {
		name := readFileTrim(filepath.Join(h, "name"))
		inputs, _ := filepath.Glob(filepath.Join(h, "fan*_input"))
		for _, in := range inputs {
			rpm := int(parseUint(readFileTrim(in)))
			out = append(out, FanInfo{Name: name, RPM: rpm})
		}
	}
	return out
}

// describeThrottle returns human-readable phrases for active/occurred issues.
func (t *ThrottleState) describe() (now, occurred []string) {
	if t == nil {
		return nil, nil
	}
	if t.UnderVoltageNow {
		now = append(now, "пониженное напряжение")
	}
	if t.FreqCappedNow {
		now = append(now, "частота ограничена")
	}
	if t.ThrottledNow {
		now = append(now, "троттлинг (перегрев/питание)")
	}
	if t.SoftTempLimitNow {
		now = append(now, "мягкий температурный лимит")
	}
	if t.UnderVoltageOccurred {
		occurred = append(occurred, "пониженное напряжение")
	}
	if t.FreqCappedOccurred {
		occurred = append(occurred, "ограничение частоты")
	}
	if t.ThrottledOccurred {
		occurred = append(occurred, "троттлинг")
	}
	if t.SoftTempLimitOccurred {
		occurred = append(occurred, "мягкий температурный лимит")
	}
	return now, occurred
}

func (t *ThrottleState) healthy() bool {
	if t == nil {
		return true
	}
	return !t.UnderVoltageNow && !t.FreqCappedNow && !t.ThrottledNow && !t.SoftTempLimitNow &&
		!t.UnderVoltageOccurred && !t.FreqCappedOccurred && !t.ThrottledOccurred && !t.SoftTempLimitOccurred
}
