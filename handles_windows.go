//go:build windows

package main

import (
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemHandleTableEntryInfoEx mirrors the undocumented-but-stable
// SYSTEM_HANDLE_TABLE_ENTRY_INFO_EX struct (NTAPI), same "small enough and
// well-documented enough in security tooling to define directly" reasoning
// systemThreadInformation gets in threads_windows.go. Deliberately the *Ex*
// variant (paired with SystemExtendedHandleInformation below), not the
// legacy SYSTEM_HANDLE_TABLE_ENTRY_INFO the backlog names colloquially --
// the legacy struct's UniqueProcessId is a 16-bit USHORT (silently wrong
// above PID 65535); this one uses a full-width ULONG_PTR for both PID and
// handle value, the same reason Process Hacker/System Informer use it too.
type systemHandleTableEntryInfoEx struct {
	Object                uintptr
	UniqueProcessId       uintptr
	HandleValue           uintptr
	GrantedAccess         uint32
	CreatorBackTraceIndex uint16
	ObjectTypeIndex       uint16
	HandleAttributes      uint32
	Reserved              uint32
}

// systemHandleInformationExHeader mirrors SYSTEM_HANDLE_INFORMATION_EX's
// fixed header, before the Handles[] array.
type systemHandleInformationExHeader struct {
	NumberOfHandles uintptr
	Reserved        uintptr
}

// querySystemHandleInformation calls NtQuerySystemInformation(
// SystemExtendedHandleInformation), growing the buffer until it's big
// enough -- same grow-buffer pattern as threads_windows.go's
// querySystemProcessInformation, just a larger starting guess: a system-wide
// handle table runs far bigger than the process/thread list.
func querySystemHandleInformation() ([]byte, error) {
	size := uint32(4 << 20) // 4MB starting guess
	for range 10 {          // bounded retries -- see querySystemProcessInformation's reasoning
		buf := make([]byte, size)
		var retLen uint32
		err := windows.NtQuerySystemInformation(windows.SystemExtendedHandleInformation, unsafe.Pointer(&buf[0]), size, &retLen)
		if err == nil {
			return buf, nil
		}
		if err != windows.STATUS_INFO_LENGTH_MISMATCH {
			return nil, fmt.Errorf("NtQuerySystemInformation(SystemExtendedHandleInformation): %w", err)
		}
		size = retLen + 4096*16 // pad more generously than the process list -- the system handle count can grow fast between the probe and the retry
	}
	return nil, fmt.Errorf("NtQuerySystemInformation(SystemExtendedHandleInformation): handle table did not stabilize")
}

// extractHandlesForPID scans an already-fetched SystemExtendedHandleInformation
// buffer for pid's own handle entries. Factored out like
// extractThreadSummary so a future caller checking many PIDs in one pass
// could pay for the expensive system-wide query once, not once per target.
func extractHandlesForPID(buf []byte, pid int32) []systemHandleTableEntryInfoEx {
	if len(buf) < int(unsafe.Sizeof(systemHandleInformationExHeader{})) {
		return nil
	}
	header := (*systemHandleInformationExHeader)(unsafe.Pointer(&buf[0]))
	entrySize := uint32(unsafe.Sizeof(systemHandleTableEntryInfoEx{}))
	headerSize := uint32(unsafe.Sizeof(systemHandleInformationExHeader{}))
	target := uintptr(pid)

	var out []systemHandleTableEntryInfoEx
	for i := uintptr(0); i < header.NumberOfHandles; i++ {
		offset := headerSize + uint32(i)*entrySize
		if int(offset)+int(entrySize) > len(buf) {
			break // truncated/malformed buffer -- stop rather than read out of bounds
		}
		e := (*systemHandleTableEntryInfoEx)(unsafe.Pointer(&buf[offset]))
		if e.UniqueProcessId == target {
			out = append(out, *e)
		}
	}
	return out
}

// procNtQueryObject: NtQueryObject isn't wrapped by golang.org/x/sys/windows
// (unlike NtQuerySystemInformation), so it gets the same LazyDLL proc lookup
// as NtQueryInformationThread -- reuses the ntdll var threads_windows.go
// already declares (same package, same build tag).
var procNtQueryObject = ntdll.NewProc("NtQueryObject")

const (
	objectNameInformation = 1 // OBJECT_INFORMATION_CLASS.ObjectNameInformation
	objectTypeInformation = 2 // OBJECT_INFORMATION_CLASS.ObjectTypeInformation
)

// unicodeString mirrors NT's UNICODE_STRING -- both OBJECT_NAME_INFORMATION
// and OBJECT_TYPE_INFORMATION begin with one (Name and TypeName
// respectively), so the same struct/reader serves both NtQueryObject calls
// below.
type unicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        uintptr
}

// readUnicodeStringFromBuffer reads the UNICODE_STRING at the front of an
// NtQueryObject result buffer. NtQueryObject places the string data inline
// within the same buffer and points Buffer at it, so this is safe to read
// directly rather than needing a second call.
func readUnicodeStringFromBuffer(buf []byte) string {
	if len(buf) < int(unsafe.Sizeof(unicodeString{})) {
		return ""
	}
	us := (*unicodeString)(unsafe.Pointer(&buf[0]))
	if us.Length == 0 || us.Buffer == 0 {
		return ""
	}
	chars := unsafe.Slice((*uint16)(unsafe.Pointer(us.Buffer)), us.Length/2)
	return windows.UTF16ToString(chars)
}

// queryObjectInfo calls NtQueryObject synchronously, growing the buffer on
// either "too small" NTSTATUS (STATUS_INFO_LENGTH_MISMATCH and
// STATUS_BUFFER_OVERFLOW both indicate that, just with different severity
// bits set). Used directly for ObjectTypeInformation (not documented to
// ever hang) and, wrapped with a timeout by queryObjectNameWithTimeout
// below, for ObjectNameInformation (which can).
func queryObjectInfo(h windows.Handle, class uintptr) ([]byte, error) {
	size := uintptr(1024)
	for range 4 {
		buf := make([]byte, size)
		var retLen uint32
		r1, _, _ := procNtQueryObject.Call(uintptr(h), class, uintptr(unsafe.Pointer(&buf[0])), size, uintptr(unsafe.Pointer(&retLen)))
		status := uint32(r1)
		if status == 0 { // NTSTATUS STATUS_SUCCESS
			return buf[:retLen], nil
		}
		if status != uint32(windows.STATUS_INFO_LENGTH_MISMATCH) && status != uint32(windows.STATUS_BUFFER_OVERFLOW) {
			return nil, fmt.Errorf("NtQueryObject: status 0x%X", status)
		}
		if uintptr(retLen) > size {
			size = uintptr(retLen)
		} else {
			size *= 2
		}
	}
	return nil, fmt.Errorf("NtQueryObject: did not stabilize")
}

// handleQueryTimedOut is queryObjectNameWithTimeout's sentinel for a name
// query abandoned after the timeout guard -- same discipline as
// threads_windows.go's unbackedStartAddr: a specific, named finding, not an
// ordinary blank/unresolved case.
const handleQueryTimedOut = "(query timed out)"

// handleNameQueryTimeout bounds how long gatherHandleSummary waits for any
// one handle's name before moving on -- see queryObjectNameWithTimeout.
const handleNameQueryTimeout = 100 * time.Millisecond

// queryObjectNameWithTimeout resolves dup's ObjectNameInformation name,
// abandoning the attempt if it doesn't return within timeout.
//
// This is the one call in the whole feature confirmed (by the backlog's own
// research, and by long real-world experience from Process Explorer/Process
// Hacker) to occasionally block indefinitely -- classically, a named pipe
// with no listener on the other end. There's no existing timeout-guarded-
// goroutine precedent anywhere else in this repo (checked) -- this is
// genuinely new infrastructure, not a copy of an established pattern.
//
// dup's closure is owned by the background goroutine itself, not the
// caller: it closes dup right after NtQueryObject returns, whatever the
// result, even if that happens well after this function has already timed
// out and returned. That way a call that's merely slow (not truly hung)
// still gets cleaned up properly; only a genuinely permanent hang leaks dup
// and its goroutine forever -- an accepted, bounded cost (handles are
// queried sequentially, so at most one such leak is ever outstanding at a
// time), the same tradeoff Process Explorer/Process Hacker themselves carry
// for this exact API, since Go (like any userspace caller) has no way to
// cancel a blocked syscall.
func queryObjectNameWithTimeout(dup windows.Handle, timeout time.Duration) (name string, timedOut bool) {
	ch := make(chan string, 1)
	go func() {
		defer windows.CloseHandle(dup)
		buf, err := queryObjectInfo(dup, objectNameInformation)
		if err != nil {
			ch <- ""
			return
		}
		ch <- readUnicodeStringFromBuffer(buf)
	}()
	select {
	case name := <-ch:
		return name, false
	case <-time.After(timeout):
		return "", true
	}
}

// buildDosDeviceMap resolves every drive letter's NT device root (e.g. "A:"
// -> "\Device\HarddiskVolume3") via the classic QueryDosDevice technique --
// the same one Sysinternals-style tools use to turn a File handle's kernel-
// form name back into a familiar drive-letter path. Built once per gather
// call (26 cheap calls), not per handle.
func buildDosDeviceMap() map[string]string {
	m := make(map[string]string, 26)
	var target [512]uint16
	for c := 'A'; c <= 'Z'; c++ {
		drive := string(c) + ":"
		drive16, err := windows.UTF16PtrFromString(drive)
		if err != nil {
			continue
		}
		n, err := windows.QueryDosDevice(drive16, &target[0], uint32(len(target)))
		if err != nil || n == 0 {
			continue
		}
		m[windows.UTF16ToString(target[:n])] = drive
	}
	return m
}

// translateDevicePath rewrites a File handle's kernel-form name (e.g.
// "\Device\HarddiskVolume3\Users\foo\file.txt") to a drive-letter path when
// its device root matches one from buildDosDeviceMap. Anything that doesn't
// match (named pipes, "\Device\Afd\Endpoint" for sockets, "\Device\Mup" for
// a UNC path) is left in raw NT form rather than guessed at.
func translateDevicePath(path string, dosDevices map[string]string) string {
	for device, drive := range dosDevices {
		if rest, ok := strings.CutPrefix(path, device); ok {
			return drive + rest
		}
	}
	return path
}

// gatherHandleSummary lists pid's open handles via
// NtQuerySystemInformation(SystemExtendedHandleInformation), then for each
// one duplicates it into this process (a handle value only means something
// inside its *owning* process's handle table, so it can't be queried
// directly) and resolves its type/name via NtQueryObject. See
// queryObjectNameWithTimeout for the name-query hang hazard this guards
// against. Registry key paths (\REGISTRY\MACHINE\...) are shown in raw NT
// form for now, not translated to HKLM/HKCU -- a nice-to-have deferred
// rather than adding SID-to-hive-abbreviation logic here.
func gatherHandleSummary(pid int32) (HandleSummary, error) {
	buf, err := querySystemHandleInformation()
	if err != nil {
		return HandleSummary{}, err
	}
	entries := extractHandlesForPID(buf, pid)

	source, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, uint32(pid))
	if err != nil {
		return HandleSummary{}, fmt.Errorf("OpenProcess(PROCESS_DUP_HANDLE): %w", err)
	}
	defer windows.CloseHandle(source)

	dosDevices := buildDosDeviceMap()

	handles := make([]HandleDetail, 0, len(entries))
	timedOut := 0
	for _, e := range entries {
		var dup windows.Handle
		if err := windows.DuplicateHandle(source, windows.Handle(e.HandleValue), windows.CurrentProcess(),
			&dup, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
			// Closed between the snapshot and here (a normal race against a
			// live process), or a type this process can't duplicate -- skip
			// this one handle rather than failing the whole gather.
			continue
		}

		typeName := ""
		if b, err := queryObjectInfo(dup, objectTypeInformation); err == nil {
			typeName = readUnicodeStringFromBuffer(b)
		}

		name, didTimeout := queryObjectNameWithTimeout(dup, handleNameQueryTimeout)
		switch {
		case didTimeout:
			timedOut++
			name = handleQueryTimedOut
		case typeName == "File" && name != "":
			name = translateDevicePath(name, dosDevices)
		}

		handles = append(handles, HandleDetail{
			Value: uint64(e.HandleValue),
			Type:  typeName,
			Path:  name,
		})
	}

	return HandleSummary{Count: int32(len(handles)), Handles: handles, TimedOut: timedOut}, nil
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
