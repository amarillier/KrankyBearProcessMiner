package main

import (
	"net"
	"strings"
)

// NetAdapterInfo summarizes one network interface for the System Info
// window -- deliberately lighter than a full network-monitoring tool: just
// enough to answer "how many adapters do I have, wired or wireless, and is
// each one actually connected."
type NetAdapterInfo struct {
	Name          string
	Type          string // "Wired", "Wireless", "Loopback", "VPN", "Other"
	Connected     bool
	SignalPercent int  // only meaningful when HasSignal
	HasSignal     bool // true only for a connected Wireless adapter we could read a signal for
}

// gatherNetAdapters lists every network interface and classifies it. Signal
// strength is looked up only for adapters classified Wireless and Connected,
// via wifiSignalPercent (implemented per-platform -- see netadapters_*.go):
// a real nl80211 netlink query on Linux (github.com/mdlayher/wifi, no shell,
// no cgo), or a shell-out to system_profiler/netsh on macOS/Windows, since
// neither OS has a comparably simple non-shell option (the alternative there
// is real cgo+Objective-C or raw WLAN-API syscall struct marshaling -- more
// code and more fragile than a one-line shell-out and text parse).
func gatherNetAdapters() []NetAdapterInfo {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var adapters []NetAdapterInfo
	for _, iface := range ifaces {
		info := NetAdapterInfo{
			Name: iface.Name,
			Type: classifyInterfaceType(iface),
		}
		info.Connected = isInterfaceConnected(iface)

		if info.Type == "Wireless" && info.Connected {
			if pct, ok := wifiSignalPercent(iface.Name); ok {
				info.SignalPercent = pct
				info.HasSignal = true
			}
		}

		adapters = append(adapters, info)
	}
	return adapters
}

// classifyInterfaceType is a simplified port of ../KrankyBearNetInfo's
// heuristic (name-pattern matching -- wlan/wifi/en0 etc. -- since we don't
// carry that project's cgo WiFi-adapter cross-check here). Good enough for
// a summary count; not meant to be perfect for exotic adapter names.
func classifyInterfaceType(iface net.Interface) string {
	name := strings.ToLower(iface.Name)

	if name == "lo" || name == "lo0" || iface.Flags&net.FlagLoopback != 0 {
		return "Loopback"
	}

	for _, p := range []string{"tun", "tap", "ppp", "wg", "utun", "ipsec", "vpn", "pptp", "l2tp", "wireguard"} {
		if strings.Contains(name, p) {
			return "VPN"
		}
	}

	for _, p := range []string{"wlan", "wifi", "wlp", "airport"} {
		if strings.Contains(name, p) {
			return "Wireless"
		}
	}
	// en0 is WiFi on the overwhelming majority of Macs; en1+ are usually
	// wired (Thunderbolt/USB Ethernet). Imperfect, but a reasonable default
	// -- see NetInfo's own comments on this same heuristic.
	if name == "en0" {
		return "Wireless"
	}

	for _, p := range []string{"eth", "enp", "ens", "em", "usb", "ethernet"} {
		if strings.Contains(name, p) {
			return "Wired"
		}
	}
	if strings.HasPrefix(name, "en") {
		return "Wired"
	}

	return "Other"
}

// isInterfaceConnected reports whether iface looks actually in-use: up,
// with a real (non-loopback, non-link-local) IP address. Simplified from
// NetInfo's IsActiveInterface.
func isInterfaceConnected(iface net.Interface) bool {
	if iface.Flags&net.FlagUp == 0 {
		return false
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.IsLinkLocalUnicast() {
			continue
		}
		return true
	}
	return false
}

// rssiToPercent converts a dBm signal reading (Linux, macOS) to the same
// 0-100 scale Windows already reports natively, so the UI shows one
// consistent unit regardless of platform. Typical WiFi RSSI range: -100 dBm
// (0%) to 0 dBm (100%).
func rssiToPercent(rssi int) int {
	percent := 2 * (rssi + 100)
	switch {
	case percent < 0:
		return 0
	case percent > 100:
		return 100
	default:
		return percent
	}
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
