package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const cmdTimeout = 3 * time.Second

// options holds the parsed command line.
type options struct {
	json     bool
	noColor  bool
	demo     bool
	install  bool
	sections map[string]bool
}

func main() {
	opts := parseFlags()

	// Language selection: --ru / --en force it, otherwise detect from the system.
	if *flagRU {
		langCode = "ru"
	} else if *flagEN {
		langCode = "en"
	} else {
		langCode = detectLanguage()
	}

	if *flagHelp || *flagH {
		printHelp()
		return
	}

	if opts.install {
		if err := installSelf(); err != nil {
			fmt.Fprintln(os.Stderr, "install:", err)
			os.Exit(1)
		}
		return
	}

	// One-shot maintenance commands that only touch the local databases.
	if *flagVendor || *flagV {
		if err := updateVendorDB(); err != nil {
			fmt.Fprintln(os.Stderr, "vendor:", err)
			os.Exit(1)
		}
		return
	}
	if *flagOUI || *flagO {
		if err := updateOUIDB(); err != nil {
			fmt.Fprintln(os.Stderr, "oui:", err)
			os.Exit(1)
		}
		return
	}

	var report Report
	if opts.demo {
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
			USB:       collectUSBDevices(),
			Sessions:  collectSessions(),
			Locale:    collectLocale(),
			Generated: time.Now(),
		}
	}

	if opts.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, "json:", err)
			os.Exit(1)
		}
		return
	}

	r := newRenderer(opts.noColor)
	r.render(report, opts.sections)
}

var (
	flagRU     = flag.Bool("ru", false, L("принудительно русский вывод", "force Russian output"))
	flagEN     = flag.Bool("en", false, L("принудительно английский вывод", "force English output"))
	flagHelp   = flag.Bool("help", false, L("показать эту справку", "show this help"))
	flagH      = flag.Bool("h", false, L("показать эту справку", "show this help"))
	flagVendor = flag.Bool("vendor", false, L("обновить базу USB-вендоров (usb.ids)", "refresh the USB vendor database (usb.ids)"))
	flagV      = flag.Bool("v", false, L("то же, что --vendor", "same as --vendor"))
	flagOUI    = flag.Bool("oui", false, L("обновить базу MAC-вендоров (IEEE OUI)", "refresh the IEEE OUI MAC vendor database"))
	flagO      = flag.Bool("o", false, L("то же, что --oui", "same as --oui"))
	flagInst   = flag.Bool("install", false, L("установить утилиту в /usr/local/bin/sysinfo", "install the tool as /usr/local/bin/sysinfo"))
	flagI      = flag.Bool("i", false, L("то же, что --install", "same as --install"))

	flagSystem  = flag.Bool("system", false, L("только блок СИСТЕМА", "only the SYSTEM block"))
	flagSys     = flag.Bool("sys", false, L("то же, что --system", "same as --system"))
	flagCPU     = flag.Bool("cpu", false, L("только блок ПРОЦЕССОР", "only the CPU block"))
	flagC       = flag.Bool("c", false, L("то же, что --cpu", "same as --cpu"))
	flagRAM     = flag.Bool("ram", false, L("только блок ПАМЯТЬ", "only the MEMORY block"))
	flagR       = flag.Bool("r", false, L("то же, что --ram", "same as --ram"))
	flagStorage = flag.Bool("storage", false, L("только блок НАКОПИТЕЛИ", "only the STORAGE block"))
	flagS       = flag.Bool("s", false, L("то же, что --storage", "same as --storage"))
	flagNetwork = flag.Bool("network", false, L("только блок СЕТЬ", "only the NETWORK block"))
	flagN       = flag.Bool("n", false, L("то же, что --network", "same as --network"))
	flagSensors = flag.Bool("sensors", false, L("только блок ДАТЧИКИ", "only the SENSORS block"))
	flagDisplay = flag.Bool("display", false, L("только блок ДИСПЛЕЙ", "only the DISPLAY block"))
	flagD       = flag.Bool("d", false, L("то же, что --display", "same as --display"))
	flagUSB     = flag.Bool("usb", false, L("только блок USB", "only the USB block"))
	flagU       = flag.Bool("u", false, L("то же, что --usb", "same as --usb"))
	flagUsers   = flag.Bool("users", false, L("только блок ПОЛЬЗОВАТЕЛИ", "only the USERS block"))
	flagSess    = flag.Bool("sessions", false, L("то же, что --users", "same as --users"))
)

func parseFlags() options {
	// Register the remaining flags here so the help text stays in one place.
	asJSON := flag.Bool("json", false, L("вывести данные в формате JSON", "print the report as JSON"))
	noColor := flag.Bool("no-color", false, L("отключить цветной вывод", "disable colored output"))
	section := flag.String("only", "", L("показать одну секцию (устаревший аналог флагов ниже)", "show a single section (legacy alias of the flags below)"))
	demo := flag.Bool("demo", false, L("показать пример вывода для Raspberry Pi 4B", "show a sample Raspberry Pi 4B report"))
	flag.Parse()

	if !*noColor && os.Getenv("NO_COLOR") != "" {
		*noColor = true
	}

	sections := map[string]bool{}
	add := func(name string, flags ...*bool) {
		for _, f := range flags {
			if *f {
				sections[name] = true
			}
		}
	}
	add("system", flagSystem, flagSys)
	add("cpu", flagCPU, flagC)
	add("memory", flagRAM, flagR)
	add("storage", flagStorage, flagS)
	add("network", flagNetwork, flagN)
	add("sensors", flagSensors)
	add("display", flagDisplay, flagD)
	add("usb", flagUSB, flagU)
	add("users", flagUsers, flagSess)

	if *section != "" {
		// Accept the legacy names, mapping the old "memory"/"display" too.
		for _, s := range strings.Split(*section, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				sections[s] = true
			}
		}
	}

	return options{
		json:     *asJSON,
		noColor:  *noColor,
		demo:     *demo,
		install:  *flagInst || *flagI,
		sections: sections,
	}
}

// Report is the complete machine-readable snapshot.
type Report struct {
	System    SystemInfo    `json:"system"`
	CPU       CPUInfo       `json:"cpu"`
	Memory    MemoryInfo    `json:"memory"`
	Storage   []Filesystem  `json:"storage"`
	Network   NetworkInfo   `json:"network"`
	Sensors   SensorInfo    `json:"sensors"`
	Display   DisplayInfo   `json:"display"`
	USB       []USBDevice   `json:"usb"`
	Sessions  []SessionInfo `json:"sessions"`
	Locale    LocaleInfo    `json:"locale"`
	Generated time.Time     `json:"generated"`
}

// installSelf copies the running binary to /usr/local/bin/sysinfo.
func installSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	dst := "/usr/local/bin/sysinfo"

	if os.Geteuid() != 0 {
		return fmt.Errorf("%s", L(
			"нужны права root: sudo "+exe+" --install",
			"root is required: sudo "+exe+" --install"))
	}

	src, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	fmt.Println(L("установлено в "+dst, "installed to "+dst))
	fmt.Println(L("запуск из любой папки: sysinfo", "run from anywhere: sysinfo"))
	return nil
}

func printHelp() {
	h := `sysinfo — ` + L("сводка о системе для Linux и Raspberry Pi",
		"system summary for Linux and Raspberry Pi") + `

` + L("Использование", "Usage") + `:
  sysinfo [--flags]

` + L("Секции (можно комбинировать)", "Sections (combinable)") + `:
  --system, -sys      ` + L("блок СИСТЕМА", "SYSTEM block") + `
  --cpu, -c           ` + L("блок ПРОЦЕССОР", "CPU block") + `
  --ram, -r           ` + L("блок ПАМЯТЬ (включая swap и ZRAM)", "MEMORY block (incl. swap and ZRAM)") + `
  --storage, -s       ` + L("блок НАКОПИТЕЛИ", "STORAGE block") + `
  --network, -n       ` + L("блок СЕТЬ", "NETWORK block") + `
  --sensors           ` + L("блок ДАТЧИКИ", "SENSORS block") + `
  --display, -d       ` + L("блок ДИСПЛЕЙ", "DISPLAY block") + `
  --usb, -u           ` + L("блок USB", "USB block") + `
  --users             ` + L("блок ПОЛЬЗОВАТЕЛИ (подключённые сессии)", "USERS block (logged-in sessions)") + `

` + L("Прочее", "Other") + `:
  --json              ` + L("вывод в формате JSON", "print the report as JSON") + `
  --no-color          ` + L("без цвета", "disable color") + `
  --ru / --en         ` + L("принудительный язык вывода", "force the output language") + `
  --demo              ` + L("пример вывода для Raspberry Pi 4B", "sample Raspberry Pi 4B report") + `
  --install, -i       ` + L("установить в /usr/local/bin/sysinfo (нужен root)", "install as /usr/local/bin/sysinfo (needs root)") + `
  --vendor, -v        ` + L("обновить базу USB-вендоров (usb.ids)", "refresh the USB vendor database (usb.ids)") + `
  --oui, -o           ` + L("обновить базу MAC-вендоров (IEEE OUI)", "refresh the IEEE OUI MAC vendor database") + `
  --help, -h          ` + L("эта справка", "this help") + `

` + L("Примеры", "Examples") + `:
  sysinfo                     ` + L("полная сводка", "full report") + `
  sysinfo --cpu --ram         ` + L("только процессор и память", "CPU and memory only") + `
  sysinfo -n -d               ` + L("сеть и дисплей", "network and display") + `
  sysinfo --json | jq .       ` + L("машиночитаемый вывод", "machine-readable output")
	fmt.Println(h)
}
