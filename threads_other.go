//go:build !windows

package main

import (
	"fmt"

	"github.com/shirou/gopsutil/v4/process"
)

// threadStartAddressSupported gates any UI/feature (see interference.go)
// that depends on resolving a thread's start address -- not available on
// macOS/Linux, see ThreadSummary's doc comment for why.
const threadStartAddressSupported = false

// unbackedStartAddr mirrors threads_windows.go's constant of the same name,
// purely so interference.go compiles on every platform -- interference.go's
// check() bails out on threadStartAddressSupported before this value could
// ever matter here.
const unbackedStartAddr = "UNBACKED (possible injection)"

// querySystemProcessInformation/extractThreadSummary are Windows-only real
// implementations (threads_windows.go) that interference.go's watcher calls
// directly rather than through gatherThreadSummary, to share one syscall
// across many watched PIDs per cycle. These stubs exist only so that file
// compiles on every platform -- check() never reaches them here, guarded by
// threadStartAddressSupported above.
func querySystemProcessInformation() ([]byte, error) {
	return nil, fmt.Errorf("not supported on this platform")
}

func extractThreadSummary([]byte, int32) (ThreadSummary, error) {
	return ThreadSummary{}, fmt.Errorf("not supported on this platform")
}

// processModules is the same story as querySystemProcessInformation above --
// a Windows-only real implementation (threads_windows.go) that
// interference.go's new-module-loaded detection calls directly. This stub
// exists only so that file compiles here; never reached, guarded by
// threadStartAddressSupported.
func processModules(int32) ([]procModule, error) {
	return nil, fmt.Errorf("not supported on this platform")
}

// gatherThreadSummary on macOS/Linux returns just a thread count -- see
// ThreadSummary's doc comment for why per-thread start-address resolution
// isn't available on these platforms without privileges this app doesn't
// have (and, on macOS, generally can't get at all for a third-party app).
func gatherThreadSummary(pid int32) (ThreadSummary, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return ThreadSummary{}, err
	}
	n, err := p.NumThreads()
	if err != nil {
		return ThreadSummary{}, err
	}
	return ThreadSummary{Count: n}, nil
}
