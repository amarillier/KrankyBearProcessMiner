//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// threadBasicInformation mirrors THREAD_BASIC_INFORMATION (NTAPI class 0),
// the same "well-documented, decades-stable NT internals" territory as
// systemThreadInformation in threads_windows.go -- ReactOS/phnt define the
// same layout. Verified empirically against a real running process before
// use here: TebBaseAddress resolved to a real, readable address, and its
// UniqueProcess/UniqueThread fields matched the PID/TID queried, confirming
// the field offsets (particularly the padding before TebBaseAddress) are
// right.
type threadBasicInformation struct {
	ExitStatus     int32
	_              [4]byte // padding -- TebBaseAddress must land 8-byte aligned
	TebBaseAddress uintptr
	UniqueProcess  uintptr
	UniqueThread   uintptr
	AffinityMask   uintptr
	Priority       int32
	BasePriority   int32
}

const threadBasicInformationClass = 0

// maxStackScanBytes caps how much of a thread's stack we'll ever read --
// default Windows thread stacks are ~1MB reserved (usually far less
// committed), so this is a generous ceiling against a pathological outlier,
// not a real-world limit.
const maxStackScanBytes = 16 << 20 // 16MB

// getThreadTEB returns tid's TEB base address via NtQueryInformationThread,
// the standard (if undocumented) way to locate a thread's TEB from outside
// the process -- same undocumented-but-stable NTAPI territory as the rest
// of this file.
func getThreadTEB(tid int32) (uintptr, error) {
	h, err := windows.OpenThread(windows.THREAD_QUERY_LIMITED_INFORMATION, false, uint32(tid))
	if err != nil {
		return 0, fmt.Errorf("OpenThread: %w", err)
	}
	defer windows.CloseHandle(h)

	var tbi threadBasicInformation
	r1, _, _ := procNtQueryInformationThread.Call(
		uintptr(h), uintptr(threadBasicInformationClass),
		uintptr(unsafe.Pointer(&tbi)), unsafe.Sizeof(tbi), 0)
	if r1 != 0 {
		return 0, fmt.Errorf("NtQueryInformationThread(ThreadBasicInformation): status 0x%X", r1)
	}
	return tbi.TebBaseAddress, nil
}

// readThreadStackRegion reads tid's entire *committed* stack region (not
// just the portion between the current stack pointer and the base) via the
// thread's TEB. Deliberately the whole region, not just live call frames:
// a function that already returned doesn't clear the stack bytes it used,
// so scanning the full committed range can still find evidence of a hook
// that already ran and returned, not only one active at this exact instant
// -- the same "recent history, not just this moment" property that makes
// this a reasonable approximation of watching call stacks scroll by over
// time, from a single point-in-time read. No thread suspension needed:
// StackBase/StackLimit in the TEB are fixed at thread creation and don't
// require a consistent/suspended snapshot to read safely, unlike the
// current instruction pointer or register state would.
func readThreadStackRegion(hProcess windows.Handle, tid int32) ([]byte, error) {
	teb, err := getThreadTEB(tid)
	if err != nil {
		return nil, err
	}
	if teb == 0 {
		return nil, fmt.Errorf("thread %d has no TEB (exited?)", tid)
	}

	// NT_TIB is the TEB's first member: ExceptionList (ptr), StackBase
	// (ptr), StackLimit (ptr) -- offsets 0, 8, 16 on x64. Stack grows down,
	// so StackBase is the high/starting address and StackLimit the current
	// low/committed boundary.
	var tib [3]uintptr
	var read uintptr
	if err := windows.ReadProcessMemory(hProcess, teb, (*byte)(unsafe.Pointer(&tib[0])), unsafe.Sizeof(tib), &read); err != nil {
		return nil, fmt.Errorf("ReadProcessMemory(TEB): %w", err)
	}
	stackBase, stackLimit := tib[1], tib[2]
	if stackBase <= stackLimit {
		return nil, fmt.Errorf("implausible stack bounds: base=0x%X limit=0x%X", stackBase, stackLimit)
	}

	size := stackBase - stackLimit
	if size > maxStackScanBytes {
		size = maxStackScanBytes
	}

	buf := make([]byte, size)
	if err := windows.ReadProcessMemory(hProcess, stackLimit, &buf[0], size, &read); err != nil {
		return nil, fmt.Errorf("ReadProcessMemory(stack): %w", err)
	}
	return buf[:read], nil
}

// scanThreadStackForModules is the entry point interference.go's watcher
// calls: opens pid for VM read access, reads tid's full committed stack
// region, and returns the distinct set of foreign-candidate module names
// whose address range that stack contains a pointer into (classification
// into system/own/foreign happens in interference.go's classifyModule,
// against the same modules list -- this function itself doesn't filter,
// it just reports every module hit so the caller can classify).
func scanThreadStackForModules(pid int32, tid int32, modules []procModule) (map[string]bool, error) {
	hProcess, err := windows.OpenProcess(windows.PROCESS_VM_READ|windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return nil, fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(hProcess)

	stackBytes, err := readThreadStackRegion(hProcess, tid)
	if err != nil {
		return nil, err
	}
	return scanStackForModules(stackBytes, modules), nil
}

// scanStackForModules scans stackBytes (as returned by readThreadStackRegion)
// for 8-byte-aligned pointer values that fall inside any of modules' address
// ranges, returning the distinct set of module names found. This is a
// "poor man's" stack walk -- no call-frame structure, no unwind metadata,
// just "does any pointer-shaped value in this memory point into a loaded
// module." Cruder than real stack unwinding (Process Explorer's "Stack"
// button), but far simpler, and answers the question that actually matters
// here: is this vendor's code anywhere near this thread.
func scanStackForModules(stackBytes []byte, modules []procModule) map[string]bool {
	found := make(map[string]bool)
	if len(stackBytes) < 8 {
		return found
	}
	for i := 0; i+8 <= len(stackBytes); i += 8 {
		val := uintptr(binary.LittleEndian.Uint64(stackBytes[i : i+8]))
		if val == 0 {
			continue
		}
		for _, m := range modules {
			if val >= m.base && val < m.base+m.size {
				found[m.name] = true
				break
			}
		}
	}
	return found
}
