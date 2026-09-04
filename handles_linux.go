package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// gatherHandleSummary lists pid's open file descriptors via /proc/[pid]/fd --
// each entry is already a symlink the kernel resolves to a path (or, for a
// non-path-backed fd, a synthetic target like "socket:[12345]"/"pipe:[12345]"
// that's still informative even though it isn't a real path, same as `lsof`
// shows). The easiest of the three platforms by a wide margin -- see
// handles_darwin.go/handles_windows.go for why the other two need real work.
func gatherHandleSummary(pid int32) (HandleSummary, error) {
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return HandleSummary{}, fmt.Errorf("reading %s: %w", dir, err)
	}

	handles := make([]HandleDetail, 0, len(entries))
	for _, e := range entries {
		fdNum, err := strconv.ParseUint(e.Name(), 10, 64)
		if err != nil {
			continue // not a plain fd-number entry -- shouldn't happen, skip rather than fail the whole listing
		}
		target, linkErr := os.Readlink(filepath.Join(dir, e.Name()))
		typ, path := classifyLinuxFDTarget(target, linkErr)
		handles = append(handles, HandleDetail{Value: fdNum, Type: typ, Path: path})
	}
	sort.Slice(handles, func(i, j int) bool { return handles[i].Value < handles[j].Value })

	return HandleSummary{Count: int32(len(handles)), Handles: handles}, nil
}

// classifyLinuxFDTarget turns one /proc/[pid]/fd/N symlink's target into a
// type name + path. A real filesystem path renders as "file"; the kernel's
// synthetic non-path targets (sockets, pipes, anonymous inodes, memfds) are
// still shown as Path since they're informative on their own, just not an
// actual filesystem location -- readlink failing (the fd closed between
// ReadDir and Readlink, a normal race against a live process) renders as an
// unresolved, blank-path row rather than dropping it from the count.
func classifyLinuxFDTarget(target string, err error) (typ, path string) {
	if err != nil {
		return "unknown", ""
	}
	switch {
	case strings.HasPrefix(target, "socket:"):
		return "socket", target
	case strings.HasPrefix(target, "pipe:"):
		return "pipe", target
	case strings.HasPrefix(target, "anon_inode:"):
		return "anon_inode", target
	case strings.HasPrefix(target, "/memfd:"):
		return "memfd", target
	default:
		return "file", target
	}
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
