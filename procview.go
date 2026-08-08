package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
)

type sortField int

const (
	sortPID sortField = iota
	sortName
	sortPPID
	sortUser
	sortCPU
	sortMem
	sortMemBytes
	sortDiskRead
	sortDiskWrite
	sortPrivate
	sortAlert   // interference-watch indicator -- see interference.go; not sortable, see compareProc
	sortNotable // AV/EDR recognition + process-masquerading heuristic -- see notable.go; not sortable, see compareProc
)

type procColumn struct {
	title   string
	field   sortField
	width   float32
	tooltip string
}

var procColumns = []procColumn{
	{"⚠", sortAlert, 30, "Interference Watch: flags a process where a thread appeared with no backing module, a new module loaded, or a thread's stack contains a third-party module -- see Interference Watch window for details. Select a process and click \"Watch for Interference\" to add it."},
	{"PID", sortPID, 70, "Process ID"},
	{"Name", sortName, 220, "Process/executable name"},
	{"PPID", sortPPID, 70, "Parent process ID"},
	{"User", sortUser, 110, "The account this process is running as"},
	{"CPU %", sortCPU, 70, "Share of total CPU capacity this process is currently using"},
	{"Mem %", sortMem, 70, "Share of total physical memory this process is currently using"},
	{"Memory", sortMemBytes, 90, "Actual physical memory in use (RSS -- Resident Set Size), the same figure Mem % is computed from. In the Parent-processes view, this is the combined total across the parent and every descendant."},
	{"Disk R", sortDiskRead, 80, "Disk read rate for this process (KB/s)"},
	{"Disk W", sortDiskWrite, 80, "Disk write rate for this process (KB/s)"},
	{"Private", sortPrivate, 90, "Private memory: real Private Bytes on Windows, an RSS-minus-shared approximation on Linux, N/A on macOS (see Help)"},
	{"Notable", sortNotable, 160, "Recognized security software (best-effort name match -- see Help), or a process-masquerading mismatch worth a second look (e.g. svchost.exe not launched by services.exe)"},
}

// consumerThresholdOptions/-Values back the "Top CPU"/"Top Mem" selects
// (see newProcessView): "Off" means unfiltered.
var consumerThresholdOptions = []string{"Off", "≥1%", "≥5%", "≥10%", "≥25%"}

var consumerThresholdValues = map[string]float64{
	"Off":  0,
	"≥1%":  1,
	"≥5%":  5,
	"≥10%": 10,
	"≥25%": 25,
}

// Split-divider positions, persisted like main window size (see main.go's
// mainWindowLaunchSize/saveMainWindowGeometry). Unlike the divider offsets,
// per-column widths can't be persisted the same way -- widget.Table has no
// getter for a column's current width after the user drags it, only
// SetColumnWidth, so there's no value to read back at quit time.
const (
	prefMainSplitOffset   = "processView.mainSplitOffset"
	prefDetailSplitOffset = "processView.detailSplitOffset"
	defaultMainSplit      = 0.7
	defaultDetailSplit    = 0.4
)

func clampSplitOffset(v float64) float64 {
	switch {
	case v < 0.1:
		return 0.1
	case v > 0.9:
		return 0.9
	default:
		return v
	}
}

// procViewState holds everything the process table + detail pane needs.
// Every field here is only ever touched from the main goroutine (sampler
// callbacks are fyne.Do-wrapped by main.go, and widget callbacks already run
// on the main goroutine), so -- like Sampler -- no mutex is needed.
type procViewState struct {
	app     fyne.App
	win     fyne.Window
	sampler *Sampler

	full           ProcessSnapshot
	displayRows    []ProcInfo
	byPID          map[int32]ProcInfo
	filterText     string
	useRegexFilter bool
	filterRegex    *regexp.Regexp // compiled from filterText when useRegexFilter is on; nil if off, empty, or invalid (fails open -- see recompileFilterRegex)
	minCPU         float64        // "top consumer" thresholds; 0 = off (see passesConsumerThreshold)
	minMem         float64
	parentsOnly    bool // "Parent processes" view -- see isParentRow
	sortCol        sortField
	sortAsc        bool
	selectedPID    int32 // -1 = none

	// reselecting is true only while re-calling table.Select to preserve
	// the already-selected row's highlight across a data refresh (every
	// sample tick, or after a sort/filter change) -- Fyne's Table.Select
	// unconditionally fires OnSelected even when the selection didn't
	// actually change, so without this, onRowSelected's showChildWindow
	// call would reopen and refocus the children drill-down window on
	// every single tick, stealing focus and reappearing even right after
	// the user closed it (confirmed via testing: reproducible purely from
	// "Parent processes only" + a selected parent, unrelated to Interference
	// Watch). Guards only that one side effect, not renderDetail -- the
	// detail pane still needs to refresh after a sort/filter change, which
	// relies on this same Select call triggering onRowSelected.
	reselecting bool

	// watcher and flaggedPIDs back the interference-watch feature (see
	// interference.go): watcher.check() runs once per recompute() (i.e. once
	// per process-list sample), and flaggedPIDs is its latest result, read by
	// updateCell for the "⚠" column.
	watcher     *interferenceWatcher
	flaggedPIDs map[int32]bool

	table       *widget.Table
	filterEntry *widget.Entry
	threadsBtn  *ttwidget.Button
	watchBtn    *ttwidget.Button
	endBtn      *ttwidget.Button

	detailTitle    *widget.Label
	detailMeta     *widget.Label
	detailParents  *widget.Label
	detailCmdline  *widget.Label
	detailChildren []ProcInfo
	childList      *widget.List

	// childWin is the single reusable "<parent> Children" drill-down window
	// opened from the Parent-processes view (see showChildWindow) -- only
	// one is ever open at a time; clicking a different parent updates it in
	// place rather than opening a second window. Sortable/filterable/
	// End-Process-capable, same as the main table, once the simpler
	// read-only version proved the approach worthwhile.
	childWin            fyne.Window
	childWinPID         int32
	childWinAllRows     []ProcInfo // unfiltered direct children of childWinPID
	childWinDisplayRows []ProcInfo // filtered + sorted, what the table shows
	childWinFilterText  string
	childWinSortCol     sortField
	childWinSortAsc     bool
	childWinSelectedPID int32 // -1 = none
	childWinTable       *widget.Table
	childWinFilterEntry *widget.Entry
	childWinEndBtn      *ttwidget.Button
}

// newProcessView builds the process table, toolbar and detail pane. The
// returned applySnapshot func must only be called from the main goroutine
// (i.e. from inside fyne.Do); refreshNow and endSelected are safe to wire
// directly into menu/tray items. saveLayout persists the current split
// positions and must be called before quit (see main.go's quitApp).
func newProcessView(a fyne.App, win fyne.Window, sampler *Sampler) (
	view fyne.CanvasObject,
	applySnapshot func(ProcessSnapshot),
	refreshNow func(),
	endSelected func(),
	saveLayout func(),
	watcher *interferenceWatcher,
	refreshAfterWatchChange func(),
) {
	st := &procViewState{
		app:         a,
		win:         win,
		sampler:     sampler,
		selectedPID: -1,
		childWinPID: -1,
		sortCol:     sortCPU,
		sortAsc:     false,
		watcher:     newInterferenceWatcher(),
	}

	st.table = widget.NewTable(
		func() (int, int) { return len(st.displayRows), len(procColumns) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		st.updateCell,
	)
	st.table.ShowHeaderRow = true
	st.table.CreateHeader = func() fyne.CanvasObject { return ttwidget.NewButton("", nil) }
	st.table.UpdateHeader = st.updateHeader
	st.table.OnSelected = st.onRowSelected
	st.table.OnUnselected = st.onRowUnselected
	for i, col := range procColumns {
		st.table.SetColumnWidth(i, col.width)
	}

	topInfo, childrenSection := st.buildDetailPane()

	st.filterEntry = widget.NewEntry()
	st.filterEntry.SetPlaceHolder("Filter by name… (using regex? check the Regex box too)")
	st.filterEntry.OnChanged = st.setFilter

	// Regex mode swaps the name filter from a plain substring match to a
	// real Go regexp.MatchString against the process name -- e.g.
	// ^(?i)(process|activity).* to compare just ProcessMiner's own
	// processes against Activity Monitor's, with nothing else cluttering
	// the list. An invalid (or mid-typing incomplete) regex fails open --
	// shows every row -- rather than going blank or erroring, since a regex
	// is often invalid for several keystrokes while being typed.
	regexCheck := ttwidget.NewCheck("Regex", func(checked bool) {
		st.useRegexFilter = checked
		st.recompileFilterRegex()
		st.applyFilterAndRefresh()
	})
	regexCheck.SetToolTip("Match the name filter as a regular expression instead of a plain substring -- e.g. ^(?i)(process|activity).*")

	// "Parent processes" view: declutters the table down to processes worth
	// drilling into (see isParentRow) -- clicking a row then opens/updates
	// the single children drill-down window (showChildWindow) instead of
	// just the inline detail pane's own (still-present) children list.
	parentsOnlyCheck := ttwidget.NewCheck("Parent processes only", func(checked bool) {
		st.parentsOnly = checked
		st.applyFilterAndRefresh()
	})
	parentsOnlyCheck.SetToolTip("Show only processes with children (or no visible parent), each with combined CPU%/Mem% across itself and its whole subtree. Click a row to see its individual children.")

	intervalSelect := ttwidget.NewSelect([]string{"1s", "2s", "5s", "10s"}, func(sel string) {
		if d, err := time.ParseDuration(sel); err == nil {
			sampler.SetProcessInterval(d)
		}
	})
	intervalSelect.SetSelected("2s")
	intervalSelect.SetToolTip("How often the process list resamples")

	// "Top consumer" filter: surfaces anything heavy on CPU or memory. Off
	// by default so behavior is unchanged unless the user opts in -- known
	// gap: no per-process disk/network figures are sampled yet (macOS has
	// no per-process disk I/O via gopsutil, and none of the platforms
	// collect per-process network use), so this only covers CPU/Mem for now.
	cpuThresholdSelect := ttwidget.NewSelect(consumerThresholdOptions, func(sel string) {
		st.minCPU = consumerThresholdValues[sel]
		st.applyFilterAndRefresh()
	})
	cpuThresholdSelect.SetSelected("Off")
	cpuThresholdSelect.SetToolTip("Hide processes below this CPU% (combines with Top Mem via OR: a process passes if it clears either)")

	memThresholdSelect := ttwidget.NewSelect(consumerThresholdOptions, func(sel string) {
		st.minMem = consumerThresholdValues[sel]
		st.applyFilterAndRefresh()
	})
	memThresholdSelect.SetSelected("Off")
	memThresholdSelect.SetToolTip("Hide processes below this Mem% (combines with Top CPU via OR: a process passes if it clears either)")

	refreshBtn := ttwidget.NewButton("Refresh Now", func() { sampler.RefreshProcessesNow() })
	refreshBtn.SetToolTip("Resample the process list immediately instead of waiting for the next tick")
	st.threadsBtn = ttwidget.NewButton("Show Threads", st.showThreadsForSelected)
	st.threadsBtn.SetToolTip("One-off snapshot of the selected process's threads, including start-address resolution on Windows (see Help)")
	st.threadsBtn.Disable()
	st.watchBtn = ttwidget.NewButton("Watch for Interference", st.toggleWatchSelected)
	st.watchBtn.SetToolTip("Add the selected process to Interference Watch -- an unbacked thread, a new module, or a third-party module found in a thread's stack all flag it and get logged automatically")
	st.watchBtn.Disable()
	if !threadStartAddressSupported {
		// Inert on this platform (see interference.go) -- disabled
		// permanently rather than left to look like a bug when nothing ever
		// gets flagged. See Help for the platform explanation.
		st.watchBtn.SetText("Watch for Interference (Windows only)")
		st.watchBtn.SetToolTip("Needs Windows-only APIs to resolve thread start addresses -- see Help")
	}
	st.endBtn = ttwidget.NewButton("End Process", st.endSelected)
	st.endBtn.SetToolTip("Terminate the selected process (with a confirmation prompt first)")
	st.endBtn.Importance = widget.DangerImportance
	st.endBtn.Disable()

	// Filter gets its own row so the entry actually has room -- crammed onto
	// one row alongside every other toolbar control (as it was originally),
	// it was too narrow to read or type a regex into comfortably.
	filterRow := container.NewBorder(nil, nil, nil,
		container.NewHBox(regexCheck, refreshBtn, widget.NewLabel("Refresh every"), intervalSelect),
		st.filterEntry,
	)

	optionsRow := container.NewHBox(
		parentsOnlyCheck,
		widget.NewLabel("Top CPU"), cpuThresholdSelect,
		widget.NewLabel("Top Mem"), memThresholdSelect,
		st.threadsBtn,
		st.watchBtn,
		st.endBtn,
	)

	toolbar := container.NewVBox(filterRow, optionsRow)

	tableSection := container.NewBorder(toolbar, nil, nil, nil, st.table)

	topScroll := container.NewVScroll(topInfo)
	topScroll.SetMinSize(fyne.NewSize(280, 150))

	// Children get a resizable share of the detail pane instead of a fixed
	// sliver -- the old layout (single VBox in one VScroll) left the child
	// list squashed to its widget minimum, which read as "one row, no
	// scrollbar affordance" even when a process had many children.
	detailSplit := container.NewVSplit(topScroll, childrenSection)
	detailSplit.SetOffset(clampSplitOffset(a.Preferences().FloatWithFallback(prefDetailSplitOffset, defaultDetailSplit)))

	// Split, not Border, for the table/detail divide too, so the user can
	// drag to trade table width for detail width instead of the detail pane
	// being stuck at a fixed 280px minimum.
	mainSplit := container.NewHSplit(tableSection, detailSplit)
	mainSplit.SetOffset(clampSplitOffset(a.Preferences().FloatWithFallback(prefMainSplitOffset, defaultMainSplit)))

	view = mainSplit

	saveLayout = func() {
		a.Preferences().SetFloat(prefMainSplitOffset, mainSplit.Offset)
		a.Preferences().SetFloat(prefDetailSplitOffset, detailSplit.Offset)
	}

	return view, st.applySnapshot, func() { sampler.RefreshProcessesNow() }, st.endSelected, saveLayout, st.watcher, st.refreshAfterWatchChange
}

// refreshAfterWatchChange re-syncs the main table's own watch button/alert
// column after the watch list changes from *outside* this view (i.e. from
// the standalone Interference Watch window removing a watch) -- returned
// to main.go as a plain func() value, and also used directly by
// toggleWatchSelected below for the same change made from *this* view, so
// both paths stay in sync through one method instead of two copies.
func (st *procViewState) refreshAfterWatchChange() {
	st.updateWatchBtn()
	st.table.Refresh()
}

// buildDetailPane returns the upper info block (title/meta/ancestry/cmdline)
// and the children-list section separately so the caller can put a
// draggable split between them instead of stacking both in one VBox.
func (st *procViewState) buildDetailPane() (topInfo, childrenSection fyne.CanvasObject) {
	st.detailTitle = widget.NewLabelWithStyle("Select a process to view details", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	st.detailTitle.Wrapping = fyne.TextWrapWord

	st.detailMeta = widget.NewLabel("")
	st.detailMeta.Wrapping = fyne.TextWrapWord

	st.detailParents = widget.NewLabel("")
	st.detailParents.Wrapping = fyne.TextWrapWord

	st.detailCmdline = widget.NewLabel("")
	st.detailCmdline.Wrapping = fyne.TextWrapWord

	topInfo = container.NewVBox(
		st.detailTitle,
		widget.NewSeparator(),
		st.detailMeta,
		st.detailParents,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Command line", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		st.detailCmdline,
	)

	st.childList = widget.NewList(
		func() int { return len(st.detailChildren) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			c := st.detailChildren[i]
			o.(*widget.Label).SetText(fmt.Sprintf("%s (PID %d)", c.Name, c.PID))
		},
	)
	st.childList.OnSelected = st.onChildSelected

	childrenSection = container.NewBorder(
		widget.NewLabelWithStyle("Children", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		nil, nil, nil,
		st.childList,
	)

	return topInfo, childrenSection
}

// ── Data pipeline: snapshot -> filtered/sorted rows + PID lookup map ────────

func (st *procViewState) applySnapshot(snap ProcessSnapshot) {
	st.full = snap
	st.recompute()
	st.refreshChildWindow()

	if idx, ok := indexOfPID(st.displayRows, st.selectedPID); ok {
		st.reselecting = true
		st.table.Select(widget.TableCellID{Row: idx, Col: 0})
		st.reselecting = false
		st.renderDetail(st.displayRows[idx])
	} else if st.selectedPID != -1 {
		st.selectedPID = -1
		st.table.UnselectAll()
		st.clearDetail()
	}
	st.table.Refresh()

	// Every sample tick can add new interference events (see recompute's
	// watcher.check call) -- keep an already-open Interference Watch window
	// live rather than requiring it to be reopened to see them.
	refreshInterferenceWindow()
}

func (st *procViewState) recompute() {
	st.byPID = make(map[int32]ProcInfo, len(st.full.Procs))
	for _, p := range st.full.Procs {
		st.byPID[p.PID] = p
	}

	// One watch cycle per process-list sample -- see interference.go's
	// doc comment for why this is cheap even though it runs unconditionally
	// (a no-op when nothing is being watched).
	st.flaggedPIDs = st.watcher.check(st.byPID)

	// hasChildPID marks every PID that is some other process's parent, and
	// childPIDs maps a PID to its direct children's PIDs -- both computed
	// once here rather than per row (which would be an O(n^2) scan of
	// st.byPID for every row) -- see isParentRow and aggregateSubtree.
	var hasChildPID map[int32]bool
	var childPIDs map[int32][]int32
	if st.parentsOnly {
		hasChildPID = make(map[int32]bool, len(st.full.Procs))
		childPIDs = make(map[int32][]int32, len(st.full.Procs))
		for _, p := range st.full.Procs {
			hasChildPID[p.PPID] = true
			childPIDs[p.PPID] = append(childPIDs[p.PPID], p.PID)
		}
	}

	rows := make([]ProcInfo, 0, len(st.full.Procs))
	for _, p := range st.full.Procs {
		if !st.matchesNameFilter(p.Name) {
			continue
		}
		if st.parentsOnly {
			if !st.isParentRow(p, hasChildPID) {
				continue
			}
			// Task Manager-style grouped total: this row stands in for the
			// whole subtree hidden behind it (see isParentRow), so show the
			// combined CPU%/Mem%/Memory across it and every descendant, not
			// just its own usage. The threshold filter below then applies
			// to this combined figure, consistent with what's displayed.
			p.CPUPercent, p.MemPercent, p.RSSBytes = aggregateSubtree(st.byPID, childPIDs, p.PID)
		}
		if !st.passesConsumerThreshold(p) {
			continue
		}
		rows = append(rows, p)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		c := compareProc(rows[i], rows[j], st.sortCol)
		if st.sortAsc {
			return c < 0
		}
		return c > 0
	})

	st.displayRows = rows
}

// passesConsumerThreshold implements the "top consumer" filter: if both
// thresholds are off (0), every process passes (matching pre-filter
// behavior). Otherwise a process passes if it clears *either* threshold --
// the point is surfacing anything heavy on CPU or heavy on memory, not
// requiring both.
func (st *procViewState) passesConsumerThreshold(p ProcInfo) bool {
	if st.minCPU <= 0 && st.minMem <= 0 {
		return true
	}
	if st.minCPU > 0 && p.CPUPercent >= st.minCPU {
		return true
	}
	if st.minMem > 0 && float64(p.MemPercent) >= st.minMem {
		return true
	}
	return false
}

// isParentRow implements the "Parent processes" view's declutter rule: a
// process is kept if it has at least one child (worth drilling into --
// e.g. a browser with a dozen helper processes), OR if its own parent isn't
// in the current snapshot (an orphan/root has nothing to be nested under).
// A childless process whose parent IS visible is hidden here -- it surfaces
// instead in that parent's own children drill-down window (childrenOf).
func (st *procViewState) isParentRow(p ProcInfo, hasChildPID map[int32]bool) bool {
	if hasChildPID[p.PID] {
		return true
	}
	_, parentVisible := st.byPID[p.PPID]
	return !parentVisible
}

// aggregateSubtree sums CPU%/Mem% for pid and every descendant (children,
// grandchildren, ...), for the Parent-processes view's grouped-total rows.
// Recurses via childPIDs rather than repeatedly scanning byPID, and guards
// against cycles in malformed PPID data with a local seen set (shouldn't
// happen in practice -- parentChain guards the same way for ancestry walks).
func aggregateSubtree(byPID map[int32]ProcInfo, childPIDs map[int32][]int32, pid int32) (cpu float64, mem float32, rssBytes uint64) {
	seen := make(map[int32]bool)
	var walk func(pid int32) (float64, float32, uint64)
	walk = func(pid int32) (float64, float32, uint64) {
		if seen[pid] {
			return 0, 0, 0
		}
		seen[pid] = true
		p, ok := byPID[pid]
		if !ok {
			return 0, 0, 0
		}
		cpu, mem, rss := p.CPUPercent, p.MemPercent, p.RSSBytes
		for _, childPID := range childPIDs[pid] {
			c, m, r := walk(childPID)
			cpu += c
			mem += m
			rss += r
		}
		return cpu, mem, rss
	}
	return walk(pid)
}

// matchesNameFilter applies the name filter: plain case-insensitive
// substring match by default, or a real regexp match when useRegexFilter is
// on (see recompileFilterRegex). An empty filter, or a regex that failed to
// compile, matches everything.
func (st *procViewState) matchesNameFilter(name string) bool {
	if st.useRegexFilter {
		if st.filterRegex == nil {
			return true // off, empty, or invalid/mid-typing regex -- fail open
		}
		return st.filterRegex.MatchString(name)
	}
	needle := strings.ToLower(strings.TrimSpace(st.filterText))
	return needle == "" || strings.Contains(strings.ToLower(name), needle)
}

// recompileFilterRegex recompiles filterRegex from the current filterText
// whenever it or useRegexFilter changes. A compile failure (including an
// incomplete pattern mid-typing) leaves filterRegex nil, which
// matchesNameFilter treats as "no filter" rather than erroring or hiding
// every row.
func (st *procViewState) recompileFilterRegex() {
	st.filterRegex = nil
	if !st.useRegexFilter {
		return
	}
	needle := strings.TrimSpace(st.filterText)
	if needle == "" {
		return
	}
	if re, err := regexp.Compile(needle); err == nil {
		st.filterRegex = re
	}
}

func compareProc(a, b ProcInfo, field sortField) int {
	switch field {
	case sortPID:
		return cmpInt32(a.PID, b.PID)
	case sortName:
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	case sortPPID:
		return cmpInt32(a.PPID, b.PPID)
	case sortUser:
		return strings.Compare(a.Username, b.Username)
	case sortCPU:
		return cmpFloat64(a.CPUPercent, b.CPUPercent)
	case sortMem:
		return cmpFloat64(float64(a.MemPercent), float64(b.MemPercent))
	case sortMemBytes:
		return cmpUint64(a.RSSBytes, b.RSSBytes)
	case sortDiskRead:
		return cmpFloat64(a.DiskReadKBs, b.DiskReadKBs)
	case sortDiskWrite:
		return cmpFloat64(a.DiskWriteKBs, b.DiskWriteKBs)
	case sortPrivate:
		return cmpUint64(a.PrivateBytes, b.PrivateBytes)
	case sortAlert:
		return 0 // flagged status lives in procViewState.flaggedPIDs, not on ProcInfo -- not sortable
	case sortNotable:
		return 0 // needs the byPID map for the masquerade check, not on ProcInfo alone -- not sortable
	default:
		return 0
	}
}

func cmpInt32(a, b int32) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpFloat64(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpUint64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func indexOfPID(rows []ProcInfo, pid int32) (int, bool) {
	if pid < 0 {
		return 0, false
	}
	for i, p := range rows {
		if p.PID == pid {
			return i, true
		}
	}
	return 0, false
}

// classifyNotableProcess returns what, if anything, the Notable column
// should show for p: a recognized AV/EDR name (informational -- just
// labeling which row actually is the antivirus, not a claim of anything
// wrong), or a process-masquerading parent mismatch (worth investigating --
// see notable.go's checkProcessMasquerade for why this is conservative
// about false positives). The two are mutually exclusive in practice,
// checked in this order since a real security product's own process
// showing an "unexpected parent" false positive would be a worse outcome
// than a masquerading process happening to also match a vendor name.
func classifyNotableProcess(p ProcInfo, byPID map[int32]ProcInfo) (text string, importance widget.Importance) {
	if vendor := matchKnownSecurityModule(p.Name); vendor != "" {
		return "🛡 " + vendor, widget.MediumImportance
	}
	if expected, mismatch := checkProcessMasquerade(p, byPID); mismatch {
		return fmt.Sprintf("🎭 not launched by %s", expected), widget.WarningImportance
	}
	return "", widget.MediumImportance
}

// ── Table cell / header rendering ───────────────────────────────────────────

func (st *procViewState) updateCell(id widget.TableCellID, o fyne.CanvasObject) {
	label := o.(*widget.Label)
	if id.Row < 0 || id.Row >= len(st.displayRows) || id.Col < 0 || id.Col >= len(procColumns) {
		label.SetText("")
		return
	}
	p := st.displayRows[id.Row]
	switch procColumns[id.Col].field {
	case sortAlert:
		if st.flaggedPIDs[p.PID] {
			label.Importance = widget.WarningImportance
			label.SetText("⚠")
		} else {
			label.Importance = widget.MediumImportance
			label.SetText("")
		}
	case sortNotable:
		text, importance := classifyNotableProcess(p, st.byPID)
		label.Importance = importance
		label.SetText(text)
	case sortPID:
		label.SetText(strconv.Itoa(int(p.PID)))
	case sortName:
		// By the time Table calls UpdateCell it has already Resize()d o to
		// the cell's live on-screen size (including any user drag-resize of
		// the column), so label.Size() reflects the real available width --
		// no need for a column-width getter (Table doesn't expose one).
		label.SetText(ellipsizeToWidth(p.Name, label.Size().Width, label.TextStyle))
	case sortPPID:
		label.SetText(strconv.Itoa(int(p.PPID)))
	case sortUser:
		label.SetText(orNA(p.Username))
	case sortCPU:
		label.SetText(fmt.Sprintf("%.1f", p.CPUPercent))
	case sortMem:
		label.SetText(fmt.Sprintf("%.1f", p.MemPercent))
	case sortMemBytes:
		label.SetText(formatBytes(p.RSSBytes))
	case sortDiskRead:
		label.SetText(fmt.Sprintf("%.0f K/s", p.DiskReadKBs))
	case sortDiskWrite:
		label.SetText(fmt.Sprintf("%.0f K/s", p.DiskWriteKBs))
	case sortPrivate:
		if p.PrivateBytes == 0 {
			label.SetText("N/A")
		} else {
			label.SetText(formatBytes(p.PrivateBytes))
		}
	}
}

func (st *procViewState) updateHeader(id widget.TableCellID, o fyne.CanvasObject) {
	btn := o.(*ttwidget.Button)
	if id.Col < 0 || id.Col >= len(procColumns) {
		btn.SetText("")
		return
	}
	col := procColumns[id.Col]
	title := col.title
	if col.field == st.sortCol {
		if st.sortAsc {
			title += " ▲"
		} else {
			title += " ▼"
		}
	}
	btn.SetText(title)
	btn.SetToolTip(col.tooltip)
	field := col.field
	btn.OnTapped = func() { st.toggleSort(field) }
}

func (st *procViewState) toggleSort(field sortField) {
	if st.sortCol == field {
		st.sortAsc = !st.sortAsc
	} else {
		st.sortCol = field
		st.sortAsc = true
	}
	st.recompute()
	if idx, ok := indexOfPID(st.displayRows, st.selectedPID); ok {
		st.reselecting = true
		st.table.Select(widget.TableCellID{Row: idx, Col: 0})
		st.reselecting = false
	}
	st.table.Refresh()
}

func (st *procViewState) setFilter(text string) {
	st.filterText = text
	st.recompileFilterRegex()
	st.applyFilterAndRefresh()
}

// applyFilterAndRefresh recomputes displayRows from the current
// filter/threshold/sort state and re-syncs the selection: the previously
// selected row stays selected if it's still visible, otherwise the
// selection is cleared. Shared by setFilter and the CPU/Mem threshold
// selects, which affect what recompute() includes the same way the name
// filter does.
func (st *procViewState) applyFilterAndRefresh() {
	st.recompute()
	if idx, ok := indexOfPID(st.displayRows, st.selectedPID); ok {
		st.reselecting = true
		st.table.Select(widget.TableCellID{Row: idx, Col: 0})
		st.reselecting = false
	} else if st.selectedPID != -1 {
		st.selectedPID = -1
		st.table.UnselectAll()
		st.clearDetail()
	}
	st.table.Refresh()
}

// ── Selection / detail pane ──────────────────────────────────────────────────

func (st *procViewState) onRowSelected(id widget.TableCellID) {
	if id.Row < 0 || id.Row >= len(st.displayRows) {
		return
	}
	p := st.displayRows[id.Row]
	st.selectedPID = p.PID
	st.renderDetail(p)
	if st.parentsOnly && !st.reselecting {
		st.showChildWindow(p)
	}
}

func (st *procViewState) onRowUnselected(widget.TableCellID) {
	st.selectedPID = -1
	st.clearDetail()
}

func (st *procViewState) onChildSelected(i widget.ListItemID) {
	if i < 0 || i >= len(st.detailChildren) {
		return
	}
	pid := st.detailChildren[i].PID
	st.childList.UnselectAll()
	st.selectPID(pid)
}

// selectPID selects pid in the main table, clearing the search filter first
// if pid is currently hidden by it (e.g. a child surfaced from the detail
// pane that doesn't match the current filter text).
func (st *procViewState) selectPID(pid int32) {
	idx, ok := indexOfPID(st.displayRows, pid)
	if !ok {
		st.filterEntry.SetText("") // triggers setFilter -> recompute
		idx, ok = indexOfPID(st.displayRows, pid)
		if !ok {
			return
		}
	}
	st.table.Select(widget.TableCellID{Row: idx, Col: 0})
}

// renderDetail shows what's already known immediately, then fetches
// Cmdline/CreateTime/Status (expensive on macOS -- Status() forks a `ps`
// subprocess) in the background for just this one pid rather than for
// every process on every sample tick. See ProcDetail in procsampler.go.
func (st *procViewState) renderDetail(p ProcInfo) {
	st.detailTitle.SetText(fmt.Sprintf("%s (PID %d)", p.Name, p.PID))
	st.detailMeta.SetText(fmt.Sprintf("User: %s    Status: %s    Started: %s", orNA(p.Username), "…", "…"))

	if chain := parentChain(st.byPID, p.PID); len(chain) == 0 {
		st.detailParents.SetText("Ancestry: (root process)")
	} else {
		names := make([]string, len(chain))
		for i, a := range chain {
			names[len(chain)-1-i] = fmt.Sprintf("%s (%d)", a.Name, a.PID)
		}
		st.detailParents.SetText("Ancestry: " + strings.Join(names, " › "))
	}

	st.detailCmdline.SetText("…")

	st.detailChildren = childrenOf(st.byPID, p.PID)
	st.childList.Refresh()

	st.threadsBtn.Enable()
	st.endBtn.Enable()
	st.updateWatchBtn()

	pid, username := p.PID, p.Username
	go func() {
		detail, err := st.sampler.FetchProcessDetail(pid)
		fyne.Do(func() {
			if st.selectedPID != pid {
				return // user selected a different row while this was in flight
			}
			if err != nil {
				st.detailMeta.SetText(fmt.Sprintf("User: %s    Status: N/A    Started: N/A", orNA(username)))
				st.detailCmdline.SetText("(unavailable)")
				return
			}
			created := "N/A"
			if !detail.CreateTime.IsZero() {
				created = detail.CreateTime.Format("2006-01-02 15:04:05")
			}
			st.detailMeta.SetText(fmt.Sprintf("User: %s    Status: %s    Started: %s", orNA(username), orNA(detail.Status), created))
			if detail.Cmdline == "" {
				st.detailCmdline.SetText("(unavailable)")
			} else {
				st.detailCmdline.SetText(detail.Cmdline)
			}
		})
	}()
}

func (st *procViewState) clearDetail() {
	st.detailTitle.SetText("Select a process to view details")
	st.detailMeta.SetText("")
	st.detailParents.SetText("")
	st.detailCmdline.SetText("")
	st.detailChildren = nil
	st.childList.Refresh()
	st.threadsBtn.Disable()
	st.watchBtn.Disable()
	st.endBtn.Disable()
}

// showThreadsForSelected opens the Threads window for the currently
// selected process -- see threadsview.go's showThreads for what it shows
// and why.
func (st *procViewState) showThreadsForSelected() {
	pid := st.selectedPID
	if pid < 0 {
		return
	}
	p, ok := st.byPID[pid]
	if !ok {
		return
	}
	showThreads(st.app, st.win, pid, p.Name)
}

// updateWatchBtn syncs the "Watch for Interference" button's label/enabled
// state to whether the currently selected process is being watched. A no-op
// on a platform where watching can never detect anything (see
// threadStartAddressSupported) -- the button stays permanently disabled
// there instead (set once in newProcessView).
func (st *procViewState) updateWatchBtn() {
	if !threadStartAddressSupported {
		return
	}
	if st.selectedPID < 0 {
		st.watchBtn.Disable()
		return
	}
	st.watchBtn.Enable()
	if st.watcher.isWatchingPID(st.selectedPID) {
		st.watchBtn.SetText("Unwatch")
	} else {
		st.watchBtn.SetText("Watch for Interference")
	}
}

// toggleWatchSelected adds or removes the currently selected process from
// the interference watch list (see interference.go), then refreshes both
// this button and the standalone Interference Watch window if it's open --
// and, only when *adding* a watch (not removing one), opens or focuses that
// window if it isn't already visible. Without this, clicking "Watch for
// Interference" gives no indication of where results actually show up
// unless you already know about the separate menu/tray item -- a real
// discoverability gap for anyone who isn't already familiar with this
// feature.
func (st *procViewState) toggleWatchSelected() {
	pid := st.selectedPID
	if pid < 0 {
		return
	}
	p, ok := st.byPID[pid]
	if !ok {
		return
	}
	wasWatching := st.watcher.isWatchingPID(pid)
	if wasWatching {
		st.watcher.unwatchPID(pid)
	} else {
		st.watcher.watchPID(pid, p.Name)
	}
	st.updateWatchBtn()
	refreshInterferenceWindow()
	if !wasWatching {
		showInterferenceWindow(st.app, st.watcher, st.refreshAfterWatchChange)
	}
}

// ── Parent-processes view: children drill-down window ──────────────────────

// showChildWindow opens the single reusable children drill-down window for
// p, or updates it in place if it's already open for a different parent --
// only one is ever open at a time (see procViewState.childWin). Sortable
// columns, a name filter, and End Process, same as the main table -- the
// original read-only version proved the overall approach worthwhile first.
func (st *procViewState) showChildWindow(p ProcInfo) {
	changingParent := st.childWinPID != p.PID
	st.childWinPID = p.PID

	if st.childWin == nil {
		st.childWinSortCol = sortCPU
		st.childWinSortAsc = false
		st.childWinSelectedPID = -1
		st.buildChildWindow()
	} else if changingParent {
		// A filter/selection from the previous parent's children usually
		// doesn't apply to the new one's -- e.g. a name filter that would
		// just hide everything. Sort preference is left alone, same as the
		// main table keeps its sort across data changes.
		st.childWinSelectedPID = -1
		st.childWinFilterEntry.SetText("") // triggers recomputeChildWinRows via OnChanged
	}

	st.refreshChildWindow()
	st.childWin.Show()
	st.childWin.RequestFocus()
}

func (st *procViewState) buildChildWindow() {
	st.childWin = st.app.NewWindow("")
	st.childWin.SetIcon(resourceKrankyBearProcessMinerPng)

	st.childWinTable = widget.NewTable(
		func() (int, int) { return len(st.childWinDisplayRows), len(procColumns) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		st.updateChildWinCell,
	)
	st.childWinTable.ShowHeaderRow = true
	st.childWinTable.CreateHeader = func() fyne.CanvasObject { return ttwidget.NewButton("", nil) }
	st.childWinTable.UpdateHeader = st.updateChildWinHeader
	st.childWinTable.OnSelected = st.onChildWinRowSelected
	st.childWinTable.OnUnselected = st.onChildWinRowUnselected
	for i, col := range procColumns {
		st.childWinTable.SetColumnWidth(i, col.width)
	}

	st.childWinFilterEntry = widget.NewEntry()
	st.childWinFilterEntry.SetPlaceHolder("Filter by name…")
	st.childWinFilterEntry.OnChanged = func(text string) {
		st.childWinFilterText = text
		st.recomputeChildWinRows()
	}

	st.childWinEndBtn = ttwidget.NewButton("End Process", st.endChildWinSelected)
	st.childWinEndBtn.SetToolTip("Terminate the selected process (with a confirmation prompt first)")
	st.childWinEndBtn.Importance = widget.DangerImportance
	st.childWinEndBtn.Disable()

	toolbar := container.NewBorder(nil, nil, nil, st.childWinEndBtn, st.childWinFilterEntry)
	st.childWin.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewBorder(toolbar, nil, nil, nil, st.childWinTable), st.childWin.Canvas()))
	st.childWin.Resize(fyne.NewSize(900, 480))

	// A plain close (not hide-and-reuse like About/Help/Update): this
	// window tracks one specific live process, so there's nothing worth
	// restoring later -- just forget it and build fresh next time.
	st.childWin.SetOnClosed(func() {
		fynetooltip.DestroyWindowToolTipLayer(st.childWin.Canvas())
		st.childWin = nil
		st.childWinPID = -1
		st.childWinSelectedPID = -1
		st.childWinFilterText = ""
		st.childWinAllRows = nil
		st.childWinDisplayRows = nil
	})
}

// refreshChildWindow re-reads the tracked parent's direct children from the
// current byPID snapshot. Called after every recompute() so the drill-down
// window stays live while open, the same way the main table does. No-op if
// no child window is open.
func (st *procViewState) refreshChildWindow() {
	if st.childWin == nil {
		return
	}
	parent, ok := st.byPID[st.childWinPID]
	if !ok {
		st.childWin.SetTitle("(process exited)")
		st.childWinAllRows = nil
		st.recomputeChildWinRows()
		return
	}
	st.childWin.SetTitle(fmt.Sprintf("%s (PID %d) — Children", parent.Name, parent.PID))
	st.childWinAllRows = childrenOf(st.byPID, st.childWinPID)
	st.recomputeChildWinRows()
}

// recomputeChildWinRows applies the child window's own name filter and sort
// to childWinAllRows, and re-syncs selection -- same shape as the main
// table's applyFilterAndRefresh, just scoped to this window's own state.
func (st *procViewState) recomputeChildWinRows() {
	needle := strings.ToLower(strings.TrimSpace(st.childWinFilterText))
	rows := make([]ProcInfo, 0, len(st.childWinAllRows))
	for _, p := range st.childWinAllRows {
		if needle == "" || strings.Contains(strings.ToLower(p.Name), needle) {
			rows = append(rows, p)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		c := compareProc(rows[i], rows[j], st.childWinSortCol)
		if st.childWinSortAsc {
			return c < 0
		}
		return c > 0
	})
	st.childWinDisplayRows = rows

	if idx, ok := indexOfPID(st.childWinDisplayRows, st.childWinSelectedPID); ok {
		st.childWinTable.Select(widget.TableCellID{Row: idx, Col: 0})
	} else if st.childWinSelectedPID != -1 {
		st.childWinSelectedPID = -1
		st.childWinTable.UnselectAll()
		st.childWinEndBtn.Disable()
	}
	st.childWinTable.Refresh()
}

func (st *procViewState) updateChildWinCell(id widget.TableCellID, o fyne.CanvasObject) {
	label := o.(*widget.Label)
	if id.Row < 0 || id.Row >= len(st.childWinDisplayRows) || id.Col < 0 || id.Col >= len(procColumns) {
		label.SetText("")
		return
	}
	p := st.childWinDisplayRows[id.Row]
	switch procColumns[id.Col].field {
	case sortAlert:
		if st.flaggedPIDs[p.PID] {
			label.Importance = widget.WarningImportance
			label.SetText("⚠")
		} else {
			label.Importance = widget.MediumImportance
			label.SetText("")
		}
	case sortNotable:
		text, importance := classifyNotableProcess(p, st.byPID)
		label.Importance = importance
		label.SetText(text)
	case sortPID:
		label.SetText(strconv.Itoa(int(p.PID)))
	case sortName:
		label.SetText(ellipsizeToWidth(p.Name, label.Size().Width, label.TextStyle))
	case sortPPID:
		label.SetText(strconv.Itoa(int(p.PPID)))
	case sortUser:
		label.SetText(orNA(p.Username))
	case sortCPU:
		label.SetText(fmt.Sprintf("%.1f", p.CPUPercent))
	case sortMem:
		label.SetText(fmt.Sprintf("%.1f", p.MemPercent))
	case sortMemBytes:
		label.SetText(formatBytes(p.RSSBytes))
	case sortDiskRead:
		label.SetText(fmt.Sprintf("%.0f K/s", p.DiskReadKBs))
	case sortDiskWrite:
		label.SetText(fmt.Sprintf("%.0f K/s", p.DiskWriteKBs))
	case sortPrivate:
		if p.PrivateBytes == 0 {
			label.SetText("N/A")
		} else {
			label.SetText(formatBytes(p.PrivateBytes))
		}
	}
}

func (st *procViewState) updateChildWinHeader(id widget.TableCellID, o fyne.CanvasObject) {
	btn := o.(*ttwidget.Button)
	if id.Col < 0 || id.Col >= len(procColumns) {
		btn.SetText("")
		return
	}
	col := procColumns[id.Col]
	title := col.title
	if col.field == st.childWinSortCol {
		if st.childWinSortAsc {
			title += " ▲"
		} else {
			title += " ▼"
		}
	}
	btn.SetText(title)
	btn.SetToolTip(col.tooltip)
	field := col.field
	btn.OnTapped = func() { st.toggleChildWinSort(field) }
}

func (st *procViewState) toggleChildWinSort(field sortField) {
	if st.childWinSortCol == field {
		st.childWinSortAsc = !st.childWinSortAsc
	} else {
		st.childWinSortCol = field
		st.childWinSortAsc = true
	}
	st.recomputeChildWinRows()
}

func (st *procViewState) onChildWinRowSelected(id widget.TableCellID) {
	if id.Row < 0 || id.Row >= len(st.childWinDisplayRows) {
		return
	}
	st.childWinSelectedPID = st.childWinDisplayRows[id.Row].PID
	st.childWinEndBtn.Enable()
}

func (st *procViewState) onChildWinRowUnselected(widget.TableCellID) {
	st.childWinSelectedPID = -1
	st.childWinEndBtn.Disable()
}

// endChildWinSelected mirrors endSelected -- same confirm-then-kill pattern,
// scoped to the child window's own selection and parented to that window
// rather than the main one.
func (st *procViewState) endChildWinSelected() {
	pid := st.childWinSelectedPID
	if pid < 0 {
		return
	}
	p, ok := st.byPID[pid]
	if !ok {
		return
	}
	dialog.NewConfirm("End Process",
		fmt.Sprintf("Terminate %q (PID %d)? This cannot be undone.", p.Name, pid),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			go func() {
				err := st.sampler.EndProcess(pid)
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(fmt.Errorf("couldn't end process %d (%s): %w", pid, p.Name, err), st.childWin)
						return
					}
					st.sampler.RefreshProcessesNow()
				})
			}()
		}, st.childWin).Show()
}

// parentChain walks PPID upward from pid, returning immediate-parent-first.
func parentChain(byPID map[int32]ProcInfo, pid int32) []ProcInfo {
	var chain []ProcInfo
	seen := map[int32]bool{pid: true}
	cur, ok := byPID[pid]
	if !ok {
		return chain
	}
	for {
		parent, ok := byPID[cur.PPID]
		if !ok || seen[cur.PPID] {
			return chain
		}
		seen[cur.PPID] = true
		chain = append(chain, parent)
		cur = parent
	}
}

func childrenOf(byPID map[int32]ProcInfo, pid int32) []ProcInfo {
	var kids []ProcInfo
	for _, p := range byPID {
		if p.PPID == pid {
			kids = append(kids, p)
		}
	}
	sort.Slice(kids, func(i, j int) bool { return kids[i].PID < kids[j].PID })
	return kids
}

// ellipsizeToWidth returns s unchanged if it fits within maxWidth, otherwise
// trims it and appends "…" so the result fits. Table cells otherwise either
// overflow into the next column or get hard-clipped mid-character depending
// on the renderer -- neither reads as intentional.
func ellipsizeToWidth(s string, maxWidth float32, style fyne.TextStyle) string {
	if maxWidth <= 0 {
		return s
	}
	avail := maxWidth - 2*theme.Padding()
	textSize := theme.TextSize()
	if fyne.MeasureText(s, textSize, style).Width <= avail {
		return s
	}

	const ellipsis = "…"
	ellipsisWidth := fyne.MeasureText(ellipsis, textSize, style).Width
	if ellipsisWidth > avail {
		return ellipsis
	}

	// Binary search the longest prefix that still fits alongside the ellipsis.
	runes := []rune(s)
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		w := fyne.MeasureText(string(runes[:mid]), textSize, style).Width
		if w+ellipsisWidth <= avail {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return ellipsis
	}
	return string(runes[:lo]) + ellipsis
}

func orNA(s string) string {
	if s == "" {
		return "N/A"
	}
	return s
}

// ── End Process ──────────────────────────────────────────────────────────────

func (st *procViewState) endSelected() {
	pid := st.selectedPID
	if pid < 0 {
		return
	}
	p, ok := st.byPID[pid]
	if !ok {
		return
	}
	dialog.NewConfirm("End Process",
		fmt.Sprintf("Terminate %q (PID %d)? This cannot be undone.", p.Name, pid),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			// dialog callbacks run on the main goroutine, where fyne.Do runs
			// INLINE (see CLAUDE.md) -- so the actual kill syscall must happen
			// off-main first, and only the result-handling fyne.Do is fired
			// from that background goroutine (where it properly enqueues).
			go func() {
				err := st.sampler.EndProcess(pid)
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(fmt.Errorf("couldn't end process %d (%s): %w", pid, p.Name, err), st.win)
						return
					}
					st.sampler.RefreshProcessesNow()
				})
			}()
		}, st.win).Show()
}
