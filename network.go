package main

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// NetworkInfo aggregates interfaces, DNS servers and link details.
type NetworkInfo struct {
	Interfaces    []NetInterface
	DNSServers    []string
	SearchDomains []string
	GatewayIP     string
	GatewayMAC    string
	GatewayVendor string
}

// NetInterface describes a single network interface.
type NetInterface struct {
	Name       string
	MAC        string
	MACVendor  string
	State      string
	MTU        int
	SpeedMbps  int
	Duplex     string
	Carrier    bool
	IPv4       []string
	IPv6       []string
	IsWireless bool
	RxBytes    uint64
	TxBytes    uint64
	Wireless   *WirelessInfo
}

// WirelessInfo holds WiFi-specific details from the `iw` tool.
type WirelessInfo struct {
	SSID         string
	Connected    bool
	BSSID        string
	BSSIDVendor  string
	FrequencyMHz int
	Channel      string
	Band         string
	BitrateMbps  float64
	WidthMHz     int
	TxPowerDBm   string
	Security     string
	SignalDBm    int
	SignalPct    int
}

func collectNetworkInfo() NetworkInfo {
	ni := NetworkInfo{}
	ni.DNSServers, ni.SearchDomains = dnsServers()

	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return ni
	}
	for _, e := range entries {
		name := e.Name()
		if name == "lo" {
			continue
		}
		iface := NetInterface{Name: name}
		base := filepath.Join("/sys/class/net", name)
		iface.MAC = readFileTrim(filepath.Join(base, "address"))
		iface.MACVendor = lookupMACVendor(iface.MAC)
		iface.State = readFileTrim(filepath.Join(base, "operstate"))
		iface.MTU = int(parseUint(readFileTrim(filepath.Join(base, "mtu"))))
		iface.Carrier = readFileTrim(filepath.Join(base, "carrier")) == "1"
		iface.RxBytes = parseUint(readFileTrim(filepath.Join(base, "statistics/rx_bytes")))
		iface.TxBytes = parseUint(readFileTrim(filepath.Join(base, "statistics/tx_bytes")))

		iface.IPv4, iface.IPv6 = interfaceAddrs(name)

		// Wireless detection: a `wireless` subdir exists for WiFi devices.
		if _, err := os.Stat(filepath.Join(base, "wireless")); err == nil {
			iface.IsWireless = true
			iface.Wireless = collectWireless(name)
		} else {
			iface.SpeedMbps, iface.Duplex = ethernetLink(name)
		}
		ni.Interfaces = append(ni.Interfaces, iface)
	}

	sort.Slice(ni.Interfaces, func(i, j int) bool { return ni.Interfaces[i].Name < ni.Interfaces[j].Name })
	ni.GatewayIP, ni.GatewayMAC = defaultGateway()
	if ni.GatewayMAC != "" {
		ni.GatewayVendor = lookupMACVendor(ni.GatewayMAC)
	}
	return ni
}

// defaultGateway finds the IPv4 default route and resolves the gateway's MAC
// from the ARP table, so a LAN setup shows the router and its vendor.
func defaultGateway() (ip, mac string) {
	f, err := os.Open("/proc/net/route")
	if err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			// Iface Destination Gateway Flags RefCnt Use Metric Mask ...
			if len(fields) < 3 || fields[1] != "00000000" {
				continue
			}
			ip = hexLEToIPv4(fields[2])
			if ip != "" {
				break
			}
		}
		f.Close()
	}
	if ip != "" {
		mac = arpLookup(ip)
	}
	return
}

// hexLEToIPv4 converts the little-endian hex form used by /proc/net/route.
func hexLEToIPv4(h string) string {
	if len(h) != 8 {
		return ""
	}
	var b [4]byte
	for i := 0; i < 4; i++ {
		v := parseUintHex(h[i*2 : i*2+2])
		b[3-i] = byte(v)
	}
	return net.IPv4(b[0], b[1], b[2], b[3]).String()
}

// arpLookup finds the MAC for an IP in /proc/net/arp.
func arpLookup(ip string) string {
	b, err := readFile("/proc/net/arp")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		// IPaddress HWtype Flags HWaddress Mask Device
		if len(fields) >= 4 && fields[0] == ip {
			if fields[3] != "00:00:00:00:00:00" {
				return fields[3]
			}
		}
	}
	return ""
}

// dnsServers reads resolv.conf, skipping the systemd-resolved stub when the
// real upstream servers are available via resolvectl.
func dnsServers() (servers, search []string) {
	if commandExists("resolvectl") {
		if out, err := runCmd(cmdTimeout, "resolvectl", "dns"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if idx := strings.Index(line, ":"); idx >= 0 {
					for _, f := range strings.Fields(line[idx+1:]) {
						if net.ParseIP(f) != nil {
							servers = append(servers, f)
						}
					}
				}
			}
			if out, err := runCmd(cmdTimeout, "resolvectl", "domain"); err == nil {
				for _, line := range strings.Split(out, "\n") {
					if idx := strings.Index(line, ":"); idx >= 0 {
						for _, f := range strings.Fields(line[idx+1:]) {
							if !strings.ContainsAny(f, "0123456789") && f != "~." {
								search = append(search, f)
							}
						}
					}
				}
			}
		}
	}

	if len(servers) == 0 {
		f, err := os.Open("/etc/resolv.conf")
		if err == nil {
			defer f.Close()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
					continue
				}
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == "nameserver" {
					if net.ParseIP(fields[1]) != nil {
						servers = append(servers, fields[1])
					}
				}
				if len(fields) >= 2 && fields[0] == "search" {
					search = append(search, fields[1:]...)
				}
			}
		}
	}
	return dedupe(servers), dedupe(search)
}

func interfaceAddrs(name string) (v4, v6 []string) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ipnet.IP.To4() != nil {
			v4 = append(v4, ipnet.String())
		} else {
			v6 = append(v6, ipnet.String())
		}
	}
	return
}

// ethernetLink reads link speed/duplex from sysfs, falling back to ethtool.
func ethernetLink(name string) (speed int, duplex string) {
	base := filepath.Join("/sys/class/net", name)
	if s := readFileTrim(filepath.Join(base, "speed")); s != "" {
		if v := int(parseUint(s)); v > 0 {
			speed = v
		}
	}
	duplex = readFileTrim(filepath.Join(base, "duplex"))
	if (speed == 0 || duplex == "") && commandExists("ethtool") {
		if out, err := runCmd(cmdTimeout, "ethtool", name); err == nil {
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "Speed:") {
					val := strings.TrimSpace(strings.TrimPrefix(line, "Speed:"))
					val = strings.TrimSuffix(val, "Mb/s")
					if v := int(parseUint(val)); v > 0 {
						speed = v
					}
				}
				if strings.HasPrefix(line, "Duplex:") {
					duplex = strings.TrimSpace(strings.TrimPrefix(line, "Duplex:"))
				}
			}
		}
	}
	// ethtool reports "Unknown! (255)" for a down link; only full/half are real.
	switch strings.ToLower(duplex) {
	case "full", "half":
	default:
		duplex = ""
	}
	return
}

// collectWireless uses `iw dev <name> link` and `iw dev <name> info`.
func collectWireless(name string) *WirelessInfo {
	wi := &WirelessInfo{}
	if !commandExists("iw") {
		return wi
	}
	if out, err := runCmd(cmdTimeout, "iw", "dev", name, "link"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "Connected to"):
				wi.Connected = true
				// "Connected to aa:bb:cc:dd:ee:ff (on wlan0)"
				f := strings.Fields(strings.TrimPrefix(line, "Connected to"))
				if len(f) > 0 && strings.Count(f[0], ":") == 5 {
					wi.BSSID = f[0]
					wi.BSSIDVendor = lookupMACVendor(f[0])
				}
			case strings.HasPrefix(line, "SSID:"):
				wi.SSID = strings.TrimSpace(strings.TrimPrefix(line, "SSID:"))
			case strings.HasPrefix(line, "freq:"):
				wi.FrequencyMHz = int(parseFloat(strings.TrimPrefix(line, "freq:")))
			case strings.HasPrefix(line, "signal:"):
				wi.SignalDBm = int(parseFloat(strings.TrimPrefix(line, "signal:")))
			case strings.HasPrefix(line, "tx bitrate:"):
				wi.BitrateMbps = parseFloat(strings.TrimPrefix(line, "tx bitrate:"))
			case strings.HasPrefix(line, "rx bitrate:"):
				if wi.BitrateMbps == 0 {
					wi.BitrateMbps = parseFloat(strings.TrimPrefix(line, "rx bitrate:"))
				}
			}
		}
	}
	wi.Band, wi.Channel = freqToBandChannel(wi.FrequencyMHz)
	if wi.SignalDBm != 0 {
		wi.SignalPct = signalPercent(wi.SignalDBm)
	}
	if wi.BitrateMbps > 0 {
		wi.WidthMHz = 0 // refined below if `iw` exposes the width token
	}
	if out, err := runCmd(cmdTimeout, "iw", "dev", name, "info"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "txpower") {
				wi.TxPowerDBm = strings.TrimSpace(strings.TrimPrefix(line, "txpower"))
			}
			if strings.HasPrefix(line, "channel ") {
				// "channel 36 (5180 MHz), width: 80 MHz, center1: ..."
				if idx := strings.Index(line, "width:"); idx >= 0 {
					rest := strings.TrimSpace(line[idx+len("width:"):])
					fields := strings.Fields(rest)
					if len(fields) > 0 {
						wi.WidthMHz = int(parseFloat(fields[0]))
					}
				}
			}
		}
	}
	wi.Security = wirelessSecurity(name)
	return wi
}

func wirelessSecurity(name string) string {
	if !commandExists("iw") {
		return ""
	}
	out, err := runCmd(cmdTimeout, "iw", "dev", name, "link")
	if err != nil {
		return ""
	}
	lower := strings.ToLower(out)
	switch {
	case strings.Contains(lower, "wpa3") || strings.Contains(lower, "sae"):
		return "WPA3"
	case strings.Contains(lower, "wpa2") || strings.Contains(lower, "rsn"):
		return "WPA2"
	case strings.Contains(lower, "wpa"):
		return "WPA"
	case strings.Contains(lower, "wep"):
		return "WEP"
	default:
		return ""
	}
}

// freqToBandChannel converts a WiFi centre frequency to a band label and
// channel number.
func freqToBandChannel(freq int) (band, channel string) {
	switch {
	case freq == 0:
		return "", ""
	case freq >= 2412 && freq <= 2484:
		ch := 0
		if freq == 2484 {
			ch = 14
		} else {
			ch = (freq - 2407) / 5
		}
		return "2.4", itoa(ch)
	case freq >= 5160 && freq <= 5895:
		return "5", itoa((freq - 5000) / 5)
	case freq >= 5955 && freq <= 7115:
		return "6", itoa((freq - 5950) / 5)
	case freq >= 5150 && freq <= 5250:
		return "5", itoa((freq - 5000) / 5)
	}
	return "", ""
}

func signalPercent(dbm int) int {
	if dbm <= -100 {
		return 0
	}
	if dbm >= -50 {
		return 100
	}
	return 2 * (dbm + 100)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func itoa(i int) string { return strconv.Itoa(i) }
