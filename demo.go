package main

import "time"

// demoReport returns a realistic Raspberry Pi 4B snapshot so the layout can be
// previewed on any machine without actual Pi hardware. It mirrors the setup in
// the field report: a swap file plus zram, a combined keyboard/mouse dongle and
// a Wi-Fi uplink.
func demoReport() Report {
	return Report{
		System: SystemInfo{
			Hostname:      "RaspiOS",
			OSName:        "Debian GNU/Linux 13 (trixie)",
			KernelVersion: "6.18.50+rpt-rpi-v8",
			Arch:          "arm64",
			BoardModel:    "Raspberry Pi 4 Model B Rev 1.2",
			BoardVendor:   "Raspberry Pi Foundation",
			BoardRevision: "c03112",
			Serial:        "100000006e648f0b",
			BootTime:      time.Now().Add(-12*time.Hour - 22*time.Minute),
			Uptime:        12*time.Hour + 22*time.Minute + 17*time.Second,
			TimeZone:      "Europe/Moscow",
		},
		Locale: LocaleInfo{
			LangName: languageName("ru"),
			LangCode: "ru",
			Layouts:  "us,ru",
			Variant:  ",phonetic",
		},
		CPU: CPUInfo{
			Model:        "Cortex-A72",
			Vendor:       "ARM",
			Architecture: "arm64",
			Cores:        4,
			Threads:      4,
			BogoMIPS:     "108.00",
			CurFreqMHz:   2000,
			MinFreqMHz:   600,
			MaxFreqMHz:   2000,
			Governor:     "ondemand",
			Features:     []string{"fp", "asimd", "evtstrm", "crc32", "sha1", "sha2", "aes"},
		},
		Memory: MemoryInfo{
			TotalKB:     3891532,
			AvailableKB: 2830000,
			FreeKB:      1700000,
			BuffersKB:   186000,
			CachedKB:    1015521,
			UsedKB:      1101532,
			SwapTotalKB: 10485760,
			SwapFreeKB:  10485760,
			SwapUsedKB:  0,
			Zram: []ZramDevice{{
				Name:              "zram0",
				Algorithm:         "zstd",
				DisksizeBytes:     2147483648,
				OriginalBytes:     4096,
				CompressedBytes:   64,
				MemUsedTotalBytes: 20480,
				CompressionRatio:  64.0,
				UsedPercent:       0.0,
			}},
			SwapDevices: []SwapDevice{
				{Filename: "/swapfile", Type: "file", SizeKB: 8392700, UsedKB: 0, Priority: 10,
					Location: L("файл на /dev/mmcblk0p2", "file on /dev/mmcblk0p2")},
				{Filename: "/dev/zram0", Type: "partition", SizeKB: 2097148, UsedKB: 0, Priority: 100,
					Location: L("ZRAM (сжатие в ОЗУ)", "ZRAM (compressed in RAM)")},
			},
		},
		Storage: []Filesystem{
			{MountPoint: "/", Device: "/dev/mmcblk0p2", FSType: "ext4", TotalBytes: 61_000_000_000, UsedBytes: 41_980_000_000, FreeBytes: 16_428_000_000, UsedPct: 69,
				Model: "SD64G", SDClass: L("Class 10 (10 МБ/с)", "Class 10 (10 MB/s)"), BusSpeed: "SDR104 (UHS-I, 104 " + L("МБ/с", "MB/s") + ")"},
			{MountPoint: "/boot/firmware", Device: "/dev/mmcblk0p1", FSType: "vfat", TotalBytes: 529_000_000, UsedBytes: 82_800_000, FreeBytes: 446_700_000, UsedPct: 16,
				Model: "SD64G"},
			{MountPoint: "/media/raspi/PERS_X86-64FRE_EN-RU_DV91", Device: "/dev/sda1", FSType: "ntfs3", TotalBytes: 31_200_000_000, UsedBytes: 5_900_000_000, FreeBytes: 25_400_000_000, UsedPct: 19,
				Model: "SanDisk Ultra Flair", BusSpeed: "480 " + L("Мбит/с", "Mbps")},
		},
		Network: NetworkInfo{
			DNSServers:    []string{"1.1.1.1", "8.8.8.8", "94.140.14.14", "94.140.14.59"},
			GatewayIP:     "192.168.100.1",
			GatewayMAC:    "a4:2b:b0:11:22:33",
			GatewayVendor: "TP-LINK TECHNOLOGIES CO.,LTD.",
			Interfaces: []NetInterface{
				{
					Name: "eth0", MAC: "dc:a6:32:5a:d5:62", MACVendor: "Raspberry Pi Trading Ltd",
					State: "down", MTU: 1500,
				},
				{
					Name: "wlan0", MAC: "dc:a6:32:5a:d5:63", MACVendor: "Raspberry Pi Trading Ltd",
					State: "up", MTU: 1500, IsWireless: true,
					IPv4:    []string{"192.168.100.85/24"},
					IPv6:    []string{"fe80::bc0e:b6cd:c4d7:172d/64"},
					RxBytes: 1_284_000_000, TxBytes: 214_000_000,
					Wireless: &WirelessInfo{
						SSID: "Gaspar", Connected: true,
						BSSID: "a4:2b:b0:11:22:34", BSSIDVendor: "TP-LINK TECHNOLOGIES CO.,LTD.",
						FrequencyMHz: 2417, Band: "2.4", Channel: "2",
						BitrateMbps: 0, WidthMHz: 40, Security: "WPA2",
						SignalDBm: -58, SignalPct: 84, TxPowerDBm: "31.00 dBm",
					},
				},
			},
		},
		Sensors: SensorInfo{
			IsRaspberryPi: true,
			TemperatureC:  52.5,
			Voltages: []SensorValue{
				{Name: voltLabel("core"), Value: 0.9938, Unit: "V"},
				{Name: voltLabel("sdram_c"), Value: 1.1000, Unit: "V"},
				{Name: voltLabel("sdram_i"), Value: 1.1000, Unit: "V"},
				{Name: voltLabel("sdram_p"), Value: 1.1000, Unit: "V"},
			},
			Clocks: []SensorValue{
				{Name: clockLabel("arm"), Value: 2000, Unit: "MHz"},
				{Name: clockLabel("core"), Value: 500, Unit: "MHz"},
				{Name: clockLabel("v3d"), Value: 500, Unit: "MHz"},
				{Name: clockLabel("pixel"), Value: 150, Unit: "MHz"},
			},
			Throttle: &ThrottleState{Raw: 0x50005, UnderVoltageOccurred: true, ThrottledOccurred: true},
			ThermalZones: []ThermalZone{
				{Name: humanThermalName("cpu-thermal"), TempC: 52.6},
			},
		},
		Display: DisplayInfo{
			Connectors: []Connector{
				{Name: "HDMI-A-1", Status: "connected", Enabled: "enabled"},
				{Name: "HDMI-A-2", Status: "disconnected"},
				{Name: "Writeback-1", Status: "unknown"},
			},
			Monitors: []Monitor{{
				Connector: "HDMI-A-1", Manufacturer: "SGT", ProductName: "P158FT",
				Year: 2023, EDIDVersion: "1.3",
				PreferredWidth: 1920, PreferredHeight: 1080, RefreshHz: 60, SizeCmW: 34, SizeCmH: 19,
			}},
		},
		USB: []USBDevice{
			{Name: "1-1.2", Vendor: "Logitech, Inc.", Product: "USB Receiver", ID: "046D:C534",
				Kind: usbKindFromClasses([]int{3}, map[int]int{3: 1}) + " + " + usbKindFromClasses([]int{3}, map[int]int{3: 2}), Driver: "usbhid", Speed: formatLinkSpeed(12)},
			{Name: "1-1.3", Vendor: "SanDisk Corp.", Product: "Ultra Flair", ID: "0781:5583",
				Kind: usbKindFromClasses([]int{8}, nil), Driver: "usb-storage", Speed: formatLinkSpeed(480),
				Serial: "4C530001120412117334"},
			{Name: "1-1", Vendor: "Terminus Technology Inc.", Product: "Hub", ID: "1A40:0101",
				Kind: usbKindFromClasses([]int{9}, nil), Driver: "hub", Speed: formatLinkSpeed(480)},
		},
		Sessions: []SessionInfo{
			{
				User: "raspi", Line: "tty1", ConnType: "local",
				LoginTime: time.Now().Add(-12 * time.Hour).Format("02.01.2006 15:04:05"),
				SessionID: "1", PID: 812,
			},
			{
				User: "raspi", Line: "pts/0", ConnType: "ssh", Remote: "192.168.100.42",
				RemoteDNS: "desktop.lan",
				LoginTime: time.Now().Add(-3 * time.Hour).Format("02.01.2006 15:04:05"),
				Client:    "OpenSSH_9.6p1 Ubuntu-3ubuntu13", SessionID: "3", PID: 1842,
			},
			{
				User: "admin", Line: "pts/1", ConnType: "ssh", Remote: "10.8.0.5",
				LoginTime: time.Now().Add(-25 * time.Minute).Format("02.01.2006 15:04:05"),
				Client:    "PuTTY_Release_0.80", SessionID: "5", PID: 2451,
			},
		},
		Generated: time.Now(),
	}
}
