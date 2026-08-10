//go:build !windows

package main

// avMonitorSupported gates the 4th Interference Watch signal (Defender
// AMFilter file-scan monitoring) -- ETW is Windows-only.
const avMonitorSupported = false

func startAVMonitor() error { return nil }

func stopAVMonitor() {}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
