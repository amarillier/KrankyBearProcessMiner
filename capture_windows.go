//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

// wprCommand builds a wpr.exe invocation with its console window
// suppressed. Without this, Windows auto-allocates and shows a visible
// console for wpr.exe (a console-mode app) since this GUI app has none of
// its own -- confirmed via real-world testing: a wpr progress window
// popped up and stayed on screen for the whole `-stop` merge/flush.
// Unnecessary here since the output is already captured and shown in our
// own dialog (see wrapWPRError).
func wprCommand(args ...string) *exec.Cmd {
	cmd := exec.Command("wpr.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}

// captureSupported gates captureview.go's Capture Trace window -- wpr.exe
// (Windows Performance Recorder) is Windows-only, in-box since Windows 10
// (confirmed present via `wpr.exe -?` on a real test machine, no separate
// install needed -- unlike WPA, the analyzer, which does need one).
const captureSupported = true

// captureSessionState tracks the WPR session's lifecycle -- see
// cancelCapture's doc comment for why captureStopping has to be a distinct
// state from captureRunning, not just a bool.
type captureSessionState int

const (
	captureIdle captureSessionState = iota
	captureRunning
	captureStopping
)

// captureMu guards captureState: startCapture/stopCapture/cancelCapture run
// on a background goroutine (spawned per button click in captureview.go),
// but cancelCapture is ALSO called from quitApp on the main goroutine --
// genuine cross-goroutine access, confirmed the hard way (see below), so
// this needs real synchronization, not just "callers happen to serialize
// themselves."
var (
	captureMu    sync.Mutex
	captureState captureSessionState
)

// startCapture starts a WPR trace session covering the given profile names
// (see captureProfiles). Building the .etl itself needs no custom ETW/TDH
// code at all -- wpr.exe already has first-class built-in profiles for
// exactly the gaps this app documents (Network, DiskIO, FileIO, and
// notably Minifilter, confirmed via `wpr.exe -profiles` on a real machine).
//
// The most common real failure here isn't a bad profile name, it's a
// leftover session from a previous run of this app that crashed or was
// killed instead of cleanly stopped -- wpr.exe's own error message for that
// case already says a session is in progress, so it's surfaced as-is
// rather than replaced with a vaguer one; the Capture Trace window's Cancel
// button (cancelCapture) is the fix.
func startCapture(profiles []string) error {
	if len(profiles) == 0 {
		return fmt.Errorf("select at least one profile to capture")
	}
	args := make([]string, 0, len(profiles)*2)
	for _, p := range profiles {
		args = append(args, "-start", p)
	}
	out, err := wprCommand(args...).CombinedOutput()
	if err != nil {
		return wrapWPRError("wpr -start", out, err)
	}
	captureMu.Lock()
	captureState = captureRunning
	captureMu.Unlock()
	return nil
}

// stopCapture stops the current session and merges/flushes it to path as a
// .etl. This can take several seconds to tens of seconds on a longer
// capture (WPR is merging buffers, not just closing a file handle) --
// callers MUST run this in a goroutine, never on the main/UI goroutine.
//
// Marking captureStopping *before* the (slow) wpr -stop call, not just
// captureIdle after it, is what actually matters here -- see cancelCapture.
func stopCapture(path string) error {
	captureMu.Lock()
	captureState = captureStopping
	captureMu.Unlock()

	out, err := wprCommand("-stop", path).CombinedOutput()

	captureMu.Lock()
	captureState = captureIdle
	captureMu.Unlock()

	if err != nil {
		return wrapWPRError("wpr -stop", out, err)
	}
	return nil
}

// cancelCapture discards the current session without saving anything --
// used both for an explicit user Cancel action and from quitApp, since an
// active WPR session is an OS-level resource that outlives this process if
// not explicitly torn down (unlike an in-process goroutine, closing
// ProcessMiner does NOT stop it on its own).
//
// Only actually runs `wpr -cancel` if a session is just sitting there
// recording (captureRunning) -- confirmed for real that this matters: a
// user exited the app while Stop && Save's `wpr -stop` was still merging
// the trace (captureStopping); quitApp's unconditional cancelCapture() call
// raced with it and ran `wpr -cancel` concurrently with the still-running
// `-stop`, corrupting the .etl WPR was in the middle of writing (WPA
// rejected the result as unreadable). Since quitApp calls this
// unconditionally, the "is it safe to cancel right now" check has to live
// here, not at each call site -- and once a save is in flight, the right
// move is to do nothing at all and let it finish on its own: wpr.exe is a
// separate process Windows doesn't tie to this one's lifetime, so it keeps
// merging in the background even after this app exits, as long as nothing
// actively interrupts it.
func cancelCapture() error {
	captureMu.Lock()
	if captureState != captureRunning {
		captureMu.Unlock()
		return nil
	}
	captureMu.Unlock()

	out, err := wprCommand("-cancel").CombinedOutput()

	captureMu.Lock()
	captureState = captureIdle
	captureMu.Unlock()

	if err != nil {
		return wrapWPRError("wpr -cancel", out, err)
	}
	return nil
}

// wprAdminRequiredCode is WPR's own error code for "not running elevated"
// -- confirmed empirically on a real machine: `wpr -start` run from a
// normal (non-admin) launch fails with exactly this code and "Failed to
// enable the policy to profile system performance," since these are all
// kernel-mode providers. The numeric code is checked rather than the
// English message text, which could vary across Windows locales/versions.
const wprAdminRequiredCode = "0xc5585011"

// wrapWPRError includes wpr.exe's own output in the error -- it's already
// a specific, readable explanation (e.g. "a data collector is already
// running") rather than a generic exit-code failure -- and appends a
// plain-language hint for the one failure mode expected to be common:
// not running as Administrator.
func wrapWPRError(step string, out []byte, err error) error {
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		return fmt.Errorf("%s failed: %w", step, err)
	}
	if strings.Contains(msg, wprAdminRequiredCode) {
		return fmt.Errorf("%s failed: %s\n\nThis needs Administrator rights (kernel-level tracing) -- close ProcessMiner and relaunch it as Administrator (right-click the app/shortcut -> \"Run as administrator\"), then try again.", step, msg)
	}
	return fmt.Errorf("%s failed: %s", step, msg)
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
