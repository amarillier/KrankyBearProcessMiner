package main

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
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

// showThreads opens a window listing pid's threads -- a fresh one-off
// snapshot, not a live view. Thread lists change fast and constantly
// refreshing one adds little value for what this is actually for: a quick
// look at whether a process has any threads starting outside its own
// loaded modules.
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
func showThreads(a fyne.App, parent fyne.Window, pid int32, name string) {
	summary, err := gatherThreadSummary(pid)
	if err != nil {
		dialog.ShowError(fmt.Errorf("couldn't read threads for %q (PID %d): %w", name, pid, err), parent)
		return
	}

	win := a.NewWindow(fmt.Sprintf("%s (PID %d) — Threads", name, pid))
	win.SetIcon(resourceKrankyBearProcessMinerPng)

	if summary.Threads == nil {
		msg := widget.NewLabel(fmt.Sprintf(
			"%d threads.\n\nPer-thread detail (start address, state) isn't available on this platform yet -- see Help for why.",
			summary.Count))
		msg.Wrapping = fyne.TextWrapWord
		win.SetContent(container.NewPadded(msg))
		win.Resize(fyne.NewSize(380, 160))
		win.Show()
		return
	}

	// A real header row over aligned columns -- graduated from a single
	// free-form text line per row (which had no way to label what each
	// field meant) once it was clear that wasn't obvious to someone who
	// hasn't been following this feature's development, same as the
	// children drill-down window graduated from a plain list once proven
	// worth the fuller treatment.
	table := widget.NewTable(
		func() (int, int) { return len(summary.Threads), len(threadColumns) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(id widget.TableCellID, o fyne.CanvasObject) {
			label := o.(*widget.Label)
			if id.Row < 0 || id.Row >= len(summary.Threads) {
				label.SetText("")
				return
			}
			t := summary.Threads[id.Row]
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
		},
	)
	table.ShowHeaderRow = true
	table.CreateHeader = func() fyne.CanvasObject { return ttwidget.NewButton("", nil) }
	table.UpdateHeader = func(id widget.TableCellID, o fyne.CanvasObject) {
		btn := o.(*ttwidget.Button)
		if id.Col < 0 || id.Col >= len(threadColumns) {
			btn.SetText("")
			return
		}
		col := threadColumns[id.Col]
		btn.SetText(col.title)
		btn.SetToolTip(col.tooltip)
	}
	for i, col := range threadColumns {
		table.SetColumnWidth(i, col.width)
	}

	var content fyne.CanvasObject = table
	if summary.ModulesUnresolved != "" {
		// See ThreadSummary.ModulesUnresolved: every Start above reads N/A
		// because the module list itself couldn't be read this time (often
		// a transient Toolhelp32 hiccup against a busy process, not a real
		// permissions problem) -- say so, rather than leaving blank start
		// addresses looking like a clean bill of health or an unexplained
		// bug. Click "Show Threads" again to retry.
		banner := widget.NewLabel("Couldn't resolve start addresses this time (module list unavailable -- " +
			summary.ModulesUnresolved + "). Every \"Start Address\" below is unknown, not verified clean. Try again.")
		banner.Wrapping = fyne.TextWrapWord
		banner.Importance = widget.WarningImportance
		content = container.NewBorder(banner, nil, nil, nil, table)
	}

	win.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(content), win.Canvas()))
	win.Resize(fyne.NewSize(760, 480))
	win.SetCloseIntercept(func() {
		fynetooltip.DestroyWindowToolTipLayer(win.Canvas())
		win.Close()
	})
	win.Show()
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
