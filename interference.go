package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// interferenceWatcher tracks a small, user-chosen set of processes -- by
// PID, or by "any process launched from this directory" -- and looks for
// newly appearing UNBACKED threads on them: code injection happening *while*
// being watched, not merely having happened at some point in the past (a
// thread that was already unbacked before watching started is recorded as
// the baseline and ignored, not reported).
//
// Deliberately opt-in rather than "watch everything": checking a PID costs a
// Toolhelp32 module snapshot on top of the one shared system-wide thread
// query per cycle (see extractThreadSummary) -- cheap for a handful of
// watched targets, not for hundreds. This mirrors the user's own framing:
// "all processes would be too much, and AV should be scanning anyway."
//
// Every method here is only ever called from the main goroutine (procview.go
// drives check() from recompute(), itself only ever called from an
// already-fyne.Do-wrapped snapshot callback or a direct widget callback), so
// no mutex is needed -- same reasoning as procViewState's own doc comment.
// Still true even with the 4th signal (EventAVFileScan): its data arrives
// from a genuinely separate goroutine (the ETW consumer callback in
// avmonitor_windows.go), but that goroutine never touches this struct
// directly -- it only writes to globalAVScanRelay (avmonitor.go), a small,
// separately-synchronized handoff point that check() drains on the main
// goroutine, same as everything else here.
type interferenceWatcher struct {
	pids map[int32]string // explicitly watched PID -> process name at watch time (for display; exited processes may not resolve a name any other way)
	dirs []string         // watched directories, normalized (trailing separator) -- see normalizeWatchDir

	exeCache map[int32]string         // pid -> resolved executable path, resolved at most once per pid's lifetime
	baseline map[int32]map[int32]bool // pid -> set of TIDs already known unbacked (baseline + already-logged) -- new unbacked TIDs not in this set are events

	// moduleBaseline backs the second detection path (see InterferenceEvent's
	// EventNewModule): pid -> set of module names already known loaded
	// (baseline + already-logged). A module that was already loaded before
	// watching started is the baseline, not reported -- same "newly
	// appeared while watching" semantics as the thread baseline above,
	// deliberately, so results from both signals read consistently.
	moduleBaseline map[int32]map[string]bool

	// flaggedModules is the subset of each pid's moduleBaseline that were
	// logged as new (not baseline) and are still loaded -- unlike a thread,
	// which checkUnbackedThreads re-detects as unbacked every cycle for as
	// long as it's alive, a loaded module doesn't get "re-detected"; without
	// tracking this separately, the "⚠" indicator would flash true for
	// exactly one sample tick (the cycle it loaded) and then go dark even
	// though the module -- and the interference it represents -- is still
	// there. Cleared per-module when it unloads (see checkNewModules).
	flaggedModules map[int32]map[string]bool

	// flaggedStackHits is the third detection path's equivalent of
	// flaggedModules: pid -> set of foreign module names already logged
	// from a thread-stack scan and still loaded. No baseline concept here
	// at all (unlike the other two) -- see checkStackForeignModules and
	// EventForeignStackModule's doc comment for why a hit is reported even
	// on the very first check.
	flaggedStackHits map[int32]map[string]bool

	events []InterferenceEvent
}

func newInterferenceWatcher() *interferenceWatcher {
	return &interferenceWatcher{
		pids:             make(map[int32]string),
		exeCache:         make(map[int32]string),
		baseline:         make(map[int32]map[int32]bool),
		moduleBaseline:   make(map[int32]map[string]bool),
		flaggedModules:   make(map[int32]map[string]bool),
		flaggedStackHits: make(map[int32]map[string]bool),
	}
}

func normalizeWatchDir(dir string) string {
	dir = filepath.Clean(dir)
	if !strings.HasSuffix(dir, string(filepath.Separator)) {
		dir += string(filepath.Separator)
	}
	return dir
}

// watchPID adds pid to the explicit watch list. A fresh baseline is taken on
// the next check() -- any thread already unbacked at that point is treated
// as pre-existing, not as a newly detected event.
func (w *interferenceWatcher) watchPID(pid int32, name string) {
	w.pids[pid] = name
	delete(w.baseline, pid)
	delete(w.moduleBaseline, pid)
	delete(w.flaggedModules, pid)
	delete(w.flaggedStackHits, pid)
}

func (w *interferenceWatcher) unwatchPID(pid int32) {
	delete(w.pids, pid)
	delete(w.baseline, pid)
	delete(w.moduleBaseline, pid)
	delete(w.flaggedModules, pid)
	delete(w.flaggedStackHits, pid)
	delete(w.exeCache, pid)
}

func (w *interferenceWatcher) isWatchingPID(pid int32) bool {
	_, ok := w.pids[pid]
	return ok
}

func (w *interferenceWatcher) watchDir(dir string) {
	norm := normalizeWatchDir(dir)
	for _, d := range w.dirs {
		if d == norm {
			return // already watched
		}
	}
	w.dirs = append(w.dirs, norm)
}

func (w *interferenceWatcher) unwatchDir(dir string) {
	norm := normalizeWatchDir(dir)
	out := w.dirs[:0]
	for _, d := range w.dirs {
		if d != norm {
			out = append(out, d)
		}
	}
	w.dirs = out
}

func (w *interferenceWatcher) hasWatches() bool {
	return len(w.pids) > 0 || len(w.dirs) > 0
}

func (w *interferenceWatcher) clearEvents() { w.events = nil }

// activeTargets returns every currently-running PID this cycle's watch set
// covers: explicit PIDs still alive, plus any process whose resolved
// executable path falls under a watched directory. Each PID's executable
// path is resolved at most once and cached (see exeCache) -- directory
// matching would otherwise mean fetching Exe() for every running process on
// every tick, the same per-tick-cost mistake CLAUDE.md already documents for
// gopsutil's Status() on macOS.
func (w *interferenceWatcher) activeTargets(byPID map[int32]ProcInfo) map[int32]bool {
	targets := make(map[int32]bool)

	for pid := range w.pids {
		if _, alive := byPID[pid]; alive {
			targets[pid] = true
		}
	}

	if len(w.dirs) > 0 {
		for pid := range byPID {
			exe, cached := w.exeCache[pid]
			if !cached {
				exe = resolveProcessExe(pid)
				w.exeCache[pid] = exe
			}
			if exe == "" {
				continue
			}
			for _, d := range w.dirs {
				if strings.HasPrefix(exe, d) {
					targets[pid] = true
					break
				}
			}
		}
	}

	return targets
}

// prune drops cached/baseline state for PIDs no longer present in the
// current snapshot. Without this, a later-reused PID could wrongly inherit
// an unrelated earlier process's baseline/exe-path cache entry.
func (w *interferenceWatcher) prune(byPID map[int32]ProcInfo) {
	for pid := range w.pids {
		if _, alive := byPID[pid]; !alive {
			delete(w.pids, pid)
		}
	}
	for pid := range w.exeCache {
		if _, alive := byPID[pid]; !alive {
			delete(w.exeCache, pid)
		}
	}
	for pid := range w.baseline {
		if _, alive := byPID[pid]; !alive {
			delete(w.baseline, pid)
		}
	}
	for pid := range w.moduleBaseline {
		if _, alive := byPID[pid]; !alive {
			delete(w.moduleBaseline, pid)
		}
	}
	for pid := range w.flaggedModules {
		if _, alive := byPID[pid]; !alive {
			delete(w.flaggedModules, pid)
		}
	}
	for pid := range w.flaggedStackHits {
		if _, alive := byPID[pid]; !alive {
			delete(w.flaggedStackHits, pid)
		}
	}
}

// check runs one watch cycle and returns the set of PIDs currently showing
// at least one unbacked thread (for the process table's alert indicator --
// see procview.go's sortAlert column). It also appends an InterferenceEvent
// for every unbacked thread that's new since the PID started being watched.
//
// A no-op (and free) call when there's nothing to watch, or on a platform
// that can't resolve start addresses at all (see threadStartAddressSupported
// in threads_windows.go/threads_other.go) -- the whole feature is inert
// there, by design, not silently wrong.
func (w *interferenceWatcher) check(byPID map[int32]ProcInfo) map[int32]bool {
	flagged := make(map[int32]bool)
	w.prune(byPID)

	if !w.hasWatches() {
		return flagged
	}

	targets := w.activeTargets(byPID)
	if len(targets) == 0 {
		return flagged
	}

	// Fourth detection path: Defender's AMFilter minifilter reporting on a
	// watched process's file activity or trust status -- genuinely
	// independent of threadStartAddressSupported (it needs Administrator +
	// a live ETW consumer, not thread/module introspection; see
	// avmonitor_windows.go), so it has to run before that gate below, not
	// after -- an earlier version of this function checked
	// threadStartAddressSupported before this block even though this
	// signal doesn't depend on it at all, which would have skipped it
	// entirely on a hypothetical platform where that flag is false but AV
	// monitoring isn't -- caught during real-world testing, not by
	// inspection. Inert by construction wherever startAVMonitor never
	// actually got a session running (non-Windows, or not elevated) --
	// there's simply never anything sitting in the relay to drain.
	globalAVScanRelay.setWatchedPIDs(targets)
	for _, sighting := range globalAVScanRelay.drain() {
		if w.checkAVFileScan(sighting, byPID[sighting.pid].Name) {
			flagged[sighting.pid] = true
		}
	}

	if !threadStartAddressSupported {
		return flagged
	}

	// bufErr is checked per-target below, not returned early here: a
	// failure only affects checkUnbackedThreads (which needs buf).
	// checkNewModules/checkStackForeignModules don't use it at all and
	// must still run regardless -- the detection paths use independent
	// APIs and one's transient failure should never silently skip another.
	// (Found by testing: two consecutive live DLL-injection tests went
	// undetected despite processModules itself proving 100% reliable in
	// isolation -- an early-return here was silently skipping
	// checkNewModules whenever querySystemProcessInformation's separate,
	// unrelated system-wide query happened to fail that cycle.)
	buf, bufErr := querySystemProcessInformation()

	for pid := range targets {
		name := byPID[pid].Name

		// Fetched once per target per cycle and shared across all three
		// checks below, rather than each independently re-fetching --
		// modules in particular was previously fetched twice (once inside
		// extractThreadSummary for start-address resolution, once again in
		// checkNewModules); now it's fetched once here for the latter two,
		// which both need the plain list rather than start-address
		// resolution against it.
		modules, modErr := processModules(pid)

		var summary ThreadSummary
		summaryErr := bufErr
		if bufErr == nil {
			summary, summaryErr = extractThreadSummary(buf, pid)
		}

		if summaryErr == nil && w.checkUnbackedThreads(pid, name, summary) {
			flagged[pid] = true
		}
		if modErr == nil && w.checkNewModules(pid, name, modules) {
			flagged[pid] = true
		}
		if modErr == nil && summaryErr == nil && w.checkStackForeignModules(pid, name, summary, modules) {
			flagged[pid] = true
		}
	}

	return flagged
}

// checkUnbackedThreads implements the first detection path: a thread with a
// start address outside every loaded module ("UNBACKED") that's new since
// pid started being watched -- the signature of *reflective* injection
// (raw shellcode, no module ever loaded). Returns whether pid currently has
// at least one unbacked thread (for the caller's flagged set), regardless
// of whether any of them were newly logged this cycle.
func (w *interferenceWatcher) checkUnbackedThreads(pid int32, name string, summary ThreadSummary) bool {
	if summary.ModulesUnresolved != "" {
		// Couldn't verify this pid this cycle (see processModules --
		// typically a transient Toolhelp32 hiccup against a busy process)
		// -- every StartAddr came back "" this time, which is NOT the same
		// as "checked and found nothing unbacked". Skip rather than treat
		// an inconclusive check as a clean one; the next cycle tries again.
		return false
	}

	known, seenBefore := w.baseline[pid]
	if !seenBefore {
		known = make(map[int32]bool)
		w.baseline[pid] = known
	}

	// Drop TIDs no longer present so a reused TID number is treated as new
	// again rather than silently inheriting an old thread's history.
	present := make(map[int32]bool, len(summary.Threads))
	for _, t := range summary.Threads {
		present[t.TID] = true
	}
	for tid := range known {
		if !present[tid] {
			delete(known, tid)
		}
	}

	unbackedNow := false
	for _, t := range summary.Threads {
		if t.StartAddr != unbackedStartAddr {
			continue
		}
		unbackedNow = true
		if known[t.TID] {
			continue
		}
		known[t.TID] = true
		if seenBefore { // don't log the pre-existing baseline itself
			w.events = append(w.events, InterferenceEvent{
				When: time.Now(), PID: pid, ProcessName: name, Kind: EventUnbackedThread,
				TID: t.TID, StartAddr: t.StartAddr,
			})
		}
	}
	return unbackedNow
}

// checkNewModules implements the second detection path: a module (DLL)
// appearing in pid's loaded-module list that wasn't there when watching
// started -- the signature of LoadLibrary-based DLL injection, the
// technique legitimate AV/EDR hooking actually uses (unlike reflective
// injection, it wants its DLL visible). Returns whether pid currently has
// at least one such flagged module still loaded -- like
// checkUnbackedThreads, this stays true for as long as the condition
// persists, not just on the one cycle it was first detected: a loaded
// module doesn't go away on its own, so the "⚠" indicator should track
// that (confirmed by testing -- an earlier version returned true only on
// the detecting cycle, so the indicator flashed on for one sample tick and
// then went dark even though the module, and the interference it
// represents, was still there).
//
// modules is fetched once per target per cycle by the caller (check()) and
// shared with checkStackForeignModules, rather than each independently
// re-fetching its own Toolhelp32 snapshot.
func (w *interferenceWatcher) checkNewModules(pid int32, name string, modules []procModule) bool {
	known, seenBefore := w.moduleBaseline[pid]
	if !seenBefore {
		known = make(map[string]bool)
		w.moduleBaseline[pid] = known
	}
	flaggedSet, ok := w.flaggedModules[pid]
	if !ok {
		flaggedSet = make(map[string]bool)
		w.flaggedModules[pid] = flaggedSet
	}

	present := make(map[string]bool, len(modules))
	for _, m := range modules {
		present[m.name] = true
	}
	for modName := range known {
		if !present[modName] {
			delete(known, modName) // unloaded -- if it (or a same-named one) loads again later, treat as new again
			delete(flaggedSet, modName)
		}
	}

	for _, m := range modules {
		if known[m.name] {
			continue
		}
		known[m.name] = true
		if seenBefore { // don't log the pre-existing baseline itself
			w.events = append(w.events, InterferenceEvent{
				When: time.Now(), PID: pid, ProcessName: name, Kind: EventNewModule,
				ModuleName: m.name, KnownVendor: matchKnownSecurityModule(m.name),
			})
			flaggedSet[m.name] = true
		}
	}
	return len(flaggedSet) > 0
}

// checkStackForeignModules implements the third detection path: an
// approximation of manual "thread stacking" -- scanning each of pid's
// threads' stack memory for a pointer into a module that isn't part of
// Windows itself or the process's own files, i.e. some other vendor's code
// is (or recently was) somewhere in that thread's call path. See
// EventForeignStackModule's doc comment for why this deliberately has no
// baseline: a hit is reported the first time it's ever seen for pid, even
// on the very first check, because the technique is inherently about
// finding evidence of something that may have happened before watching
// started, not a delta.
//
// modules is shared with checkNewModules (fetched once per target per
// cycle by check()). summary supplies the TIDs to scan -- reusing the same
// thread list checkUnbackedThreads already has, not a separate
// enumeration.
func (w *interferenceWatcher) checkStackForeignModules(pid int32, name string, summary ThreadSummary, modules []procModule) bool {
	ownDir := w.ownExeDirFor(pid)
	winDir := windowsDirectory()

	flaggedSet, ok := w.flaggedStackHits[pid]
	if !ok {
		flaggedSet = make(map[string]bool)
		w.flaggedStackHits[pid] = flaggedSet
	}

	// A module that's since unloaded can't still be "on" a live thread's
	// stack in any way that matters going forward -- if it (or a
	// same-named one) loads and gets caught again later, treat it as a
	// fresh finding rather than silently staying flagged forever.
	present := make(map[string]bool, len(modules))
	for _, m := range modules {
		present[m.name] = true
	}
	for modName := range flaggedSet {
		if !present[modName] {
			delete(flaggedSet, modName)
		}
	}

	for _, t := range summary.Threads {
		hits, err := scanThreadStackForModules(pid, t.TID, modules)
		if err != nil {
			continue // this thread's stack couldn't be read this cycle -- try the next one/next cycle, not fatal to the others
		}
		for modName := range hits {
			if flaggedSet[modName] {
				continue // already logged and still loaded
			}
			if classifyModule(modName, modules, ownDir, winDir) != moduleForeign {
				continue // the process's own module or a Windows system one -- not interesting
			}
			flaggedSet[modName] = true
			w.events = append(w.events, InterferenceEvent{
				When: time.Now(), PID: pid, ProcessName: name, Kind: EventForeignStackModule,
				TID: t.TID, ModuleName: modName, KnownVendor: matchKnownSecurityModule(modName),
			})
		}
	}

	return len(flaggedSet) > 0
}

// checkAVFileScan implements the fourth detection path: Windows Defender's
// own AMFilter minifilter reporting on pid -- either a real file-scan
// interception (EventAVFileScan, the *other* meaning of "AV interference"
// the other three signals can't see since they only look at what's
// happening *inside* the watched process itself) or, when Defender fast-
// tracked pid through its trusted-process path instead of ever scanning a
// specific file (confirmed with notepad.exe -- see EventAVTrustEval's doc
// comment), a trust-evaluation registration. See avmonitor_windows.go for
// how sightings actually get here (an async ETW consumer goroutine,
// relayed through globalAVScanRelay -- this method itself still only ever
// runs on the main goroutine, from check()).
//
// Unlike the other three signals, there's no baseline or "still ongoing"
// state to track: both of these are momentary events, not a persistent
// condition like a loaded module or a live thread, so every sighting is
// logged unconditionally and the returned bool only reflects *this* cycle
// -- naturally stops being true the next tick if nothing new arrived,
// rather than staying sticky.
func (w *interferenceWatcher) checkAVFileScan(sighting avScanSighting, name string) bool {
	kind := EventAVFileScan
	if sighting.trustEval {
		kind = EventAVTrustEval
	}
	w.events = append(w.events, InterferenceEvent{
		When: time.Now(), PID: sighting.pid, ProcessName: name, Kind: kind,
		FilePath: sighting.fileName,
	})
	return true
}

// ownExeDirFor returns the directory containing pid's own executable
// (cached via exeCache, resolved at most once per pid's lifetime -- the
// same cache activeTargets uses for directory-watch matching, now also
// used here for module classification).
func (w *interferenceWatcher) ownExeDirFor(pid int32) string {
	exe, cached := w.exeCache[pid]
	if !cached {
		exe = resolveProcessExe(pid)
		w.exeCache[pid] = exe
	}
	if exe == "" {
		return ""
	}
	return filepath.Dir(exe)
}

// windowsDirectory returns the Windows installation directory (e.g.
// C:\Windows), for classifying a module as a system file. Reads the
// standard SystemRoot environment variable rather than calling
// GetWindowsDirectory -- always set on Windows, and avoids one more
// syscall wrapper for something this stable.
func windowsDirectory() string {
	if dir := os.Getenv("SystemRoot"); dir != "" {
		return dir
	}
	return `C:\Windows` // fallback -- SystemRoot is unset only in unusual/misconfigured environments
}

// moduleClass is classifyModule's verdict for one module.
type moduleClass int

const (
	moduleOwn     moduleClass = iota // inside the watched process's own executable's directory
	moduleSystem                     // inside the Windows directory (System32, SysWOW64, WinSxS, ...)
	moduleForeign                    // neither -- some other vendor's code
)

// classifyModule looks up modName in modules (by name, matching what
// scanThreadStackForModules reports) and classifies its on-disk directory
// against ownDir/winDir. A module classifyModule can't find at all (should
// not normally happen -- it came from the same modules list) is treated as
// foreign rather than silently ignored.
func classifyModule(modName string, modules []procModule, ownDir, winDir string) moduleClass {
	for _, m := range modules {
		if m.name != modName {
			continue
		}
		// A prefix match, not exact equality: many installers (Chrome
		// among them) put the real DLL in a versioned subdirectory below
		// the exe's own folder, e.g. ...\Application\chrome.exe alongside
		// ...\Application\138.0.7204.100\chrome.dll -- exact-equality
		// against just the exe's immediate directory misclassified
		// chrome.dll/chrome_elf.dll as "foreign" (caught via the debug
		// dump before shipping, not by a user report). Trailing separator
		// via normalizeWatchDir avoids a bare prefix like "...\Chrome"
		// wrongly matching an unrelated "...\ChromeOther" directory.
		dir := normalizeWatchDir(filepath.Dir(m.path))
		if ownDir != "" && strings.HasPrefix(strings.ToLower(dir), strings.ToLower(normalizeWatchDir(ownDir))) {
			return moduleOwn
		}
		if winDir != "" && strings.HasPrefix(strings.ToLower(dir), strings.ToLower(normalizeWatchDir(winDir))) {
			return moduleSystem
		}
		return moduleForeign
	}
	return moduleForeign
}

// knownSecurityModules is a best-effort, deliberately incomplete list of
// module/process name fragments publicly associated with common AV/EDR
// products. Used two ways: attaching a friendly label when a foreign-stack
// hit matches one (an unmatched foreign hit is still reported, see
// InterferenceEvent.KnownVendor, just without a vendor name attached), and
// by procview.go's Notable column to recognize which row in the main table
// actually *is* the antivirus/EDR agent -- a real gap for anyone who isn't
// already familiar with what to look for. Security vendors rename,
// version, and sometimes randomize their module/process names over time,
// so this list will drift out of date -- extend it yourself as you
// encounter real names in the field; matching is a plain case-insensitive
// substring check, not exhaustive fingerprinting.
var knownSecurityModules = map[string]string{
	"mfehidk":        "McAfee/Trellix",
	"mfefire":        "McAfee/Trellix",
	"mfeaack":        "McAfee/Trellix",
	"mfencrdc":       "McAfee/Trellix",
	"mfewfpk":        "McAfee/Trellix",
	"mcshield":       "McAfee/Trellix",
	"masvc":          "McAfee/Trellix",
	"sysfer":         "Symantec/Broadcom SEP",
	"ccsvchst":       "Symantec/Broadcom SEP",
	"symefa":         "Symantec/Broadcom SEP",
	"rtvscan":        "Symantec/Broadcom SEP",
	"csfalcon":       "CrowdStrike Falcon",
	"csagent":        "CrowdStrike Falcon",
	"csdevice":       "CrowdStrike Falcon",
	"sentinelone":    "SentinelOne",
	"sentinelagent":  "SentinelOne",
	"sentinelhelper": "SentinelOne",
	"carbonblack":    "VMware Carbon Black",
	"cbdefense":      "VMware Carbon Black",
	"cbstream":       "VMware Carbon Black",
	"savservice":     "Sophos",
	"sophosfs":       "Sophos",
	"hmpalert":       "Sophos",
	"tmlisten":       "Trend Micro",
	"ntrtscan":       "Trend Micro",
	"pccntmon":       "Trend Micro",
	"cylancesvc":     "Cylance/BlackBerry Protect",
	"cyoptics":       "Cylance/BlackBerry Protect",
	"cortex":         "Palo Alto Cortex XDR",
	"trapsd":         "Palo Alto Cortex XDR",
	"msmpeng":        "Microsoft Defender",
	"mpdefender":     "Microsoft Defender",
	"windefend":      "Microsoft Defender",
	// mpclient/mpoav found in the wild during this feature's own real-world
	// testing: a thread-stack scan hit against one of a watched Chrome
	// child process's threads, right as a download was being scanned --
	// MpOav.dll is literally Defender's on-access-scan module.
	"mpclient":    "Microsoft Defender",
	"mpoav":       "Microsoft Defender",
	"mbamservice": "Malwarebytes",
	"mbamtray":    "Malwarebytes",
	"ekrn":        "ESET",
	"egui":        "ESET",
	"avpsvc":      "Kaspersky",
	"kavfs":       "Kaspersky",
	"bdagent":     "Bitdefender",
	"vsserv":      "Bitdefender",
}

// matchKnownSecurityModule returns a friendly vendor label if modName
// matches knownSecurityModules, or "" if not -- "" means "unrecognized
// third-party module," not "definitely not security software."
func matchKnownSecurityModule(modName string) string {
	lower := strings.ToLower(modName)
	for pattern, vendor := range knownSecurityModules {
		if strings.Contains(lower, pattern) {
			return vendor
		}
	}
	return ""
}

// interferenceEventSeverity classifies e for display purposes only -- a
// hint for which icon/color to use, not a certified verdict:
//   - EventUnbackedThread is always "danger": reflective injection is the
//     technique malware uses specifically to avoid ever looking legitimate,
//     so there's no "known-good" case for it the way the other two kinds
//     have.
//   - EventNewModule/EventForeignStackModule are "warning" when the module
//     matched knownSecurityModules (a real security vendor's own code
//     showing up is exactly what Interference Watch exists to catch, but
//     it's attributable and expected in kind, if not necessarily in
//     degree), and "danger" when unmatched (unknown code is the case that
//     most needs a closer look).
//   - EventAVFileScan/EventAVTrustEval are always "warning," never
//     "danger": Defender scanning or evaluating a watched process is
//     expected, legitimate behavior in a way an unbacked thread never is
//     -- these signals exist to make that activity visible, not to
//     accuse it.
func interferenceEventSeverity(e InterferenceEvent) (icon string, isDanger bool) {
	if e.Kind == EventUnbackedThread {
		return "🛑", true
	}
	if e.Kind == EventAVFileScan || e.Kind == EventAVTrustEval {
		return "⚠", false
	}
	if e.KnownVendor != "" {
		return "⚠", false
	}
	return "🛑", true
}

// formatInterferenceEvent renders one event as a single line, for the
// Interference Watch window's event list and its Copy to Clipboard button --
// kept in one place so the two can never drift apart.
func formatInterferenceEvent(e InterferenceEvent) string {
	when := e.When.Format("2006-01-02 15:04:05")
	switch e.Kind {
	case EventNewModule:
		return fmt.Sprintf("%s — %s (PID %d): new module loaded: %s", when, e.ProcessName, e.PID, e.ModuleName)
	case EventForeignStackModule:
		if e.KnownVendor != "" {
			return fmt.Sprintf("%s — %s (PID %d), TID %d: possible %s module in thread stack: %s", when, e.ProcessName, e.PID, e.TID, e.KnownVendor, e.ModuleName)
		}
		return fmt.Sprintf("%s — %s (PID %d), TID %d: unrecognized third-party module in thread stack: %s (verify with Process Explorer/Procmon)", when, e.ProcessName, e.PID, e.TID, e.ModuleName)
	case EventAVFileScan:
		return fmt.Sprintf("%s — %s (PID %d): Windows Defender scanned a file this process opened: %s", when, e.ProcessName, e.PID, e.FilePath)
	case EventAVTrustEval:
		return fmt.Sprintf("%s — %s (PID %d): Windows Defender registered this process for trust evaluation (no specific file scan seen -- common for trusted/Microsoft-signed processes, which skip the full file-scan event path)", when, e.ProcessName, e.PID)
	default: // EventUnbackedThread
		return fmt.Sprintf("%s — %s (PID %d), TID %d: %s", when, e.ProcessName, e.PID, e.TID, e.StartAddr)
	}
}

// resolveProcessExe returns pid's on-disk executable path, or "" if it can't
// be read (exited, permission-denied, etc.) -- never fatal to the caller,
// same "N/A rather than error" convention as the rest of this app's
// per-process fields.
func resolveProcessExe(pid int32) string {
	p, err := process.NewProcess(pid)
	if err != nil {
		return ""
	}
	exe, err := p.Exe()
	if err != nil {
		return ""
	}
	return exe
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
