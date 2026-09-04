package main

import "sync"

// sigCheckResult is one relayed background signature-check outcome, handed
// from the goroutine that ran checkFileSignature to signatureWatcher.check's
// next call on the main goroutine -- same handoff shape as avmonitor.go's
// avScanSighting/avScanRelay, needed for the same reason: the check itself
// (WinVerifyTrustEx / catalog lookup, see signature_windows.go) is real disk
// and crypto work, too slow to run inline on the UI goroutine every tick.
type sigCheckResult struct {
	pid    int32
	result SignatureResult
}

// sigCheckSem bounds real concurrent signature-check work (WinVerifyTrust and
// catalog-hash calls) to a small, fixed number regardless of how many
// per-process goroutines signatureWatcher.check has launched -- simplicity
// over a real work-queue, same tradeoff avmonitor.go and interference.go's
// module-scan cost already make.
var sigCheckSem = make(chan struct{}, 2)

// sigChecksPerTick caps how many *new* checks signatureWatcher.check launches
// in a single call -- a fresh launch against a process list of hundreds would
// otherwise fire hundreds of goroutines (and file opens) in the same instant.
// Spreading this over several ~2s ticks instead is exactly the "populate as
// possible" behavior the backlog asked for, at near-zero added complexity.
const sigChecksPerTick = 8

// signatureWatcher runs checkFileSignature in the background, at most once
// per PID's lifetime, and caches the result for the Signed column
// (procview.go's updateCell). Modeled on interferenceWatcher's exeCache/prune
// (interference.go) for the "check once per PID, prune on exit" half, and on
// avmonitor.go's avScanRelay for the "hand results back from a foreign
// goroutine" half. Every field except relay is only ever touched from the
// main goroutine (signatureWatcher.check, called from procview.go's
// recompute -- see procViewState's own doc comment on the same assumption),
// so only relay needs its own mutex.
type signatureWatcher struct {
	results  map[int32]SignatureResult // latest known result per PID
	inFlight map[int32]bool            // PIDs with a check already launched, not yet relayed back

	relay struct {
		mu      sync.Mutex
		pending []sigCheckResult
	}
}

func newSignatureWatcher() *signatureWatcher {
	return &signatureWatcher{
		results:  make(map[int32]SignatureResult),
		inFlight: make(map[int32]bool),
	}
}

// offer is called from a background check goroutine to hand back its result.
func (w *signatureWatcher) offer(pid int32, result SignatureResult) {
	w.relay.mu.Lock()
	w.relay.pending = append(w.relay.pending, sigCheckResult{pid: pid, result: result})
	w.relay.mu.Unlock()
}

// drain returns and clears every result relayed since the last call.
func (w *signatureWatcher) drain() []sigCheckResult {
	w.relay.mu.Lock()
	defer w.relay.mu.Unlock()
	if len(w.relay.pending) == 0 {
		return nil
	}
	out := w.relay.pending
	w.relay.pending = nil
	return out
}

// check is called once per recompute() tick. It folds in any results that
// finished since the last call, drops stale state for PIDs no longer
// running, and (when enabled) launches a bounded batch of new checks for
// not-yet-seen PIDs. Returns the current per-PID cache directly for
// updateCell to read -- same shape as interferenceWatcher.check's returned
// flaggedPIDs map.
func (w *signatureWatcher) check(byPID map[int32]ProcInfo, enabled bool) map[int32]SignatureResult {
	for _, r := range w.drain() {
		delete(w.inFlight, r.pid)
		if _, alive := byPID[r.pid]; alive {
			w.results[r.pid] = r.result
		}
	}

	for pid := range w.results {
		if _, alive := byPID[pid]; !alive {
			delete(w.results, pid)
		}
	}
	for pid := range w.inFlight {
		if _, alive := byPID[pid]; !alive {
			delete(w.inFlight, pid)
		}
	}

	if !enabled || !signatureCheckSupported {
		return w.results
	}

	launched := 0
	for pid := range byPID {
		if launched >= sigChecksPerTick {
			break
		}
		if _, checked := w.results[pid]; checked {
			continue
		}
		if w.inFlight[pid] {
			continue
		}
		w.inFlight[pid] = true
		launched++
		go func(pid int32) {
			result := SignatureResult{Status: SignatureUnknown}
			if exe := resolveProcessExe(pid); exe != "" {
				sigCheckSem <- struct{}{}
				if r, err := checkFileSignature(exe); err == nil {
					result = r
				}
				<-sigCheckSem
			}
			w.offer(pid, result)
		}(pid)
	}

	return w.results
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
