package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type sortField int

const (
	sortPID sortField = iota
	sortName
	sortPPID
	sortUser
	sortCPU
	sortMem
)

type procColumn struct {
	title string
	field sortField
	width float32
}

var procColumns = []procColumn{
	{"PID", sortPID, 70},
	{"Name", sortName, 220},
	{"PPID", sortPPID, 70},
	{"User", sortUser, 110},
	{"CPU %", sortCPU, 70},
	{"Mem %", sortMem, 70},
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
	win     fyne.Window
	sampler *Sampler

	full        ProcessSnapshot
	displayRows []ProcInfo
	byPID       map[int32]ProcInfo
	filterText  string
	minCPU      float64 // "top consumer" thresholds; 0 = off (see passesConsumerThreshold)
	minMem      float64
	sortCol     sortField
	sortAsc     bool
	selectedPID int32 // -1 = none

	table       *widget.Table
	filterEntry *widget.Entry
	endBtn      *widget.Button

	detailTitle    *widget.Label
	detailMeta     *widget.Label
	detailParents  *widget.Label
	detailCmdline  *widget.Label
	detailChildren []ProcInfo
	childList      *widget.List
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
) {
	st := &procViewState{
		win:         win,
		sampler:     sampler,
		selectedPID: -1,
		sortCol:     sortCPU,
		sortAsc:     false,
	}

	st.table = widget.NewTable(
		func() (int, int) { return len(st.displayRows), len(procColumns) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		st.updateCell,
	)
	st.table.ShowHeaderRow = true
	st.table.CreateHeader = func() fyne.CanvasObject { return widget.NewButton("", nil) }
	st.table.UpdateHeader = st.updateHeader
	st.table.OnSelected = st.onRowSelected
	st.table.OnUnselected = st.onRowUnselected
	for i, col := range procColumns {
		st.table.SetColumnWidth(i, col.width)
	}

	topInfo, childrenSection := st.buildDetailPane()

	st.filterEntry = widget.NewEntry()
	st.filterEntry.SetPlaceHolder("Filter by name…")
	st.filterEntry.OnChanged = st.setFilter

	intervalSelect := widget.NewSelect([]string{"1s", "2s", "5s", "10s"}, func(sel string) {
		if d, err := time.ParseDuration(sel); err == nil {
			sampler.SetProcessInterval(d)
		}
	})
	intervalSelect.SetSelected("2s")

	// "Top consumer" filter: surfaces anything heavy on CPU or memory. Off
	// by default so behavior is unchanged unless the user opts in -- known
	// gap: no per-process disk/network figures are sampled yet (macOS has
	// no per-process disk I/O via gopsutil, and none of the platforms
	// collect per-process network use), so this only covers CPU/Mem for now.
	cpuThresholdSelect := widget.NewSelect(consumerThresholdOptions, func(sel string) {
		st.minCPU = consumerThresholdValues[sel]
		st.applyFilterAndRefresh()
	})
	cpuThresholdSelect.SetSelected("Off")

	memThresholdSelect := widget.NewSelect(consumerThresholdOptions, func(sel string) {
		st.minMem = consumerThresholdValues[sel]
		st.applyFilterAndRefresh()
	})
	memThresholdSelect.SetSelected("Off")

	refreshBtn := widget.NewButton("Refresh Now", func() { sampler.RefreshProcessesNow() })
	st.endBtn = widget.NewButton("End Process", st.endSelected)
	st.endBtn.Importance = widget.DangerImportance
	st.endBtn.Disable()

	toolbar := container.NewBorder(nil, nil, nil,
		container.NewHBox(
			widget.NewLabel("Top CPU"), cpuThresholdSelect,
			widget.NewLabel("Top Mem"), memThresholdSelect,
			refreshBtn, widget.NewLabel("Refresh every"), intervalSelect, st.endBtn,
		),
		st.filterEntry,
	)

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

	return view, st.applySnapshot, func() { sampler.RefreshProcessesNow() }, st.endSelected, saveLayout
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

	if idx, ok := indexOfPID(st.displayRows, st.selectedPID); ok {
		st.table.Select(widget.TableCellID{Row: idx, Col: 0})
		st.renderDetail(st.displayRows[idx])
	} else if st.selectedPID != -1 {
		st.selectedPID = -1
		st.table.UnselectAll()
		st.clearDetail()
	}
	st.table.Refresh()
}

func (st *procViewState) recompute() {
	st.byPID = make(map[int32]ProcInfo, len(st.full.Procs))
	for _, p := range st.full.Procs {
		st.byPID[p.PID] = p
	}

	needle := strings.ToLower(strings.TrimSpace(st.filterText))
	rows := make([]ProcInfo, 0, len(st.full.Procs))
	for _, p := range st.full.Procs {
		if needle != "" && !strings.Contains(strings.ToLower(p.Name), needle) {
			continue
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

// ── Table cell / header rendering ───────────────────────────────────────────

func (st *procViewState) updateCell(id widget.TableCellID, o fyne.CanvasObject) {
	label := o.(*widget.Label)
	if id.Row < 0 || id.Row >= len(st.displayRows) || id.Col < 0 || id.Col >= len(procColumns) {
		label.SetText("")
		return
	}
	p := st.displayRows[id.Row]
	switch procColumns[id.Col].field {
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
	}
}

func (st *procViewState) updateHeader(id widget.TableCellID, o fyne.CanvasObject) {
	btn := o.(*widget.Button)
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
		st.table.Select(widget.TableCellID{Row: idx, Col: 0})
	}
	st.table.Refresh()
}

func (st *procViewState) setFilter(text string) {
	st.filterText = text
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
		st.table.Select(widget.TableCellID{Row: idx, Col: 0})
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

	st.endBtn.Enable()

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
	st.endBtn.Disable()
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
