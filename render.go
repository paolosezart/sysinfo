package main

import (
	"fmt"
	"strings"
)

type renderer struct {
	color bool
}

func newRenderer(noColor bool) *renderer {
	return &renderer{color: !noColor}
}

const (
	cReset  = "\033[0m"
	cBold   = "\033[1m"
	cDim    = "\033[2m"
	cCyan   = "\033[36m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cRed    = "\033[31m"
	cBlue   = "\033[34m"
	// Bright yellow is far easier to read than dim grey for the field labels.
	cLabel = "\033[1;93m"
)

func (r *renderer) paint(code, s string) string {
	if !r.color {
		return s
	}
	return code + s + cReset
}

func (r *renderer) header(title string) {
	line := strings.Repeat("─", 60)
	fmt.Println()
	fmt.Println(r.paint(cCyan+cBold, title))
	fmt.Println(r.paint(cDim, line))
}

// kv prints a "label: value" line with a bright yellow label.
func (r *renderer) kv(key, val string) {
	if val == "" {
		return
	}
	fmt.Printf("  %s %s\n", r.paint(cLabel, key+":"), val)
}

// kvIndent is like kv but indents the label, used for nested details.
func (r *renderer) kvIndent(key, val string) {
	if val == "" {
		return
	}
	fmt.Printf("    %s %s\n", r.paint(cLabel, key+":"), val)
}

func (r *renderer) render(rep Report, sections map[string]bool) {
	want := func(name string) bool { return len(sections) == 0 || sections[name] }

	if want("system") {
		r.renderSystem(rep.System, rep.Locale)
	}
	if want("cpu") {
		r.renderCPU(rep.CPU)
	}
	if want("memory") {
		r.renderMemory(rep.Memory)
	}
	if want("storage") {
		r.renderStorage(rep.Storage)
	}
	if want("network") {
		r.renderNetwork(rep.Network)
	}
	if want("sensors") {
		r.renderSensors(rep.Sensors)
	}
	if want("display") {
		r.renderDisplay(rep.Display)
	}
	if want("usb") {
		r.renderUSB(rep.USB)
	}
	if want("users") {
		r.renderUsers(rep.Sessions)
	}
	if want("overclock") && rep.Overclock != nil {
		r.renderOverclock(rep.Overclock, rep.System.BoardModel)
	}
	if want("rtc") && rep.RTC != nil {
		r.renderRTC(*rep.RTC)
	}
	fmt.Println()
}

// renderOverclock shows the CPU/GPU/SDRAM settings from config.txt alongside the
// values the firmware reports as active.
func (r *renderer) renderOverclock(oc *OverclockInfo, model string) {
	r.header(L("ОВЕРКЛОК / НАПРЯЖЕНИЕ", "OVERCLOCK / VOLTAGE"))
	if oc.ConfigPath != "" {
		r.kv(L("Источник", "Source"), oc.ConfigPath)
	}

	freqLine := func(configured, live int) string {
		switch {
		case configured > 0 && live > 0 && configured != live:
			return fmt.Sprintf("%d %s (%s %d)", configured, L("МГц", "MHz"),
				L("активно", "active"), live)
		case configured > 0:
			return fmt.Sprintf("%d %s", configured, L("МГц", "MHz"))
		case live > 0:
			return fmt.Sprintf("%d %s", live, L("МГц", "MHz"))
		}
		return ""
	}
	r.kv(L("CPU (arm_freq)", "CPU (arm_freq)"), freqLine(oc.ARMfreqMHz, oc.LiveARMfreqMHz))
	r.kv(L("GPU (gpu_freq)", "GPU (gpu_freq)"), freqLine(oc.GPUfreqMHz, 0))
	r.kv(L("Ядро GPU (core_freq)", "GPU core (core_freq)"), freqLine(oc.CorefreqMHz, oc.LiveCorefreqMHz))
	r.kv(L("Память (sdram_freq)", "Memory (sdram_freq)"), freqLine(oc.SDRAMfreqMHz, oc.LiveSDRAMfreqMHz))
	r.kv(L("Напряжение (over_voltage)", "Voltage (over_voltage)"), overclockVoltageLine(oc, model))
	if oc.ForceTurbo {
		r.kv(L("Турбо-режим (force_turbo)", "Turbo mode (force_turbo)"), L("включён", "enabled"))
	}
	if oc.ArmBoost {
		r.kv(L("Ускорение (arm_boost)", "Boost (arm_boost)"), L("включено", "enabled"))
	}
	if oc.TempLimitC > 0 {
		r.kv(L("Температурный предел", "Temperature limit"), fmt.Sprintf("%d °C", oc.TempLimitC))
	}
	if !oc.HasLive && oc.ARMfreqMHz == 0 && oc.GPUfreqMHz == 0 && oc.CorefreqMHz == 0 && oc.SDRAMfreqMHz == 0 && !oc.OverVoltageSet {
		r.kv(L("Разгон", "Overclock"), r.paint(cDim, L("параметры не заданы (заводские частоты)", "no parameters set (factory clocks)")))
	}
}

// renderRTC shows the hardware clock and, crucially, whether the system uses it.
func (r *renderer) renderRTC(rtc RTCInfo) {
	r.header(L("RTC (часы реального времени)", "RTC (real-time clock)"))
	if rtc.Name != "" {
		r.kv(L("Устройство", "Device"), fmt.Sprintf("%s (%s)", rtc.Device, rtc.Name))
	} else if rtc.Device != "" {
		r.kv(L("Устройство", "Device"), rtc.Device)
	}
	if rtc.Date != "" {
		r.kv(L("Время RTC (UTC)", "RTC time (UTC)"), strings.TrimSpace(rtc.Date+" "+rtc.Time))
	}
	if rtc.I2CFound {
		r.kv(L("Плата часов (I2C)", "Clock board (I2C)"), rtc.chipLine())
		if rtc.OSFRead {
			state := L("генератор в норме", "oscillator OK")
			if rtc.OSF {
				state = L("останов генератора (OSF) — время недостоверно, часы не выставляли",
					"oscillator stopped (OSF) — time unreliable, the clock was never set")
			}
			osfColor := cGreen
			if rtc.OSF {
				osfColor = cRed
			}
			r.kv(L("Состояние", "State"), r.paint(osfColor, state))
		}
	} else if rtc.ScanError != "" {
		r.kv(L("Плата часов (I2C)", "Clock board (I2C)"), r.paint(cDim, rtc.ScanError))
	} else if rtc.Scanned {
		r.kv(L("Плата часов (I2C)", "Clock board (I2C)"), r.paint(cDim, L("не обнаружена", "not detected")))
	}
	if rtc.OverlayEnabled {
		r.kv(L("dtoverlay в config.txt", "dtoverlay in config.txt"), rtc.OverlayParams)
	}
	if status := rtc.statusLine(); status != "" {
		color := cYellow
		if rtc.Hctosys {
			color = cGreen
		}
		r.kv(L("Синхронизация", "Synchronisation"), r.paint(color, status))
	}
	if hint := rtc.statusHint(); hint != "" {
		r.kv(L("Пояснение", "Note"), r.paint(cDim, hint))
	}
}

func (r *renderer) renderSystem(s SystemInfo, loc LocaleInfo) {
	r.header(L("СИСТЕМА", "SYSTEM"))
	r.kv(L("Имя узла", "Hostname"), s.Hostname)
	r.kv(L("ОС", "OS"), s.OSName)
	r.kv(L("Ядро", "Kernel"), s.KernelVersion)
	r.kv(L("Архитектура", "Architecture"), archHuman(s.Arch))
	if s.BoardModel != "" {
		r.kv(L("Плата", "Board"), s.BoardModel)
	}
	if s.BoardVendor != "" {
		r.kv(L("Производитель", "Vendor"), s.BoardVendor)
	}
	if s.BoardRevision != "" && !strings.Contains(s.BoardModel, s.BoardRevision) {
		r.kv(L("Ревизия", "Revision"), s.BoardRevision)
	}
	if s.Serial != "" {
		r.kv(L("Серийный номер", "Serial number"), s.Serial)
	}
	r.kv(L("Часовой пояс", "Time zone"), s.TimeZone)
	r.kv(L("Текущее время", "Current time"), fmt.Sprintf("%s (%s)", s.CurrentTime.Format("02.01.2006 15:04:05"), s.TimeZoneOffset))
	if loc.LangName != "" {
		r.kv(L("Язык системы", "System language"), fmt.Sprintf("%s (%s)", loc.LangName, loc.LangCode))
	}
	if loc.Layouts != "" {
		r.kv(L("Раскладки клавиатуры", "Keyboard layouts"), keyboardLayouts(loc))
	}
	if s.InstallTime != nil {
		r.kv(L("Время установки", "Install time"), s.InstallTime.Format("02.01.2006 15:04:05"))
	}
	r.kv(L("Время загрузки", "Boot time"), s.BootTime.Format("02.01.2006 15:04:05"))
	r.kv(L("Время работы", "Uptime"), humanDuration(s.Uptime))
	if s.InternetTime != nil {
		r.kv(L("Интернет-время", "Internet time"), fmt.Sprintf("%s UTC (%s)", s.InternetTime.Format("02.01.2006 15:04:05"), s.ClockDrift))
	}
}

func (r *renderer) renderCPU(c CPUInfo) {
	r.header(L("ПРОЦЕССОР", "CPU"))
	r.kv(L("Модель", "Model"), c.Model)
	if c.Vendor != "" {
		r.kv(L("Производитель", "Vendor"), c.Vendor)
	}
	r.kv(L("Ядер / потоков", "Cores / threads"), fmt.Sprintf("%d / %d", c.Cores, c.Threads))
	if c.CurFreqMHz > 0 {
		r.kv(L("Текущая частота", "Current frequency"), r.paint(cGreen, fmt.Sprintf("%.0f %s", c.CurFreqMHz, L("МГц", "MHz"))))
	}
	if c.MinFreqMHz > 0 || c.MaxFreqMHz > 0 {
		r.kv(L("Мин / макс частота", "Min / max frequency"), fmt.Sprintf("%.0f / %.0f %s", c.MinFreqMHz, c.MaxFreqMHz, L("МГц", "MHz")))
	}
	if c.Governor != "" {
		r.kv(L("Губернатор", "Governor"), governorHuman(c.Governor))
	}
	if c.BogoMIPS != "" {
		r.kv("BogoMIPS", c.BogoMIPS)
	}
	if len(c.Features) > 0 {
		r.kv(L("Флаги", "Features"), summarizeFeatures(c.Features))
	}
}

func (r *renderer) renderMemory(m MemoryInfo) {
	r.header(L("ОЗУ", "RAM"))
	r.kv(L("Всего ОЗУ", "Total RAM"), humanBytes(m.TotalKB*1024))
	r.kv(L("Использовано", "Used"), fmt.Sprintf("%s (%.0f%%)", humanBytes(m.UsedKB*1024), percent(m.UsedKB, m.TotalKB)))
	r.kv(L("Свободно", "Available"), humanBytes(m.AvailableKB*1024))
	r.kv(L("Буферы/кэш", "Buffers/cache"), fmt.Sprintf("%s / %s", humanBytes(m.BuffersKB*1024), humanBytes(m.CachedKB*1024)))

	// RAM details split over short lines so the block stays readable in a
	// narrow SSH/Android terminal. An sdram_freq overclock shows as "base / oc".
	spec := ramSpec{Type: m.RAMType, SpeedMTs: m.RAMSpeedMTs, BusBits: m.RAMBusBits}
	if t := formatRAMType(spec); t != "" {
		r.kv(L("Тип", "Type"), t)
	}
	if f := formatRAMFrequency(spec, m.RAMOCSpeedMTs); f != "" {
		r.kv(L("Частота", "Frequency"), f)
	}
	if s := formatRAMSpeed(spec, m.RAMOCSpeedMTs); s != "" {
		r.kv(L("Скорость", "Speed"), s)
	}

	// RAM frequency is only exposed on some SoCs; show it when present.
	if m.RAMCurMHz > 0 {
		r.kv(L("Частота ОЗУ (текущая)", "RAM frequency (current)"), fmt.Sprintf("%.0f %s", m.RAMCurMHz, L("МГц", "MHz")))
	}
	if m.RAMMinMHz > 0 || m.RAMMaxMHz > 0 {
		r.kv(L("Частота ОЗУ (мин/макс)", "RAM frequency (min/max)"), fmt.Sprintf("%.0f / %.0f %s", m.RAMMinMHz, m.RAMMaxMHz, L("МГц", "MHz")))
	}

	// Swap summary plus the individual backing devices.
	if m.SwapTotalKB > 0 {
		r.kv(L("Подкачка (swap)", "Swap"), fmt.Sprintf("%s %s %s", humanBytes(m.SwapUsedKB*1024), L("из", "of"), humanBytes(m.SwapTotalKB*1024)))
	} else {
		r.kv(L("Подкачка (swap)", "Swap"), r.paint(cDim, L("не используется", "not in use")))
	}

	if len(m.SwapDevices) > 0 {
		fmt.Println()
		fmt.Println("  " + r.paint(cBold, L("Устройства подкачки (/proc/swaps)", "Swap devices (/proc/swaps)")))
		for _, sw := range m.SwapDevices {
			fmt.Printf("    %s\n", r.paint(cBlue, sw.Filename))
			r.kvIndent(L("Тип", "Type"), sw.Type)
			r.kvIndent(L("Размер", "Size"), humanBytesShort(sw.SizeKB*1024))
			r.kvIndent(L("Использовано", "Used"), humanBytesShort(sw.UsedKB*1024))
			r.kvIndent(L("Приоритет", "Priority"), fmt.Sprintf("%d", sw.Priority))
			r.kvIndent(L("Расположение", "Location"), sw.Location)
		}
	}

	if len(m.Zram) > 0 {
		fmt.Println()
		fmt.Println("  " + r.paint(cBold, L("ZRAM (сжатие swap в ОЗУ)", "ZRAM (swap compressed in RAM)")))
		for _, z := range m.Zram {
			fmt.Printf("    %s\n", r.paint(cBlue, z.Name))
			r.kvIndent(L("Алгоритм сжатия", "Compression algorithm"), z.Algorithm)
			r.kvIndent(L("Занято", "Used"), fmt.Sprintf("%.1f%% (%s %s %s)", z.UsedPercent, humanBytesShort(z.OriginalBytes), L("из", "of"), humanBytesShort(z.DisksizeBytes)))
			r.kvIndent(L("В сжатом виде", "Compressed"), humanBytesShort(z.CompressedBytes))
			r.kvIndent(L("В реальности в ОЗУ", "Actual RAM used"), humanBytesShort(z.MemUsedTotalBytes))
			if z.CompressionRatio > 0 {
				r.kvIndent(L("Коэффициент сжатия", "Compression ratio"), r.paint(cGreen, fmt.Sprintf("%.2f×", z.CompressionRatio)))
			}
		}
	}
}

func (r *renderer) renderStorage(fs []Filesystem) {
	r.header(L("НАКОПИТЕЛИ", "STORAGE"))
	if len(fs) == 0 {
		fmt.Println("  " + r.paint(cDim, L("нет доступных данных", "no data available")))
		return
	}
	for _, f := range fs {
		bar := progressBar(f.UsedPct, 20)
		fmt.Printf("  %s  %s\n", r.paint(cBold, f.MountPoint), r.paint(cDim, fmt.Sprintf("%s · %s", f.Device, f.FSType)))
		if f.Model != "" || f.BusSpeed != "" || f.SDClass != "" {
			var parts []string
			if f.Model != "" {
				parts = append(parts, f.Model)
			}
			if f.SDClass != "" {
				parts = append(parts, f.SDClass)
			}
			if f.BusSpeed != "" {
				parts = append(parts, f.BusSpeed)
			}
			fmt.Printf("    %s\n", r.paint(cDim, strings.Join(parts, " · ")))
		}
		fmt.Printf("    %s  %s\n", bar, r.paint(cDim, fmt.Sprintf("%.0f%%", f.UsedPct)))
		fmt.Printf("    %s %s · %s %s · %s %s\n",
			humanBytesShort(f.UsedBytes), L("занято", "used"),
			humanBytesShort(f.FreeBytes), L("свободно", "free"),
			L("всего", "total"), humanBytesShort(f.TotalBytes))
		fmt.Println()
	}
}

func (r *renderer) renderNetwork(n NetworkInfo) {
	// Vendors already named by a local interface or access point, so the gateway
	// vendor is not repeated when the gateway is the same box as the AP.
	localVendors := map[string]bool{}
	for _, iface := range n.Interfaces {
		if iface.MACVendor != "" {
			localVendors[strings.ToLower(iface.MACVendor)] = true
		}
		if iface.Wireless != nil && iface.Wireless.BSSIDVendor != "" {
			localVendors[strings.ToLower(iface.Wireless.BSSIDVendor)] = true
		}
	}

	r.header(L("СЕТЬ", "NETWORK"))
	if len(n.DNSServers) > 0 {
		r.kv(L("DNS-серверы", "DNS servers"), strings.Join(n.DNSServers, ", "))
	}
	if len(n.SearchDomains) > 0 {
		r.kv(L("Домены поиска", "Search domains"), strings.Join(n.SearchDomains, ", "))
	}
	if n.GatewayIP != "" {
		gw := n.GatewayIP
		if n.GatewayMAC != "" {
			gw += "  " + r.paint(cDim, n.GatewayMAC)
		}
		r.kv(L("Шлюз", "Gateway"), gw)
		if v := n.GatewayVendor; v != "" && !localVendors[strings.ToLower(v)] {
			r.kv(L("Производитель шлюза", "Gateway vendor"), r.paint(cDim, v))
		}
	}
	fmt.Println()

	for _, iface := range n.Interfaces {
		kind := "LAN"
		switch {
		case iface.IsWireless:
			kind = "Wi-Fi"
		case iface.Virtual:
			kind = virtualKindLabel(iface.VKind)
		}
		state := r.paint(cDim, L("неизвестно", "unknown"))
		switch iface.State {
		case "up":
			state = r.paint(cGreen, L("активен", "up"))
		case "down":
			state = r.paint(cDim, L("не активен", "down"))
		}
		fmt.Printf("  %s [%s]  %s\n", r.paint(cBold, iface.Name), kind, state)
		if iface.MAC != "" {
			r.kvIndent(L("MAC-адрес", "MAC address"), iface.MAC)
			if iface.MACVendor != "" {
				r.kvIndent(L("Производитель", "Vendor"), iface.MACVendor)
			}
		}
		for _, ip := range iface.IPv4 {
			r.kvIndent("IPv4", ip)
		}
		for _, ip := range iface.IPv6 {
			r.kvIndent("IPv6", ip)
		}
		if iface.MTU > 0 {
			r.kvIndent("MTU", fmt.Sprintf("%d", iface.MTU))
		}
		if iface.RxBytes > 0 || iface.TxBytes > 0 {
			r.kvIndent(L("Принято / отправлено", "RX / TX"), fmt.Sprintf("%s / %s", humanBytes(iface.RxBytes), humanBytes(iface.TxBytes)))
		}

		if iface.IsWireless && iface.Wireless != nil {
			r.renderWireless(iface.Wireless)
		} else if !iface.Virtual {
			r.renderEthernet(iface)
		}
		fmt.Println()
	}

	// When a MAC is known but its vendor is not, the local OUI database is
	// missing the entry (or is absent), so point at the refresh command.
	if unresolvedVendors(n) {
		r.kv(L("Вендоры не распознаны", "Vendors unresolved"),
			r.paint(cDim, L("обновите базу: sysinfo --oui", "refresh the database: sysinfo --oui")))
	}
}

// unresolvedVendors reports whether any known MAC lacks a vendor name, which
// means the local OUI database could not resolve it.
func unresolvedVendors(n NetworkInfo) bool {
	if n.GatewayMAC != "" && n.GatewayVendor == "" {
		return true
	}
	for _, iface := range n.Interfaces {
		if iface.MAC != "" && iface.MACVendor == "" {
			return true
		}
		if iface.Wireless != nil && iface.Wireless.BSSID != "" && iface.Wireless.BSSIDVendor == "" {
			return true
		}
	}
	return false
}

// virtualKindLabel maps a virtual interface sub-type to a short bracketed tag.
func virtualKindLabel(kind string) string {
	switch kind {
	case "bridge":
		return L("мост", "bridge")
	case "veth":
		return "veth"
	case "tunnel":
		return L("туннель", "tunnel")
	case "wireguard":
		return "WireGuard"
	case "ppp":
		return "PPP"
	case "bond":
		return L("агрегация", "bond")
	case "vpn":
		return "VPN"
	}
	return L("виртуальный", "virtual")
}

func (r *renderer) renderEthernet(iface NetInterface) {
	if iface.SpeedMbps > 0 {
		r.kvIndent(L("Скорость линка", "Link speed"), fmt.Sprintf("%d %s", iface.SpeedMbps, L("Мбит/с", "Mbps")))
	} else if iface.State == "up" {
		r.kvIndent(L("Скорость линка", "Link speed"), r.paint(cDim, L("нет данных", "no data")))
	}
	if iface.Duplex != "" {
		r.kvIndent(L("Дуплекс", "Duplex"), duplexHuman(iface.Duplex))
	}
	if iface.State == "up" {
		carrier := r.paint(cGreen, L("кабель подключён", "cable connected"))
		if !iface.Carrier {
			carrier = r.paint(cYellow, L("нет сигнала", "no signal"))
		}
		r.kvIndent(L("Порт LAN", "LAN port"), carrier)
	}
}

func (r *renderer) renderWireless(w *WirelessInfo) {
	if w.SSID != "" {
		r.kvIndent(L("Точка доступа", "Access point"), r.paint(cGreen, w.SSID))
	} else if w.Connected {
		r.kvIndent(L("Точка доступа", "Access point"), L("(скрытая)", "(hidden)"))
	} else {
		r.kvIndent(L("Точка доступа", "Access point"), r.paint(cDim, L("не подключён", "not connected")))
	}
	if w.BSSID != "" {
		r.kvIndent(L("MAC точки доступа", "AP MAC (BSSID)"), w.BSSID)
		if w.BSSIDVendor != "" {
			r.kvIndent(L("Производитель", "Vendor"), w.BSSIDVendor)
		}
	}
	if w.Band != "" {
		r.kvIndent(L("Диапазон", "Band"), fmt.Sprintf("%s %s, %s %s", w.Band, unitLocal("GHz"), L("канал", "channel"), w.Channel))
	}
	if w.FrequencyMHz > 0 {
		r.kvIndent(L("Частота", "Frequency"), fmt.Sprintf("%d %s", w.FrequencyMHz, L("МГц", "MHz")))
	}
	if w.BitrateMbps > 0 {
		r.kvIndent(L("Скорость линка", "Link speed"), fmt.Sprintf("%.0f %s", w.BitrateMbps, L("Мбит/с", "Mbps")))
	}
	if w.WidthMHz > 0 {
		r.kvIndent(L("Ширина полосы", "Channel width"), fmt.Sprintf("%d %s", w.WidthMHz, L("МГц", "MHz")))
	}
	if w.Security != "" {
		r.kvIndent(L("Шифрование", "Encryption"), w.Security)
	}
	if w.SignalDBm != 0 {
		r.kvIndent(L("Сигнал", "Signal"), fmt.Sprintf("%d dBm (%d%%)", w.SignalDBm, w.SignalPct))
	}
	if w.TxPowerDBm != "" {
		r.kvIndent(L("Мощность передатчика", "Tx power"), w.TxPowerDBm)
	}
}

func (r *renderer) renderSensors(s SensorInfo) {
	r.header(L("ДАТЧИКИ", "SENSORS"))
	if s.TemperatureC > 0 {
		r.kv(L("Температура CPU", "CPU temperature"), r.tempColored(s.TemperatureC))
	}
	for _, tz := range s.ThermalZones {
		if s.TemperatureC > 0 && tz.TempC == s.TemperatureC {
			continue
		}
		r.kv(tz.Name, r.tempColored(tz.TempC))
	}
	for _, v := range s.Voltages {
		r.kv(L("Напряжение ", "Voltage ")+v.Name, fmt.Sprintf("%.4f %s", v.Value, unitLocal(v.Unit)))
	}
	for _, c := range s.Clocks {
		r.kv(L("Такт ", "Clock ")+c.Name, fmt.Sprintf("%.0f %s", c.Value, unitLocal(c.Unit)))
	}
	for _, f := range s.Fans {
		r.kv(L("Вентилятор ", "Fan ")+f.Name, fmt.Sprintf("%d %s", f.RPM, unitLocal("RPM")))
	}

	if s.Throttle != nil {
		fmt.Println()
		now, occurred := s.Throttle.describe()
		if s.Throttle.healthy() {
			r.kv(L("Состояние", "State"), r.paint(cGreen, L("норма — троттлинга и просадок питания не было", "OK — no throttling or undervoltage")))
		} else {
			if len(now) > 0 {
				r.kv(L("Сейчас", "Now"), r.paint(cRed, strings.Join(now, ", ")))
			}
			if len(occurred) > 0 {
				r.kv(L("Было с загрузки", "Since boot"), r.paint(cYellow, strings.Join(occurred, ", ")))
			}
		}
	} else if s.IsRaspberryPi {
		r.kv(L("Состояние", "State"), r.paint(cDim, L("vcgencmd недоступен", "vcgencmd unavailable")))
	}
}

func (r *renderer) renderDisplay(d DisplayInfo) {
	r.header(L("ДИСПЛЕЙ", "DISPLAY"))
	if len(d.Connectors) == 0 {
		fmt.Println("  " + r.paint(cDim, L("нет DRM-разъёмов", "no DRM connectors")))
		return
	}
	for _, c := range d.Connectors {
		status := r.paint(cDim, L("не подключён", "disconnected"))
		switch c.Status {
		case "connected":
			status = r.paint(cGreen, L("подключён", "connected"))
		case "unknown":
			status = r.paint(cDim, L("неизвестно", "unknown"))
		}
		r.kv(c.Name, status)
	}
	if len(d.Monitors) == 0 {
		if len(d.Connectors) > 0 {
			r.kv(L("Мониторы", "Monitors"), r.paint(cDim, L("EDID недоступен", "EDID unavailable")))
		}
		return
	}
	fmt.Println()
	for _, m := range d.Monitors {
		fmt.Printf("  %s\n", r.paint(cBold, fmt.Sprintf("%s (%s)", m.ProductName, m.Connector)))
		if m.Manufacturer != "" {
			r.kvIndent(L("Производитель", "Manufacturer"), m.Manufacturer)
		}
		if m.PreferredWidth > 0 {
			res := fmt.Sprintf("%d × %d", m.PreferredWidth, m.PreferredHeight)
			if m.RefreshHz > 0 {
				res += fmt.Sprintf(" @ %.0f %s", m.RefreshHz, L("Гц", "Hz"))
			}
			r.kvIndent(L("Разрешение", "Resolution"), res)
		}
		if m.SizeCmW > 0 && m.SizeCmH > 0 {
			r.kvIndent(L("Размер", "Size"),
				fmt.Sprintf("%.0f × %.0f %s", m.SizeCmW, m.SizeCmH, unitLocal("cm")))
		}
		if m.Serial != "" {
			r.kvIndent(L("Серийный номер", "Serial number"), m.Serial)
		}
		if m.Year > 0 {
			r.kvIndent(L("Год выпуска", "Year"), fmt.Sprintf("%d", m.Year))
		}
		if m.EDIDVersion != "" {
			r.kvIndent("EDID", m.EDIDVersion)
		}
	}
}

func (r *renderer) renderUSB(devs []USBDevice) {
	r.header("USB")
	if len(devs) == 0 {
		fmt.Println("  " + r.paint(cDim, L("USB-устройства не найдены", "no USB devices found")))
		return
	}
	for _, d := range devs {
		label := d.Product
		if label == "" {
			label = d.Name
		}
		line := r.paint(cBold, label)
		if d.Kind != "" {
			line += "  " + r.paint(cDim, "["+d.Kind+"]")
		}
		fmt.Printf("  %s\n", line)
		if d.Name != "" && d.Name != label {
			r.kvIndent(L("Идентификатор", "Name"), d.Name)
		}
		if d.Vendor != "" {
			r.kvIndent(L("Вендор", "Vendor"), d.Vendor)
		}
		if d.ID != "" {
			r.kvIndent("VID:PID", d.ID)
		}
		if d.Driver != "" {
			r.kvIndent(L("Драйвер", "Driver"), d.Driver)
		}
		if d.Speed != "" {
			r.kvIndent(L("Скорость", "Speed"), d.Speed)
		}
		if d.Serial != "" {
			r.kvIndent(L("Серийный номер", "Serial"), d.Serial)
		}
		fmt.Println()
	}
}

func (r *renderer) tempColored(t float64) string {
	s := fmt.Sprintf("%.1f °C", t)
	switch {
	case t >= 80:
		return r.paint(cRed, s)
	case t >= 70:
		return r.paint(cYellow, s)
	default:
		return r.paint(cGreen, s)
	}
}

func progressBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct / 100 * float64(width))
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

func percent(part, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

func archHuman(arch string) string {
	switch arch {
	case "arm64", "aarch64":
		return L("arm64 (64-бит ARM)", "arm64 (64-bit ARM)")
	case "arm":
		return L("arm (32-бит ARM)", "arm (32-bit ARM)")
	case "amd64":
		return "amd64 (x86-64)"
	case "386":
		return "386 (x86)"
	case "riscv64":
		return "riscv64"
	}
	return arch
}

func governorHuman(g string) string {
	switch g {
	case "ondemand":
		return L("ondemand (адаптивно)", "ondemand (adaptive)")
	case "conservative":
		return L("conservative (плавно)", "conservative (smooth)")
	case "performance":
		return L("performance (максимум)", "performance (max)")
	case "powersave":
		return L("powersave (энергосбережение)", "powersave (power saving)")
	case "schedutil":
		return L("schedutil (по планировщику)", "schedutil (scheduler-driven)")
	}
	return g
}

func duplexHuman(d string) string {
	switch strings.ToLower(d) {
	case "full":
		return L("полный", "full")
	case "half":
		return L("полу", "half")
	}
	return d
}

// keyboardLayouts renders the layout list with its variants, pairing each layout
// with the variant at the same index and dropping empty entries. RPiOS stores
// "us,ru" with a variant string like ",phonetic" where the first entry is blank.
func keyboardLayouts(loc LocaleInfo) string {
	layouts := strings.Split(loc.Layouts, ",")
	variants := strings.Split(loc.Variant, ",")
	var parts []string
	for i, name := range layouts {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		part := name
		if i < len(variants) {
			if v := strings.TrimSpace(variants[i]); v != "" {
				part += " (" + v + ")"
			}
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return loc.Layouts
	}
	return strings.Join(parts, ", ")
}

func summarizeFeatures(features []string) string {
	interesting := []string{"neon", "asimd", "fp", "vfp", "crc32", "sha1", "sha2", "aes", "sve", "sve2", "sse4_2", "avx2", "avx512"}
	var found []string
	for _, f := range features {
		for _, want := range interesting {
			if strings.EqualFold(f, want) {
				found = append(found, f)
			}
		}
	}
	if len(found) == 0 {
		return fmt.Sprintf(L("%d флагов", "%d features"), len(features))
	}
	return strings.Join(found, ", ")
}

// sessionLabel renders the session kind for display: SSH, SSH without a
// terminal, a graphical desktop or a plain local terminal. A remote session
// with no tty is a non-interactive SSH connection (sftp, scp, rsync, a tunnel);
// the data cannot say which, so the label stays generic.
func sessionLabel(s SessionInfo) string {
	switch {
	case s.ConnType == "ssh" && s.Line == "":
		return L("SSH без терминала", "SSH, no terminal")
	case s.ConnType == "ssh":
		return "SSH"
	case s.Type == "x11" || s.Type == "wayland":
		return L("рабочий стол", "desktop")
	default:
		return L("локальный терминал", "local terminal")
	}
}

func (r *renderer) renderUsers(sessions []SessionInfo) {
	title := L("ПОЛЬЗОВАТЕЛИ", "USERS")
	if len(sessions) > 0 {
		title += fmt.Sprintf(" (%d)", len(sessions))
	}
	r.header(title)
	if len(sessions) == 0 {
		fmt.Println("  " + r.paint(cDim, L("нет активных сессий", "no active sessions")))
		return
	}
	for _, s := range sessions {
		head := r.paint(cBold, s.User)
		head += "  " + r.paint(cDim, "["+sessionLabel(s)+"]")
		if s.Remote != "" {
			remote := s.Remote
			if s.RemoteDNS != "" {
				remote += " (" + s.RemoteDNS + ")"
			}
			head += "  " + r.paint(cGreen, remote)
		}
		fmt.Printf("  %s\n", head)
		if s.LoginTime != "" {
			r.kvIndent(L("Время входа", "Login time"), s.LoginTime)
		}
		if s.Idle != "" {
			r.kvIndent(L("Простой", "Idle"), s.Idle)
		}
		if s.Client != "" {
			r.kvIndent(L("Клиент", "Client"), s.Client)
		}
		if s.Detail != "" {
			label := L("Служба", "Service")
			if s.Type == "x11" || s.Type == "wayland" {
				label = L("Рабочий стол", "Desktop")
			}
			r.kvIndent(label, s.Detail)
		}
		if s.Line != "" {
			r.kvIndent(L("Терминал", "Terminal"), s.Line)
		}
		meta := ""
		if s.SessionID != "" {
			meta = s.SessionID
		}
		if s.PID > 0 {
			if meta != "" {
				meta += ", "
			}
			meta += fmt.Sprintf("PID %d", s.PID)
		}
		if meta != "" {
			r.kvIndent(L("Сессия", "Session"), meta)
		}
		fmt.Println()
	}
}
