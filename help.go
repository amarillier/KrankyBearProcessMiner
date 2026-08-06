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
• PID, Name, PPID, User, CPU%, Mem%, Disk R, Disk W, Private — click a
  column header to sort by it, click again to reverse. Drag a column
  boundary to resize it.
• Disk R/W show live per-process read/write KB/s. Private shows real
  Private Bytes on Windows, an RSS-minus-shared-pages approximation on
  Linux, and N/A on macOS (the closest available figure there is reserved
  virtual address space, not real private memory — showing it would be
  misleading rather than just incomplete). Either column reads N/A for a
  process owned by another user/system account, same as any other
  permission-restricted field.
• Long process names are ellipsized (…) to fit the Name column instead of
  overflowing into whatever's next to it.
• Filter by name (top-left box), and/or narrow the list to actual resource
  hogs with the "Top CPU" / "Top Mem" selects (Off / ≥1% / ≥5% / ≥10% /
  ≥25%, independently adjustable) — a combination not offered out of the
  box by any of the platform-native tools this app draws on.
• "Regex" checkbox switches the name filter from a plain substring match
  to a real regexp match — e.g. ^(?i)(process|activity).* to compare just
  this app's own processes against Activity Monitor's, with nothing else
  cluttering the list. An invalid or still-being-typed regex shows every
  row rather than going blank or erroring.
• "Refresh every" adjusts the process-list sample interval (1s/2s/5s/10s);
  "Refresh Now" forces an immediate resample.
• Select a row and click "End Process" to terminate it, after a
  confirmation dialog.

PARENT PROCESSES VIEW:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
"Parent processes only" declutters the table down to processes worth
drilling into: anything with at least one child (e.g. a browser with a
dozen helper processes), plus anything with no visible parent of its own.
A childless process whose parent IS shown is hidden here, not gone — it's
one click away. Click a parent row to open (or update) a single read-only
"Children" window listing its direct children with live CPU%/Mem%; only
one such window is ever open, and clicking a different parent swaps its
contents into it rather than opening another.

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

RESOURCE DETAILS WINDOW:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Click any top-strip mini graph (CPU/Memory/Disk/Network), or use the View
menu / system tray ("Resource Details"), to open a bigger, clearer view of
just one resource at a time, Task Manager-style: a resizable list to switch
between the four, one large graph for whichever is selected. All four keep
recording history in the background even while not the one shown, so
switching between them never shows a blank graph. The big graph shows
value-axis gridlines (%/KB/s) and a time axis (elapsed time back from
"now") so peaks and the visible time span are actually readable.
Below the resource list, a Top Consumers panel shows the processes
actually driving CPU, Memory, or Disk — name plus just that one metric —
filtered by an adjustable threshold (Off / ≥1% / ≥5% / ≥10% / ≥25% for
CPU/Mem; KB/s tiers for Disk), capped at 8 rows. Not available for
Network — there's no per-process network API to draw from.

SYSTEM INFO WINDOW:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
View menu / system tray ("System Info") shows a point-in-time summary:
computer name and OS/platform/version/architecture, CPU model/cores/
logical processors/speed, total memory, mounted disk volumes with size,
and network adapters with type (wired/wireless), connected status, and
WiFi signal strength where applicable. Adapter type is a name-pattern
heuristic, not guaranteed accurate for unusual adapter names. Disk volumes
are mounted filesystems, not raw physical disks. "Copy to Clipboard" grabs
the whole summary as plain text. The window opens right away with a
"Collecting…" message while gathering runs in the background — on macOS
this can take 10+ seconds (WiFi signal strength there needs a slow system
tool), so the delay is expected, not a hang.

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
• No per-process network usage or GPU usage on any platform yet — no
  platform offers a simple API for either (Windows' own Task Manager
  relies on ETW tracing for per-process network) — the Top CPU/Mem filter
  is scoped accordingly for now.
• No real "Private" memory figure on macOS (see PROCESS TABLE above).
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
