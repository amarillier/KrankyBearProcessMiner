package main

import (
	"fmt"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
)

// resourceKind identifies one of the four top-strip resources, shared by
// sysview.go (mini graphs) and this file (the bigger single-resource view).
type resourceKind int

const (
	resCPU resourceKind = iota
	resMem
	resDisk
	resNet
)

var resourceNames = []string{"CPU", "Memory", "Disk", "Network"}
var resourceUnits = []string{"%", "%", " KB/s", " KB/s"}

// diskThresholdOptions/-Values mirror procview.go's consumerThresholdOptions/
// -Values (reused as-is for CPU/Mem below) but in per-process disk KB/s
// terms instead of percent -- process-level disk I/O rates run much lower
// than percent-scale figures, so the same 1/5/10/25 tiers wouldn't mean
// anything here.
var diskThresholdOptions = []string{"Off", "≥10 KB/s", "≥100 KB/s", "≥500 KB/s", "≥1000 KB/s"}

var diskThresholdValues = map[string]float64{
	"Off":        0,
	"≥10 KB/s":   10,
	"≥100 KB/s":  100,
	"≥500 KB/s":  500,
	"≥1000 KB/s": 1000,
}

// topWindowOptions/-Values back the Top Consumers panel's "Averaged over"
// select: "Off" ranks/filters by the latest sample only (the original,
// still-default behavior); the rest average CPU%/Mem%/Disk KB/s over the
// trailing window instead, catching a process that's a heavy consumer
// overall but bursty rather than continuously high -- one that a single
// instantaneous sample can just as easily catch mid-lull as mid-spike.
var topWindowOptions = []string{"Off", "10s", "30s", "1 min", "2 min"}

var topWindowValues = map[string]time.Duration{
	"Off":   0,
	"10s":   10 * time.Second,
	"30s":   30 * time.Second,
	"1 min": time.Minute,
	"2 min": 2 * time.Minute,
}

// maxHistoryWindow is the longest lookback any topWindowOptions tier needs;
// recordHistory prunes anything older than this every tick so the
// per-process history map doesn't grow without bound over a long session.
const maxHistoryWindow = 2 * time.Minute

// procHistorySample is one process's per-tick metrics, kept only for as
// long as maxHistoryWindow needs (see resourceDetailState.history).
type procHistorySample struct {
	at   time.Time
	cpu  float64
	mem  float64
	disk float64
}

// maxTopProcessRows caps the Resource Details window's top-consumers list
// regardless of how many processes clear the threshold -- the point is a
// quick "what's actually driving this" glance, not a full sortable table
// (the main window's process table already covers that).
const maxTopProcessRows = 8

// topProcRow is one row of the Resource Details window's top-consumers
// list: a process name and its already-formatted value for whichever
// resource is currently selected. pid identifies exactly which process to
// jump to on click -- name alone isn't reliably unique (e.g. several
// chrome.exe rows at once).
type topProcRow struct {
	name  string
	value string
	pid   int32
}

// resourceDetailWindow/-Open mirror the state a caller cares about for
// hide-all/show-all purposes (windows.go) -- see aboutOpen in about.go. The
// window itself is still owned by resourceDetailState below; these are kept
// in sync with it since newResourceDetailWindow's closures-based API has no
// other package-level hook for windows.go to reach into.
var resourceDetailWindow fyne.Window
var resourceDetailOpen bool

// resourceDetailHistoryLen is longer than the top-strip minis'
// sparklineHistoryLen -- the whole point of "click to see bigger" is a
// clearer, and slightly deeper, view of one resource at a time.
const resourceDetailHistoryLen = 300 // ~5 minutes at the 1s system-sample cadence

// resourceDetailState holds the Resource Details window. All four big graphs
// are fed every tick regardless of which is currently displayed (via
// container.Stack + Show/Hide), so switching resources shows history that
// was already accumulating in the background, not a blank graph.
type resourceDetailState struct {
	app fyne.App
	win fyne.Window

	selected resourceKind
	last     SystemSnapshot

	list      *widget.List
	bigLabel  *widget.Label
	bigGraphs [4]*Sparkline
	stack     *fyne.Container

	// Top-consumers list for the selected resource. lastProcs is the most
	// recent process snapshot (fed from main.go's OnProcessSnapshot, a
	// separate, slower cadence than the 1s SystemSnapshot the graphs use).
	// topThreshold is per-resourceKind so switching resources doesn't reset
	// a threshold you'd already picked for another one; resNet has no entry
	// that matters since there's no per-process network data at all.
	lastProcs           ProcessSnapshot
	topThreshold        [4]float64
	topRows             []topProcRow
	topHeader           *widget.Label
	topThresholdSelect  *ttwidget.Select
	topList             *widget.List
	topNormalGroup      *fyne.Container
	topUnavailableLabel *widget.Label
	topStack            *fyne.Container

	// history backs the "Averaged over" window (see topWindowOptions):
	// per-PID metrics recorded every ProcessSnapshot tick, pruned to
	// maxHistoryWindow by recordHistory. topWindow is per-resourceKind,
	// same reasoning as topThreshold -- switching resources shouldn't reset
	// a window you'd already picked for another one.
	history         map[int32][]procHistorySample
	topWindow       [4]time.Duration
	topWindowSelect *ttwidget.Select

	// jumpToPID brings the main window forward and selects a PID there --
	// set once at construction (see newResourceDetailWindow), called when
	// the user clicks a Top Consumers row.
	jumpToPID func(int32)
}

// newResourceDetailWindow returns update (called on every SystemSnapshot),
// updateProcesses (called on every ProcessSnapshot -- both main-goroutine
// only, see main.go), and show (opens the window already displaying the
// given resource, called from a mini graph's OnTapped). jumpToPID (from
// procview.go's newProcessView) lets a Top Consumers row click bring the
// main window forward with that process selected.
func newResourceDetailWindow(a fyne.App, jumpToPID func(int32)) (update func(SystemSnapshot), updateProcesses func(ProcessSnapshot), show func(resourceKind)) {
	st := &resourceDetailState{app: a, selected: resCPU, jumpToPID: jumpToPID}

	st.bigGraphs = [4]*Sparkline{
		NewSparkline(graphColorCPU, scaleFixed0to100, resourceDetailHistoryLen),
		NewSparkline(graphColorMem, scaleFixed0to100, resourceDetailHistoryLen),
		NewSparkline(graphColorDisk, scaleAuto, resourceDetailHistoryLen),
		NewSparkline(graphColorNet, scaleAuto, resourceDetailHistoryLen),
	}
	for i, g := range st.bigGraphs {
		g.ShowAxes = true
		g.Unit = resourceUnits[i]
		g.SampleInterval = time.Second // matches procSampler's system-sample cadence
	}

	update = func(s SystemSnapshot) {
		st.last = s
		for i, g := range st.bigGraphs {
			g.Push(valueForResource(resourceKind(i), s))
		}
		if st.list != nil {
			st.list.Refresh()
		}
		st.refreshBigLabel()
	}

	updateProcesses = func(snap ProcessSnapshot) {
		st.lastProcs = snap
		st.recordHistory(snap)
		st.refreshTopProcesses()
	}

	show = func(kind resourceKind) {
		st.selected = kind
		if st.win == nil {
			st.buildWindow()
		}
		st.selectResource(kind)
		resourceDetailOpen = true
		st.win.Show()
		st.win.RequestFocus()
	}

	return update, updateProcesses, show
}

// valueForResource returns the single number each resource's graph plots --
// the same values sysview.go's mini graphs push, kept in one place so the
// two views can never drift apart.
func valueForResource(kind resourceKind, s SystemSnapshot) float64 {
	switch kind {
	case resCPU:
		return s.CPUPercent
	case resMem:
		return s.MemPercent
	case resDisk:
		return s.DiskReadKBs + s.DiskWriteKBs
	case resNet:
		return s.NetRecvKBs + s.NetSentKBs
	default:
		return 0
	}
}

func (st *resourceDetailState) buildWindow() {
	st.win = st.app.NewWindow(appName + " - Resource Details")
	st.win.SetIcon(resourceKrankyBearProcessMinerPng)
	resourceDetailWindow = st.win

	st.list = widget.NewList(
		func() int { return len(resourceNames) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(st.listRowText(resourceKind(i)))
		},
	)
	st.list.OnSelected = func(id widget.ListItemID) { st.selectResource(resourceKind(id)) }

	st.stack = container.NewStack(st.bigGraphs[0], st.bigGraphs[1], st.bigGraphs[2], st.bigGraphs[3])

	st.bigLabel = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	// Resource picker gets a fixed-ish height (roughly 4 rows) so the
	// top-consumers area below it -- previously empty, wasted space -- gets
	// the rest of the left pane instead of the picker list expanding to
	// fill it.
	resourceListScroll := container.NewVScroll(st.list)
	resourceListScroll.SetMinSize(fyne.NewSize(0, 170))

	leftPane := container.NewBorder(resourceListScroll, nil, nil, nil, st.buildTopProcessesArea())

	rightPane := container.NewBorder(st.bigLabel, nil, nil, nil, st.stack)

	// Split rather than a fixed-width leading pane: the list's summary text
	// (e.g. "Memory: 62% (29.5 GiB / 48.0 GiB)") doesn't fit a narrow fixed
	// column, and this app already prefers draggable splits over guessing a
	// width (see procview.go).
	split := container.NewHSplit(leftPane, rightPane)
	split.SetOffset(0.3)

	st.win.SetContent(fynetooltip.AddWindowToolTipLayer(split, st.win.Canvas()))
	st.win.Resize(fyne.NewSize(820, 500))

	st.win.SetCloseIntercept(func() {
		resourceDetailOpen = false
		st.win.Hide()
	})
}

// buildTopProcessesArea returns the top-consumers section for the selected
// resource: a header, a threshold select (options depend on the resource --
// percent for CPU/Mem, KB/s for Disk), and the list itself, stacked with a
// "not available" message shown instead for Network (there's no per-process
// network API -- see the known gap noted throughout this app).
func (st *resourceDetailState) buildTopProcessesArea() fyne.CanvasObject {
	st.topHeader = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	st.topThresholdSelect = ttwidget.NewSelect(nil, func(sel string) {
		if st.selected == resDisk {
			st.topThreshold[resDisk] = diskThresholdValues[sel]
		} else {
			st.topThreshold[st.selected] = consumerThresholdValues[sel]
		}
		st.refreshTopProcesses()
	})
	st.topThresholdSelect.SetToolTip("Hide processes below this threshold in the list below")

	st.topWindowSelect = ttwidget.NewSelect(topWindowOptions, func(sel string) {
		st.topWindow[st.selected] = topWindowValues[sel]
		st.topHeader.SetText(st.topHeaderText(st.selected))
		st.refreshTopProcesses()
	})
	st.topWindowSelect.SetToolTip("Rank and filter by the average over this trailing window instead of just the latest sample -- catches a process that's a heavy consumer overall but bursty rather than continuously high")

	st.topList = widget.NewList(
		func() int { return len(st.topRows) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			r := st.topRows[i]
			o.(*widget.Label).SetText(fmt.Sprintf("%s — %s", r.name, r.value))
		},
	)
	st.topList.OnSelected = func(i widget.ListItemID) {
		if i < 0 || i >= len(st.topRows) {
			return
		}
		pid := st.topRows[i].pid
		st.topList.UnselectAll()
		if st.jumpToPID != nil {
			st.jumpToPID(pid)
		}
	}

	headerRow := container.NewBorder(nil, nil, st.topHeader, st.topThresholdSelect, nil)
	windowRow := container.NewBorder(nil, nil, widget.NewLabel("Averaged over"), nil, st.topWindowSelect)
	st.topNormalGroup = container.NewBorder(
		container.NewVBox(headerRow, windowRow),
		nil, nil, nil,
		st.topList,
	)

	st.topUnavailableLabel = widget.NewLabel("Per-process network usage isn't available (no simple cross-platform API exists).")
	st.topUnavailableLabel.Wrapping = fyne.TextWrapWord

	st.topStack = container.NewStack(st.topNormalGroup, st.topUnavailableLabel)
	return st.topStack
}

func (st *resourceDetailState) selectResource(kind resourceKind) {
	st.selected = kind
	for i, g := range st.bigGraphs {
		if resourceKind(i) == kind {
			g.Show()
		} else {
			g.Hide()
		}
	}
	st.list.Select(widget.ListItemID(kind))
	st.refreshBigLabel()

	if kind == resNet {
		st.topNormalGroup.Hide()
		st.topUnavailableLabel.Show()
	} else {
		st.topUnavailableLabel.Hide()
		st.topNormalGroup.Show()

		st.topHeader.SetText(st.topHeaderText(kind))
		if kind == resDisk {
			st.topThresholdSelect.Options = diskThresholdOptions
			st.topThresholdSelect.SetSelected(thresholdOptionFor(diskThresholdOptions, diskThresholdValues, st.topThreshold[kind]))
		} else {
			st.topThresholdSelect.Options = consumerThresholdOptions
			st.topThresholdSelect.SetSelected(thresholdOptionFor(consumerThresholdOptions, consumerThresholdValues, st.topThreshold[kind]))
		}
		st.topThresholdSelect.Refresh()
		st.topWindowSelect.Options = topWindowOptions
		st.topWindowSelect.SetSelected(windowOptionFor(topWindowOptions, topWindowValues, st.topWindow[kind]))
		st.topWindowSelect.Refresh()
	}

	st.refreshTopProcesses()
}

// thresholdOptionFor reverse-looks-up which option string (via values) maps
// to v, so switching resources can restore each one's own remembered
// threshold selection instead of resetting the Select to its first option.
func thresholdOptionFor(options []string, values map[string]float64, v float64) string {
	for _, opt := range options {
		if values[opt] == v {
			return opt
		}
	}
	return options[0]
}

// windowOptionFor is thresholdOptionFor's equivalent for the "Averaged
// over" select, whose values are durations rather than float64s.
func windowOptionFor(options []string, values map[string]time.Duration, v time.Duration) string {
	for _, opt := range options {
		if values[opt] == v {
			return opt
		}
	}
	return options[0]
}

// topHeaderText labels the top-consumers list with which averaging window
// (if any) it's currently ranked by, so "Top CPU consumers (30s avg)"
// doesn't require reading the select to notice it's not just the latest
// sample.
func (st *resourceDetailState) topHeaderText(kind resourceKind) string {
	base := fmt.Sprintf("Top %s consumers", resourceNames[kind])
	if w := st.topWindow[kind]; w > 0 {
		base += fmt.Sprintf(" (%s avg)", windowOptionFor(topWindowOptions, topWindowValues, w))
	}
	return base
}

// refreshTopProcesses recomputes the top-consumers list for the currently
// selected resource from the most recent process snapshot. No-op if the
// window hasn't been built yet -- a ProcessSnapshot can arrive before the
// user has ever opened Resource Details.
func (st *resourceDetailState) refreshTopProcesses() {
	if st.topList == nil {
		return
	}
	if st.selected == resNet {
		st.topRows = nil
	} else {
		st.topRows = topProcessesFor(st.selected, st.lastProcs, st.topThreshold[st.selected], st.topWindow[st.selected], st.history)
	}
	st.topList.Refresh()
}

// recordHistory appends this tick's per-process metrics to history (used by
// the "Averaged over" window) and prunes anything older than
// maxHistoryWindow, plus any PID that's no longer in the current snapshot
// (an exited process, so nothing left to average) -- otherwise the map only
// ever grows across a long-running session.
func (st *resourceDetailState) recordHistory(snap ProcessSnapshot) {
	if st.history == nil {
		st.history = make(map[int32][]procHistorySample)
	}
	seen := make(map[int32]bool, len(snap.Procs))
	cutoff := snap.Timestamp.Add(-maxHistoryWindow)
	for _, p := range snap.Procs {
		seen[p.PID] = true
		hist := append(st.history[p.PID], procHistorySample{
			at:   snap.Timestamp,
			cpu:  p.CPUPercent,
			mem:  float64(p.MemPercent),
			disk: p.DiskReadKBs + p.DiskWriteKBs,
		})
		i := 0
		for i < len(hist) && hist[i].at.Before(cutoff) {
			i++
		}
		st.history[p.PID] = hist[i:]
	}
	for pid := range st.history {
		if !seen[pid] {
			delete(st.history, pid)
		}
	}
}

// averageOverWindow averages one metric from hist over samples at or after
// cutoff, returning ok=false if none exist yet (e.g. a process that just
// appeared) so the caller can fall back to its instantaneous value instead
// of hiding it entirely.
func averageOverWindow(hist []procHistorySample, kind resourceKind, cutoff time.Time) (avg float64, ok bool) {
	var sum float64
	var n int
	for _, s := range hist {
		if s.at.Before(cutoff) {
			continue
		}
		switch kind {
		case resCPU:
			sum += s.cpu
		case resMem:
			sum += s.mem
		case resDisk:
			sum += s.disk
		}
		n++
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}

// topProcessesFor returns up to maxTopProcessRows processes by the given
// resource's per-process metric, filtered to those clearing threshold and
// sorted descending. Network isn't handled here -- there's no per-process
// network data (see the "not available" message in buildTopProcessesArea).
//
// window > 0 ranks/filters by each process's average over that trailing
// window (from history) instead of its instantaneous value from snap --
// see topWindowOptions. A process with no history yet (just appeared) falls
// back to its instantaneous value rather than being silently dropped.
func topProcessesFor(kind resourceKind, snap ProcessSnapshot, threshold float64, window time.Duration, history map[int32][]procHistorySample) []topProcRow {
	type scored struct {
		name  string
		value float64
		pid   int32
	}
	var cutoff time.Time
	if window > 0 {
		cutoff = snap.Timestamp.Add(-window)
	}
	var candidates []scored
	for _, p := range snap.Procs {
		var v float64
		switch kind {
		case resCPU:
			v = p.CPUPercent
		case resMem:
			v = float64(p.MemPercent)
		case resDisk:
			v = p.DiskReadKBs + p.DiskWriteKBs
		default:
			continue
		}
		if window > 0 {
			if avg, ok := averageOverWindow(history[p.PID], kind, cutoff); ok {
				v = avg
			}
		}
		if v < threshold {
			continue
		}
		candidates = append(candidates, scored{name: p.Name, value: v, pid: p.PID})
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].value > candidates[j].value })
	if len(candidates) > maxTopProcessRows {
		candidates = candidates[:maxTopProcessRows]
	}

	rows := make([]topProcRow, len(candidates))
	for i, c := range candidates {
		rows[i] = topProcRow{name: c.name, value: formatTopValue(kind, c.value), pid: c.pid}
	}
	return rows
}

func formatTopValue(kind resourceKind, v float64) string {
	switch kind {
	case resCPU, resMem:
		return fmt.Sprintf("%.1f%%", v)
	case resDisk:
		return fmt.Sprintf("%.0f KB/s", v)
	default:
		return ""
	}
}

func (st *resourceDetailState) refreshBigLabel() {
	if st.bigLabel == nil {
		return
	}
	st.bigLabel.SetText(st.listRowText(st.selected))
}

// listRowText formats the same summary line sysview.go's mini-graph labels
// use, for the left-hand resource list and (for the selected one) the big
// label above the large graph.
func (st *resourceDetailState) listRowText(kind resourceKind) string {
	s := st.last
	switch kind {
	case resCPU:
		return fmt.Sprintf("CPU: %.0f%%", s.CPUPercent)
	case resMem:
		return fmt.Sprintf("Memory: %.0f%% (%s / %s)", s.MemPercent, formatBytes(s.MemUsedBytes), formatBytes(s.MemTotalBytes))
	case resDisk:
		return fmt.Sprintf("Disk: %.0f KB/s (R %.0f / W %.0f)", s.DiskReadKBs+s.DiskWriteKBs, s.DiskReadKBs, s.DiskWriteKBs)
	case resNet:
		return fmt.Sprintf("Network: %.0f KB/s (↓ %.0f / ↑ %.0f)", s.NetRecvKBs+s.NetSentKBs, s.NetRecvKBs, s.NetSentKBs)
	default:
		return ""
	}
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
