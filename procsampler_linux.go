//go:build linux

package main

import "github.com/shirou/gopsutil/v4/process"

// linuxPrivateBytes approximates a process's private (non-shared) resident
// memory as RSS minus its shared-page count -- the standard technique on
// Linux, which has no single "private bytes" figure the way Windows has
// PagefileUsage. gopsutil's MemoryInfoExStat has a different shape per
// platform (Windows/macOS's is an empty stub), so this needs its own
// build-tagged file rather than a runtime.GOOS check in procsampler.go.
func linuxPrivateBytes(p *process.Process) uint64 {
	ex, err := p.MemoryInfoEx()
	if err != nil || ex == nil || ex.RSS < ex.Shared {
		return 0
	}
	return ex.RSS - ex.Shared
}
