package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

const cmdTimeout = 3 * time.Second

func main() {
	var (
		asJSON  = flag.Bool("json", false, "вывести данные в формате JSON")
		noColor = flag.Bool("no-color", false, "отключить цветной вывод")
		section = flag.String("only", "", "показать только одну секцию: system|cpu|memory|storage|network|sensors|display")
		demo    = flag.Bool("demo", false, "показать пример вывода для Raspberry Pi 4B (без реального железа)")
	)
	flag.Parse()

	if !*noColor && os.Getenv("NO_COLOR") != "" {
		*noColor = true
	}

	var report Report
	if *demo {
		report = demoReport()
	} else {
		sys := collectSystemInfo()
		report = Report{
			System:    sys,
			CPU:       collectCPUInfo(),
			Memory:    collectMemoryInfo(),
			Storage:   collectFilesystems(),
			Network:   collectNetworkInfo(),
			Sensors:   collectSensors(sys),
			Display:   collectDisplayInfo(),
			Generated: time.Now(),
		}
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, "ошибка вывода JSON:", err)
			os.Exit(1)
		}
		return
	}

	r := newRenderer(*noColor)
	r.render(report, *section)
}

// Report is the complete machine-readable snapshot.
type Report struct {
	System    SystemInfo   `json:"system"`
	CPU       CPUInfo      `json:"cpu"`
	Memory    MemoryInfo   `json:"memory"`
	Storage   []Filesystem `json:"storage"`
	Network   NetworkInfo  `json:"network"`
	Sensors   SensorInfo   `json:"sensors"`
	Display   DisplayInfo  `json:"display"`
	Generated time.Time    `json:"generated"`
}

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

func (r *renderer) kv(key, val string) {
	if val == "" {
		return
	}
	fmt.Printf("  %s %s\n", r.paint(cDim, key+":"), val)
}

// kvIndent is like kv but indents the label, used for nested details.
func (r *renderer) kvIndent(key, val string) {
	if val == "" {
		return
	}
	fmt.Printf("    %s %s\n", r.paint(cDim, key+":"), val)
}

func (r *renderer) render(rep Report, only string) {
	want := func(name string) bool { return only == "" || only == name }

	if want("system") {
		r.renderSystem(rep.System)
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
	fmt.Println()
}

func (r *renderer) renderSystem(s SystemInfo) {
	r.header("СИСТЕМА")
	r.kv("Имя узла", s.Hostname)
	r.kv("ОС", s.OSName)
	r.kv("Ядро", s.KernelVersion)
	r.kv("Архитектура", archHuman(s.Arch))
	if s.BoardModel != "" {
		r.kv("Плата", s.BoardModel)
	}
	if s.BoardVendor != "" {
		r.kv("Производитель", s.BoardVendor)
	}
	if s.BoardRevision != "" && !strings.Contains(s.BoardModel, s.BoardRevision) {
		r.kv("Ревизия", s.BoardRevision)
	}
	if s.Serial != "" {
		r.kv("Серийный номер", s.Serial)
	}
	r.kv("Часовой пояс", s.TimeZone)
	r.kv("Время загрузки", s.BootTime.Format("02.01.2006 15:04:05"))
	r.kv("Время работы", humanDuration(s.Uptime))
}

func (r *renderer) renderCPU(c CPUInfo) {
	r.header("ПРОЦЕССОР")
	r.kv("Модель", c.Model)
	if c.Vendor != "" {
		r.kv("Производитель", c.Vendor)
	}
	r.kv("Ядер / потоков", fmt.Sprintf("%d / %d", c.Cores, c.Threads))
	if c.CurFreqMHz > 0 {
		r.kv("Текущая частота", fmt.Sprintf("%s", r.paint(cGreen, fmt.Sprintf("%.0f МГц", c.CurFreqMHz))))
	}
	if c.MinFreqMHz > 0 || c.MaxFreqMHz > 0 {
		r.kv("Мин / макс частота", fmt.Sprintf("%.0f / %.0f МГц", c.MinFreqMHz, c.MaxFreqMHz))
	}
	if c.Governor != "" {
		r.kv("Губернатор", governorHuman(c.Governor))
	}
	if c.BogoMIPS != "" {
		r.kv("BogoMIPS", c.BogoMIPS)
	}
	if len(c.Features) > 0 {
		r.kv("Флаги", summarizeFeatures(c.Features))
	}
}

func (r *renderer) renderMemory(m MemoryInfo) {
	r.header("ПАМЯТЬ")
	r.kv("Всего ОЗУ", humanBytes(m.TotalKB*1024))
	r.kv("Использовано", fmt.Sprintf("%s (%.0f%%)", humanBytes(m.UsedKB*1024), percent(m.UsedKB, m.TotalKB)))
	r.kv("Свободно", humanBytes(m.AvailableKB*1024))
	r.kv("Буферы/кэш", fmt.Sprintf("%s / %s", humanBytes(m.BuffersKB*1024), humanBytes(m.CachedKB*1024)))
	if m.SwapTotalKB > 0 {
		r.kv("Подкачка (swap)", fmt.Sprintf("%s из %s", humanBytes(m.SwapUsedKB*1024), humanBytes(m.SwapTotalKB*1024)))
	} else {
		r.kv("Подкачка (swap)", r.paint(cDim, "не используется"))
	}

	if len(m.Zram) > 0 {
		fmt.Println()
		fmt.Println("  " + r.paint(cBold, "ZRAM (сжатие swap в ОЗУ)"))
		for _, z := range m.Zram {
			fmt.Printf("    %s\n", r.paint(cBlue, z.Name))
			r.kvIndent("Алгоритм сжатия", z.Algorithm)
			r.kvIndent("Занято", fmt.Sprintf("%.1f%% (%s из %s)", z.UsedPercent, humanBytesShort(z.OriginalBytes), humanBytesShort(z.DisksizeBytes)))
			r.kvIndent("В сжатом виде", humanBytesShort(z.CompressedBytes))
			r.kvIndent("В реальности в ОЗУ", humanBytesShort(z.MemUsedTotalBytes))
			if z.CompressionRatio > 0 {
				r.kvIndent("Коэффициент сжатия", r.paint(cGreen, fmt.Sprintf("%.2f×", z.CompressionRatio)))
			}
		}
	}
}

func (r *renderer) renderStorage(fs []Filesystem) {
	r.header("НАКОПИТЕЛИ")
	if len(fs) == 0 {
		fmt.Println("  " + r.paint(cDim, "нет доступных данных"))
		return
	}
	for _, f := range fs {
		bar := progressBar(f.UsedPct, 20)
		label := f.MountPoint
		extra := fmt.Sprintf("%s · %s", f.Device, f.FSType)
		fmt.Printf("  %s  %s\n", r.paint(cBold, label), r.paint(cDim, extra))
		fmt.Printf("    %s  %s\n", bar, r.paint(cDim, fmt.Sprintf("%.0f%%", f.UsedPct)))
		fmt.Printf("    %s занято · %s свободно · всего %s\n",
			humanBytesShort(f.UsedBytes), humanBytesShort(f.FreeBytes), humanBytesShort(f.TotalBytes))
		fmt.Println()
	}
}

func (r *renderer) renderNetwork(n NetworkInfo) {
	r.header("СЕТЬ")
	if len(n.DNSServers) > 0 {
		r.kv("DNS-серверы", strings.Join(n.DNSServers, ", "))
	}
	if len(n.SearchDomains) > 0 {
		r.kv("Домены поиска", strings.Join(n.SearchDomains, ", "))
	}
	fmt.Println()

	for _, iface := range n.Interfaces {
		kind := "LAN"
		if iface.IsWireless {
			kind = "Wi-Fi"
		}
		state := r.paint(cGreen, "активен")
		if iface.State != "up" {
			state = r.paint(cDim, "не активен")
		}
		fmt.Printf("  %s [%s]  %s\n", r.paint(cBold, iface.Name), kind, state)
		if iface.MAC != "" {
			r.kvIndent("MAC-адрес", iface.MAC)
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

		if iface.IsWireless && iface.Wireless != nil {
			r.renderWireless(iface.Wireless)
		} else {
			r.renderEthernet(iface)
		}
		fmt.Println()
	}
}

func (r *renderer) renderEthernet(iface NetInterface) {
	if iface.SpeedMbps > 0 {
		r.kvIndent("Скорость линка", fmt.Sprintf("%d Мбит/с", iface.SpeedMbps))
	} else if iface.State == "up" {
		r.kvIndent("Скорость линка", r.paint(cDim, "нет данных"))
	}
	if iface.Duplex != "" {
		r.kvIndent("Дуплекс", duplexHuman(iface.Duplex))
	}
	if iface.State == "up" {
		carrier := r.paint(cGreen, "кабель подключён")
		if !iface.Carrier {
			carrier = r.paint(cYellow, "нет сигнала")
		}
		r.kvIndent("Порт LAN", carrier)
	}
}

func (r *renderer) renderWireless(w *WirelessInfo) {
	if w.SSID != "" {
		r.kvIndent("Точка доступа", r.paint(cGreen, w.SSID))
	} else if w.Connected {
		r.kvIndent("Точка доступа", "(скрытая)")
	} else {
		r.kvIndent("Точка доступа", r.paint(cDim, "не подключён"))
	}
	if w.Band != "" {
		r.kvIndent("Диапазон", fmt.Sprintf("%s, канал %s", w.Band, w.Channel))
	}
	if w.FrequencyMHz > 0 {
		r.kvIndent("Частота", fmt.Sprintf("%d МГц", w.FrequencyMHz))
	}
	if w.BitrateMbps > 0 {
		r.kvIndent("Скорость линка", fmt.Sprintf("%.0f Мбит/с", w.BitrateMbps))
	}
	if w.WidthMHz > 0 {
		r.kvIndent("Ширина полосы", fmt.Sprintf("%d МГц", w.WidthMHz))
	}
	if w.Security != "" {
		r.kvIndent("Шифрование", w.Security)
	}
	if w.SignalDBm != 0 {
		r.kvIndent("Сигнал", fmt.Sprintf("%d dBm (%d%%)", w.SignalDBm, w.SignalPct))
	}
	if w.TxPowerDBm != "" {
		r.kvIndent("Мощность передатчика", w.TxPowerDBm)
	}
}

func (r *renderer) renderSensors(s SensorInfo) {
	r.header("ДАТЧИКИ")
	if s.TemperatureC > 0 {
		r.kv("Температура CPU", r.tempColored(s.TemperatureC))
	}
	for _, tz := range s.ThermalZones {
		if s.TemperatureC > 0 && tz.TempC == s.TemperatureC {
			continue
		}
		r.kv(tz.Name, r.tempColored(tz.TempC))
	}
	for _, v := range s.Voltages {
		r.kv("Напряжение "+v.Name, fmt.Sprintf("%.4f %s", v.Value, v.Unit))
	}
	for _, c := range s.Clocks {
		r.kv("Такт "+c.Name, fmt.Sprintf("%.0f %s", c.Value, c.Unit))
	}
	for _, f := range s.Fans {
		r.kv("Вентилятор "+f.Name, fmt.Sprintf("%d об/мин", f.RPM))
	}

	if s.Throttle != nil {
		fmt.Println()
		now, occurred := s.Throttle.describe()
		if s.Throttle.healthy() {
			r.kv("Состояние", r.paint(cGreen, "норма — троттлинга и просадок питания не было"))
		} else {
			if len(now) > 0 {
				r.kv("Сейчас", r.paint(cRed, strings.Join(now, ", ")))
			}
			if len(occurred) > 0 {
				r.kv("Было с загрузки", r.paint(cYellow, strings.Join(occurred, ", ")))
			}
		}
	} else if s.IsRaspberryPi {
		r.kv("Состояние", r.paint(cDim, "vcgencmd недоступен"))
	}
}

func (r *renderer) renderDisplay(d DisplayInfo) {
	r.header("ДИСПЛЕЙ")
	if len(d.Connectors) == 0 {
		fmt.Println("  " + r.paint(cDim, "нет DRM-разъёмов"))
		return
	}
	for _, c := range d.Connectors {
		status := r.paint(cDim, "не подключён")
		if c.Status == "connected" {
			status = r.paint(cGreen, "подключён")
		}
		r.kv(c.Name, status)
	}
	if len(d.Monitors) == 0 {
		if len(d.Connectors) > 0 {
			r.kv("Мониторы", r.paint(cDim, "EDID недоступен"))
		}
		return
	}
	fmt.Println()
	for _, m := range d.Monitors {
		fmt.Printf("  %s\n", r.paint(cBold, fmt.Sprintf("%s (%s)", m.ProductName, m.Connector)))
		if m.Manufacturer != "" {
			r.kvIndent("Производитель", m.Manufacturer)
		}
		if m.PreferredWidth > 0 {
			res := fmt.Sprintf("%d × %d", m.PreferredWidth, m.PreferredHeight)
			if m.RefreshHz > 0 {
				res += fmt.Sprintf(" @ %.0f Гц", m.RefreshHz)
			}
			r.kvIndent("Разрешение", res)
		}
		if m.SizeCm != "" {
			r.kvIndent("Размер", m.SizeCm)
		}
		if m.Serial != "" {
			r.kvIndent("Серийный номер", m.Serial)
		}
		if m.Year > 0 {
			r.kvIndent("Год выпуска", fmt.Sprintf("%d", m.Year))
		}
		if m.EDIDVersion != "" {
			r.kvIndent("EDID", m.EDIDVersion)
		}
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
	bar := "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
	return bar
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
		return "arm64 (64-бит ARM)"
	case "arm":
		return "arm (32-бит ARM)"
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
		return "ondemand (адаптивно)"
	case "conservative":
		return "conservative (плавно)"
	case "performance":
		return "performance (максимум)"
	case "powersave":
		return "powersave (энергосбережение)"
	case "schedutil":
		return "schedutil (по планировщику)"
	}
	return g
}

func duplexHuman(d string) string {
	switch strings.ToLower(d) {
	case "full":
		return "полный"
	case "half":
		return "полу"
	}
	return d
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
		return fmt.Sprintf("%d флагов", len(features))
	}
	return strings.Join(found, ", ")
}
