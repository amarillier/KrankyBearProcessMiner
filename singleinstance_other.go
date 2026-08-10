//go:build !windows

package main

// elevationCheckSupported gates anotherInstanceRunning's "allow one normal +
// one elevated" relaxation -- UAC elevation is a Windows-only concept, so
// this platform stays strictly single-instance (see singleinstance.go).
const elevationCheckSupported = false

func isCurrentProcessElevated() bool { return false }

func isProcessElevated(int32) bool { return false }

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
