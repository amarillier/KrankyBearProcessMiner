//go:build windows

package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// relaunchElevated launches a second, elevated copy of this app via the
// standard UAC "runas" verb. Deliberately does NOT quit this instance --
// one normal + one elevated instance is explicitly allowed to run side by
// side (see singleinstance.go), so there's no coordinated handoff needed
// here, unlike a naive "quit then relaunch" would require.
func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	dir, err := windows.UTF16PtrFromString(filepath.Dir(exe))
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, dir, windows.SW_SHOWNORMAL)
}

// isUserCancelledElevation reports whether err is ShellExecute's specific
// "the user declined the UAC prompt" outcome -- a normal, valid choice,
// not a failure worth an error dialog.
func isUserCancelledElevation(err error) bool {
	return err == windows.Errno(windows.ERROR_CANCELLED)
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
