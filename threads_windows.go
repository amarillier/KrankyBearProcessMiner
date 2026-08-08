//go:build windows

package main

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemThreadInformation mirrors the undocumented-but-stable
// SYSTEM_THREAD_INFORMATION struct (NTAPI). Not defined by
// golang.org/x/sys/windows (unlike the accompanying SYSTEM_PROCESS_INFORMATION,
// which is -- this one is small enough, and well-documented enough
// (ReactOS headers, Process Hacker/System Informer's phnt, countless
// security tooling), to define here directly. Field order/types matter:
// this is read via unsafe.Pointer cast over raw bytes from
// NtQuerySystemInformation, not JSON/struct-tag decoded.
type systemThreadInformation struct {
	KernelTime      int64
	UserTime        int64
	CreateTime      int64
	WaitTime        uint32
	StartAddress    uintptr
	UniqueProcess   uintptr // CLIENT_ID.UniqueProcess
	UniqueThread    uintptr // CLIENT_ID.UniqueThread
	Priority        int32
	BasePriority    int32
	ContextSwitches uint32
	State           uint32
	WaitReason      uint32
}

// threadStateNames indexes systemThreadInformation.State (the KTHREAD_STATE
// enum) -- see ReactOS's ntddk.h / Process Hacker's phnt for the same list.
var threadStateNames = []string{
	"Initialized", "Ready", "Running", "Standby", "Terminated",
	"Waiting", "Transition", "DeferredReady", "GateWaitObsolete", "WaitingForProcessInSwap",
}

func threadStateName(state uint32) string {
	if int(state) < len(threadStateNames) {
		return threadStateNames[state]
	}
	return fmt.Sprintf("Unknown(%d)", state)
}

// querySystemProcessInformation calls NtQuerySystemInformation(
// SystemProcessInformation), growing the buffer until it's big enough --
// the classic pattern for this API, since there's no way to know the
// required size upfront (the process/thread list can change between the
// size probe and the real call, hence looping rather than a single retry).
func querySystemProcessInformation() ([]byte, error) {
	size := uint32(1 << 20) // 1MB starting guess -- typically enough for the whole system's process/thread list
	for range 8 {           // bounded retries -- a system that never settles after 8 doublings has bigger problems
		buf := make([]byte, size)
		var retLen uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buf[0]), size, &retLen)
		if err == nil {
			return buf, nil
		}
		if err != windows.STATUS_INFO_LENGTH_MISMATCH {
			return nil, fmt.Errorf("NtQuerySystemInformation: %w", err)
		}
		size = retLen + 4096 // pad a little -- the list can grow between the failed probe and the retry
	}
	return nil, fmt.Errorf("NtQuerySystemInformation: process list did not stabilize")
}

// threadStartAddressSupported gates any UI/feature (see interference.go)
// that depends on resolving a thread's start address -- Windows-only, see
// ThreadSummary's doc comment for why macOS/Linux can't do this.
const threadStartAddressSupported = true

// unbackedStartAddr is resolveStartAddress's flag value for a thread whose
// start address falls outside every one of its process's loaded modules --
// shared with interference.go's watcher, which looks for this exact string
// appearing on a thread that wasn't there the last time it checked.
const unbackedStartAddr = "UNBACKED (possible injection)"

// gatherThreadSummary returns the full per-thread list for pid, with each
// thread's start address resolved against pid's own loaded modules -- see
// ThreadSummary's doc comment for what this can and can't detect.
func gatherThreadSummary(pid int32) (ThreadSummary, error) {
	buf, err := querySystemProcessInformation()
	if err != nil {
		return ThreadSummary{}, err
	}
	return extractThreadSummary(buf, pid)
}

// extractThreadSummary scans an already-fetched NtQuerySystemInformation
// buffer for pid's threads. Factored out of gatherThreadSummary so a caller
// checking many watched PIDs in one pass (interference.go's watcher) pays
// for the expensive system-wide query once per cycle, not once per watched
// process -- the same buffer is scanned once per target instead of re-
// querying the whole system's process/thread list for each one.
func extractThreadSummary(buf []byte, pid int32) (ThreadSummary, error) {
	modules, modErr := processModules(pid)
	// modErr is deliberately not returned as this function's error: module
	// resolution failing (protected process, or a transient Toolhelp32
	// hiccup -- see processModules) just means every StartAddress below
	// renders unresolved rather than "UNBACKED", not that the thread list
	// itself is unusable. Surfaced instead via ModulesUnresolved so callers
	// can tell "checked, found nothing" apart from "couldn't check" (see
	// its doc comment) rather than silently treating a failed check as a
	// clean one.

	target := uintptr(pid)
	offset := uint32(0)
	for {
		if int(offset)+int(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{})) > len(buf) {
			break // malformed/truncated buffer -- stop rather than read out of bounds
		}
		proc := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[offset]))

		if proc.UniqueProcessID == target {
			threads := make([]ThreadDetail, 0, proc.NumberOfThreads)
			threadsOffset := offset + uint32(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}))
			for i := uint32(0); i < proc.NumberOfThreads; i++ {
				o := threadsOffset + i*uint32(unsafe.Sizeof(systemThreadInformation{}))
				if int(o)+int(unsafe.Sizeof(systemThreadInformation{})) > len(buf) {
					break
				}
				t := (*systemThreadInformation)(unsafe.Pointer(&buf[o]))
				startAddr := t.StartAddress
				if startAddr == 0 {
					// The bulk field reads 0 for some threads that do have
					// a real, resolvable start address (confirmed by
					// testing against both a process's own first thread
					// and a freshly-injected one) -- fall back to the
					// slower per-thread query rather than accepting a
					// blank 0 as truth, since that would let a genuinely
					// unbacked/injected thread masquerade as merely
					// "unresolved" instead of getting flagged.
					if real, err := queryWin32StartAddressPerThread(int32(t.UniqueThread)); err == nil {
						startAddr = real
					}
				}
				threads = append(threads, ThreadDetail{
					TID:        int32(t.UniqueThread),
					StartAddr:  resolveStartAddress(startAddr, modules),
					KernelTime: ntTicksToDuration(t.KernelTime),
					UserTime:   ntTicksToDuration(t.UserTime),
					State:      threadStateName(t.State),
				})
			}
			summary := ThreadSummary{Count: int32(proc.NumberOfThreads), Threads: threads}
			if modErr != nil {
				summary.ModulesUnresolved = modErr.Error()
			}
			return summary, nil
		}

		if proc.NextEntryOffset == 0 {
			break
		}
		offset += proc.NextEntryOffset
	}
	return ThreadSummary{}, fmt.Errorf("process %d not found in system information snapshot (exited?)", pid)
}

// ntdll and ntQueryInformationThread: NtQueryInformationThread isn't wrapped
// by golang.org/x/sys/windows (unlike NtQuerySystemInformation), so it's
// declared the same way this codebase already declares other undocumented
// NT APIs it needs -- a LazyDLL proc lookup.
var (
	ntdll                        = windows.NewLazySystemDLL("ntdll.dll")
	procNtQueryInformationThread = ntdll.NewProc("NtQueryInformationThread")
)

// threadQuerySetWin32StartAddress is THREADINFOCLASS's
// ThreadQuerySetWin32StartAddress (9) -- see ReactOS/phnt.
const threadQuerySetWin32StartAddress = 9

// queryWin32StartAddressPerThread reads a single thread's start address via
// NtQueryInformationThread instead of the bulk SYSTEM_THREAD_INFORMATION
// field extractThreadSummary normally uses. Confirmed by real-world testing
// (injecting a thread into a live Chrome process and comparing) that the
// bulk field can read back as 0 for some threads that do have a real,
// resolvable start address -- notably a process's very first thread, but
// also, concerningly, a freshly-injected one -- which would otherwise make
// a genuinely UNBACKED thread look merely "unresolved" instead of flagged.
// This per-thread call is what Process Explorer/Process Hacker actually use
// for their own "Start Address" column, and is the fallback extractThreadSummary
// reaches for specifically when the bulk field comes back 0, rather than
// accepting that as gospel.
func queryWin32StartAddressPerThread(tid int32) (uintptr, error) {
	h, err := windows.OpenThread(windows.THREAD_QUERY_LIMITED_INFORMATION, false, uint32(tid))
	if err != nil {
		return 0, fmt.Errorf("OpenThread: %w", err)
	}
	defer windows.CloseHandle(h)

	var addr uintptr
	r1, _, _ := procNtQueryInformationThread.Call(
		uintptr(h), uintptr(threadQuerySetWin32StartAddress),
		uintptr(unsafe.Pointer(&addr)), unsafe.Sizeof(addr), 0)
	if r1 != 0 { // NTSTATUS != STATUS_SUCCESS
		return 0, fmt.Errorf("NtQueryInformationThread: status 0x%X", r1)
	}
	return addr, nil
}

// ntTicksToDuration converts an NT LARGE_INTEGER time value (100-nanosecond
// units) to a time.Duration.
func ntTicksToDuration(ticks int64) time.Duration {
	return time.Duration(ticks) * 100 * time.Nanosecond
}

// processModules lists pid's loaded modules via the classic Toolhelp
// snapshot API, retrying on ERROR_PARTIAL_COPY -- a well-documented,
// genuinely transient failure (MSDN's own remarks for
// CreateToolhelp32Snapshot call it out explicitly) that happens more often
// against a busy, many-threaded process. Confirmed in practice against a
// real 48-thread Chrome browser process: every single start address came
// back blank with no error shown anywhere, because the previous version of
// this function only checked for CreateToolhelp32Snapshot itself failing --
// Module32First/Next failing afterward (which is where ERROR_PARTIAL_COPY
// actually showed up) was silently swallowed into a "no modules" result
// indistinguishable from a real empty module list. See ThreadSummary's
// ModulesUnresolved for how that failure is surfaced now instead.
func processModules(pid int32) ([]procModule, error) {
	const maxAttempts = 5
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		modules, err := snapshotModulesOnce(pid)
		if err == nil {
			return modules, nil
		}
		lastErr = err
		if !errors.Is(err, windows.ERROR_PARTIAL_COPY) {
			break // a real failure (access denied, wrong bitness, exited, ...) -- retrying won't help
		}
		time.Sleep(5 * time.Millisecond) // brief backoff -- the module list was mid-change, give it a moment to settle
	}
	return nil, lastErr
}

// snapshotModulesOnce is one attempt at the Toolhelp module snapshot +
// enumeration -- factored out so processModules can retry the whole thing
// (a fresh snapshot handle each time) rather than just one failed call
// within it.
func snapshotModulesOnce(pid int32) ([]procModule, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPMODULE|windows.TH32CS_SNAPMODULE32, uint32(pid))
	if err != nil {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)

	var mod windows.ModuleEntry32
	mod.Size = uint32(unsafe.Sizeof(mod))

	var modules []procModule
	err = windows.Module32First(snap, &mod)
	for err == nil {
		modules = append(modules, procModule{
			name: windows.UTF16ToString(mod.Module[:]),
			path: windows.UTF16ToString(mod.ExePath[:]),
			base: mod.ModBaseAddr,
			size: uintptr(mod.ModBaseSize),
		})
		err = windows.Module32Next(snap, &mod)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		// Enumeration ended some way other than the normal "no more
		// modules" signal -- e.g. cut short mid-list by the same kind of
		// transient failure CreateToolhelp32Snapshot itself can hit.
		// Surfacing this rather than returning the partial list as if it
		// were complete matters here specifically: an incomplete module
		// list can make a perfectly normal thread look "UNBACKED" simply
		// because the module that actually contains its start address
		// never made it into the list.
		return nil, fmt.Errorf("Module32First/Next: %w", err)
	}
	return modules, nil
}

// resolveStartAddress finds which loaded module (if any) contains addr and
// formats "module.dll+0xOFFSET". A thread whose start address falls
// outside every loaded module is flagged "UNBACKED (possible injection)" --
// the classic signal for reflective DLL injection or shellcode, since a
// legitimately-created thread always starts inside some loaded module
// (ntdll.dll's thread-start thunk, at minimum).
func resolveStartAddress(addr uintptr, modules []procModule) string {
	if addr == 0 {
		return ""
	}
	for _, m := range modules {
		if addr >= m.base && addr < m.base+m.size {
			return fmt.Sprintf("%s+0x%X", m.name, addr-m.base)
		}
	}
	if modules == nil {
		return "" // module list unavailable (e.g. access denied) -- unresolved, not a claim either way
	}
	return unbackedStartAddr
}
