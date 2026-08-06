//go:build linux

package main

import "github.com/mdlayher/wifi"

// wifiSignalPercent reads real signal strength via nl80211 netlink --
// github.com/mdlayher/wifi is a maintained, pure-Go library (no cgo, no
// shelling out), unlike the macOS/Windows implementations of this function.
func wifiSignalPercent(ifaceName string) (percent int, ok bool) {
	c, err := wifi.New()
	if err != nil {
		return 0, false
	}
	defer c.Close()

	ifaces, err := c.Interfaces()
	if err != nil {
		return 0, false
	}
	for _, ifi := range ifaces {
		if ifi.Name != ifaceName {
			continue
		}
		stations, err := c.StationInfo(ifi)
		if err != nil || len(stations) == 0 {
			return 0, false
		}
		return rssiToPercent(stations[0].Signal), true
	}
	return 0, false
}
