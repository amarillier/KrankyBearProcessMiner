//go:build !windows

package main

import "fmt"

// scanThreadStackForModules is TEB/stack-reading scaffolding -- Windows
// only (see stackscan_windows.go). This stub exists only so
// interference.go compiles on every platform; check() never reaches it
// here, guarded by threadStartAddressSupported.
func scanThreadStackForModules(int32, int32, []procModule) (map[string]bool, error) {
	return nil, fmt.Errorf("not supported on this platform")
}
