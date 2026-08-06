package main

import (
	"fmt"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
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

// maxTopProcessRows caps the Resource Details window's top-consumers list
// regardless of how many processes clear the threshold -- the point is a
// quick "what's actually driving this" glance, not a full sortable table
// (the main window's process table already covers that).
const maxTopProcessRows = 8

// topProcRow is one row of the Resource Details window's top-consumers
// list: a process name and its already-formatted value for whichever
// resource is currently selected.
type topProcRow struct {
	name  string
	value string
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
	topThresholdSelect  *widget.Select
	topList             *widget.List
	topNormalGroup      *fyne.Container
	topUnavailableLabel *widget.Label
	topStack            *fyne.Container
}

// newResourceDetailWindow returns update (called on every SystemSnapshot),
// updateProcesses (called on every ProcessSnapshot -- both main-goroutine
// only, see main.go), and show (opens the window already displaying the
// given resource, called from a mini graph's OnTapped).
func newResourceDetailWindow(a fyne.App) (update func(SystemSnapshot), updateProcesses func(ProcessSnapshot), show func(resourceKind)) {
	st := &resourceDetailState{app: a, selected: resCPU}

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

	st.win.SetContent(split)
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

	st.topThresholdSelect = widget.NewSelect(nil, func(sel string) {
		if st.selected == resDisk {
			st.topThreshold[resDisk] = diskThresholdValues[sel]
		} else {
			st.topThreshold[st.selected] = consumerThresholdValues[sel]
		}
		st.refreshTopProcesses()
	})

	st.topList = widget.NewList(
		func() int { return len(st.topRows) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			r := st.topRows[i]
			o.(*widget.Label).SetText(fmt.Sprintf("%s — %s", r.name, r.value))
		},
	)

	st.topNormalGroup = container.NewBorder(
		container.NewBorder(nil, nil, st.topHeader, st.topThresholdSelect, nil),
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

		st.topHeader.SetText(fmt.Sprintf("Top %s consumers", resourceNames[kind]))
		if kind == resDisk {
			st.topThresholdSelect.Options = diskThresholdOptions
			st.topThresholdSelect.SetSelected(thresholdOptionFor(diskThresholdOptions, diskThresholdValues, st.topThreshold[kind]))
		} else {
			st.topThresholdSelect.Options = consumerThresholdOptions
			st.topThresholdSelect.SetSelected(thresholdOptionFor(consumerThresholdOptions, consumerThresholdValues, st.topThreshold[kind]))
		}
		st.topThresholdSelect.Refresh()
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
		st.topRows = topProcessesFor(st.selected, st.lastProcs, st.topThreshold[st.selected])
	}
	st.topList.Refresh()
}

// topProcessesFor returns up to maxTopProcessRows processes by the given
// resource's per-process metric, filtered to those clearing threshold and
// sorted descending. Network isn't handled here -- there's no per-process
// network data (see the "not available" message in buildTopProcessesArea).
func topProcessesFor(kind resourceKind, snap ProcessSnapshot, threshold float64) []topProcRow {
	type scored struct {
		name  string
		value float64
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
		if v < threshold {
			continue
		}
		candidates = append(candidates, scored{name: p.Name, value: v})
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].value > candidates[j].value })
	if len(candidates) > maxTopProcessRows {
		candidates = candidates[:maxTopProcessRows]
	}

	rows := make([]topProcRow, len(candidates))
	for i, c := range candidates {
		rows[i] = topProcRow{name: c.name, value: formatTopValue(kind, c.value)}
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
