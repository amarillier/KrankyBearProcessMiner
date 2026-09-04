package main

import (
	"fmt"
	"os"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
)

var captureWindow fyne.Window

// captureOpen tracks whether the Capture Trace window should be considered
// "open" for hide-all/show-all purposes (windows.go) -- see aboutOpen in
// about.go.
var captureOpen bool

var captureStatusLabel *widget.Label
var captureStartBtn *ttwidget.Button
var captureStopBtn *ttwidget.Button
var captureCancelBtn *ttwidget.Button
var captureOpenFolderBtn *ttwidget.Button
var captureOpenWPABtn *ttwidget.Button
var captureProfileChecks []*ttwidget.Check

// lastCaptureSavePath is the most recently saved .etl's path -- captureOpenFolderBtn/
// captureOpenWPABtn stay enabled across a later capture (still a valid, real
// file until something else deletes it), only reset by a fresh save
// overwriting it or the window not existing yet (empty, buttons disabled).
var lastCaptureSavePath string

// showCaptureWindow opens (or reveals) the Capture Trace window: pick which
// WPR profiles to record (see captureProfiles), Start, then Stop & Save to
// a .etl for opening in WPA later -- see README's Known limitations for why
// this app doesn't try to be its own trace analyzer. Windows-only; on other
// platforms the window still opens (consistent with Interference Watch's
// own "always visible, gated inside" precedent) but shows an explanation
// instead of the controls.
func showCaptureWindow(a fyne.App) {
	if captureWindow != nil && captureOpen {
		captureWindow.Show()
		captureWindow.RequestFocus()
		return
	}
	if captureWindow == nil {
		buildCaptureWindow(a)
	}
	captureOpen = true
	captureWindow.Show()
	captureWindow.RequestFocus()
}

func buildCaptureWindow(a fyne.App) {
	win := a.NewWindow(appName + " - Capture Trace")
	win.SetIcon(resourceKrankyBearProcessMinerPng)
	captureWindow = win

	// Checked once at window-build time, not on every open -- a process's
	// own elevation is fixed for its whole lifetime, so this can't change
	// out from under an already-open window.
	var content fyne.CanvasObject
	switch {
	case !captureSupported:
		msg := widget.NewLabel("Capture Trace needs Windows Performance Recorder (wpr.exe), which is Windows-only. Not available on this platform yet.")
		msg.Wrapping = fyne.TextWrapWord
		content = container.NewPadded(msg)
		win.Resize(fyne.NewSize(420, 160))
	case !isCurrentProcessElevated():
		// Checked upfront rather than just letting Start fail -- no reason
		// to make the user click through a whole checkbox+Start round trip
		// only to hit the same admin-rights error every time.
		msg := widget.NewLabel("Capture Trace needs Administrator rights (all four profiles are kernel-level tracing providers).\n\nClick below to launch a second, elevated copy via a UAC prompt -- this instance stays open too; one of each elevation level is allowed side by side.")
		msg.Wrapping = fyne.TextWrapWord
		relaunchBtn := ttwidget.NewButton("Relaunch as Administrator", relaunchElevatedClicked)
		relaunchBtn.SetToolTip("Launch a second, elevated copy of ProcessMiner for Capture Trace -- doesn't close this one")
		content = container.NewPadded(container.NewBorder(nil, container.NewCenter(relaunchBtn), nil, nil, msg))
		win.Resize(fyne.NewSize(420, 220))
	default:
		content = buildCaptureControls()
		win.Resize(fyne.NewSize(480, 420))
	}

	win.SetContent(fynetooltip.AddWindowToolTipLayer(content, win.Canvas()))

	// Hide-and-reuse (not a plain close like the Threads/Children windows):
	// this window isn't tied to one process's live state, same reasoning as
	// System Info and Interference Watch. Deliberately does NOT cancel an
	// active capture on close -- closing the window is not the same as
	// quitting the app, and a capture is meant to keep running in the
	// background while you go do the thing you're trying to capture;
	// quitApp's cancelCapture call is the real safety net.
	win.SetCloseIntercept(func() {
		captureOpen = false
		win.Hide()
	})
}

// buildCaptureControls builds the real Windows content: a short banner, one
// checkbox per profile (see captureProfiles, all checked by default),
// Start/Stop & Save/Cancel buttons, a status label, and a reminder of where
// to actually look at the result.
func buildCaptureControls() fyne.CanvasObject {
	banner := widget.NewLabel("Capture Network/Disk/File/Minifilter activity to a .etl trace, then open it in Windows Performance Analyzer (WPA) for deep analysis -- free via the Microsoft Store or the Windows ADK's \"Windows Performance Toolkit\" component. This app doesn't analyze the trace itself; see Help.")
	banner.Wrapping = fyne.TextWrapWord

	checksBox := container.NewVBox()
	captureProfileChecks = make([]*ttwidget.Check, len(captureProfiles))
	for i, p := range captureProfiles {
		chk := ttwidget.NewCheck(p.label, nil)
		chk.SetChecked(true)
		chk.SetToolTip(p.tooltip)
		captureProfileChecks[i] = chk
		checksBox.Add(chk)
	}

	captureStatusLabel = widget.NewLabel("Idle")

	captureStartBtn = ttwidget.NewButton("Start Capture", startCaptureClicked)
	captureStartBtn.SetToolTip("Start recording the checked profiles")

	captureStopBtn = ttwidget.NewButton("Stop && Save…", stopCaptureClicked)
	captureStopBtn.SetToolTip("Stop recording and save the trace to a .etl file")
	captureStopBtn.Disable()

	captureCancelBtn = ttwidget.NewButton("Cancel", cancelCaptureClicked)
	captureCancelBtn.SetToolTip("Discard the current recording without saving")
	captureCancelBtn.Disable()

	captureOpenFolderBtn = ttwidget.NewButton("Open File Location", openCaptureFolderClicked)
	captureOpenFolderBtn.SetToolTip("Open Explorer with the last saved .etl selected")
	captureOpenFolderBtn.Disable()

	captureOpenWPABtn = ttwidget.NewButton("Open in WPA", openCaptureInWPAClicked)
	captureOpenWPABtn.SetToolTip("Open the last saved .etl in Windows Performance Analyzer")
	captureOpenWPABtn.Disable()

	buttons := container.NewHBox(captureStartBtn, captureStopBtn, captureCancelBtn, captureOpenFolderBtn, captureOpenWPABtn)

	return container.NewPadded(container.NewBorder(
		container.NewVBox(banner, widget.NewSeparator(), checksBox, widget.NewSeparator()),
		captureStatusLabel, nil, nil,
		buttons,
	))
}

func setProfileChecksEnabled(enabled bool) {
	for _, chk := range captureProfileChecks {
		if enabled {
			chk.Enable()
		} else {
			chk.Disable()
		}
	}
}

func startCaptureClicked() {
	var selected []string
	for i, chk := range captureProfileChecks {
		if chk.Checked {
			selected = append(selected, captureProfiles[i].name)
		}
	}
	if len(selected) == 0 {
		dialog.ShowError(fmt.Errorf("select at least one profile to capture"), captureWindow)
		return
	}

	captureStatusLabel.SetText("Starting…")
	captureStartBtn.Disable()
	go func() {
		err := startCapture(selected)
		fyne.Do(func() {
			if captureWindow == nil {
				return // window torn down while this was in flight
			}
			if err != nil {
				captureStatusLabel.SetText("Idle")
				captureStartBtn.Enable()
				dialog.ShowError(err, captureWindow)
				return
			}
			captureStatusLabel.SetText("Capturing… (started " + time.Now().Format("15:04:05") + ")")
			captureStopBtn.Enable()
			captureCancelBtn.Enable()
			setProfileChecksEnabled(false)
		})
	}()
}

func stopCaptureClicked() {
	fd := dialog.NewFileSave(func(uri fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, captureWindow)
			return
		}
		if uri == nil {
			return // user cancelled the save dialog
		}
		path := uri.URI().Path()
		uri.Close()
		// The save dialog already created an empty placeholder at path as
		// a side effect of resolving the writer above -- remove it so
		// wpr.exe (which writes the real file itself, not through this
		// writer) creates it fresh rather than potentially balking at an
		// existing file.
		_ = os.Remove(path)

		// Red/danger styling deliberately, not just informational text --
		// confirmed for real that this needs to be hard to miss: a user
		// closed the app during this exact window (having seen a plainer
		// version of this message once, then forgotten by the time it
		// mattered) and wpr.exe -stop was still merging when the app
		// exited, corrupting the .etl (WPA rejected the result as
		// unreadable). Exiting can't actually be blocked from here, so
		// this is the mitigation: make it as visually unmissable as
		// possible that this specific moment is not safe to exit through.
		captureStatusLabel.Importance = widget.DangerImportance
		captureStatusLabel.SetText("Saving… this can take up to a minute. Do NOT exit ProcessMiner until this finishes -- doing so will corrupt the .etl.")
		captureStopBtn.Disable()
		captureCancelBtn.Disable()
		go func() {
			err := stopCapture(path)
			fyne.Do(func() {
				if captureWindow == nil {
					return
				}
				setProfileChecksEnabled(true)
				captureStartBtn.Enable()
				captureStatusLabel.Importance = widget.MediumImportance
				if err != nil {
					captureStatusLabel.SetText("Idle")
					dialog.ShowError(err, captureWindow)
					return
				}
				captureStatusLabel.SetText("Saved to " + path)
				lastCaptureSavePath = path
				captureOpenFolderBtn.Enable()
				captureOpenWPABtn.Enable()
			})
		}()
	}, captureWindow)
	fd.SetFileName("capture.etl")
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".etl"}))
	fd.Show()
}

func cancelCaptureClicked() {
	captureStatusLabel.SetText("Cancelling…")
	captureStopBtn.Disable()
	captureCancelBtn.Disable()
	go func() {
		err := cancelCapture()
		fyne.Do(func() {
			if captureWindow == nil {
				return
			}
			setProfileChecksEnabled(true)
			captureStartBtn.Enable()
			if err != nil {
				captureStatusLabel.SetText("Idle")
				dialog.ShowError(err, captureWindow)
				return
			}
			captureStatusLabel.SetText("Idle (cancelled)")
		})
	}()
}

// openCaptureFolderClicked and openCaptureInWPAClicked both just start a
// separate process (explorer.exe/wpa.exe) and return immediately -- unlike
// startCapture/stopCapture/cancelCapture, there's no slow work here needing
// its own goroutine, only a possible immediate error (e.g. wpa.exe missing)
// to surface.
func openCaptureFolderClicked() {
	if lastCaptureSavePath == "" {
		return
	}
	if err := openCaptureFileLocation(lastCaptureSavePath); err != nil {
		dialog.ShowError(err, captureWindow)
	}
}

func openCaptureInWPAClicked() {
	if lastCaptureSavePath == "" {
		return
	}
	if err := openCaptureInWPA(lastCaptureSavePath); err != nil {
		dialog.ShowError(err, captureWindow)
	}
}

// relaunchElevatedClicked launches a second, elevated copy of this app --
// run in a goroutine since ShellExecute's "runas" verb blocks until the UAC
// prompt is answered, which could take a while if the user steps away.
func relaunchElevatedClicked() {
	go func() {
		err := relaunchElevated()
		fyne.Do(func() {
			if captureWindow == nil {
				return
			}
			if err != nil {
				if isUserCancelledElevation(err) {
					return // declining the UAC prompt is a normal choice, not an error
				}
				dialog.ShowError(err, captureWindow)
				return
			}
			dialog.ShowInformation("Capture Trace", "A new, elevated instance is starting -- use Capture Trace there. This window can stay open.", captureWindow)
		})
	}()
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
