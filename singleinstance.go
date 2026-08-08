package main

import (
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/shirou/gopsutil/v4/process"
)

// anotherInstanceRunning reports whether another running process has the
// same executable path as this one. A deliberately simple, real launch-time
// check -- no lock file, no IPC -- matching by path rather than name to
// avoid mistaking an unrelated process that happens to share a generic
// name for a second instance of this app.
//
// parentPID is excluded on purpose: if this process was just relaunched by
// itself (see internal/startup's Mesa3D fallback, which execs a fresh copy
// of itself and then exits the old one), the parent is that old instance --
// possibly still mid-exit when this check runs -- not a second real
// instance the user actually launched.
func anotherInstanceRunning() bool {
	selfPath, err := os.Executable()
	if err != nil {
		return false // can't tell -- fail open rather than block a legitimate launch
	}
	selfPID := int32(os.Getpid())
	parentPID := int32(os.Getppid())

	procs, err := process.Processes()
	if err != nil {
		return false
	}
	for _, p := range procs {
		if p.Pid == selfPID || p.Pid == parentPID {
			continue
		}
		exe, err := p.Exe()
		if err != nil {
			continue // permission denied, exited mid-scan, etc. -- not a comparable match
		}
		if exe == selfPath {
			return true
		}
	}
	return false
}

// showAlreadyRunningAndExit displays a small window saying another instance
// is already running, and quits once acknowledged. Deliberately simple: no
// IPC, no bringing the other instance's window to the foreground -- just
// "you already have one open, close this one instead."
func showAlreadyRunningAndExit(a fyne.App) {
	win := a.NewWindow(appName)
	win.SetIcon(resourceKrankyBearProcessMinerPng)

	msg := widget.NewLabel(appName + " is already running.\n\nTo minimize excessive resource use, only one instance can run at a time.")
	msg.Wrapping = fyne.TextWrapWord
	msg.Alignment = fyne.TextAlignCenter

	quitBtn := widget.NewButton("Quit", func() { a.Quit() })

	win.SetContent(container.NewPadded(container.NewVBox(msg, quitBtn)))
	win.Resize(fyne.NewSize(360, 160))
	win.SetCloseIntercept(func() { a.Quit() })
	win.ShowAndRun()
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
