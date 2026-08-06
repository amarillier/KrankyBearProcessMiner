//go:build darwin

package main

import (
	"os/exec"
	"regexp"
	"strconv"
)

// signalNoiseRe matches system_profiler SPAirPortDataType's
// "Signal / Noise: -50 dBm / -92 dBm" line under the current network.
var signalNoiseRe = regexp.MustCompile(`Signal / Noise:\s*(-?\d+)\s*dBm`)

// wifiSignalPercent shells out to system_profiler -- a standard, public
// macOS tool, not a private API -- since there's no comparably simple
// non-shell option here (the real alternative is cgo+Objective-C bindings
// to CoreWLAN, more code and more fragile than a one-line shell-out and
// text parse). Reports the current network's signal regardless of
// ifaceName: virtually all Macs have exactly one active WiFi adapter.
func wifiSignalPercent(_ string) (percent int, ok bool) {
	out, err := exec.Command("system_profiler", "SPAirPortDataType").Output()
	if err != nil {
		return 0, false
	}
	m := signalNoiseRe.FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	dbm, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 0, false
	}
	return rssiToPercent(dbm), true
}
