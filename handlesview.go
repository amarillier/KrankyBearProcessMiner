package main

import (
	"fmt"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
)

// handleColumn is handlesview.go's equivalent of threadsview.go's
// threadColumn -- same tooltip-header-over-aligned-table shape as every
// other list of process-y data in this app.
type handleColumn struct {
	title   string
	width   float32
	tooltip string
}

var handleColumns = []handleColumn{
	{"Handle", 80, "Handle value (Windows) / file descriptor number (macOS, Linux)"},
	{"Type", 110, "Kernel-reported object type -- e.g. File, Key, Event, Section (Windows); vnode, socket, pipe (macOS); file, socket, pipe, anon_inode (Linux)"},
	{"Path", 460, "Resolved target where known -- blank means unresolvable (not a claim it has none). See Help for why some Windows handles show \"(query timed out)\" instead."},
}

// handlesWindow/-Open mirror the state windows.go's hide-all/show-all cares
// about, same reasoning as threadsview.go's threadsWindow/-Open: the window
// itself is owned by procViewState.handlesWin, private state with no other
// package-level hook for windows.go to reach into.
var handlesWindow fyne.Window
var handlesOpen bool

// buildHandlesWindow constructs the single reusable "Show Handles" window,
// lazily, the first time it's used -- direct structural mirror of
// threadsview.go's buildThreadsWindow, same plain-close-not-hide-and-reuse
// reasoning (this window tracks one specific live process, so there's
// nothing worth restoring after it closes).
func (st *procViewState) buildHandlesWindow() {
	st.handlesWin = st.app.NewWindow("")
	st.handlesWin.SetIcon(resourceKrankyBearProcessMinerPng)
	handlesWindow = st.handlesWin

	st.handlesWinTable = widget.NewTable(
		func() (int, int) { return len(st.handlesWinSummary.Handles), len(handleColumns) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		st.updateHandlesWinCell,
	)
	st.handlesWinTable.ShowHeaderRow = true
	st.handlesWinTable.CreateHeader = func() fyne.CanvasObject { return ttwidget.NewButton("", nil) }
	st.handlesWinTable.UpdateHeader = st.updateHandlesWinHeader
	for i, col := range handleColumns {
		st.handlesWinTable.SetColumnWidth(i, col.width)
	}

	st.handlesWinBanner = widget.NewLabel("")
	st.handlesWinBanner.Wrapping = fyne.TextWrapWord
	st.handlesWinBanner.Importance = widget.WarningImportance
	st.handlesWinBanner.Hide()

	st.handlesWinTableSection = container.NewBorder(st.handlesWinBanner, nil, nil, nil, st.handlesWinTable)

	// Covers two states that both replace the table outright: a fetch is in
	// flight, or it failed -- stacked with the table rather than swapping
	// SetContent so the window can be updated in place without rebuilding it.
	st.handlesWinCountLabel = widget.NewLabel("")
	st.handlesWinCountLabel.Wrapping = fyne.TextWrapWord

	st.handlesWinStack = container.NewStack(st.handlesWinTableSection, st.handlesWinCountLabel)

	st.handlesWin.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(st.handlesWinStack), st.handlesWin.Canvas()))
	st.handlesWin.Resize(fyne.NewSize(820, 480))

	st.handlesWin.SetOnClosed(func() {
		fynetooltip.DestroyWindowToolTipLayer(st.handlesWin.Canvas())
		st.handlesWin = nil
		handlesWindow = nil
		handlesOpen = false
		st.handlesWinPID = -1
		st.handlesWinSummary = HandleSummary{}
	})
}

// openOrRefreshHandlesWindow shows the single reusable Handles window for
// pid/name, creating it on first use, or updating it in place if already
// open -- line-for-line the same shape as threadsview.go's
// openOrRefreshThreadsWindow, including following the selection
// automatically (see procview.go's renderDetail) and re-checking pid after
// the fetch in case the user moved on meanwhile.
//
// The actual gather runs in a goroutine: on Windows especially, this does
// real syscalls per handle (DuplicateHandle + two NtQueryObject calls each),
// not a cheap read, so keeping it off the UI goroutine matters even more
// here than for gatherThreadSummary.
func (st *procViewState) openOrRefreshHandlesWindow(pid int32, name string) {
	if st.handlesWin == nil {
		st.buildHandlesWindow()
	}
	st.handlesWinPID = pid
	st.handlesWin.SetTitle(fmt.Sprintf("%s%s (PID %d) — Handles", adminTitlePrefix(), name, pid))
	st.handlesWinTableSection.Hide()
	st.handlesWinCountLabel.SetText("Loading…")
	st.handlesWinCountLabel.Show()
	handlesOpen = true
	st.handlesWin.Show()
	st.handlesWin.RequestFocus()

	go func() {
		summary, err := gatherHandleSummary(pid)
		fyne.Do(func() {
			if st.handlesWin == nil || st.handlesWinPID != pid {
				return // window closed, or the user selected yet another process meanwhile
			}
			if err != nil {
				st.handlesWinSummary = HandleSummary{}
				st.handlesWinTableSection.Hide()
				st.handlesWinCountLabel.SetText(fmt.Sprintf("Couldn't read handles for %q (PID %d): %v", name, pid, err))
				st.handlesWinCountLabel.Show()
				return
			}
			st.handlesWinSummary = summary

			if summary.TimedOut > 0 {
				st.handlesWinBanner.SetText(fmt.Sprintf(
					"%d handle name lookup(s) timed out and show \"(query timed out)\" instead of a path -- a rare, known hazard for certain handle types (e.g. some named pipes). See Help.",
					summary.TimedOut))
				st.handlesWinBanner.Show()
			} else {
				st.handlesWinBanner.Hide()
			}
			st.handlesWinTable.Refresh()
			st.handlesWinCountLabel.Hide()
			st.handlesWinTableSection.Show()
		})
	}()
}

func (st *procViewState) updateHandlesWinCell(id widget.TableCellID, o fyne.CanvasObject) {
	label := o.(*widget.Label)
	handles := st.handlesWinSummary.Handles
	if id.Row < 0 || id.Row >= len(handles) {
		label.SetText("")
		return
	}
	h := handles[id.Row]
	switch id.Col {
	case 0:
		label.SetText(strconv.FormatUint(h.Value, 10))
	case 1:
		label.SetText(orNA(h.Type))
	case 2:
		label.SetText(orNA(h.Path))
	}
}

func (st *procViewState) updateHandlesWinHeader(id widget.TableCellID, o fyne.CanvasObject) {
	btn := o.(*ttwidget.Button)
	if id.Col < 0 || id.Col >= len(handleColumns) {
		btn.SetText("")
		return
	}
	col := handleColumns[id.Col]
	btn.SetText(col.title)
	btn.SetToolTip(col.tooltip)
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
