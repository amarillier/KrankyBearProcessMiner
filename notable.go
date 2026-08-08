package main

import "strings"

// knownParentByChild maps a well-known Windows process name to its
// legitimate, stable parent process name -- a mismatch is the classic
// "malware pretending to be a system process" trick (e.g. a fake
// svchost.exe spawned directly by something other than the Service
// Control Manager). Deliberately short and conservative: only names with a
// well-established, version-stable parent relationship are included.
// csrss.exe is deliberately excluded despite being an obvious-seeming
// candidate -- its real parent (smss.exe) exits immediately after creating
// it, so a naive parent-name check on csrss.exe would misfire constantly,
// which would be worse than not checking it at all.
//
// Harmless on macOS/Linux: these process names simply never appear there,
// so this never matches, no build-tag guard needed.
var knownParentByChild = map[string]string{
	"svchost.exe": "services.exe",
	"lsass.exe":   "wininit.exe",
}

// checkProcessMasquerade reports whether p is one of the small set of
// well-known process names above and its actual parent doesn't match the
// expected one -- worth a second look, not proof on its own: an unusual
// but entirely legitimate launch path could also trigger this. Returns
// mismatch=false (nothing to report) for any process not in
// knownParentByChild at all, which covers the overwhelming majority of
// rows.
func checkProcessMasquerade(p ProcInfo, byPID map[int32]ProcInfo) (expectedParent string, mismatch bool) {
	expected, ok := knownParentByChild[strings.ToLower(p.Name)]
	if !ok {
		return "", false
	}
	parent, ok := byPID[p.PPID]
	if !ok {
		// No visible parent at all for a process that should always have
		// one running (services.exe/wininit.exe never legitimately exit)
		// -- also worth flagging, not just a silent pass.
		return expected, true
	}
	if strings.EqualFold(parent.Name, expected) {
		return "", false
	}
	return expected, true
}
