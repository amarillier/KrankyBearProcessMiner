package main

import (
	"os"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/shirou/gopsutil/v4/process"
)

// anotherInstanceRunning reports whether launching this process should be
// blocked because of another already-running one at the same executable
// path. A deliberately simple, real launch-time check -- no lock file, no
// IPC -- matching by path rather than name to avoid mistaking an unrelated
// process that happens to share a generic name for a second instance of
// this app.
//
// On Windows, exactly one exception to "always just one instance": a
// second copy is allowed if its elevation differs from the other one's
// (one normal + one elevated, whichever order they were launched in) --
// added specifically so Capture Trace's admin-rights requirement doesn't
// force fully quitting and relaunching the app every time you want to
// record a trace. Two normal instances (or two elevated ones) still isn't
// allowed -- that's the actual resource-doubling concern the original
// single-instance rule exists for (see showAlreadyRunningAndExit), and
// elevation-checking is a Windows-only concept (elevationCheckSupported),
// so every other platform keeps the original strict behavior.
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
	var others []int32
	for _, p := range procs {
		if p.Pid == selfPID || p.Pid == parentPID {
			continue
		}
		exe, err := p.Exe()
		if err != nil {
			continue // permission denied, exited mid-scan, etc. -- not a comparable match
		}
		if exe == selfPath {
			others = append(others, p.Pid)
		}
	}

	switch {
	case len(others) == 0:
		return false
	case !elevationCheckSupported || len(others) > 1:
		return true // already at (or past) the 2-instance cap, or this platform doesn't distinguish elevation
	default:
		return isCurrentProcessElevated() == isProcessElevated(others[0])
	}
}

// showAlreadyRunningAndExit displays a small window saying another instance
// is already running, and quits once acknowledged. Deliberately simple: no
// IPC, no bringing the other instance's window to the foreground -- just
// "you already have one open, close this one instead."
//
// This window is its own fully separate process (the rejected launch
// attempt), with no connection at all to whichever instance(s) it's
// complaining about -- so closing one of those doesn't touch this window;
// confirmed for real, it just sits there as an orphaned "remnant" until
// dismissed on its own. autoCloseWhenClear polls the same
// anotherInstanceRunning() check this window's whole reason for existing
// came from, and quits on its own once it's no longer true -- still no
// real cross-process IPC (just noticing the process list changed, the same
// way the check itself already works), but avoids the leftover-window
// annoyance.
func showAlreadyRunningAndExit(a fyne.App) {
	win := a.NewWindow(appName)
	win.SetIcon(resourceKrankyBearProcessMinerPng)

	msgText := appName + " is already running."
	if elevationCheckSupported {
		msgText = appName + " is already running at this elevation level.\n\nClose it, or relaunch as Administrator instead for Capture Trace."
	}
	msg := widget.NewLabel(msgText)
	msg.Wrapping = fyne.TextWrapWord
	msg.Alignment = fyne.TextAlignCenter

	quitBtn := widget.NewButton("Quit", func() { a.Quit() })

	win.SetContent(container.NewPadded(container.NewVBox(msg, quitBtn)))
	win.Resize(fyne.NewSize(380, 120))
	// A fixed size, not just a starting one: a wrapped label's minimum-size
	// calculation can occasionally blow out the window's height (seen for
	// real on Windows once this message grew past a couple of short lines)
	// -- pinning the size sidesteps that outright rather than chasing the
	// exact layout cause for what's just a small alert dialog.
	win.SetFixedSize(true)
	win.SetCloseIntercept(func() { a.Quit() })

	stopAutoClose := autoCloseWhenClear(a)
	defer stopAutoClose()

	win.ShowAndRun()
}

// autoCloseWhenClear polls anotherInstanceRunning() every couple of
// seconds and quits a once it stops returning true, i.e. whatever this
// alert window was complaining about has gone away on its own (the
// instance(s) it named were closed) -- returns a stop func to cancel the
// polling once the window closes some other way (Quit button, close
// intercept) so the goroutine doesn't outlive it.
func autoCloseWhenClear(a fyne.App) func() {
	ticker := time.NewTicker(2 * time.Second)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				if !anotherInstanceRunning() {
					fyne.Do(a.Quit)
					return
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		ticker.Stop()
		close(done)
	}
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
