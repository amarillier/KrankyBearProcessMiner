package main

import (
	"fmt"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
)

// connColumn is connectionsview.go's equivalent of handlesview.go's
// handleColumn -- same tooltip-header-over-aligned-table shape as every
// other list of process-y data in this app.
type connColumn struct {
	title   string
	width   float32
	tooltip string
}

var connColumns = []connColumn{
	{"Local Address", 140, "This process's local endpoint"},
	{"Remote Address", 140, "The other endpoint this connection is talking to"},
	{"Direction", 80, "Outbound: this process called connect(). Inbound: this process accepted a connection someone else initiated. Existing: first noticed from its traffic, not from its Connect/Accept -- it was already open before \"Show Network/Disk I/O\" was turned on. ETW has no separate \"established\" event, so this is the closest available state label"},
	{"Duration", 90, "Time since this connection was first seen -- via its Connect/Accept event, or (for a connection already open before \"Show Network/Disk I/O\" was turned on, see Direction) its first Send/Recv. Not the connection's true age in the Existing case"},
}

// connWindow/-Open mirror the state windows.go's hide-all/show-all cares
// about, same reasoning as handlesview.go's handlesWindow/-Open: the window
// itself is owned by procViewState.connWin, private state with no other
// package-level hook for windows.go to reach into.
var connWindow fyne.Window
var connWindowOpen bool

// buildConnectionsWindow constructs the single reusable "Show Connections"
// window, lazily, the first time it's used. Unlike handlesview.go's
// buildHandlesWindow this has no loading/error banner state to stack --
// there's no fetch in flight ever, just a read of globalNetIOWatcher's
// already-live connActive map (see openOrRefreshConnectionsWindow and
// refreshConnectionsWindow), the same "poll what's already computed" shape
// procview.go's childWin uses for live children rows.
func (st *procViewState) buildConnectionsWindow() {
	st.connWin = st.app.NewWindow("")
	st.connWin.SetIcon(resourceKrankyBearProcessMinerPng)
	connWindow = st.connWin

	st.connWinTable = widget.NewTable(
		func() (int, int) { return len(st.connWinRows), len(connColumns) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		st.updateConnWinCell,
	)
	st.connWinTable.ShowHeaderRow = true
	st.connWinTable.CreateHeader = func() fyne.CanvasObject { return ttwidget.NewButton("", nil) }
	st.connWinTable.UpdateHeader = st.updateConnWinHeader
	for i, col := range connColumns {
		st.connWinTable.SetColumnWidth(i, col.width)
	}

	st.connWinCountLabel = widget.NewLabel("")
	st.connWinCountLabel.Wrapping = fyne.TextWrapWord

	stack := container.NewStack(st.connWinTable, st.connWinCountLabel)

	st.connWin.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(stack), st.connWin.Canvas()))
	st.connWin.Resize(fyne.NewSize(560, 420))

	st.connWin.SetOnClosed(func() {
		fynetooltip.DestroyWindowToolTipLayer(st.connWin.Canvas())
		st.connWin = nil
		connWindow = nil
		connWindowOpen = false
		st.connWinPID = -1
		st.connWinRows = nil
	})
}

// openOrRefreshConnectionsWindow shows the single reusable Connections
// window for pid/name, creating it on first use, or updating it in place if
// already open -- same shape as handlesview.go's openOrRefreshHandlesWindow,
// including following the selection automatically (see procview.go's
// renderDetail). No goroutine fetch here (contrast handlesview.go): the
// data is already sitting in globalNetIOWatcher, decoded live off the ETW
// callback goroutine as Connect/Accept/Disconnect events arrive.
func (st *procViewState) openOrRefreshConnectionsWindow(pid int32, name string) {
	if st.connWin == nil {
		st.buildConnectionsWindow()
	}
	st.connWinPID = pid
	st.connWin.SetTitle(fmt.Sprintf("%s%s (PID %d) — Connections", adminTitlePrefix(), name, pid))
	st.populateConnWinRows()
	connWindowOpen = true
	st.connWin.Show()
	st.connWin.RequestFocus()
}

// refreshConnectionsWindow is called once per applySnapshot tick (see
// procview.go) to keep an already-open Connections window live -- the same
// "keep it in sync every sample, not just on open" behavior
// refreshChildWindow already gives procview.go's child drill-down window,
// needed here because connActive changes continuously as the ETW session
// sees new Connect/Accept/Disconnect events, not just when the user
// re-opens the window.
func (st *procViewState) refreshConnectionsWindow() {
	if st.connWin == nil {
		return
	}
	if p, ok := st.byPID[st.connWinPID]; ok {
		st.connWin.SetTitle(fmt.Sprintf("%s%s (PID %d) — Connections", adminTitlePrefix(), p.Name, p.PID))
	} else {
		st.connWin.SetTitle(adminTitlePrefix() + "(process exited)")
	}
	st.populateConnWinRows()
}

// populateConnWinRows reads globalNetIOWatcher's current snapshot for
// st.connWinPID and refreshes the table/empty-state in place.
func (st *procViewState) populateConnWinRows() {
	st.connWinRows = globalNetIOWatcher.ConnectionsForPID(st.connWinPID)
	if len(st.connWinRows) == 0 {
		if st.netIOEnabled {
			st.connWinCountLabel.SetText("No active TCP connections seen for this process yet.")
		} else {
			st.connWinCountLabel.SetText("\"Show Network/Disk I/O\" is off -- turn it on to see live connections here.")
		}
		st.connWinCountLabel.Show()
		st.connWinTable.Hide()
	} else {
		st.connWinCountLabel.Hide()
		st.connWinTable.Show()
	}
	st.connWinTable.Refresh()
}

func (st *procViewState) updateConnWinCell(id widget.TableCellID, o fyne.CanvasObject) {
	label := o.(*widget.Label)
	rows := st.connWinRows
	if id.Row < 0 || id.Row >= len(rows) {
		label.SetText("")
		return
	}
	c := rows[id.Row]
	switch id.Col {
	case 0:
		label.SetText(c.LocalAddr + ":" + strconv.Itoa(int(c.LocalPort)))
	case 1:
		label.SetText(c.RemoteAddr + ":" + strconv.Itoa(int(c.RemotePort)))
	case 2:
		label.SetText(c.Direction.String())
	case 3:
		label.SetText(time.Since(c.Since).Round(time.Second).String())
	}
}

func (st *procViewState) updateConnWinHeader(id widget.TableCellID, o fyne.CanvasObject) {
	btn := o.(*ttwidget.Button)
	if id.Col < 0 || id.Col >= len(connColumns) {
		btn.SetText("")
		return
	}
	col := connColumns[id.Col]
	btn.SetText(col.title)
	btn.SetToolTip(col.tooltip)
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
