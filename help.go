package main

import (
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

var helpWindow fyne.Window

// helpOpen tracks whether the Help window should be considered "open" for
// hide-all/show-all purposes (windows.go) -- see aboutOpen in about.go.
var helpOpen bool

// showHelp displays comprehensive help documentation
// Reusable pattern from KrankyBearClock - customize these for your app:
//   - appName: Your application name
//   - resourceKrankyBearProcessMinerPng: Your embedded icon resource
//   - helpText: Your application's help content (see below for structure)
//   - GitHub and License URLs
//
// Help text structure recommendation:
//   - Use section headers with visual separators (━━━)
//   - Group related features together
//   - Include tips, tricks, and known limitations
//   - Add keyboard shortcuts
//   - Provide links to external resources
func showHelp(a fyne.App) {
	if helpWindow != nil && helpWindow.Content().Visible() {
		helpWindow.Show()
		helpWindow.RequestFocus()
		return
	}

	helpWindow = a.NewWindow(appName + " - Help")
	helpWindow.SetIcon(resourceKrankyBearProcessMinerPng)

	helpText := `` + appName + ` - Help

OVERVIEW:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
A free, cross-platform process/system monitor in the spirit of Activity
Monitor, Task Manager, top, and Sysinternals Process Explorer — the same
app, with the same feature set, on Windows, macOS, and Linux.

SYSTEM GRAPHS:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Live sparklines across the top of the window for CPU, Memory, Disk I/O,
and Network, updated every second, with the current numeric values shown
alongside each graph.

PROCESS TABLE:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
• PID, Name, PPID, User, CPU%, Mem% — click a column header to sort by
  it, click again to reverse. Drag a column boundary to resize it.
• Long process names are ellipsized (…) to fit the Name column instead of
  overflowing into whatever's next to it.
• Filter by name (top-left box), and/or narrow the list to actual resource
  hogs with the "Top CPU" / "Top Mem" selects (Off / ≥1% / ≥5% / ≥10% /
  ≥25%, independently adjustable) — a combination not offered out of the
  box by any of the platform-native tools this app draws on.
• "Refresh every" adjusts the process-list sample interval (1s/2s/5s/10s);
  "Refresh Now" forces an immediate resample.
• Select a row and click "End Process" to terminate it, after a
  confirmation dialog.

DETAIL PANE:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Selecting a process shows its user, live status, start time, full command
line, ancestry (parent chain), and a scrollable list of child processes.
Click a child to jump straight to it in the main table — the name filter
clears automatically first if it would otherwise hide that child.

RESIZABLE LAYOUT:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Drag the divider between the process table and the detail pane, and the
one between the detail info and its children list, to trade space between
them. Both positions persist across launches, alongside the main window
size.

WINDOW MANAGEMENT:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Hide All / Show All (Window menu or system tray) hides the main window and
any open About/Help/Update windows together, then brings back exactly
that same set later. Alt+H is a boss-key hotkey for Hide All — there's no
matching Show hotkey by design; reveal via the Window menu or tray instead.

SMART FEATURES:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
✨ Theme Support: Light, Dark, or System theme (View menu).
✨ Lightweight sampling: the expensive-to-read fields (command line, start
   time, live status) are only fetched for whichever single process is
   currently selected, not for every process on every sample tick — this
   app aims to stay out of the way of the very thing it's measuring.
✨ Throttled update checker: a silent, once-per-day automatic check plus an
   always-available manual "Check for Updates" (Help menu / tray).

KEYBOARD SHORTCUTS:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
• Alt+H - Hide All windows (main + any open About/Help/Update)
• Cmd/Ctrl+Q - Quit
• Cmd/Ctrl+W - Close window
• Cmd/Ctrl+M - Minimize

KNOWN LIMITATIONS:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
• No per-process disk I/O on macOS, and no per-process network usage on
  any platform yet — the Top CPU/Mem filter is scoped accordingly for now.
• End Process is a hard kill; no graceful-terminate or elevation flow yet.
• Column widths aren't remembered across launches (only the split-pane
  divider positions and window size are) — Fyne's table widget has no way
  to read back a column's width after it's been resized.

MORE INFORMATION:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
For documentation, bug reports, or feature requests:
📦 GitHub: https://github.com/amarillier/KrankyBearProcessMiner
📄 License: https://github.com/amarillier/KrankyBearProcessMiner/blob/main/LICENSE
📝 Release Notes: Check "Help → Check for Updates"

FREE SOFTWARE - Use anywhere, anytime, any purpose!
No registration, no tracking, no phone-home (except manual update checks).
`

	helpLabel := widget.NewLabel(helpText)
	helpLabel.Wrapping = fyne.TextWrapWord

	// Links - update URLs for your project
	githubURL, _ := url.Parse("https://github.com/amarillier/KrankyBearProcessMiner")
	githubLink := widget.NewHyperlink("Visit GitHub Repository", githubURL)
	githubLink.Alignment = fyne.TextAlignCenter

	licenseURL, _ := url.Parse("https://github.com/amarillier/KrankyBearProcessMiner/blob/main/LICENSE")
	licenseLink := widget.NewHyperlink("View License", licenseURL)
	licenseLink.Alignment = fyne.TextAlignCenter

	// Create scrollable area with minimum size for better readability
	scrollContent := container.NewScroll(helpLabel)
	scrollContent.SetMinSize(fyne.NewSize(750, 550))

	// Layout with better proportions
	header := container.NewVBox(
		widget.NewLabelWithStyle(appName+" - Help", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
	)

	footer := container.NewVBox(
		widget.NewSeparator(),
		container.NewCenter(container.NewHBox(githubLink, licenseLink)),
	)

	content := container.NewBorder(header, footer, nil, nil, scrollContent)

	helpWindow.SetContent(container.NewPadded(content))
	helpWindow.Resize(fyne.NewSize(850, 700))

	helpWindow.SetCloseIntercept(func() {
		helpOpen = false
		helpWindow.Hide()
	})

	helpOpen = true
	helpWindow.Show()
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
