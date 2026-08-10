package main

import "sync"

// avScanSighting is one relayed sighting from Defender's AMFilter minifilter
// -- handed from the ETW consumer's own callback goroutine
// (avmonitor_windows.go) to interferenceWatcher.check(), which runs on the
// main goroutine every recompute() tick, via avScanRelay below.
//
// trustEval distinguishes the two distinct signals sharing this same relay
// (see EventAVFileScan vs EventAVTrustEval in threads.go for the full
// reasoning): false means a real file-scan interception (fileName set,
// from AMFilter_FileScan); true means a trust-evaluation registration (no
// file involved at all -- fileName is unused -- from
// AMFilter_TrustedProcess), the fallback added after real-world testing
// found the file-scan signal never fires for trusted/Microsoft-signed
// processes like notepad.exe.
type avScanSighting struct {
	pid       int32
	fileName  string
	trustEval bool
}

// avScanRelay is the one piece of new state in this feature that actually
// needs synchronization: interferenceWatcher itself is documented as
// "only ever touched from the main goroutine," and stays that way here too
// -- rather than retrofitting a mutex onto that whole pre-existing struct
// (and every direct field read interferenceview.go already does on it),
// this small, separate relay is the single handoff point between the async
// ETW callback goroutine (offer) and the main goroutine's next check() cycle
// (setWatchedPIDs + drain). No build tag needed here -- pure Go, no ETW
// types -- only avmonitor_windows.go's actual consumer needs one.
type avScanRelay struct {
	mu          sync.Mutex
	watchedPIDs map[int32]bool
	pending     []avScanSighting
}

var globalAVScanRelay = &avScanRelay{}

// setWatchedPIDs replaces the relay's watched-PID set -- called once per
// check() cycle with the same target set the other three signals use
// (interferenceWatcher.activeTargets), so a directory watch extends to this
// signal too, not just explicit PID watches.
func (r *avScanRelay) setWatchedPIDs(pids map[int32]bool) {
	r.mu.Lock()
	r.watchedPIDs = pids
	r.mu.Unlock()
}

// offer is called from the ETW consumer's callback goroutine for every
// decoded scan/trust-eval event -- silently dropped if pid isn't currently
// watched (checked here, under the relay's own lock, rather than reading
// interferenceWatcher's state directly from a foreign goroutine).
func (r *avScanRelay) offer(pid int32, fileName string, trustEval bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.watchedPIDs[pid] {
		return
	}
	r.pending = append(r.pending, avScanSighting{pid: pid, fileName: fileName, trustEval: trustEval})
}

// drain returns and clears every sighting accumulated since the last call
// -- called once per check() cycle, on the main goroutine.
func (r *avScanRelay) drain() []avScanSighting {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) == 0 {
		return nil
	}
	out := r.pending
	r.pending = nil
	return out
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
