//go:build windows

package main

import (
	"os/exec"
	"regexp"
	"strconv"
)

// signalPercentRe matches `netsh wlan show interfaces`' "Signal  : 80%" line.
var signalPercentRe = regexp.MustCompile(`Signal\s*:\s*(\d+)%`)

// wifiSignalPercent shells out to netsh, a standard Windows tool, since
// there's no comparably simple non-shell option here (the real alternative
// is raw WLAN-API syscall struct marshaling via wlanapi.dll -- more code and
// more fragile than a one-line shell-out and text parse). Windows already
// reports signal as a percentage, so no dBm conversion is needed. Reports
// whichever interface netsh lists as connected regardless of ifaceName --
// most Windows machines have exactly one active WiFi adapter.
func wifiSignalPercent(_ string) (percent int, ok bool) {
	out, err := exec.Command("netsh", "wlan", "show", "interfaces").Output()
	if err != nil {
		return 0, false
	}
	m := signalPercentRe.FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	pct, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 0, false
	}
	return pct, true
}
