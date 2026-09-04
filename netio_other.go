//go:build !windows

package main

// netIOSupported gates procview.go's "Net Send"/"Net Recv"/"Disk Latency"
// columns and the "Show Network/Disk I/O" checkbox -- ETW is Windows-only.
const netIOSupported = false

func startNetIOMonitor() error { return nil }

func stopNetIOMonitor() {}

func netIOUsingLegacySession() bool { return false }

// refreshTidToPidSnapshot is a no-op here -- see netio_windows.go for the
// real implementation, needed there to attribute DiskIo completion events
// (which carry no PID, only IssuingThreadId) back to a process.
func refreshTidToPidSnapshot() {}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
