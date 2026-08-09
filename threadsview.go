package main

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
)

// threadColumn is threadsview.go's equivalent of procview.go's procColumn --
// a static header row (with a hover tooltip explaining the field) over an
// aligned table, the same shape as every other list of process-y data in
// this app, instead of one free-form text line per row.
type threadColumn struct {
	title   string
	width   float32
	tooltip string
}

var threadColumns = []threadColumn{
	{"TID", 80, "Thread ID"},
	{"State", 110, "The thread's current scheduling state (Running, Waiting, etc.)"},
	{"Start Address", 320, "Where this thread began executing, resolved to \"module.dll+0xOFFSET\" against the process's own loaded modules -- or \"UNBACKED (possible injection)\" if the address falls outside every one of them, the classic sign of reflective DLL injection or shellcode. Blank/N/A means unresolved (not a claim either way), not verified clean."},
	{"Kernel Time", 100, "Total CPU time this thread has spent in kernel mode"},
	{"User Time", 100, "Total CPU time this thread has spent in user mode"},
}

// buildThreadsWindow constructs the single reusable Threads window, lazily,
// the first time "Show Threads" is used. Only one is ever open at a time --
// openOrRefreshThreadsWindow updates it in place for a newly selected
// process instead of opening a second one, mirroring showChildWindow.
//
// Windows: full detail via gatherThreadSummary -- TID, state, start
// address resolved to "module.dll+0xOFFSET", or "UNBACKED (possible
// injection)" if the address falls outside every loaded module (the
// classic sign of reflective DLL injection or shellcode). This does NOT
// detect AV/EDR-style *hooking* of an otherwise-legitimate module (patched
// bytes inside e.g. ntdll.dll) -- that needs comparing in-memory bytes
// against the on-disk DLL at known API entry points, a separate, larger
// piece of work (see ReleaseNotes.txt's "Some day maybe").
//
// macOS/Linux: just a thread count for now -- resolving start addresses
// needs reading another process's memory, which needs an entitlement Apple
// doesn't grant ordinary third-party apps on macOS, or root/CAP_SYS_PTRACE
// on Linux (see threads_other.go / Help for the full explanation).
func (st *procViewState) buildThreadsWindow() {
	st.threadsWin = st.app.NewWindow("")
	st.threadsWin.SetIcon(resourceKrankyBearProcessMinerPng)

	// A real header row over aligned columns -- graduated from a single
	// free-form text line per row (which had no way to label what each
	// field meant) once it was clear that wasn't obvious to someone who
	// hasn't been following this feature's development, same as the
	// children drill-down window graduated from a plain list once proven
	// worth the fuller treatment.
	st.threadsWinTable = widget.NewTable(
		func() (int, int) { return len(st.threadsWinSummary.Threads), len(threadColumns) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		st.updateThreadsWinCell,
	)
	st.threadsWinTable.ShowHeaderRow = true
	st.threadsWinTable.CreateHeader = func() fyne.CanvasObject { return ttwidget.NewButton("", nil) }
	st.threadsWinTable.UpdateHeader = st.updateThreadsWinHeader
	for i, col := range threadColumns {
		st.threadsWinTable.SetColumnWidth(i, col.width)
	}

	st.threadsWinBanner = widget.NewLabel("")
	st.threadsWinBanner.Wrapping = fyne.TextWrapWord
	st.threadsWinBanner.Importance = widget.WarningImportance
	st.threadsWinBanner.Hide()

	st.threadsWinTableSection = container.NewBorder(st.threadsWinBanner, nil, nil, nil, st.threadsWinTable)

	// Covers three states that all replace the table outright: the platform
	// has no per-thread detail at all (macOS/Linux), a fetch is in flight
	// (see openOrRefreshThreadsWindow's "Loading…"), or the fetch failed --
	// stacked with the table rather than swapping SetContent so the window
	// can be updated in place without rebuilding it.
	st.threadsWinCountLabel = widget.NewLabel("")
	st.threadsWinCountLabel.Wrapping = fyne.TextWrapWord

	st.threadsWinStack = container.NewStack(st.threadsWinTableSection, st.threadsWinCountLabel)

	st.threadsWin.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(st.threadsWinStack), st.threadsWin.Canvas()))
	st.threadsWin.Resize(fyne.NewSize(760, 480))

	// A plain close (not hide-and-reuse like About/Help/Update): this
	// window tracks one specific live process, so there's nothing worth
	// restoring later -- just forget it and build fresh next time.
	st.threadsWin.SetOnClosed(func() {
		fynetooltip.DestroyWindowToolTipLayer(st.threadsWin.Canvas())
		st.threadsWin = nil
		st.threadsWinPID = -1
		st.threadsWinSummary = ThreadSummary{}
	})
}

// openOrRefreshThreadsWindow shows the single reusable Threads window for
// pid/name, creating it on first use, or updating it in place if it's
// already open. Called both from the "Show Threads" button (always,
// re-snapshotting even if pid is already what's shown -- a deliberate
// fresh look, not a live view, see the doc comment above) and,
// automatically, from renderDetail whenever a different process becomes
// selected while the window happens to be open -- so switching processes
// in the main table, a child window, or Resource Details' Top Consumers
// never leaves the Threads window pointed at a now-unrelated process, the
// gap that prompted this (previously, re-clicking "Show Threads" for a new
// selection just opened a second window).
//
// The actual gather runs in a goroutine (gatherThreadSummary does real
// syscalls -- a system-wide NtQuerySystemInformation call on Windows, not a
// cheap read) so switching processes stays responsive; pid is re-checked
// after the fetch in case the user moved on again before it finished.
func (st *procViewState) openOrRefreshThreadsWindow(pid int32, name string) {
	if st.threadsWin == nil {
		st.buildThreadsWindow()
	}
	st.threadsWinPID = pid
	st.threadsWin.SetTitle(fmt.Sprintf("%s (PID %d) — Threads", name, pid))
	st.threadsWinTableSection.Hide()
	st.threadsWinCountLabel.SetText("Loading…")
	st.threadsWinCountLabel.Show()
	st.threadsWin.Show()
	st.threadsWin.RequestFocus()

	go func() {
		summary, err := gatherThreadSummary(pid)
		fyne.Do(func() {
			if st.threadsWin == nil || st.threadsWinPID != pid {
				return // window closed, or the user selected yet another process meanwhile
			}
			if err != nil {
				st.threadsWinSummary = ThreadSummary{}
				st.threadsWinTableSection.Hide()
				st.threadsWinCountLabel.SetText(fmt.Sprintf("Couldn't read threads for %q (PID %d): %v", name, pid, err))
				st.threadsWinCountLabel.Show()
				return
			}
			st.threadsWinSummary = summary

			if summary.Threads == nil {
				st.threadsWinCountLabel.SetText(fmt.Sprintf(
					"%d threads.\n\nPer-thread detail (start address, state) isn't available on this platform yet -- see Help for why.",
					summary.Count))
				st.threadsWinTableSection.Hide()
				st.threadsWinCountLabel.Show()
				return
			}

			// See ThreadSummary.ModulesUnresolved: every Start above reads
			// N/A because the module list itself couldn't be read this time
			// (often a transient Toolhelp32 hiccup against a busy process,
			// not a real permissions problem) -- say so, rather than
			// leaving blank start addresses looking like a clean bill of
			// health or an unexplained bug.
			if summary.ModulesUnresolved != "" {
				st.threadsWinBanner.SetText("Couldn't resolve start addresses this time (module list unavailable -- " +
					summary.ModulesUnresolved + "). Every \"Start Address\" below is unknown, not verified clean. Reselect the process (or click \"Show Threads\" again) to retry.")
				st.threadsWinBanner.Show()
			} else {
				st.threadsWinBanner.Hide()
			}
			st.threadsWinTable.Refresh()
			st.threadsWinCountLabel.Hide()
			st.threadsWinTableSection.Show()
		})
	}()
}

func (st *procViewState) updateThreadsWinCell(id widget.TableCellID, o fyne.CanvasObject) {
	label := o.(*widget.Label)
	threads := st.threadsWinSummary.Threads
	if id.Row < 0 || id.Row >= len(threads) {
		label.SetText("")
		return
	}
	t := threads[id.Row]
	switch id.Col {
	case 0:
		label.SetText(fmt.Sprintf("%d", t.TID))
	case 1:
		label.SetText(t.State)
	case 2:
		label.SetText(orNA(t.StartAddr))
	case 3:
		label.SetText(t.KernelTime.Round(time.Millisecond).String())
	case 4:
		label.SetText(t.UserTime.Round(time.Millisecond).String())
	}
}

func (st *procViewState) updateThreadsWinHeader(id widget.TableCellID, o fyne.CanvasObject) {
	btn := o.(*ttwidget.Button)
	if id.Col < 0 || id.Col >= len(threadColumns) {
		btn.SetText("")
		return
	}
	col := threadColumns[id.Col]
	btn.SetText(col.title)
	btn.SetToolTip(col.tooltip)
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
