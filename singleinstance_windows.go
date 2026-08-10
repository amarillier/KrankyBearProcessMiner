//go:build windows

package main

import "golang.org/x/sys/windows"

// elevationCheckSupported gates anotherInstanceRunning's "allow one normal +
// one elevated" relaxation -- UAC elevation is a Windows-only concept.
const elevationCheckSupported = true

// isCurrentProcessElevated reports whether this process is running with an
// elevated (UAC) token -- windows.Token.IsElevated() is a ready-made
// wrapper around GetTokenInformation(TokenElevation), not hand-rolled.
func isCurrentProcessElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// isProcessElevated reports whether pid's process is running elevated --
// false (not an error) if it can't be determined at all (process exited
// mid-check, or some other genuine permission wall), the same
// fail-open-on-uncertainty convention anotherInstanceRunning already uses
// for its own os/exec-free process-list scan. PROCESS_QUERY_LIMITED_INFORMATION
// is deliberately the access level requested -- the same one Task Manager's
// own "Elevated" column relies on to report on processes across integrity
// levels without itself needing to be elevated.
func isProcessElevated(pid int32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer windows.CloseHandle(windows.Handle(token))

	return token.IsElevated()
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
