//go:build !windows

package main

import "errors"

// relaunchElevated is Windows-only (UAC) -- unreachable in practice on
// this platform since captureview.go only ever offers the "Relaunch as
// Administrator" button when captureSupported is also true, but declared
// here so the call site compiles everywhere.
func relaunchElevated() error {
	return errors.New("elevated relaunch isn't available on this platform")
}

func isUserCancelledElevation(error) bool { return false }

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
