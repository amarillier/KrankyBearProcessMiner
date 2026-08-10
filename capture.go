package main

// captureProfile is one WPR (Windows Performance Recorder) built-in
// recording profile this app offers as a checkbox in the Capture Trace
// window (see captureview.go). name is the exact string wpr.exe expects
// after "-start". These four map directly to gaps this app documents
// elsewhere (per-process network usage, I/O latency, and the minifilter
// file-scan interference signal Interference Watch can't see) -- see
// README's Known limitations.
type captureProfile struct {
	name    string
	label   string
	tooltip string
}

var captureProfiles = []captureProfile{
	{"Network", "Network", "Per-process network send/receive activity -- the network-usage gap this app's Top CPU/Mem filters and Resource Details' Network view can't fill on their own"},
	{"DiskIO", "Disk I/O", "Per-process disk I/O activity, including latency -- deeper than the throughput-only Disk R/W columns already shown"},
	{"FileIO", "File I/O", "Per-process file I/O activity -- which files, not just how many bytes"},
	{"Minifilter", "Minifilter", "Minifilter driver activity -- the *other* meaning of \"AV interference\" (synchronous file-scan latency from a security product's filter driver), which Interference Watch's own three signals don't cover"},
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
