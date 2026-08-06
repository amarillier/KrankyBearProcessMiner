//go:build !linux

package main

import "github.com/shirou/gopsutil/v4/process"

// linuxPrivateBytes is a no-op on non-Linux platforms; see procsampler_linux.go.
func linuxPrivateBytes(*process.Process) uint64 { return 0 }
