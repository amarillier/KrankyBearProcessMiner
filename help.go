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
• PID, Name, PPID, User, CPU%, Mem%, Memory, Disk R, Disk W, Private,
  Handles — click a column header to sort by it; clicking the same header
  again cycles ascending → descending → unsorted (back to whatever order
  the last snapshot/filter produced, no arrow shown) → ascending again.
  Drag a column boundary to resize it.
• "Columns…" (next to Refresh Now) hides individual columns you don't need
  -- handy once the column count grows past what a narrower/laptop display
  can show without horizontal scrolling. Name always stays visible (every
  row needs at least one column that says which process it is); every
  other column, including ones added by later features, gets a checkbox.
  Persists across launches.
• "Export CSV…" (next to Columns…) saves exactly what's on screen --
  whichever columns are currently visible, in the current filter/sort order
  -- to a CSV file. Cell values match the display (same number formatting)
  but without Name/User/Notable's on-screen truncation, which only exists
  to fit a table cell, not a spreadsheet column.
• Memory shows actual physical memory in use (RSS -- Resident Set Size),
  the same figure Mem% is computed from, on all three platforms. In the
  Parent-processes view, both Mem% and Memory show the combined total
  across the parent and every descendant, same as CPU%.
• Disk R/W show live per-process read/write KB/s. Private shows real
  Private Bytes on Windows, an RSS-minus-shared-pages approximation on
  Linux, and N/A on macOS (the closest available figure there is reserved
  virtual address space, not real private memory — showing it would be
  misleading rather than just incomplete). Either column reads N/A for a
  process owned by another user/system account, same as any other
  permission-restricted field.
• Handles shows open handles on Windows, open file descriptors on macOS
  and Linux — the same underlying idea on all three, and (unlike Private)
  genuinely available everywhere: a single cheap syscall on every platform,
  not a batch-per-tick concern. A count that climbs steadily and never
  comes back down, even while the process otherwise looks idle, is a
  classic sign of a handle/fd leak — worth watching if a process seems to
  slowly degrade or eventually hang.
• Long process names are ellipsized (…) to fit the Name column instead of
  overflowing into whatever's next to it.
• Notable column (Windows): "🛡 <vendor>" for a process recognized as
  security software (a small, best-effort name-fragment list -- see
  Interference Watch below), or "🎭 not launched by <expected>" for the
  small set of well-known Windows process names with a stable expected
  parent (currently svchost.exe/services.exe and lsass.exe/wininit.exe) --
  a mismatch is the classic malware-hides-as-a-system-process trick, worth
  a second look, though an unusual but legitimate launch path could also
  cause it. Blank for the overwhelming majority of rows.
• Filter by name (top-left box), and/or narrow the list to actual resource
  hogs with "Top CPU" / "Top Mem" (Off / ≥1% / ≥5% / ≥10% / ≥25%), "Top
  Memory" (Off / ≥100 MB / ≥500 MB / ≥1 GB / ≥4 GB — an absolute-size
  complement to Top Mem's percentage, since 5% means something very
  different on a 16GB laptop than a 128GB workstation), and "Top Disk"
  (Off / ≥10 / ≥100 / ≥500 / ≥1000 KB/s), independently adjustable and
  combined via OR — a process shows if it clears *any* one of the enabled
  thresholds. A combination not offered out of the box by any of the
  platform-native tools this app draws on.
• "Notify on Threshold Breach" (off by default) sends a desktop notification
  the instant a process newly crosses one of those same four thresholds --
  a rising-edge event, so a process that stays hot only notifies once, not
  every tick. Leave every threshold above at Off and this simply never
  fires; no separate configuration. Checks every running process, not just
  what the name filter or Parent-processes view happens to be showing.
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
one click away. Click a parent row to open (or update) a single "Children"
window listing its direct children — sortable columns, a name filter, and
End Process, same as the main table; only one such window is ever open,
and clicking a different parent swaps its contents into it rather than
opening another (resetting the filter/selection, but keeping whatever sort
was already chosen).
Each parent row's CPU%/Mem% is the combined total across it and every
descendant it's standing in for (Task Manager-style), not just its own
usage — the children window still shows each child's own individual
figures, which is the point of opening it.

DETAIL PANE:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Selecting a process shows its user, live status, start time, full command
line, ancestry (parent chain), and a scrollable list of child processes.
Click a child to jump straight to it in the main table — the name filter
clears automatically first if it would otherwise hide that child.

THREAD INSPECTION:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Select a process and click "Show Threads" for a one-off snapshot (not a
live view — thread lists change fast). On Windows: TID, state, kernel/user
CPU time, and each thread's start address resolved to
"module.dll+0xOFFSET" — or "UNBACKED (possible injection)" if the address
falls outside every one of the process's own loaded modules, the classic
sign of code injection (reflective DLL injection, shellcode). Useful for
verifying AV/security-software exclusions are actually configured, not
just trusting that they are. This does NOT detect an AV/EDR-style *hook*
patched into an otherwise-legitimate module (e.g. ntdll.dll) — only
injected code running outside any loaded module. macOS/Linux show just a
thread count for now: resolving start addresses needs reading another
process's memory, which needs privileges/entitlements neither platform
grants a normal third-party app the way Windows does. Only one Threads
window is ever open at a time — selecting a different process elsewhere
(the main table, a Children window, Resource Details' Top Consumers) while
it's open updates it to that process automatically instead of opening a
second one.

INTERFERENCE WATCH:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
A more accessible, continuous layer on top of Thread Inspection above, for
catching AV/security-software interference as it happens rather than
one snapshot at a time. Select a process and click "Watch for Interference"
(next to "Show Threads") to add it to the watch list — this also opens (or
brings to focus) the Interference Watch window itself, so it's immediately
clear where to look for results; the View/Process menu and tray's "Check
for Interference" opens the same window any other time. It also lets you
watch a whole directory — any process launched from it is watched
automatically, without re-selecting it every time it restarts.
Every process-list sample (same 1-10s interval as the main table), each
watched process is checked for four signs of interference:
• A thread with an "UNBACKED" start address that wasn't there the *first*
  time it was checked — reflective injection (raw shellcode, no module
  ever loaded), the technique malware uses specifically to stay off the
  module list.
• A new module (DLL) loading into the process that wasn't there when
  watching started — the technique legitimate AV/EDR hooking actually
  uses instead (it wants its DLL visible, not hidden), so this is what
  catches a real security-vendor hook that the thread-based check won't.
• A thread's stack memory containing a pointer into a module that isn't
  part of Windows or the process's own files — a coarse approximation of
  the classic "thread stacking" technique (manually watching live call
  stacks for a security vendor's code in the call path). Unlike the two
  signals above, this one is NOT relative to a baseline: it scans each
  thread's entire stack region (not just current call frames), so it can
  find evidence of a hook that was already there before you started
  watching, on the very first check. This is a pointer-address scan, not
  true call-stack unwinding, so treat a hit as worth confirming with
  Process Explorer/Procmon, not proof on its own. If the module name
  matches a short, best-effort known-vendor list, the event names the
  likely vendor; otherwise it's reported as an unrecognized third-party
  module — still worth investigating, just without a friendly label.
• Windows Defender's own real-time-protection minifilter (AMFilter)
  intercepting a file the watched process opens — the *other* meaning of
  "AV interference" (synchronous file-scan latency), which the other three
  signals can't see at all since they only look at what's happening
  *inside* the watched process itself. Unlike the other three, this one
  needs a live, real-time ETW subscription, which needs Administrator
  rights to create at all (see WINDOW MANAGEMENT below for the
  one-normal-plus-one-elevated exception to the single-instance rule, and
  CAPTURE TRACE below for the same requirement) — silently just doesn't
  show up if not running elevated, same as the other three simply don't
  show up on macOS/Linux.
  Always shown as "⚠", never "🛑": Defender scanning a file your watched
  process opened is expected, legitimate behavior, not something to flag
  as alarming — this signal exists to make it visible, not to accuse it.
  Confirmed via real-world testing that this specific signal never fires
  at all for a well-known, Microsoft-signed "trusted" process (Notepad: no
  file-scan event despite a completed Save, while Defender's own trust
  bookkeeping fired reliably for other processes at the same moment) —
  Defender appears to fast-track trusted processes through a different
  code path that skips the file-scan event entirely. There's a fallback
  for exactly this: a process being registered for Defender's own trust
  evaluation is also logged (no file path — just "this process is being
  evaluated," a different claim than "this file was scanned"), so a
  trusted process still shows *something* rather than going silent.
  Also, unlike the other three, there's no baseline/persistence concept: a
  file scan is a momentary event, so it's logged every time it happens
  while watching, not just the first time or only while "still ongoing."
For the first two signals, whatever was already present *before* you
started watching is treated as the baseline, not reported, so watching an
unusual-but-legitimate process doesn't immediately cry wolf. A "⚠" appears
in the main table (and the Parent-processes children window) for any
watched process currently showing any of the three signals, and stays lit
for as long as the condition persists, not just the one moment it was
first detected. The Interference Watch window's event log has Copy to
Clipboard and Clear buttons.
Each watched process in the "Watched Targets" list shows an at-a-glance
status: "✓" if nothing has ever been logged against it, "⚠" if everything
logged so far matched the known-vendor list (attributable, still worth
attention but less alarming), or "🛑" if anything unrecognized was found
(the case that most needs a closer look). Individual events in the log get
the same "⚠"/"🛑" treatment for the same reason. This is a display hint
based on a best-effort name match, not a certified verdict either way —
directory watches don't get a status icon (attributing a directory's
history back to it reliably isn't worth the added complexity for a glance
indicator), just a plain listing.
All four signals are Windows-only — on macOS/Linux the watch list can
still be built but nothing will ever be flagged (the button and "Add
Directory" say so). The file-scan signal specifically also needs
Administrator rights (see above) — the other three work the same either
way.
Expect some genuinely benign "new module loaded" events: browsers and other
large apps delay-load Windows OS components on demand well after startup
(e.g. Windows.Devices.Bluetooth.dll/BthRadioMedia.dll appearing the moment
a Bluetooth or device-enumeration API actually gets touched) -- normal
behavior, not evidence of anything. The event log helps you build a sense
of what's normal for a given process versus what's worth a second look.

WATCH PERSISTENCE:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Interference Watch's watch list survives a restart. Directory watches are
simple -- just a path -- so they're restored silently and take effect
immediately, no review needed. Process watches are different: a PID means
nothing across a restart, so each one is remembered by its executable path
(the strong, unambiguous key) with its name as a fallback for the rare case
the path couldn't be captured. On the next launch, once the process list is
available, a one-time "Reactivate Watches" window lists each remembered
process watch against what's actually running now: found and ready to
reactivate (checked by default -- uncheck to skip it just this once),
"N matching processes found" if more than one currently-running process
shares that same executable path (reactivating watches all of them, not
just one -- there's no picker for choosing among them), or "not currently
running" with a "Forget" button to remove it for good. Leaving an entry
unchecked, or just closing the window, keeps it pending and offers it again
next launch rather than losing it. This window only appears when there's
something to review -- nothing shows up if no process watches were saved.

CHECK SIGNATURE:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
A different angle on the same "trust but verify" idea as Thread Inspection
and Interference Watch above, but aimed at the executable file itself
rather than its runtime behavior: select a process and click "Check
Signature" for a one-off Authenticode check of its on-disk .exe, Windows
only. Three outcomes: ✓ signed with a certificate that chains to a trusted
authority; ⚠ not signed at all (not necessarily malicious -- plenty of
legitimate software, especially open-source or in-house tools, ships
unsigned -- but worth a second look for something you don't recognize); or
🛑 signed, but something's wrong (hash mismatch suggesting the file was
modified after signing, an untrusted/self-signed certificate, an expired
certificate, etc.). This also checks Windows' catalog-signing mechanism,
not just an embedded signature -- most System32 binaries (e.g. notepad.exe)
are catalog-signed rather than individually signed, so checking only for an
embedded signature would flag a huge share of stock Windows as "unsigned."
Revocation isn't checked (that would mean a network fetch per check,
against this app's offline-first design elsewhere) -- this is a structural
check (is it signed, does the chain lead to somewhere trusted), not a live
"has this certificate been revoked since" verdict. On Windows, the main
table also has a "Signed" column running the same check automatically in
the background, once per process, showing the same ✓/⚠/🛑 icons without
needing to click the button -- the "Check Signatures" checkbox above the
table turns this off if you'd rather not have it running at all (it won't
show an icon for a process it hasn't gotten to checking yet). Neither the
column nor the checkbox appear at all on macOS/Linux -- there's nothing
useful either one could ever show there, so rather than leave a permanently
blank column and a permanently disabled checkbox in view, both are simply
absent.

OPEN HANDLES:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Select a process and click "Show Handles" for a one-off snapshot (not a
live view) of every open handle/file descriptor it holds and what it
points to, where resolvable — the individual detail behind the count the
"Handles" column already shows. Unlike Thread Inspection, every platform
gets a real listing here; the difference between them is how hard it is to
get, not whether it's possible. Linux: full path resolution straight from
/proc/[pid]/fd. macOS: the same proc_pidinfo call the Handles column's
count already uses can list each one, and a second call
(proc_pidfdinfo/PROC_PIDFDVNODEPATHINFO) resolves file-backed ones to a
path — no root needed for your own processes, but a different-user or
protected process fails outright, same as elsewhere in this app. Windows is
the hardest: an undocumented system call (NtQuerySystemInformation) lists
every handle system-wide, then each one belonging to the selected process
is duplicated into this app and queried (NtQueryObject) for its type and,
where applicable, its path — a File-type handle's path is translated from
its raw kernel form (e.g. "\Device\HarddiskVolume3\...") back to a familiar
drive letter. One specific query (resolving a handle's name) is a
well-known, if rare, way to hang indefinitely — classically a named pipe
with no listener on the other end.
This app guards against that with a timeout: a handle that doesn't resolve
in time shows "(query timed out)" instead of blocking the whole snapshot,
and a banner at the top of the window says how many hit this. Registry key
paths currently show in their raw internal form (e.g. "\REGISTRY\MACHINE\...")
rather than translated to "HKLM"/"HKCU". Same single-reusable-window,
follows-the-selection behavior as the Threads window above.

CAPTURE TRACE:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Windows-only, via the View menu or tray. Records an ETW (Event Tracing for
Windows) trace to a .etl file using Windows Performance Recorder (wpr.exe,
built into Windows -- no separate install). Four checkboxes, checked by
default, each a real WPR profile: Network (per-process send/receive
activity), Disk I/O (per-process disk activity including latency, not just
the throughput this app's own Disk R/W columns show), File I/O (which files,
not just how many bytes), and Minifilter (a security product's filter
driver intercepting file I/O -- the *other* meaning of "AV interference"
Interference Watch's own three signals can't see). Click Start, do whatever
you're trying to capture, then Stop && Save to a .etl file -- Cancel
discards it instead. A short capture can still produce a large file (one
real test: ~700MB for well under a minute with Network+File I/O alone), so
watch disk space for a longer one. While saving, the status line turns red
and says not to exit ProcessMiner until it finishes -- take that literally:
exiting mid-save corrupts the .etl (confirmed for real -- WPA refused to
open the result). This app does not analyze the trace
itself -- open the saved .etl in Windows Performance Analyzer (WPA), free
via the Microsoft Store or the Windows ADK's "Windows Performance Toolkit"
component; see Known Limitations for why a built-in analyzer isn't planned.
Needs Administrator rights -- these are all kernel-level tracing providers.
Opening this window without them shows an explanation and a "Relaunch as
Administrator" button instead of the checkboxes/buttons, rather than
letting you click through to a Start failure -- click it for a UAC prompt
and a second, elevated copy, no need to close this one first (see WINDOW
MANAGEMENT below for the one-normal-plus-one-elevated exception to the
usual single-instance rule). Declining the UAC prompt is a normal choice,
not an error -- nothing happens, no error dialog. An elevated window's
title bar says so ("Administrator: KrankyBear ProcessMiner"), same
convention Windows' own elevated console windows use. The one realistic
Start failure that can
still happen even elevated is a stale capture left running by an earlier
crash -- Cancel clears that.

NETWORK/DISK I/O MONITORING:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Windows-only, via the "Show Network/Disk I/O" checkbox above the main
table. Unlike Capture Trace above, this is live, in-app ETW consumption --
not a recording to analyze later in WPA, but four columns that update
every sample tick: "Net Send"/"Net Recv" (live per-process network
throughput), "Disk Latency" (average disk I/O completion time attributed
to this process), and "Connections" (how many TCP connections this
process currently has open -- a quick way to tell which of several
same-named processes, e.g. a dozen Chrome renderers, is actually talking
to the network right now, without opening each one's own Connections
window to check). This is the "real per-process network usage and I/O
latency" piece this app's own backlog left open for a while -- per-process
network use in particular isn't available from gopsutil at all (unlike
CPU/Memory/Disk R/W), so this needed ETW specifically.
Off by default, unlike the Signed column's own "Check Signatures"
checkbox -- this one starts a real kernel-level tracing session (needs
Administrator, same requirement Capture Trace has), so it shouldn't turn
on silently. Turning it on and failing (most likely: not running elevated)
shows the reason in a dialog rather than doing nothing. Currently always
uses the same underlying kernel session Capture Trace's own Network/Disk
I/O profiles use (regardless of Windows version -- a from-scratch modern
per-provider mechanism exists in the code for Windows 11+ but is disabled
for now; its exact event format turned out to be undocumented anywhere
and didn't produce data in real testing, unlike the older mechanism, which
is what wpr.exe itself actually relies on), so the two can't run together
-- attempting either while the other is active shows a clear error
explaining why, rather than one silently stopping the other. A process
shows blank in these columns until it's actually done something
measurable (sent/received data, completed a disk I/O, opened a
connection) since the checkbox was turned on -- not an error, just nothing
to report yet; Connections specifically shows "0" rather than blank once
something else about the process has been observed, since a confirmed
zero is different information from "nothing known yet". If the columns
stay blank even after that, a diagnostic log
(<per-user config dir>/netio-debug.jsonl) records the raw shape of every
distinct kind of event actually captured, for troubleshooting.

"Show Connections" (next to Show Handles/Show Threads) opens a live,
per-process window listing the selected process's currently open TCP
connections -- local/remote address:port and direction (Outbound: this
process called connect(); Inbound: this process accepted an incoming
connection). Needs "Show Network/Disk I/O" turned on, same session. Unlike
Show Handles/Show Threads (a one-off snapshot, re-taken only when you
select a different process), this window updates every sample tick while
open, since the underlying data is itself continuously live. Ordinary
browsing connections are often too brief to catch between sample ticks --
a sustained transfer (e.g. a large file download) gives a much clearer,
longer-lived view than everyday traffic does.

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
Network — there's no per-process network API to draw from. Click a row to
bring the main window forward with that process selected in the table.
"Averaged over" (Off / 10s / 30s / 1 min / 2 min) ranks and filters by each
process's average over that trailing window instead of just the latest
sample — useful for a process that's a heavy consumer overall but bursty
(e.g. spikes to 80% CPU for a second every few seconds), which a single
instantaneous sample can just as easily catch mid-lull as mid-spike. The
header shows which window (if any) is active, e.g. "Top CPU consumers (30s
avg)". A brand-new process with no history yet still shows using its
instantaneous value rather than being hidden.

SYSTEM INFO WINDOW:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
View menu / system tray ("System Info") shows a point-in-time summary:
computer name and OS/platform/version/architecture, the computer's
model/vendor/serial and BIOS vendor/version/date (via
github.com/jaypipes/ghw -- WMI on Windows, /sys/class/dmi on Linux,
diskutil on macOS; blank rather than a guess wherever the platform or
this particular machine doesn't populate that data), CPU model/cores/
logical processors/speed, total memory, physical disks (model/vendor/size/
type -- HDD vs. SSD where the platform can tell, also via ghw) as a
separate section from the mounted disk volumes with size (the same
distinction Disk R/W's own Help entry makes: a physical disk vs. a
filesystem mounted on it), and network adapters with type (wired/
wireless), connected status, and WiFi signal strength where applicable.
Adapter type is a name-pattern heuristic, not guaranteed accurate for
unusual adapter names. "Copy to Clipboard" grabs the whole summary as
plain text. The window opens right away with a "Collecting…" message while
gathering runs in the background — on macOS this can take 10+ seconds
(WiFi signal strength there needs a slow system tool), so the delay is
expected, not a hang.

WINDOW MANAGEMENT:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Hovering the system tray icon (Windows, macOS) shows the app name as a
tooltip, same as most other tray icons -- "Administrator: ..." too, on an
elevated instance, matching every window's title bar under the same
convention.
Hide All / Show All (Window menu or system tray) hides the main window and
every other currently-open window together (About/Help/Update, System
Info, Resource Details, Interference Watch, a Children drill-down, a
Threads window, a Handles window), then brings back exactly that same set
later. Alt+H is a boss-key hotkey for Hide All — there's no matching Show
hotkey by design; reveal via the Window menu or tray instead.
Only one instance of this app runs at a time — launching a second copy
shows a small "already running" window with a Quit button instead. On
Windows, one exception: a second copy at a *different* elevation level
(one normal + one elevated) is allowed, specifically so Capture Trace's
admin-rights requirement doesn't force fully quitting and relaunching every
time. Two at the same elevation level still isn't allowed. An elevated
instance's title bar (every window, not just the main one) is prefixed
"Administrator:", the same convention Windows' own elevated console windows
use, so it's always visible at a glance which one you're looking at. The
"already running" window is its own separate process with no connection to
whichever instance(s) it's complaining about, so closing those doesn't
close it directly -- it notices on its own within a couple of seconds
(polling, not real IPC) and closes itself once the instance it was
blocking is no longer there, rather than sitting around as a leftover.

SMART FEATURES:
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
✨ Tooltips: hover any column header, button, checkbox, or select to see
   what it does — including the "⚠" Interference Watch column, which isn't
   obvious from the glyph alone.
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
  is scoped accordingly for now. Capture Trace (Windows-only) can record
  the raw ETW data for per-process network/disk/file activity to a .etl
  for analysis in WPA, but nothing from a capture feeds back into this
  app's own live view -- that would need a real-time ETW consumer, a much
  bigger separate effort, not implemented.
• No real "Private" memory figure on macOS (see PROCESS TABLE above).
• Check Signature and Capture Trace are both Windows-only (Authenticode/
  WinVerifyTrust, and wpr.exe/ETW respectively) — macOS has a Check
  Signature equivalent (codesign) but it isn't implemented yet.
• Capture Trace needs Administrator rights (confirmed by testing — all
  four of its profiles are kernel-level) and there's no in-app elevation
  prompt yet — relaunch ProcessMiner as Administrator first.
• No built-in ETW trace viewer/analyzer, and none planned: Windows
  Performance Analyzer (WPA) already does this well, and everyone on
  Windows already has free access to it (Microsoft Store, or the Windows
  ADK's "Windows Performance Toolkit" component) — matching even a
  fraction of its depth here would be a second product, not a
  complementary feature to a live process monitor.
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
