package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// USBDevice describes one USB device found under /sys/bus/usb/devices.
type USBDevice struct {
	Name      string // sysfs device name, e.g. "1-1.2"
	Vendor    string // resolved vendor name (from usb.ids or sysfs)
	Product   string // resolved product name
	ID        string // VID:PID
	Kind      string // human device class, e.g. "клавиатура", "хаб", "накопитель"
	Driver    string // bound kernel driver, when known
	Speed     string // link speed, e.g. "480 Мбит/с"
	Serial    string
	Classes   []int       // all interface class codes, for combo devices
	Protocols map[int]int // class -> interface protocol (1 keyboard, 2 mouse)
}

// usbClassNames maps a USB base class code to a human label.
var usbClassNames = map[int]string{
	1:   "аудио",
	2:   "связь (CDC)",
	3:   "HID",
	5:   "физический",
	6:   "изображение",
	7:   "принтер",
	8:   "накопитель",
	9:   "хаб",
	10:  "CDC-данные",
	11:  "смарт-карта",
	13:  "контент-защита",
	14:  "видео",
	220: "диагностика",
	224: "беспроводной",
	239: "разное",
	254: "специфичный",
	255: "вендор",
}

// usbHIDSubclasses maps HID interface subclasses/protocols to a device kind.
// Protocol 1 = keyboard, 2 = mouse.
func usbKindFromClasses(classes []int, protocols map[int]int) string {
	kinds := map[string]bool{}
	for _, c := range classes {
		switch c {
		case 1:
			kinds[L("аудио", "audio")] = true
		case 3:
			// HID: refine with the protocol when we have it.
			switch protocols[3] {
			case 1:
				kinds[L("клавиатура", "keyboard")] = true
			case 2:
				kinds[L("мышь", "mouse")] = true
			default:
				kinds[L("HID-устройство", "HID device")] = true
			}
		case 6:
			kinds[L("камера/сканер", "camera/scanner")] = true
		case 7:
			kinds[L("принтер", "printer")] = true
		case 8:
			kinds[L("USB-накопитель", "USB storage")] = true
		case 9:
			kinds[L("хаб", "hub")] = true
		case 224:
			kinds[L("беспроводной адаптер (Bluetooth/WiFi)", "wireless adapter (Bluetooth/WiFi)")] = true
		case 10, 2:
			kinds[L("модем/сеть", "modem/network")] = true
		}
	}
	if len(kinds) == 0 {
		return ""
	}
	// Stable ordering so a combo dongle always reads the same way.
	order := []string{
		L("клавиатура", "keyboard"), L("мышь", "mouse"), L("HID-устройство", "HID device"),
		L("USB-накопитель", "USB storage"), L("хаб", "hub"), L("аудио", "audio"),
		L("камера/сканер", "camera/scanner"), L("принтер", "printer"),
		L("беспроводной адаптер (Bluetooth/WiFi)", "wireless adapter (Bluetooth/WiFi)"),
		L("модем/сеть", "modem/network"),
	}
	var out []string
	for _, k := range order {
		if kinds[k] {
			out = append(out, k)
		}
	}
	return strings.Join(out, " + ")
}

// collectUSBDevices walks /sys/bus/usb/devices and describes each device.
func collectUSBDevices() []USBDevice {
	entries, err := os.ReadDir("/sys/bus/usb/devices")
	if err != nil {
		return nil
	}
	var out []USBDevice
	for _, e := range entries {
		name := e.Name()
		// Skip interface nodes such as "1-1:1.0"; keep only device nodes.
		if strings.Contains(name, ":") {
			continue
		}
		base := filepath.Join("/sys/bus/usb/devices", name)
		if readFileTrim(filepath.Join(base, "idVendor")) == "" {
			continue
		}
		d := parseUSBDevice(base, name)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// parseUSBDevice reads one device directory. Split out so it can be tested.
func parseUSBDevice(base, name string) USBDevice {
	d := USBDevice{Name: name}
	vid := readFileTrim(filepath.Join(base, "idVendor"))
	pid := readFileTrim(filepath.Join(base, "idProduct"))
	if vid != "" && pid != "" {
		d.ID = strings.ToUpper(vid) + ":" + strings.ToUpper(pid)
	}
	d.Serial = readFileTrim(filepath.Join(base, "serial"))

	d.Vendor = readFileTrim(filepath.Join(base, "manufacturer"))
	d.Product = readFileTrim(filepath.Join(base, "product"))
	if d.Product == "" {
		d.Product = readFileTrim(filepath.Join(base, "interface"))
	}
	// Fill in names from the downloaded usb.ids database when available.
	if v, p := usbIDsLookup(vid, pid); v != "" || p != "" {
		if d.Vendor == "" {
			d.Vendor = v
		}
		if d.Product == "" || d.Product == d.Vendor {
			d.Product = p
		}
	}

	if s := readFileTrim(filepath.Join(base, "speed")); s != "" {
		if mbps := parseFloat(s); mbps > 0 {
			d.Speed = formatLinkSpeed(mbps)
		}
	}

	d.Classes, d.Protocols, d.Driver = usbInterfaces(base)
	d.Kind = usbKindFromClasses(d.Classes, d.Protocols)
	if d.Kind == "" {
		// Fall back to the device-level class (hubs often only set this).
		if c := int(parseUintHex(readFileTrim(filepath.Join(base, "bDeviceClass")))); c > 0 {
			if n, ok := usbClassNames[c]; ok {
				d.Kind = n
			}
		}
	}
	return d
}

// usbInterfaces returns the interface class codes, the class->protocol map and
// the first bound driver name.
func usbInterfaces(base string) (classes []int, protocols map[int]int, driver string) {
	protocols = map[int]int{}
	// Interfaces are separate directories named "<dev>:<config>.<iface>".
	name := filepath.Base(base)
	matches, _ := filepath.Glob(filepath.Join(base, "..", name+":*"))
	for _, m := range matches {
		c := int(parseUintHex(readFileTrim(filepath.Join(m, "bInterfaceClass"))))
		if c > 0 {
			classes = append(classes, c)
		}
		p := int(parseUintHex(readFileTrim(filepath.Join(m, "bInterfaceProtocol"))))
		if c > 0 && p > 0 {
			protocols[c] = p
		}
		if driver == "" {
			if link, err := os.Readlink(filepath.Join(m, "driver")); err == nil {
				driver = filepath.Base(link)
			}
		}
	}
	return
}

// formatLinkSpeed renders a USB link speed in the usual USB marketing units.
func formatLinkSpeed(mbps float64) string {
	switch {
	case mbps >= 10000:
		return "10 " + L("Гбит/с", "Gbps") + " (USB 3.1/3.2)"
	case mbps >= 5000:
		return "5 " + L("Гбит/с", "Gbps") + " (USB 3.0)"
	case mbps >= 1000:
		return L("1.5 Гбит/с (USB 3.x)", "1.5 Gbps (USB 3.x)")
	case mbps >= 480:
		return L("480 Мбит/с (USB 2.0)", "480 Mbps (USB 2.0)")
	case mbps >= 12:
		return L("12 Мбит/с (USB 1.1)", "12 Mbps (USB 1.1)")
	default:
		return strconv.FormatFloat(mbps, 'f', 0, 64) + L(" Мбит/с", " Mbps")
	}
}
