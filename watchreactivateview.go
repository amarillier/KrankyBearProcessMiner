package main

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"
)

// offerWatchReactivation shows a one-time startup dialog listing every
// pending process watch (see watchpersist.go's pendingProcessWatches) and
// its current match against byPID -- a no-op if there's nothing pending,
// same "silent when nothing to show" precedent update.go's
// checkForUpdatesAuto already sets for its own launch-time dialog.
//
// Unlike every other secondary window in this app, this one is transient,
// not persistent: shown at most once per launch, dismissed, never reopened
// -- no windows.go Hide All/Show All registration, same as the "already
// running" alert window.
//
// Modeled on interferenceview.go's own "Watched Targets" list (label/
// checkbox + action button per row) rather than a dialog.NewCustomConfirm --
// a list with per-row state needs more layout room than a modal dialog
// gives comfortably.
func offerWatchReactivation(a fyne.App, watcher *interferenceWatcher, byPID map[int32]ProcInfo) {
	rows := buildWatchReactivationRows(byPID)
	if len(rows) == 0 {
		return
	}

	win := a.NewWindow(appName + " - Reactivate Watches")
	win.SetIcon(resourceKrankyBearProcessMinerPng)

	header := widget.NewLabel("These processes were being watched for interference last time " +
		appName + " ran. Choose which to resume watching. \"Forget\" removes an entry for good; " +
		"leaving one unchecked (or just closing this window) keeps it pending for next launch " +
		"instead of losing it.")
	header.Wrapping = fyne.TextWrapWord

	listBox := container.NewVBox()
	checks := make(map[*widget.Check]watchReactivationRow)

	var render func()
	render = func() {
		checks = make(map[*widget.Check]watchReactivationRow)
		var objs []fyne.CanvasObject
		for _, row := range rows {
			row := row
			if len(row.MatchedPIDs) == 0 {
				label := widget.NewLabel(fmt.Sprintf("%s — not currently running", watchEntryDisplayName(row.Entry)))
				forgetBtn := ttwidget.NewButton("Forget", func() {
					removePendingProcessWatch(row.Entry)
					rows = removeReactivationRow(rows, row.Entry)
					if len(rows) == 0 {
						win.Close()
						return
					}
					render()
				})
				forgetBtn.SetToolTip("Remove this from the watch list for good -- won't be offered again")
				objs = append(objs, container.NewBorder(nil, nil, nil, forgetBtn, label))
				continue
			}

			label := fmt.Sprintf("%s — found, ready to reactivate", watchEntryDisplayName(row.Entry))
			if len(row.MatchedPIDs) > 1 {
				label = fmt.Sprintf("%s — %d matching processes found, reactivating watches all of them",
					watchEntryDisplayName(row.Entry), len(row.MatchedPIDs))
			}
			check := widget.NewCheck(label, nil)
			check.SetChecked(true)
			checks[check] = row
			objs = append(objs, check)
		}
		listBox.Objects = objs
		listBox.Refresh()
	}
	render()

	reactivateBtn := ttwidget.NewButton("Reactivate Selected", func() {
		for check, row := range checks {
			if !check.Checked {
				continue
			}
			for _, pid := range row.MatchedPIDs {
				watcher.watchPID(pid, row.Entry.Name)
			}
			removePendingProcessWatch(row.Entry)
		}
		win.Close()
	})
	skipBtn := ttwidget.NewButton("Skip All", func() { win.Close() })
	skipBtn.SetToolTip("Do nothing this launch -- every entry stays pending and is offered again next time")

	content := container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()),
		container.NewHBox(reactivateBtn, skipBtn),
		nil, nil,
		container.NewVScroll(listBox),
	)
	win.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(content), win.Canvas()))
	win.Resize(fyne.NewSize(560, 360))
	win.SetOnClosed(func() { fynetooltip.DestroyWindowToolTipLayer(win.Canvas()) })
	win.Show()
	win.RequestFocus()
}

// watchEntryDisplayName prefers the captured process name (the common case)
// and falls back to the exe path only for the rare entry that somehow has a
// path but no name.
func watchEntryDisplayName(entry persistedProcessWatch) string {
	if entry.Name != "" {
		return entry.Name
	}
	return entry.ExePath
}

// removeReactivationRow drops one row from an in-memory rows slice --
// local-only bookkeeping for this dialog's own re-render, distinct from
// removePendingProcessWatch which mutates the actual persisted pending list.
func removeReactivationRow(rows []watchReactivationRow, entry persistedProcessWatch) []watchReactivationRow {
	out := rows[:0]
	for _, r := range rows {
		if r.Entry != entry {
			out = append(out, r)
		}
	}
	return out
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
