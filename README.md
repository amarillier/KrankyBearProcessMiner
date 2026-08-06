# KrankyBear ProcessMiner

A free, cross-platform system/process monitor for Windows, macOS, and Linux —
built with Go and the [Fyne](https://fyne.io/) GUI toolkit, in the spirit of
Activity Monitor, Task Manager, `top`, and Sysinternals Process Explorer.

Design philosophy aligns with Fyne: ease of use, solid functionality, steady bug
fixing and performance work.

## Why this instead of Activity Monitor / Task Manager / top / Process Explorer?

Each platform's native tool is genuinely good at what it does, and this project
owes real debts to each of them for feature ideas: Activity Monitor's live
graphs, Task Manager's sortable detail columns, `top`'s just-the-numbers speed,
Process Explorer's process tree and ancestry/children drill-down. The case for
reaching for this one instead: it's the exact same app, with the exact same
feature set, on Windows, macOS, and Linux — no relearning a different tool (and
a different set of column names, shortcuts, and quirks) every time you switch
machines or OSes. One familiar interface instead of four.

It's also built to stay out of the way of the very thing it's measuring: a
monitoring tool that itself burns a large, constant share of CPU defeats the
point. See **Lightweight by design** below.

## Features

- **Live system-wide sparklines** for CPU, Memory, Disk I/O, and Network,
  updated every second, with the current numeric values shown alongside each
  graph.
- **Sortable, resizable process table** — PID, Name, PPID, User, CPU%, Mem%,
  Disk R, Disk W, Private; click a header to sort, click again to reverse;
  drag a column boundary to resize. Long process names are ellipsized to fit
  the column instead of overflowing into the next one. Disk R/W show live
  per-process read/write KB/s; Private shows real Private Bytes on Windows,
  an RSS-minus-shared-pages approximation on Linux, and N/A on macOS (see
  **Known limitations**). Either reads N/A for a process you don't own, same
  as any other permission-restricted field.
- **Detail pane** for the selected process — user, live status, start time,
  full command line, ancestry (parent chain), and a scrollable list of child
  processes. Click a child to jump straight to it in the main table (clearing
  the name filter first if needed so it's not hidden). Only the selected
  process's detail is fetched on demand — see **Lightweight by design**.
- **Resizable, persisted layout** — drag the divider between the process table
  and the detail pane, and between the detail info and its children list; both
  positions are remembered across launches, along with the main window size.
- **"Parent processes only" view** — declutters the table down to processes
  worth drilling into (anything with children, or with no visible parent of
  its own); click one to open a single read-only "Children" window with live
  CPU%/Mem% for just its direct children, closer to Task Manager's own
  grouped-process view.
- **Resource Details window** — click any top-strip mini graph, or use the
  View menu / tray, to open a bigger, single-resource view, Task Manager-style
  (a resizable list to switch between CPU/Memory/Disk/Network, one large graph
  for whichever is selected, with value/time-axis gridlines). All four keep
  recording history in the background, so switching between them never shows
  a blank graph. A Top Consumers panel shows which processes are actually
  driving CPU/Memory/Disk (name + that one metric), filtered by an adjustable
  threshold rather than a fixed "top N" — not available for Network (no
  per-process network API exists).
- **System Info window** (View menu / tray) — a point-in-time summary:
  computer name and OS/platform/version/architecture, CPU model/cores/
  logical processors/speed, total memory, mounted disk volumes with size,
  and network adapters with type (wired/wireless), connected status, and
  WiFi signal strength where applicable, plus a "Copy to Clipboard" button.
  Opens immediately with a "Collecting…" placeholder while gathering runs
  in the background — this
  can take 10+ seconds on macOS specifically (see Known limitations).
- **Filter by name**, with an optional **regex mode** (e.g.
  `^(?i)(process|activity).*` to compare just this app's own processes
  against Activity Monitor's), plus a **"Top CPU" / "Top Mem" consumer
  filter** (Off / ≥1% / ≥5% / ≥10% / ≥25%, independently adjustable) to
  instantly narrow a busy process list down to whatever's actually using
  resources — a combination not offered out of the box by any of the
  platform-native tools this app draws on.
- **End Process** with a confirmation dialog before anything is killed.
- **Adjustable sample interval** (1s/2s/5s/10s) for the process list, plus a
  manual "Refresh Now".
- **Hide All / Show All windows**, with an **Alt+H** boss-key hotkey (mirrored
  in the Window menu and system tray) to instantly hide the main window and
  any open About/Help/Update windows together, and bring back exactly that
  same set later.
- Light / Dark / System theme, a system tray icon with the same actions as the
  main menu, and a throttled (once-per-day, silent-unless-found) update
  checker with a manual "Check for Updates" always available.

## Lightweight by design

Monitoring tools that constantly poll every process can end up burning more
CPU than what they're measuring. ProcessMiner samples the full process list
every 2 seconds (adjustable), but only reads the fields the table actually
displays for every row — cmdline, start time, and live status (the more
expensive fields, only ever shown in the detail pane) are fetched on demand
for just the one currently-selected process, not batched into the per-tick
loop. On macOS specifically, this avoids a real gopsutil trap: `Status()`
forks a `ps` subprocess per call, so fetching it for every process on every
tick meant hundreds of process forks a second. Making that on-demand-only
took this app's own steady-state CPU usage from over 30% down to roughly 9%
on a typical Mac.

## Cross-platform support

- **Linux**: GNOME, KDE, XFCE, Cinnamon, MATE, etc. on X11 or Wayland.
- **macOS**: 10.13 (High Sierra) or later.
- **Windows**: Windows 10 or later.

## Known limitations

- No per-process network usage (no platform offers a simple API for it —
  Windows' own Task Manager relies on ETW tracing for this) and no
  per-process GPU usage yet — the "Top CPU"/"Top Mem" consumer filter is
  scoped accordingly for now (Disk R/W are shown as columns but not yet a
  filterable threshold).
- No real "Private" memory figure on macOS — the closest gopsutil provides
  there is reserved virtual address space (often >1TB for a browser
  renderer), not actual private memory, so this app shows N/A rather than a
  misleading number.
- The System Info window's disk list is mounted filesystems (gopsutil's
  view), not raw physical disks — there's no simple cross-platform API for
  the latter. Network adapter type (wired/wireless) is a name-pattern
  heuristic, not guaranteed accurate for unusual adapter names.
- System Info gathering can take 10+ seconds on macOS: WiFi signal strength
  there shells out to `system_profiler`, which alone takes ~8s regardless
  of detail level, and there's no fast non-privileged alternative (`wdutil
  info` is near-instant but needs `sudo`; the legacy `airport` binary that
  used to be fast no longer exists on current macOS). The window shows a
  loading state rather than appearing frozen, but the wait itself is real.
- End Process is a hard kill; no graceful-terminate or elevation flow yet.

## Building & running

Requires Go and a Fyne-capable toolchain (CGo + OpenGL on desktop):

```
go run .
go build -o <app> .
```

Platform helpers: `compile-mac.sh`, `compile-win.sh`, `compile-linux.sh`, and
`package.sh` (`.deb`/`.rpm`, macOS `.pkg`).

## License

Free for personal, educational and commercial use, under the GNU GPL-3.0.

## Author

Allan Marillier

## Acknowledgments

- Built with [Fyne](https://fyne.io/) — an easy-to-use GUI toolkit for Go.
- Process/system sampling via [gopsutil](https://github.com/shirou/gopsutil).
- Linux WiFi signal strength via [mdlayher/wifi](https://github.com/mdlayher/wifi).
