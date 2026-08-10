package main

import "time"

// ThreadDetail is one OS thread within a process. Only populated on
// platforms that support resolving a thread's start address without
// elevated privileges -- currently Windows only (see threads_windows.go).
// On other platforms, ThreadSummary.Threads is left nil and only Count is
// set (see threads_other.go).
type ThreadDetail struct {
	TID        int32
	StartAddr  string // "module.dll+0x1234", "UNBACKED (possible injection)", or "" if unresolvable
	KernelTime time.Duration
	UserTime   time.Duration
	State      string
}

// ThreadSummary is the best-effort per-process thread info gatherable on
// this platform.
//
// Windows: a full per-thread list via NtQuerySystemInformation, including
// each thread's start address resolved against the process's loaded
// modules -- a thread starting outside any loaded module ("UNBACKED") is
// the classic indicator of code injection (reflective DLL injection,
// shellcode, etc.), the same signal Process Explorer/Process Hacker-style
// tools use. This does NOT itself detect AV/EDR *hooking* (patched bytes
// inside an otherwise-legitimate module like ntdll.dll) -- that needs
// comparing in-memory bytes against the on-disk DLL at known API entry
// points, a separate, larger piece of work (see ReleaseNotes.txt).
//
// macOS/Linux: just a thread count (gopsutil's NumThreads) for now.
// Resolving start addresses needs reading another process's memory, which
// needs an entitlement Apple doesn't grant ordinary third-party apps on
// macOS (SIP restricts task_for_pid), or root/CAP_SYS_PTRACE on Linux
// (Yama's ptrace_scope blocks it otherwise) -- a real platform gap, not
// something this app can code around.
type ThreadSummary struct {
	Count   int32
	Threads []ThreadDetail // nil unless the platform resolves per-thread detail

	// ModulesUnresolved is non-empty when Threads was read successfully but
	// the process's loaded-module list couldn't be -- every StartAddr in
	// Threads is "" this time (not a real "UNBACKED" claim, just unknown),
	// see threads_windows.go's processModules. Distinct from the function-
	// level error return (which means the process/thread list itself
	// couldn't be read) so a single transient module-snapshot hiccup
	// doesn't hide an otherwise-good thread list behind an error dialog,
	// and so interference.go's watcher can tell "verified clean this cycle"
	// apart from "couldn't check this cycle" instead of conflating them.
	ModulesUnresolved string
}

// gatherThreadSummary is implemented per-platform -- see
// threads_windows.go and threads_other.go.

// procModule is one loaded module's address range -- cross-platform data
// shape (no OS-specific fields), so interference.go's watcher can hold onto
// module names for its baseline/delta logic without needing a build-tagged
// type. processModules, which actually fills these in, is still
// Windows-only (threads_windows.go); threads_other.go's stub just never
// returns any.
type procModule struct {
	name       string // filename only, e.g. "ntdll.dll"
	path       string // full on-disk path, e.g. "C:\Windows\System32\ntdll.dll" -- used to classify a module as system/own/foreign (see interference.go's classifyModule)
	base, size uintptr
}

// InterferenceEventKind distinguishes what an InterferenceEvent detected --
// see interference.go's watcher for both detection paths.
type InterferenceEventKind int

const (
	// EventUnbackedThread: a thread with a start address outside every
	// loaded module ("UNBACKED") newly appeared -- the classic signature of
	// *reflective* injection (raw shellcode, no module ever loaded), the
	// technique malware uses specifically to avoid appearing in the module
	// list at all. See threads_windows.go's resolveStartAddress.
	EventUnbackedThread InterferenceEventKind = iota
	// EventNewModule: a new module (DLL) appeared in the process's loaded-
	// module list that wasn't there when watching started -- the signature
	// of LoadLibrary-based DLL injection, the technique legitimate AV/EDR
	// hooking actually uses (it wants its DLL visible, not hidden). A
	// reflective-injection thread is UNBACKED precisely because malware
	// skips this step; a normal security-vendor hook doesn't, so this is
	// the signal that actually fires for that case instead.
	EventNewModule
	// EventForeignStackModule: a thread's stack memory contains a pointer
	// into a module that isn't part of the watched process's own files or
	// Windows itself -- i.e. some other vendor's code is (or recently was)
	// somewhere in that thread's call path. Unlike the two signals above,
	// this is NOT baseline-relative: a hook installed before watching
	// started still shows up here, on the very first check, because it
	// scans the thread's *entire* committed stack region (not just live
	// call frames) for lingering evidence, not just what changed. This is
	// the closest approximation to the classic "thread stacking" technique
	// (walking live call stacks looking for a security vendor's module in
	// the call path) -- a coarser, address-scanning approximation of it,
	// not true call-stack unwinding, so treat a hit as "worth investigating
	// with Process Explorer/Procmon for confirmation," not proof on its
	// own -- see interference.go's classifyModule and knownSecurityModules.
	EventForeignStackModule
	// EventAVFileScan: Windows Defender's own AMFilter minifilter
	// intercepted a file pid opened -- the *other* meaning of "AV
	// interference" (synchronous file-scan latency from a security
	// product's filter driver), which the other three signals can't see at
	// all since they only look at what's happening *inside* the watched
	// process itself. Windows-only, and only live when running elevated
	// (creating the real-time ETW session needs Administrator rights,
	// confirmed empirically) -- see avmonitor_windows.go. Unlike the other
	// three, a file scan is a momentary event, not an ongoing condition, so
	// there's no baseline/persistence concept here -- see
	// interference.go's checkAVFileScan.
	EventAVFileScan
	// EventAVTrustEval: Defender's AMFilter registered pid for trust
	// evaluation ("AMFilter_TrustedProcess"/Reason=="create") -- a
	// complementary fallback to EventAVFileScan, added after real-world
	// testing found that EventAVFileScan never fires at all for a
	// well-known, Microsoft-signed "trusted" process (confirmed with
	// notepad.exe: zero AMFilter_FileScan events despite a completed file
	// save, while AMFilter_TrustedProcess fired reliably for other
	// processes in the same window) -- Defender appears to fast-track
	// trusted processes through a different, lighter-weight code path that
	// skips the full file-scan event entirely. This event carries no file
	// path at all (confirmed empirically -- the provider's own Path field
	// is always "NULL" for this task), so it's a categorically different
	// claim than EventAVFileScan: "Defender is aware of and evaluating
	// this process," not "Defender scanned this specific file." Also
	// unlike EventAVFileScan, the correlating PID lives in the event's
	// *payload* (a "Pid" property), not the event header's ProcessID
	// (which is the Defender component emitting it, e.g. MsMpEng.exe) --
	// see avmonitor_windows.go.
	EventAVTrustEval
)

// InterferenceEvent records one detected sign of interference on a process
// this app was actively watching -- see interference.go's watcher for the
// detection semantics, which differ between EventForeignStackModule and the
// other two kinds (see EventForeignStackModule's doc comment).
type InterferenceEvent struct {
	When        time.Time
	PID         int32
	ProcessName string
	Kind        InterferenceEventKind

	// TID/StartAddr are set for EventUnbackedThread; ModuleName for
	// EventNewModule and EventForeignStackModule; TID is also set for
	// EventForeignStackModule (which thread's stack the hit was found on).
	// KnownVendor is set only when ModuleName matched knownSecurityModules
	// -- empty means "unrecognized third-party module," not "definitely
	// not security software."
	TID         int32
	StartAddr   string
	ModuleName  string
	KnownVendor string

	// FilePath is set for EventAVFileScan: the file Defender's AMFilter
	// minifilter scanned when pid opened it.
	FilePath string
}
