package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
)

// handlesWinMinPollInterval throttles the Activity column's re-gather
// independent of the main process sampler's own "Refresh every" setting --
// re-listing handles does real per-handle syscalls (DuplicateHandle +
// NtQueryObject each on Windows, proc_pidfdinfo each on macOS), so a 1-2s
// process-refresh setting must not translate into 1-2s handle re-gathers.
// This is checked inline on the applySnapshot tick that's already
// happening, rather than a ticker/goroutine of its own -- see
// refreshHandlesWindow.
const handlesWinMinPollInterval = 3 * time.Second

// handleActivityTier is the Activity column's light/medium/heavy classification,
// derived from a handle's resolved-path file size delta between polls (see
// computeHandleActivity). There is deliberately no OS-level per-handle byte
// counter behind this on any platform (Windows/macOS/Linux handle-enumeration
// APIs only resolve type+path, not I/O counters) -- this is a best-effort
// proxy from watching file size grow, not a real throughput measurement.
type handleActivityTier int

const (
	// handleActivityNone: no size signal at all -- not a file handle
	// (socket/pipe/etc.), unresolvable path, or not yet stat-able. Renders
	// blank, same "blank is not an alarm" discipline as signatureColumnGlyph.
	handleActivityNone handleActivityTier = iota
	// handleActivityIdle: a real file handle with a known size, but no
	// growth since the last poll (or this is the first poll, so there's no
	// prior size to compare against yet).
	handleActivityIdle
	handleActivityLight
	handleActivityMedium
	handleActivityHeavy
)

// Thresholds, in KB/s of file growth between polls. Deliberately coarse --
// this is a "does this look busy" indicator, not a precise measurement.
const (
	handleActivityMediumKBps = 512.0
	handleActivityHeavyKBps  = 4096.0
)

func classifyHandleActivityRate(kbps float64) handleActivityTier {
	switch {
	case kbps <= 0:
		return handleActivityIdle
	case kbps < handleActivityMediumKBps:
		return handleActivityLight
	case kbps < handleActivityHeavyKBps:
		return handleActivityMedium
	default:
		return handleActivityHeavy
	}
}

// handleActivityGlyph renders the Activity column's icon for one tier,
// direct structural mirror of procview.go's signatureColumnGlyph (same
// glyph-plus-Importance shape, same reliance on widget.Label.Importance to
// actually produce the color).
func handleActivityGlyph(tier handleActivityTier) (text string, importance widget.Importance) {
	switch tier {
	case handleActivityIdle:
		return "○", widget.MediumImportance
	case handleActivityLight:
		return "●", widget.SuccessImportance
	case handleActivityMedium:
		return "●", widget.WarningImportance
	case handleActivityHeavy:
		return "●", widget.DangerImportance
	default: // handleActivityNone
		return "", widget.MediumImportance
	}
}

// handleActivityKey identifies a handle across polls for the size-delta
// comparison. Keyed by resolved path (not handle value/fd number, which the
// OS can and does reuse across opens) -- Type is folded in only to keep a
// path that briefly reappears under a different reported type from being
// compared against a stale size.
func handleActivityKey(h HandleDetail) string {
	return h.Type + "\x00" + h.Path
}

// computeHandleActivity stat()s every handle with a resolved path and turns
// the size delta since the previous poll into a per-handle activity tier.
// This is the same cumulative-counter -> per-tick-delta pattern as
// procsampler.go's Disk R/W computation (prevProcIO / clampDelta), just
// keyed by handle instead of by PID, and using file size in place of an
// IOCounters byte count since no platform exposes real per-handle I/O
// counters (see handleActivityTier's doc comment). Updates
// st.handlesWinPrevSizeByKey/-LastPollAt in place for the next call; callers
// that are starting a fresh PID must clear those first (see
// openOrRefreshHandlesWindow) so the new process's files aren't compared
// against the old process's sizes.
func (st *procViewState) computeHandleActivity(handles []HandleDetail, now time.Time) map[string]handleActivityTier {
	dt := 0.0
	if !st.handlesWinLastPollAt.IsZero() {
		dt = now.Sub(st.handlesWinLastPollAt).Seconds()
	}

	activity := make(map[string]handleActivityTier, len(handles))
	newSizes := make(map[string]int64, len(handles))
	for _, h := range handles {
		if h.Path == "" {
			continue // no filesystem target to stat -- leave as handleActivityNone
		}
		info, err := os.Stat(h.Path)
		if err != nil || !info.Mode().IsRegular() {
			continue // socket/pipe synthetic path, directory, or gone -- handleActivityNone
		}

		key := handleActivityKey(h)
		size := info.Size()
		newSizes[key] = size

		prevSize, ok := st.handlesWinPrevSizeByKey[key]
		if !ok || dt <= 0 {
			activity[key] = handleActivityIdle // no baseline yet -- don't guess a rate
			continue
		}
		delta := size - prevSize
		if delta < 0 {
			delta = 0 // truncated/replaced file guard, same discipline as clampDelta
		}
		activity[key] = classifyHandleActivityRate(float64(delta) / 1024 / dt)
	}

	st.handlesWinPrevSizeByKey = newSizes
	st.handlesWinLastPollAt = now
	return activity
}

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
	{"Activity", 70, "Best-effort recent activity on this handle's file, from watching its size change every few seconds: ○ idle (no growth) -- ● green/orange/red = light/medium/heavy write throughput. Blank = no signal available (sockets, pipes, unresolved paths) -- not a claim the handle is inactive."},
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
		func() (int, int) { return len(st.handlesWinDisplay), len(handleColumns) },
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

	st.handlesWinFilterEntry = widget.NewEntry()
	st.handlesWinFilterEntry.SetPlaceHolder("Filter by type or path…")
	st.handlesWinFilterEntry.OnChanged = func(text string) {
		st.handlesWinFilterText = text
		st.recomputeHandlesWinRows()
	}

	content := container.NewBorder(st.handlesWinFilterEntry, nil, nil, nil, st.handlesWinStack)
	st.handlesWin.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(content), st.handlesWin.Canvas()))
	st.handlesWin.Resize(fyne.NewSize(820, 480))

	st.handlesWin.SetOnClosed(func() {
		fynetooltip.DestroyWindowToolTipLayer(st.handlesWin.Canvas())
		st.handlesWin = nil
		handlesWindow = nil
		handlesOpen = false
		st.handlesWinPID = -1
		st.handlesWinSummary = HandleSummary{}
		st.handlesWinAllHandles = nil
		st.handlesWinDisplay = nil
		st.handlesWinFilterText = ""
		st.handlesWinActivity = nil
		st.handlesWinPrevSizeByKey = nil
		st.handlesWinLastPollAt = time.Time{}
		st.handlesWinFetchInFlight = false
	})
}

// openOrRefreshHandlesWindow shows the single reusable Handles window for
// pid/name, creating it on first use, or updating it in place if already
// open -- line-for-line the same shape as threadsview.go's
// openOrRefreshThreadsWindow, including following the selection
// automatically (see procview.go's renderDetail).
//
// Unlike threadsWin, this window keeps polling after the initial open (see
// refreshHandlesWindow) to drive the Activity column, so a PID change here
// must also throw away the previous process's size-delta baseline --
// otherwise the first poll for the new process would compare its files'
// sizes against the old process's, producing meaningless deltas.
func (st *procViewState) openOrRefreshHandlesWindow(pid int32, name string) {
	if st.handlesWin == nil {
		st.buildHandlesWindow()
	}
	st.handlesWinPID = pid
	st.handlesWinActivity = nil
	st.handlesWinPrevSizeByKey = nil
	st.handlesWinLastPollAt = time.Time{}
	st.handlesWin.SetTitle(fmt.Sprintf("%s%s (PID %d) — Handles", adminTitlePrefix(), name, pid))
	st.handlesWinTableSection.Hide()
	st.handlesWinCountLabel.SetText("Loading…")
	st.handlesWinCountLabel.Show()
	handlesOpen = true
	st.handlesWin.Show()
	st.handlesWin.RequestFocus()
	st.fetchHandles(pid, name)
}

// refreshHandlesWindow is called once per applySnapshot tick (see
// procview.go) to keep an already-open Handles window's Activity column
// live -- the same "keep it in sync every sample" behavior
// refreshConnectionsWindow/refreshChildWindow already give their windows.
// Unlike those, re-gathering handles does real per-handle syscalls (see
// openOrRefreshHandlesWindow's doc comment), so this self-throttles to
// handlesWinMinPollInterval regardless of how fast the main process sampler
// is configured to tick -- no new ticker/goroutine, just a time check
// piggybacked on the tick that already happens. No-op if no Handles window
// is open, if a fetch is already in flight, or if the tracked process has
// exited.
func (st *procViewState) refreshHandlesWindow() {
	if st.handlesWin == nil || st.handlesWinFetchInFlight {
		return
	}
	if !st.handlesWinLastPollAt.IsZero() && time.Since(st.handlesWinLastPollAt) < handlesWinMinPollInterval {
		return
	}
	p, ok := st.byPID[st.handlesWinPID]
	if !ok {
		st.handlesWin.SetTitle(adminTitlePrefix() + "(process exited)")
		return
	}
	st.handlesWin.SetTitle(fmt.Sprintf("%s%s (PID %d) — Handles", adminTitlePrefix(), p.Name, p.PID))
	st.fetchHandles(p.PID, p.Name)
}

// fetchHandles runs gatherHandleSummary in a goroutine and applies the
// result back on the UI goroutine -- shared by the user-initiated open
// (openOrRefreshHandlesWindow) and the periodic live refresh
// (refreshHandlesWindow). On Windows especially this does real syscalls per
// handle (DuplicateHandle + two NtQueryObject calls each), not a cheap
// read, so keeping it off the UI goroutine matters even more here than for
// gatherThreadSummary -- and handlesWinFetchInFlight keeps a slow gather
// from overlapping with the next tick's attempt.
func (st *procViewState) fetchHandles(pid int32, name string) {
	st.handlesWinFetchInFlight = true
	go func() {
		summary, err := gatherHandleSummary(pid)
		now := time.Now()
		fyne.Do(func() {
			st.handlesWinFetchInFlight = false
			if st.handlesWin == nil || st.handlesWinPID != pid {
				return // window closed, or the user selected yet another process meanwhile
			}
			if err != nil {
				st.handlesWinSummary = HandleSummary{}
				st.handlesWinAllHandles = nil
				st.recomputeHandlesWinRows()
				st.handlesWinTableSection.Hide()
				st.handlesWinCountLabel.SetText(fmt.Sprintf("Couldn't read handles for %q (PID %d): %v", name, pid, err))
				st.handlesWinCountLabel.Show()
				return
			}
			st.handlesWinSummary = summary
			st.handlesWinAllHandles = summary.Handles
			st.handlesWinActivity = st.computeHandleActivity(summary.Handles, now)
			st.recomputeHandlesWinRows()

			if summary.TimedOut > 0 {
				st.handlesWinBanner.SetText(fmt.Sprintf(
					"%d handle name lookup(s) timed out and show \"(query timed out)\" instead of a path -- a rare, known hazard for certain handle types (e.g. some named pipes). See Help.",
					summary.TimedOut))
				st.handlesWinBanner.Show()
			} else {
				st.handlesWinBanner.Hide()
			}
			st.handlesWinCountLabel.Hide()
			st.handlesWinTableSection.Show()
		})
	}()
}

// recomputeHandlesWinRows applies the Handles window's own type/path filter
// and column sort to handlesWinAllHandles -- same shape as procview.go's
// recomputeChildWinRows, just scoped to this window's own state.
func (st *procViewState) recomputeHandlesWinRows() {
	needle := strings.ToLower(strings.TrimSpace(st.handlesWinFilterText))
	rows := make([]HandleDetail, 0, len(st.handlesWinAllHandles))
	for _, h := range st.handlesWinAllHandles {
		if needle == "" ||
			strings.Contains(strings.ToLower(h.Type), needle) ||
			strings.Contains(strings.ToLower(h.Path), needle) {
			rows = append(rows, h)
		}
	}
	if st.handlesWinSortActive {
		col := st.handlesWinSortCol
		sort.SliceStable(rows, func(i, j int) bool {
			c := st.compareHandle(rows[i], rows[j], col)
			if st.handlesWinSortAsc {
				return c < 0
			}
			return c > 0
		})
	}
	st.handlesWinDisplay = rows
	st.handlesWinTable.Refresh()
}

// handleActivityTierFor looks up a handle's current activity tier the same
// way updateHandlesWinCell renders it -- shared so compareHandle's Activity
// column sort ranks rows exactly as the icon they show would suggest.
func (st *procViewState) handleActivityTierFor(h HandleDetail) handleActivityTier {
	if h.Path == "" {
		return handleActivityNone
	}
	if t, ok := st.handlesWinActivity[handleActivityKey(h)]; ok {
		return t
	}
	return handleActivityNone
}

// compareHandle orders two handles by one handleColumns index: Handle
// numerically, Type/Path alphabetically (case-insensitive), Activity by
// tier (blank/no-signal < idle < light < medium < heavy).
func (st *procViewState) compareHandle(a, b HandleDetail, col int) int {
	switch col {
	case 0:
		switch {
		case a.Value < b.Value:
			return -1
		case a.Value > b.Value:
			return 1
		default:
			return 0
		}
	case 1:
		return strings.Compare(strings.ToLower(a.Type), strings.ToLower(b.Type))
	case 2:
		ta, tb := st.handleActivityTierFor(a), st.handleActivityTierFor(b)
		switch {
		case ta < tb:
			return -1
		case ta > tb:
			return 1
		default:
			return 0
		}
	case 3:
		return strings.Compare(strings.ToLower(a.Path), strings.ToLower(b.Path))
	default:
		return 0
	}
}

func (st *procViewState) updateHandlesWinCell(id widget.TableCellID, o fyne.CanvasObject) {
	label := o.(*widget.Label)
	handles := st.handlesWinDisplay
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
		text, importance := handleActivityGlyph(st.handleActivityTierFor(h))
		label.Importance = importance
		label.SetText(text)
	case 3:
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
	title := col.title
	if st.handlesWinSortActive && id.Col == st.handlesWinSortCol {
		if st.handlesWinSortAsc {
			title += " ▲"
		} else {
			title += " ▼"
		}
	}
	btn.SetText(title)
	btn.SetToolTip(col.tooltip)
	colIdx := id.Col
	btn.OnTapped = func() { st.toggleHandlesWinSort(colIdx) }
}

// toggleHandlesWinSort mirrors procview.go's toggleChildWinSort ascending ->
// descending -> unsorted -> ascending, ... cycle, scoped to the Handles
// window's own sort state and keyed by column index (see compareHandle).
func (st *procViewState) toggleHandlesWinSort(col int) {
	switch {
	case !st.handlesWinSortActive || st.handlesWinSortCol != col:
		st.handlesWinSortCol = col
		st.handlesWinSortAsc = true
		st.handlesWinSortActive = true
	case st.handlesWinSortAsc:
		st.handlesWinSortAsc = false
	default:
		st.handlesWinSortActive = false
	}
	st.recomputeHandlesWinRows()
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
