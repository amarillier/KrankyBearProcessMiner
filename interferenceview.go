package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
)

var interferenceWindow fyne.Window

// interferenceOpen tracks whether the Interference Watch window should be
// considered "open" for hide-all/show-all purposes (windows.go) -- see
// aboutOpen in about.go.
var interferenceOpen bool

// interferenceRenderWatchList/-Events re-populate the currently-open
// window's two lists from the shared watcher, without rebuilding the whole
// window. Set once by showInterferenceWindow (nil until it's been opened at
// least once); refreshInterferenceWindow calls both, cheaply, whenever the
// watch list or event log changes -- see procview.go's toggleWatchSelected
// and interference.go's check().
var interferenceRenderWatchList func()
var interferenceRenderEvents func()

// refreshInterferenceWindow re-renders the Interference Watch window's
// contents if it's currently open; a no-op otherwise (including before it's
// ever been built). Safe to call unconditionally after anything that could
// change the watch list or event log.
func refreshInterferenceWindow() {
	if !interferenceOpen {
		return
	}
	if interferenceRenderWatchList != nil {
		interferenceRenderWatchList()
	}
	if interferenceRenderEvents != nil {
		interferenceRenderEvents()
	}
}

// watchedPIDStatus returns an at-a-glance icon/color for one explicitly-
// watched process: "✓" (success) if nothing has ever been logged against
// it, otherwise the worst severity among its own events (see
// interferenceEventSeverity) -- "🛑"/danger if any of them are, "⚠"/warning
// if all of them are merely a recognized vendor's module. Deliberately
// scoped to explicit PID watches only, not directory entries: attributing
// a directory watch's *historical* events back to it reliably would need
// tracking that per-event, which isn't worth the complexity for a glance
// indicator -- a directory entry just shows as a plain listing.
func watchedPIDStatus(watcher *interferenceWatcher, pid int32) (icon string, importance widget.Importance) {
	hasEvent := false
	worstDanger := false
	for _, e := range watcher.events {
		if e.PID != pid {
			continue
		}
		hasEvent = true
		if _, isDanger := interferenceEventSeverity(e); isDanger {
			worstDanger = true
		}
	}
	if !hasEvent {
		return "✓", widget.SuccessImportance
	}
	if worstDanger {
		return "🛑", widget.DangerImportance
	}
	return "⚠", widget.WarningImportance
}

// showInterferenceWindow opens (or reveals) the Interference Watch window:
// the watched-targets list (processes added via the main table's "Watch for
// Interference" button, plus any watched directories) and the interference
// event log, with Copy to Clipboard / Clear. See interference.go's
// interferenceWatcher for the detection semantics this surfaces.
//
// onWatchListChanged is called whenever a watch is removed from here, so the
// main table's own Watch button/alert column stay in sync (procview.go's
// refreshAfterWatchChange).
func showInterferenceWindow(a fyne.App, watcher *interferenceWatcher, onWatchListChanged func()) {
	if interferenceWindow != nil && interferenceOpen {
		interferenceWindow.Show()
		interferenceWindow.RequestFocus()
		refreshInterferenceWindow()
		return
	}

	win := a.NewWindow(appName + " - Interference Watch")
	win.SetIcon(resourceKrankyBearProcessMinerPng)
	interferenceWindow = win

	var banner *widget.Label
	if threadStartAddressSupported {
		banner = widget.NewLabel("Watches selected processes and/or directories for four signs of " +
			"interference: a thread starting outside any loaded module (\"UNBACKED\" -- reflective " +
			"injection, malware's usual technique for staying off the module list) or a new module " +
			"(DLL) loading in (the technique legitimate AV/EDR hooking actually uses instead) are " +
			"only reported if they appear after you started watching; a thread-stack scan finding a " +
			"third-party module in a thread's call path is reported the moment it's first seen, even " +
			"on the very first check, since that's evidence of something that may already have been " +
			"happening -- a coarse approximation of the classic \"thread stacking\" technique, not " +
			"true call-stack unwinding, so treat a hit as worth confirming with Process Explorer/" +
			"Procmon, not proof on its own. The fourth -- Windows Defender's own AMFilter minifilter " +
			"scanning a file the process opens, the *other* meaning of \"AV interference\" the other " +
			"three can't see -- only shows up when running elevated (Administrator), and is always " +
			"shown as a plain \"⚠\" rather than \"🛑\" since it's expected, legitimate behavior, not an " +
			"accusation. Trusted/Microsoft-signed processes (e.g. Notepad) skip that scan event " +
			"entirely, so a Defender trust-evaluation registration is logged as a fallback for those -- " +
			"a different, narrower claim (\"Defender is aware of this process\", no file path) than an " +
			"actual file scan. Select a process in the main table and click \"Watch for " +
			"Interference\" to add it here, or add a whole directory below (any process launched from " +
			"it is watched automatically).")
	} else {
		banner = widget.NewLabel("Interference detection needs Windows-only APIs to resolve thread " +
			"start addresses -- watching is inert on this platform (targets can still be added below, " +
			"but nothing will ever be flagged). See Help for the full explanation.")
	}
	banner.Wrapping = fyne.TextWrapWord

	watchListBox := container.NewVBox()
	renderWatchList := func() {
		var rows []fyne.CanvasObject

		pids := make([]int32, 0, len(watcher.pids))
		for pid := range watcher.pids {
			pids = append(pids, pid)
		}
		sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
		for _, pid := range pids {
			pid := pid
			icon, importance := watchedPIDStatus(watcher, pid)
			row := widget.NewLabel(fmt.Sprintf("%s Process: %s (PID %d)", icon, watcher.pids[pid], pid))
			row.Importance = importance
			removeBtn := ttwidget.NewButton("Remove", func() {
				watcher.unwatchPID(pid)
				onWatchListChanged()
				refreshInterferenceWindow()
			})
			removeBtn.SetToolTip("Stop watching this process")
			rows = append(rows, container.NewBorder(nil, nil, nil, removeBtn, row))
		}

		dirs := append([]string(nil), watcher.dirs...)
		sort.Strings(dirs)
		for _, dir := range dirs {
			dir := dir
			row := widget.NewLabel("Directory: " + dir)
			removeBtn := ttwidget.NewButton("Remove", func() {
				watcher.unwatchDir(dir)
				onWatchListChanged()
				refreshInterferenceWindow()
			})
			removeBtn.SetToolTip("Stop watching this directory (processes already flagged/logged stay in the event log)")
			rows = append(rows, container.NewBorder(nil, nil, nil, removeBtn, row))
		}

		if len(rows) == 0 {
			rows = append(rows, widget.NewLabel("Nothing watched yet."))
		}
		watchListBox.Objects = rows
		watchListBox.Refresh()
	}
	interferenceRenderWatchList = renderWatchList

	addDirBtn := ttwidget.NewButton("Add Directory to Watch…", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			watcher.watchDir(uri.Path())
			refreshInterferenceWindow()
		}, win)
	})
	addDirBtn.SetToolTip("Watch every process launched from this directory, automatically -- including ones started after you add it")
	if !threadStartAddressSupported {
		addDirBtn.Disable()
	}

	watchSection := container.NewBorder(
		container.NewVBox(widget.NewLabelWithStyle("Watched Targets", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), addDirBtn),
		nil, nil, nil,
		container.NewVScroll(watchListBox),
	)

	eventsBox := container.NewVBox()
	renderEvents := func() {
		var rows []fyne.CanvasObject
		for i := len(watcher.events) - 1; i >= 0; i-- {
			e := watcher.events[i]
			icon, isDanger := interferenceEventSeverity(e)
			lbl := widget.NewLabel(icon + " " + formatInterferenceEvent(e))
			if isDanger {
				lbl.Importance = widget.DangerImportance
			} else {
				lbl.Importance = widget.WarningImportance
			}
			rows = append(rows, lbl)
		}
		if len(rows) == 0 {
			rows = append(rows, widget.NewLabel("No interference detected yet."))
		}
		eventsBox.Objects = rows
		eventsBox.Refresh()
	}
	interferenceRenderEvents = renderEvents

	const copyLabel = "Copy to Clipboard"
	var copyBtn *ttwidget.Button
	copyBtn = ttwidget.NewButton(copyLabel, func() {
		var sb strings.Builder
		for _, e := range watcher.events {
			icon, _ := interferenceEventSeverity(e)
			sb.WriteString(icon)
			sb.WriteString(" ")
			sb.WriteString(formatInterferenceEvent(e))
			sb.WriteString("\n")
		}
		if sb.Len() == 0 {
			sb.WriteString("No interference detected yet.\n")
		}
		win.Clipboard().SetContent(sb.String())

		// Clicking a button gives no other feedback that anything happened,
		// so flip the label to confirm the copy actually worked, then
		// revert after a couple seconds -- same pattern as System Info's
		// own Copy to Clipboard button.
		copyBtn.SetText("Copied to Clipboard ✓")
		copyBtn.Importance = widget.SuccessImportance
		copyBtn.Refresh()
		time.AfterFunc(2*time.Second, func() {
			fyne.Do(func() {
				copyBtn.SetText(copyLabel)
				copyBtn.Importance = widget.MediumImportance
				copyBtn.Refresh()
			})
		})
	})
	copyBtn.SetToolTip("Copy the full interference event log as plain text")
	clearBtn := ttwidget.NewButton("Clear Log", func() {
		watcher.clearEvents()
		renderEvents()
	})
	clearBtn.SetToolTip("Permanently clear the interference event log (does not stop watching)")
	clearBtn.Importance = widget.DangerImportance

	eventsSection := container.NewBorder(
		container.NewVBox(widget.NewLabelWithStyle("Interference Events", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			container.NewHBox(copyBtn, clearBtn)),
		nil, nil, nil,
		container.NewVScroll(eventsBox),
	)

	split := container.NewVSplit(watchSection, eventsSection)
	split.SetOffset(0.35)

	content := container.NewBorder(container.NewVBox(banner, widget.NewSeparator()), nil, nil, nil, split)

	win.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(content), win.Canvas()))
	win.Resize(fyne.NewSize(640, 560))

	win.SetCloseIntercept(func() {
		interferenceOpen = false
		win.Hide()
	})

	renderWatchList()
	renderEvents()

	interferenceOpen = true
	win.Show()
	win.RequestFocus()
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
