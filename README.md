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
  Memory, Disk R, Disk W, Private, Handles, Notable; click a header to sort,
  click again to reverse; drag a column boundary to resize. Long process
  names are ellipsized to fit the column instead of overflowing into the
  next one. Memory shows actual physical memory in use (RSS), the same
  figure Mem% is computed from, on all three platforms — in the
  Parent-processes view both show the combined total across the parent and
  every descendant, same as CPU%. Disk R/W show live per-process
  read/write KB/s; Private shows real
  Private Bytes on Windows, an RSS-minus-shared-pages approximation on
  Linux, and N/A on macOS (see **Known limitations**). Handles shows open
  handles on Windows, open file descriptors on macOS/Linux — genuinely
  available on all three platforms (a single cheap syscall everywhere) —
  and a count that climbs steadily and never comes back down is a classic
  sign of a handle/fd leak. Either Private or Handles reads N/A for
  a process you don't own, same as any other permission-restricted field.
  Notable (Windows) labels a row as recognized security software, or flags
  a process-masquerading mismatch (e.g. a fake svchost.exe not actually
  launched by services.exe) — blank for the overwhelming majority of rows.
- **Detail pane** for the selected process — user, live status, start time,
  full command line, ancestry (parent chain), and a scrollable list of child
  processes. Click a child to jump straight to it in the main table (clearing
  the name filter first if needed so it's not hidden). Only the selected
  process's detail is fetched on demand — see **Lightweight by design**.
- **Thread inspection** ("Show Threads") — a one-off snapshot of a process's
  threads. On Windows: TID, state, CPU time, and each thread's start address
  resolved against the process's own loaded modules — flagged "UNBACKED
  (possible injection)" if it falls outside all of them, the classic sign of
  code injection. Useful for verifying AV/security-software exclusions are
  actually configured, not just trusting that they are. macOS/Linux show a
  thread count only for now — see **Known limitations**. Only one Threads
  window is ever open — selecting a different process elsewhere while it's
  open updates it in place rather than opening a second one.
- **Interference Watch** — a continuous, more accessible layer on top of
  Thread Inspection: watch one or a few chosen processes (or every process
  launched from a designated directory) and get an alert — a "⚠" that stays
  lit for as long as the condition persists, plus a logged event — the
  moment any of three signs of interference appears on one of them, not
  just in a one-off snapshot: a new UNBACKED thread (reflective injection,
  malware's usual technique for staying off the module list), a new module
  loading into the process (the technique legitimate AV/EDR hooking
  actually uses instead, since it wants its DLL visible), or a thread's
  stack containing a pointer into a third-party module — a coarse
  approximation of manually "thread stacking" in Process Explorer, and
  unlike the other two, not relative to a baseline: it can find evidence of
  a hook that was already there before you started watching, right on the
  first check, since it scans a thread's whole stack region rather than
  just what changed. A hit against a short, best-effort known-vendor list
  gets a friendly label; an unmatched one is still reported, just without
  one. Deliberately not "watch everything": AV/security software should be
  scanning regardless, this is for verifying specific exclusions. Adding a
  watch opens (or focuses) the Interference Watch window itself, so results
  are never more than one click away; the View/Process menu and tray's
  "Check for Interference" opens the same window any other time. It lists
  what's being watched — each with an at-a-glance "✓"/"⚠"/"🛑" status — and
  the event log (same icons, same meaning), with Copy to Clipboard and
  Clear. All three signals are Windows-only — see **Known limitations**.
- **Check Signature** — a different angle on the same trust-but-verify idea,
  aimed at the executable file itself rather than its runtime behavior:
  select a process and click "Check Signature" for a one-off Authenticode
  check of its on-disk .exe (Windows-only). Reports ✓ signed and trusted, ⚠
  not signed at all (not necessarily malicious, but worth a second look), or
  🛑 signed with a problem (hash mismatch, untrusted/self-signed or expired
  certificate). Also checks Windows' catalog-signing mechanism, not just an
  embedded signature — most System32 binaries are catalog-signed rather
  than individually signed, so checking only for an embedded one would flag
  a huge share of stock Windows as "unsigned." No revocation check (would
  mean a network fetch per check) — a structural check, not a live verdict.
- **Resizable, persisted layout** — drag the divider between the process table
  and the detail pane, and between the detail info and its children list; both
  positions are remembered across launches, along with the main window size.
- **"Parent processes only" view** — declutters the table down to processes
  worth drilling into (anything with children, or with no visible parent of
  its own); each parent row shows the combined CPU%/Mem% across it and every
  descendant, Task Manager-style grouped totals, not just its own usage.
  Click one to open a single "Children" window — sortable columns, a name
  filter, and End Process, same as the main table — showing live,
  individual figures for just its direct children.
- **Resource Details window** — click any top-strip mini graph, or use the
  View menu / tray, to open a bigger, single-resource view, Task Manager-style
  (a resizable list to switch between CPU/Memory/Disk/Network, one large graph
  for whichever is selected, with value/time-axis gridlines). All four keep
  recording history in the background, so switching between them never shows
  a blank graph. A Top Consumers panel shows which processes are actually
  driving CPU/Memory/Disk (name + that one metric), filtered by an adjustable
  threshold rather than a fixed "top N" — not available for Network (no
  per-process network API exists). Click a row to jump straight to it in the
  main window's process table, same as clicking a child in the detail pane.
  An "Averaged over" select (Off / 10s / 30s / 1 min / 2 min) ranks and
  filters by each process's average over that trailing window instead of
  just the latest sample, for catching a bursty-but-heavy consumer a single
  instantaneous sample could just as easily catch mid-lull as mid-spike.
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
  against Activity Monitor's), plus **"Top CPU" / "Top Mem" / "Top Memory" /
  "Top Disk" consumer filters** (Off / ≥1% / ≥5% / ≥10% / ≥25% for CPU/Mem;
  Off / ≥100 MB / ≥500 MB / ≥1 GB / ≥4 GB for Memory — an absolute-size
  complement to Top Mem's percentage, since 5% means something very
  different on a 16GB laptop than a 128GB workstation; Off / ≥10 / ≥100 /
  ≥500 / ≥1000 KB/s for Disk — same tiers as Resource Details' own Top
  Consumers panel), independently adjustable and combined via OR (a process
  shows if it clears any one) to instantly narrow a busy process list down
  to whatever's actually using resources — a combination not offered out
  of the box by any of the platform-native tools this app draws on.
- **End Process** with a confirmation dialog before anything is killed.
- **Adjustable sample interval** (1s/2s/5s/10s) for the process list, plus a
  manual "Refresh Now".
- **Hide All / Show All windows**, with an **Alt+H** boss-key hotkey (mirrored
  in the Window menu and system tray) to instantly hide the main window and
  any open About/Help/Update windows together, and bring back exactly that
  same set later.
- **Single-instance enforcement** — launching a second copy shows a small
  "already running" window with a Quit button rather than opening a second
  full instance (no cross-process IPC, no bringing the first instance's
  window to the foreground — deliberately simple).
- **Tooltips** on column headers, buttons, checkboxes, and selects throughout
  the app (via [dweymouth/fyne-tooltip](https://github.com/dweymouth/fyne-tooltip),
  since Fyne itself has no built-in tooltip support yet) — hover to see what
  something does, including the "⚠" Interference Watch column.
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
- Thread inspection's start-address resolution ("UNBACKED (possible
  injection)") is Windows-only — macOS (SIP blocks `task_for_pid` for a
  third-party app without a special entitlement) and Linux (needs root or
  `CAP_SYS_PTRACE`) can't read another process's memory the way Windows
  allows without elevation. It also detects *injected* code, not an
  AV/EDR-style *hook* patched into an otherwise-legitimate module like
  `ntdll.dll` — that's a separate, bigger piece of work, not implemented.
  Interference Watch inherits the same Windows-only constraint — the watch
  list can still be built on macOS/Linux, but nothing will ever be flagged
  there. It also only covers *code injection* into a watched process, not
  the other common meaning of "AV interference" (synchronous file-scan
  latency from a minifilter driver), which would need a different,
  ETW-based mechanism.
- Check Signature is Windows-only (Authenticode/WinVerifyTrust) — macOS has
  an equivalent (codesign/Security.framework) but it isn't implemented yet.
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
