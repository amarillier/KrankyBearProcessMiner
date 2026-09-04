package main

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// This is the first purego usage in this repo, but not the first in this
// app's dependency tree: github.com/shirou/gopsutil/v4 already uses it for
// exactly the two libSystem/libproc calls needed here (proc_pidinfo,
// proc_pidfdinfo -- see its process/process_darwin.go NumFDsWithContext and
// internal/common/common_darwin.go), for the same reason this file does --
// neither is a raw BSD syscall golang.org/x/sys/unix wraps, and cgo would
// mean a C compiler dependency this project has otherwise avoided
// throughout (see CLAUDE.md). purego is already in go.mod as an indirect
// dependency (pulled in via gopsutil), so this only promotes it to direct.

const (
	procPidListFDs         = 1 // PROC_PIDLISTFDS
	procPidFDVNodePathInfo = 2 // PROC_PIDFDVNODEPATHINFO

	proxFDTypeVnode = 1 // PROX_FDTYPE_VNODE -- the only fd type with a resolvable path
)

// fdTypeNames indexes the PROX_FDTYPE_* constants from sys/proc_info.h.
var fdTypeNames = map[int]string{
	0:  "atalk",
	1:  "vnode",
	2:  "socket",
	3:  "pshm",
	4:  "psem",
	5:  "kqueue",
	6:  "pipe",
	7:  "fsevents",
	9:  "netpolicy",
	10: "channel",
	11: "nexus",
}

func fdTypeName(t uint32) string {
	if name, ok := fdTypeNames[int(t)]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", t)
}

// procFDInfo mirrors sys/proc_info.h's struct proc_fdinfo -- verified
// against gopsutil's own identical definition (process/process_darwin.go).
type procFDInfo struct {
	ProcFd     int32
	ProcFdtype uint32
}

// vinfoStat/vnodeInfo/fsid/vnodeInfoPath mirror sys/proc_info.h's
// vinfo_stat/vnode_info/fsid/vnode_info_path -- field-for-field copies of
// gopsutil's own verified type defs (process/process_darwin_arm64.go and
// process_darwin_amd64.go, identical on both), the same "vendor a known-
// correct copy of a small, stable OS-ABI struct" approach
// threads_windows.go already takes for SYSTEM_THREAD_INFORMATION. Only
// vnodeInfoPath.Path is actually read here; the rest exists so the struct's
// size/layout (and therefore the offset of Path) comes out right.
type vinfoStat struct {
	Dev           uint32
	Mode          uint16
	Nlink         uint16
	Ino           uint64
	Uid           uint32
	Gid           uint32
	Atime         int64
	Atimensec     int64
	Mtime         int64
	Mtimensec     int64
	Ctime         int64
	Ctimensec     int64
	Birthtime     int64
	Birthtimensec int64
	Size          int64
	Blocks        int64
	Blksize       int32
	Flags         uint32
	Gen           uint32
	Rdev          uint32
	Qspare        [2]int64
}

type fsid struct {
	Val [2]int32
}

type vnodeInfo struct {
	Stat vinfoStat
	Type int32
	Pad  int32
	Fsid fsid
}

// vnodeInfoPath is vnode_info_path: a vnodeInfo followed by the path itself.
type vnodeInfoPath struct {
	Vi   vnodeInfo
	Path [1024]int8 // MAXPATHLEN
}

// procFileInfo mirrors sys/proc_info.h's struct proc_fileinfo -- the header
// used by every *detailed* per-fd info struct (vnode_fdinfowithpath,
// socket_fdinfo, pipe_fdinfo, ...), NOT the same as procFDInfo above
// (proc_fdinfo, the small {fd, fdtype} pair PROC_PIDLISTFDS's array uses).
// Confirmed the hard way: passing procFDInfo as this header made
// vnodeFDInfoWithPath 16 bytes too small, so proc_pidfdinfo rejected every
// real call with ENOMEM (its buffersize argument must match the kernel's
// expected struct size exactly) -- every vnode-type fd came back with an
// empty path despite ProcFdtype correctly reading "vnode", not an
// unresolvable-path case at all.
type procFileInfo struct {
	OpenFlags  uint32
	Status     uint32
	Offset     int64
	FDType     int32
	GuardFlags uint32
}

// vnodeFDInfoWithPath mirrors sys/proc_info.h's struct vnode_fdinfowithpath
// -- proc_pidfdinfo(PROC_PIDFDVNODEPATHINFO)'s actual payload shape.
type vnodeFDInfoWithPath struct {
	Pfi  procFileInfo
	Pvip vnodeInfoPath
}

var (
	libprocOnce sync.Once
	libprocErr  error

	procPidInfo   func(pid, flavor int32, arg uint64, buffer unsafe.Pointer, bufferSize int32) int32
	procPidFDInfo func(pid, fd, flavor int32, buffer unsafe.Pointer, bufferSize int32) int32
)

// loadLibproc dlopens libSystem and resolves proc_pidinfo/proc_pidfdinfo
// once, cached for the process lifetime -- repeatedly Dlopen/Dlclose-ing has
// caused real crashes in gopsutil's own history (see its common_darwin.go
// comment on libCache), so this follows the same "open once, never close"
// discipline.
func loadLibproc() error {
	libprocOnce.Do(func() {
		handle, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			libprocErr = fmt.Errorf("dlopen libSystem: %w", err)
			return
		}
		purego.RegisterLibFunc(&procPidInfo, handle, "proc_pidinfo")
		purego.RegisterLibFunc(&procPidFDInfo, handle, "proc_pidfdinfo")
	})
	return libprocErr
}

// gatherHandleSummary lists pid's open file descriptors via proc_pidinfo
// (PROC_PIDLISTFDS), then resolves a path for each file-backed (vnode) one
// via proc_pidfdinfo (PROC_PIDFDVNODEPATHINFO) -- the same two calls
// gopsutil's own NumFDs already uses for the count, extended here to the
// per-fd detail. No root needed for the current user's own processes (per
// real-world testing); a different-user or protected process fails with
// EPERM, surfaced as this function's error.
func gatherHandleSummary(pid int32) (HandleSummary, error) {
	if err := loadLibproc(); err != nil {
		return HandleSummary{}, err
	}

	bufSize := procPidInfo(pid, procPidListFDs, 0, nil, 0)
	if bufSize <= 0 {
		return HandleSummary{}, fmt.Errorf("proc_pidinfo(PROC_PIDLISTFDS): pid %d not found or not accessible", pid)
	}
	entrySize := int32(unsafe.Sizeof(procFDInfo{}))
	fds := make([]procFDInfo, bufSize/entrySize)
	ret := procPidInfo(pid, procPidListFDs, 0, unsafe.Pointer(&fds[0]), bufSize)
	if ret <= 0 {
		return HandleSummary{}, fmt.Errorf("proc_pidinfo(PROC_PIDLISTFDS): returned %d", ret)
	}
	fds = fds[:ret/entrySize]

	handles := make([]HandleDetail, 0, len(fds))
	for _, fd := range fds {
		path := ""
		if fd.ProcFdtype == proxFDTypeVnode {
			path = resolveVnodePath(pid, fd.ProcFd)
		}
		handles = append(handles, HandleDetail{
			Value: uint64(fd.ProcFd),
			Type:  fdTypeName(fd.ProcFdtype),
			Path:  path,
		})
	}
	return HandleSummary{Count: int32(len(handles)), Handles: handles}, nil
}

// resolveVnodePath resolves one file-backed fd to its path. Returns "" on
// any failure (a transient race against the fd closing between the list
// call and this one is the common case, not usually a real error worth
// surfacing) -- same "blank means unresolved, not a claim otherwise"
// discipline as resolveStartAddress in threads_windows.go.
func resolveVnodePath(pid int32, fd int32) string {
	var info vnodeFDInfoWithPath
	size := int32(unsafe.Sizeof(info))
	ret := procPidFDInfo(pid, fd, procPidFDVNodePathInfo, unsafe.Pointer(&info), size)
	if ret <= 0 {
		return ""
	}
	return cStringToGo(info.Pvip.Path[:])
}

// cStringToGo converts a NUL-terminated []int8 (as libproc returns fixed-size
// char buffers) to a Go string, stopping at the first NUL.
func cStringToGo(b []int8) string {
	for i, c := range b {
		if c == 0 {
			b = b[:i]
			break
		}
	}
	buf := make([]byte, len(b))
	for i, c := range b {
		buf[i] = byte(c)
	}
	return string(buf)
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
