package main

import "time"

// demoReport returns a realistic Raspberry Pi 4B snapshot so the layout can be
// previewed on any machine without actual Pi hardware.
func demoReport() Report {
	return Report{
		System: SystemInfo{
			Hostname:      "raspberrypi",
			OSName:        "Debian GNU/Linux 12 (bookworm)",
			KernelVersion: "6.6.51+rpt-rpi-v8",
			Arch:          "arm64",
			BoardModel:    "Raspberry Pi 4 Model B Rev 1.4",
			BoardVendor:   "Raspberry Pi Foundation",
			BoardRevision: "c03114",
			Serial:        "10000000abcdef01",
			BootTime:      time.Now().Add(-38*time.Hour - 12*time.Minute),
			Uptime:        38*time.Hour + 12*time.Minute,
			TimeZone:      "MSK",
		},
		CPU: CPUInfo{
			Model:        "Cortex-A72",
			Vendor:       "ARM",
			Architecture: "arm64",
			Cores:        4,
			Threads:      4,
			BogoMIPS:     "108.00",
			CurFreqMHz:   1500,
			MinFreqMHz:   600,
			MaxFreqMHz:   1500,
			Governor:     "ondemand",
			Features:     []string{"fp", "asimd", "evtstrm", "crc32", "sha1", "sha2", "aes"},
		},
		Memory: MemoryInfo{
			TotalKB:     8123904,
			AvailableKB: 6120000,
			FreeKB:      5400000,
			BuffersKB:   82000,
			CachedKB:    2100000,
			UsedKB:      2003904,
			SwapTotalKB: 2097152,
			SwapFreeKB:  1048576,
			SwapUsedKB:  1048576,
			Zram: []ZramDevice{{
				Name:              "zram0",
				Algorithm:         "zstd",
				DisksizeBytes:     1073741824,
				OriginalBytes:     268435456,
				CompressedBytes:   67108864,
				MemUsedTotalBytes: 75497472,
				CompressionRatio:  4.0,
				UsedPercent:       25.0,
			}},
		},
		Storage: []Filesystem{
			{MountPoint: "/", Device: "/dev/mmcblk0p2", FSType: "ext4", TotalBytes: 31_200_000_000, UsedBytes: 8_400_000_000, FreeBytes: 22_800_000_000, UsedPct: 27},
			{MountPoint: "/boot", Device: "/dev/mmcblk0p1", FSType: "vfat", TotalBytes: 268_435_456, UsedBytes: 52_428_800, FreeBytes: 216_006_656, UsedPct: 19.5},
			{MountPoint: "/mnt/usb", Device: "/dev/sda1", FSType: "ext4", TotalBytes: 500_000_000_000, UsedBytes: 120_000_000_000, FreeBytes: 380_000_000_000, UsedPct: 24},
		},
		Network: NetworkInfo{
			DNSServers:    []string{"192.168.1.1", "8.8.8.8"},
			SearchDomains: []string{"lan"},
			Interfaces: []NetInterface{
				{
					Name: "eth0", MAC: "dc:a6:32:1a:2b:3c", State: "up", MTU: 1500,
					SpeedMbps: 1000, Duplex: "full", Carrier: true,
					IPv4: []string{"192.168.1.42/24"},
				},
				{
					Name: "wlan0", MAC: "dc:a6:32:1a:2b:3d", State: "up", MTU: 1500,
					IsWireless: true,
					IPv4:       []string{"192.168.1.43/24"},
					Wireless: &WirelessInfo{
						SSID: "HomeNet_5G", Connected: true,
						FrequencyMHz: 5180, Band: "5 ГГц", Channel: "36",
						BitrateMbps: 433.3, WidthMHz: 80, Security: "WPA2",
						SignalDBm: -58, SignalPct: 84, TxPowerDBm: "31.00 dBm",
					},
				},
			},
		},
		Sensors: SensorInfo{
			IsRaspberryPi: true,
			TemperatureC:  47.8,
			Voltages: []SensorValue{
				{Name: "Ядро (core)", Value: 0.8700, Unit: "В"},
				{Name: "SDRAM (ядро)", Value: 1.1000, Unit: "В"},
			},
			Clocks: []SensorValue{
				{Name: "Ядро ARM", Value: 1500, Unit: "МГц"},
				{Name: "Ядро GPU (core)", Value: 500, Unit: "МГц"},
				{Name: "3D-ускоритель (V3D)", Value: 500, Unit: "МГц"},
			},
			Throttle: &ThrottleState{Raw: 0x0},
			ThermalZones: []ThermalZone{
				{Name: "Процессор (SoC)", TempC: 47.8},
			},
			Fans: []FanInfo{{Name: "pwmfan", RPM: 2100}},
		},
		Display: DisplayInfo{
			Connectors: []Connector{
				{Name: "HDMI-A-1", Status: "connected", Enabled: "enabled"},
				{Name: "HDMI-A-2", Status: "disconnected"},
			},
			Monitors: []Monitor{{
				Connector: "HDMI-A-1", Manufacturer: "DEL", ProductName: "DELL U2419H",
				Serial: "ABC123", Year: 2020, EDIDVersion: "1.4",
				PreferredWidth: 1920, PreferredHeight: 1080, RefreshHz: 60, SizeCm: "52 × 29 см",
			}},
		},
		Generated: time.Now(),
	}
}
