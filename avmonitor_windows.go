//go:build windows

package main

import (
	"context"
	"sync"

	"github.com/tekert/goetw/etw"
)

// avMonitorSupported gates whether this app even attempts the 4th
// Interference Watch signal -- ETW is Windows-only. Doesn't gate elevation
// specifically (see startAVMonitor's doc comment) -- that's handled by the
// session simply failing to start, silently, same as any other best-effort
// background enhancement.
const avMonitorSupported = true

var (
	avMonitorMu       sync.Mutex
	avMonitorSession  *etw.RealTimeSession
	avMonitorConsumer *etw.Consumer
	avMonitorCancel   context.CancelFunc
)

// startAVMonitor subscribes to Microsoft-Antimalware-AMFilter (Windows
// Defender's own minifilter) and starts consuming its file-scan events in
// the background, feeding matches for currently-watched processes into
// globalAVScanRelay for interference.go's checkAVFileScan to pick up next
// check() cycle.
//
// Confirmed via a real-world spike this session (a throwaway prototype
// against the real box, not part of this file): creating a new real-time
// ETW session needs Administrator rights regardless of which provider gets
// enabled afterward, same requirement Capture Trace already has. Rather
// than surface that as an error anywhere, this is called unconditionally
// from main.go and simply does nothing useful if it fails -- Interference
// Watch's other three signals work fine without this one; it's a bonus
// signal when running elevated, not something the user explicitly asked
// for the way starting a capture is, so there's no dialog to show for "it
// didn't start."
//
// Two AMFilter tasks are used, out of everything the provider emits:
//
//   - AMFilter_FileScan/"OnOpen": a real file-scan interception, the
//     scanning-triggered process's real PID in the event header. Confirmed
//     against real PIDs during the original spike (an injected
//     explorer.exe, Chrome touching its own profile files) -- but also
//     confirmed via later real-world testing that this task never fires
//     at all for a well-known, Microsoft-signed "trusted" process
//     (notepad.exe: zero AMFilter_FileScan events despite a completed
//     file save) -- Defender appears to fast-track trusted processes
//     through a different code path that skips this event.
//   - AMFilter_TrustedProcess/Reason=="create": added as a fallback for
//     exactly that gap -- fires reliably for trusted processes too, but
//     carries no file path at all (its own Path field is always "NULL"),
//     and -- unlike every other field used in this file -- the
//     correlating PID is in the event *payload* ("Pid"), not the header's
//     Execution.ProcessID (that's the Defender component emitting it,
//     e.g. MsMpEng.exe, confirmed by that field being a stable low PID
//     shared across many unrelated subject processes in the same dump).
//
// Not used: Microsoft-Antimalware-RTP's own file-scan-result task has the
// *engine's* PID in the event header too, not the scanned process's, so
// it's no easier to correlate than AMFilter_FileScan already is; the
// Engine provider's "Behavior Monitoring" task is rich (it even captured
// this project's own test/injection tooling's exact OpenProcess call) but
// very high-volume and system-wide, a bigger scope than this pass.
func startAVMonitor() error {
	avMonitorMu.Lock()
	defer avMonitorMu.Unlock()
	if avMonitorSession != nil {
		return nil // already running
	}

	s := etw.NewRealTimeSession("KrankyBearProcessMinerAVMonitor")
	provider, err := etw.ParseProvider("Microsoft-Antimalware-AMFilter")
	if err != nil {
		return err
	}
	if err := s.EnableProvider(provider); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := etw.NewConsumer(ctx)
	c.FromSessions(s)
	c.EventCallback = func(e *etw.EventRecordHelper) error {
		meta := e.System()
		switch meta.Task.Name {
		case "AMFilter_FileScan":
			reason, err := e.GetPropertyString("Reason")
			if err != nil || reason != "OnOpen" {
				return nil // OnClose carries no new information for this signal
			}
			fileName, _ := e.GetPropertyString("FileName")
			globalAVScanRelay.offer(int32(meta.Execution.ProcessID), fileName, false)
		case "AMFilter_TrustedProcess":
			reason, err := e.GetPropertyString("Reason")
			if err != nil || reason != "create" {
				return nil // "trust"/"exit" carry no new information for this signal
			}
			pid, err := e.GetPropertyInt("Pid") // payload field, NOT meta.Execution.ProcessID -- see doc comment above
			if err != nil {
				return nil
			}
			globalAVScanRelay.offer(int32(pid), "", true)
		}
		return nil
	}

	if err := c.Start(); err != nil {
		cancel()
		s.Stop()
		return err
	}

	avMonitorSession = s
	avMonitorConsumer = c
	avMonitorCancel = cancel
	return nil
}

// stopAVMonitor tears down the session/consumer -- called from quitApp,
// same "stop background work first" slot procSampler/cancelCapture already
// occupy. A no-op if startAVMonitor never actually got one running (e.g.
// not elevated).
func stopAVMonitor() {
	avMonitorMu.Lock()
	defer avMonitorMu.Unlock()
	if avMonitorSession == nil {
		return
	}
	avMonitorCancel()
	avMonitorConsumer.Stop()
	avMonitorSession.Stop()
	avMonitorSession = nil
	avMonitorConsumer = nil
	avMonitorCancel = nil
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
