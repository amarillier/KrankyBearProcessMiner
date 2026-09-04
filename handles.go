package main

// HandleDetail is one open handle/file descriptor within a process.
type HandleDetail struct {
	Value uint64 // handle value (Windows) / fd number (macOS, Linux)

	// Type is the kernel-reported type name: "File"/"Key"/"Event"/"Section"/...
	// on Windows (via NtQueryObject -- see handles_windows.go), "vnode"/
	// "socket"/"pipe"/... on macOS (via proc_pidinfo's fdtype -- see
	// handles_darwin.go), or "file"/"socket"/"pipe"/"anon_inode"/... on Linux
	// (derived from the /proc/[pid]/fd symlink target -- see handles_linux.go).
	Type string

	// Path is the resolved target where known -- "" means unresolvable, not a
	// claim the handle has no target, same discipline as ThreadDetail.StartAddr
	// (see threads.go). See handleQueryTimedOut for Windows' one further
	// distinct case.
	Path string
}

// HandleSummary is the best-effort per-process open-handle list gatherable
// on this platform. Unlike ThreadSummary (where macOS/Linux are genuinely
// blocked by OS privilege restrictions from resolving thread start
// addresses), every platform produces a real per-handle listing here -- the
// difference between platforms is implementation difficulty, not
// capability, so there's no "supported" gating const for this feature.
type HandleSummary struct {
	Count   int32
	Handles []HandleDetail // nil only on a hard failure -- see gatherHandleSummary's error return

	// TimedOut counts Windows handles whose name query was abandoned by the
	// timeout guard (see handles_windows.go's hang hazard doc comment) --
	// always 0 on macOS/Linux, which have no equivalent hang risk.
	TimedOut int
}

// gatherHandleSummary is implemented per-platform -- see handles_windows.go,
// handles_darwin.go, handles_linux.go.

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
