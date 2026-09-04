package main

import (
	"sort"
	"sync"
	"time"
)

// netIOStats is one process's latest per-process network throughput and disk
// I/O latency figures, backing the "Net Send"/"Net Recv"/"Disk Latency"
// main-table columns (see procview.go's updateCell) -- Phase 2 ETW continued,
// the "real per-process network usage and I/O latency" item ReleaseNotes.txt's
// Future ideas left open. DiskLatencySamples distinguishes "never seen a
// completion for this PID" (blank column, same convention signatureColumnGlyph
// already uses for "not yet checked") from a genuine 0ms latency. ConnCount
// backs the "Connections" column -- a quick main-table-visible signal for
// which of several same-named processes (e.g. a dozen Chrome renderers) are
// actually talking to the network right now, without opening each one's own
// Connections window to check.
type netIOStats struct {
	NetSendKBs         float64
	NetRecvKBs         float64
	DiskLatencyMs      float64
	DiskLatencySamples int32
	ConnCount          int
}

// netIOEvent is one relayed raw sighting from the ETW consumer's callback
// goroutine (netio_windows.go) to netIOWatcher.check's next call on the main
// goroutine -- same handoff shape as avmonitor.go's avScanSighting/avScanRelay
// and signature_watcher.go's sigCheckResult, needed for the same reason: the
// event source runs on a foreign goroutine, and this app's convention is a
// small mutex-protected relay rather than retrofitting synchronization onto
// state that's otherwise main-goroutine-only.
//
// kind selects which fields are valid, the same "one struct, a discriminant,
// several optional payloads" shape isDiskLatency already used before this
// added a third payload -- a union type would need the same discriminant
// anyway, and Go has no union types.
type netIOEvent struct {
	pid           int32
	sentBytes     uint64
	recvBytes     uint64
	diskLatencyMs float64
	kind          netIOEventKind
	conn          tcpConnSighting // valid when kind is netIOEventConnect or netIOEventDisconnect
}

type netIOEventKind int

const (
	netIOEventThroughput  netIOEventKind = iota // sentBytes/recvBytes valid
	netIOEventDiskLatency                       // diskLatencyMs valid
	netIOEventConnect                           // conn valid, conn.disconnect false
	netIOEventDisconnect                        // conn valid (only conn.id meaningful)
)

// tcpConnDirection distinguishes which side of a TCP connection this process
// was on -- ETW's Connect opcode fires for the dialing side, Accept for the
// listening side that took an inbound connection. There is no separate
// "established" event, so this is the closest a live per-process connections
// list gets to a state label (see tcpConnection).
//
// tcpConnExisting covers a real gap found via real-world testing: Connect/
// Accept only fire at the moment a connection is established, so a
// connection that was already open before "Show Network/Disk I/O" was
// turned on (e.g. a browser's kept-alive HTTP/2 connection, reused for a
// large download started well after the tab was first opened) never
// produces one -- it would otherwise be entirely invisible to this
// connections list despite generating a steady stream of very real
// Send/Recv traffic. netIOEventCallback's Send/Recv handling opportunistically
// treats the first Send/Recv sighting of a not-yet-tracked connid as a
// substitute "connection exists" signal for exactly this case (Connect/
// Accept share the same connid/address fields anyway); this direction
// value records that this entry's existence, not its direction, is all
// that's actually known.
type tcpConnDirection int

const (
	tcpConnOutbound tcpConnDirection = iota // this process called connect()
	tcpConnInbound                          // this process accepted an inbound connection
	tcpConnExisting                         // first seen via Send/Recv, not Connect/Accept -- see doc comment above
)

func (d tcpConnDirection) String() string {
	switch d {
	case tcpConnInbound:
		return "Inbound"
	case tcpConnExisting:
		return "Existing"
	default:
		return "Outbound"
	}
}

// connID is the kernel's per-connection identifier (the TcpIp MOF "connid"
// property, a TCB pointer value) -- used as the map key for connActive below
// instead of the 4-tuple (local/remote addr+port) because it stays stable
// and unique for the connection's lifetime, which a 4-tuple isn't guaranteed
// to be under rapid port reuse.
type connID uint64

// tcpConnSighting is the Connect/Accept/Disconnect payload relayed from
// netio_windows.go's netIOEventCallback -- see tcpConnection for the
// long-lived record it becomes once applied in check().
type tcpConnSighting struct {
	id         connID
	pid        int32
	direction  tcpConnDirection
	localAddr  string
	localPort  uint16
	remoteAddr string
	remotePort uint16
}

// tcpConnection is one live TCP connection currently attributed to a
// process, decoded from the same TcpIp ETW session netIOWatcher already
// runs for the Net Send/Net Recv columns -- see netio_windows.go's
// netIOEventCallback for the Connect/Accept/Disconnect opcodes this comes
// from. Both IPv4 and IPv6: real captured events (netio-debug.jsonl) showed
// both address families arrive under the same "TcpIp" task, distinguished
// only by an "IPV4"/"IPV6" opcode-name suffix -- there's no separate
// "TcpIpV6" task the way an earlier version of this comment assumed.
type tcpConnection struct {
	PID        int32
	LocalAddr  string
	LocalPort  uint16
	RemoteAddr string
	RemotePort uint16
	Direction  tcpConnDirection
	Since      time.Time
}

// netIOWatcher accumulates raw ETW-relayed byte counts and latency samples
// per PID between check() calls, then converts them into rates (KB/s) and
// averages on each call -- the same "diff since last sample" idea
// procsampler.go's Disk R/W columns already use, just diffing ETW-pushed
// counters instead of gopsutil-polled cumulative ones (there's no cumulative
// counter to diff here; the ETW events themselves are the deltas, so this
// just sums them up and divides by elapsed wall-clock time). Every field
// except relay is only ever touched from the main goroutine (check(), called
// from procview.go's recompute()), matching signatureWatcher's own documented
// assumption.
type netIOWatcher struct {
	sentAccum    map[int32]uint64
	recvAccum    map[int32]uint64
	latencySum   map[int32]float64
	latencyCount map[int32]int32
	results      map[int32]netIOStats
	lastCheck    time.Time

	// connActive is every TCP connection currently believed open, keyed by
	// the kernel's connid -- populated on Connect/Accept, removed on
	// Disconnect (see check()). Backs connectionsview.go's live per-process
	// connections window the same way results backs the Net Send/Net Recv
	// columns, except there's nothing to reset every tick: a connection
	// stays listed until its own Disconnect event removes it.
	connActive map[connID]tcpConnection

	// knownConn mirrors connActive's key set, but is safe to read from the
	// ETW callback goroutine (connActive itself isn't -- see its own doc
	// comment). IsConnKnown lets netio_windows.go's Send/Recv handling skip
	// the extra PID/address/port property extraction it does to catch
	// already-open connections (see tcpConnExisting) once a connid is
	// already tracked, rather than paying that cost on every single packet
	// for the rest of the connection's life -- Send/Recv fire once per
	// network packet, so this is a genuinely hot path during a sustained
	// high-throughput transfer, the same "don't do more per-event work than
	// necessary" lesson CLAUDE.md's gopsutil sampling gotcha already
	// established for per-tick work.
	knownConnMu sync.Mutex
	knownConn   map[connID]struct{}

	relay struct {
		mu      sync.Mutex
		pending []netIOEvent
	}
}

// IsConnKnown reports whether id is already tracked in connActive -- safe to
// call from any goroutine, unlike touching connActive directly. See
// knownConn's doc comment.
func (w *netIOWatcher) IsConnKnown(id connID) bool {
	w.knownConnMu.Lock()
	defer w.knownConnMu.Unlock()
	_, ok := w.knownConn[id]
	return ok
}

// globalNetIOWatcher is the single instance backing procview.go's "Show
// Network/Disk I/O" checkbox -- package-level (not owned by procViewState)
// since netio_windows.go's ETW consumer callback runs on its own goroutine,
// started/stopped independently of any particular view, and needs a stable
// reference to relay into. Same package-level-singleton shape as avmonitor.go's
// globalAVScanRelay, and read the same way (procViewState.recompute() calls
// globalNetIOWatcher.check() directly, mirroring interference.go's own
// globalAVScanRelay.drain() call).
var globalNetIOWatcher = newNetIOWatcher()

func newNetIOWatcher() *netIOWatcher {
	return &netIOWatcher{
		sentAccum:    make(map[int32]uint64),
		recvAccum:    make(map[int32]uint64),
		latencySum:   make(map[int32]float64),
		latencyCount: make(map[int32]int32),
		results:      make(map[int32]netIOStats),
		connActive:   make(map[connID]tcpConnection),
		knownConn:    make(map[connID]struct{}),
	}
}

// offerNet is called from the ETW consumer's callback goroutine for every
// decoded TcpIp/UdpIp send or receive event.
func (w *netIOWatcher) offerNet(pid int32, bytes uint64, isSend bool) {
	ev := netIOEvent{pid: pid}
	if isSend {
		ev.sentBytes = bytes
	} else {
		ev.recvBytes = bytes
	}
	w.relay.mu.Lock()
	w.relay.pending = append(w.relay.pending, ev)
	w.relay.mu.Unlock()
}

// offerDiskLatency is called from the ETW consumer's callback goroutine for
// every DiskIo completion event whose IssuingThreadId resolved to a PID (see
// netio_windows.go's TID->PID snapshot) -- events that don't resolve are
// dropped by the caller before ever reaching here.
func (w *netIOWatcher) offerDiskLatency(pid int32, latencyMs float64) {
	w.relay.mu.Lock()
	w.relay.pending = append(w.relay.pending, netIOEvent{pid: pid, diskLatencyMs: latencyMs, kind: netIOEventDiskLatency})
	w.relay.mu.Unlock()
}

// offerConn is called from the ETW consumer's callback goroutine for every
// decoded TcpIp Connect (outbound) or Accept (inbound) event -- see
// netio_windows.go's netIOEventCallback.
func (w *netIOWatcher) offerConn(id connID, pid int32, direction tcpConnDirection, localAddr string, localPort uint16, remoteAddr string, remotePort uint16) {
	ev := netIOEvent{
		kind: netIOEventConnect,
		conn: tcpConnSighting{
			id:         id,
			pid:        pid,
			direction:  direction,
			localAddr:  localAddr,
			localPort:  localPort,
			remoteAddr: remoteAddr,
			remotePort: remotePort,
		},
	}
	w.relay.mu.Lock()
	w.relay.pending = append(w.relay.pending, ev)
	w.relay.mu.Unlock()
}

// offerDisconnect is called from the ETW consumer's callback goroutine for
// every decoded TcpIp Disconnect event -- id is enough, the connection's
// other details are already in connActive from the Connect/Accept that
// added it.
func (w *netIOWatcher) offerDisconnect(id connID) {
	w.relay.mu.Lock()
	w.relay.pending = append(w.relay.pending, netIOEvent{kind: netIOEventDisconnect, conn: tcpConnSighting{id: id}})
	w.relay.mu.Unlock()
}

func (w *netIOWatcher) drain() []netIOEvent {
	w.relay.mu.Lock()
	defer w.relay.mu.Unlock()
	if len(w.relay.pending) == 0 {
		return nil
	}
	out := w.relay.pending
	w.relay.pending = nil
	return out
}

// check is called once per recompute() tick. When enabled is false (the
// checkbox is off, or the platform doesn't support this at all -- see
// netIOSupported), it returns the last-known results unchanged without
// draining the relay -- the actual session is stopped/never started in that
// case (see procview.go's checkbox wiring), so there's nothing new to drain
// anyway; this just avoids a stale accumulation window skewing the next rate
// calculation if the checkbox gets re-enabled later.
func (w *netIOWatcher) check(byPID map[int32]ProcInfo, enabled bool) map[int32]netIOStats {
	if !enabled || !netIOSupported {
		return w.results
	}

	// Refreshed once per tick, before draining -- see netio_windows.go's doc
	// comment on why DiskIo completion attribution needs a fresh TID->PID
	// snapshot (a no-op on platforms where this feature doesn't exist).
	refreshTidToPidSnapshot()

	now := time.Now()
	for _, ev := range w.drain() {
		switch ev.kind {
		case netIOEventDiskLatency:
			w.latencySum[ev.pid] += ev.diskLatencyMs
			w.latencyCount[ev.pid]++
		case netIOEventConnect:
			// A tcpConnExisting sighting (see that constant's doc comment)
			// fires on every single Send/Recv for a connection, not once --
			// skip it once the connection is already tracked so it doesn't
			// keep resetting Since or downgrade a real Connect/Accept's
			// direction back to "first seen via traffic, direction unknown".
			// A genuine Connect/Accept, in contrast, only ever fires once per
			// connection, so always applying it below is correct either way.
			if _, tracked := w.connActive[ev.conn.id]; tracked && ev.conn.direction == tcpConnExisting {
				continue
			}
			w.connActive[ev.conn.id] = tcpConnection{
				PID:        ev.conn.pid,
				LocalAddr:  ev.conn.localAddr,
				LocalPort:  ev.conn.localPort,
				RemoteAddr: ev.conn.remoteAddr,
				RemotePort: ev.conn.remotePort,
				Direction:  ev.conn.direction,
				Since:      now,
			}
			w.knownConnMu.Lock()
			w.knownConn[ev.conn.id] = struct{}{}
			w.knownConnMu.Unlock()
		case netIOEventDisconnect:
			delete(w.connActive, ev.conn.id)
			w.knownConnMu.Lock()
			delete(w.knownConn, ev.conn.id)
			w.knownConnMu.Unlock()
		default: // netIOEventThroughput
			w.sentAccum[ev.pid] += ev.sentBytes
			w.recvAccum[ev.pid] += ev.recvBytes
		}
	}

	// The first tick after (re)starting has no prior lastCheck to diff
	// against -- report no rate rather than a huge one-off spike, the same
	// fix 0.2.0 applied to the top-strip Disk/Network graphs for the same
	// "diffed against a zero/stale baseline" mistake (see ReleaseNotes.txt).
	firstTick := w.lastCheck.IsZero()
	elapsed := now.Sub(w.lastCheck).Seconds()
	w.lastCheck = now
	if firstTick || elapsed <= 0 {
		w.sentAccum = make(map[int32]uint64)
		w.recvAccum = make(map[int32]uint64)
		w.latencySum = make(map[int32]float64)
		w.latencyCount = make(map[int32]int32)
		return w.results
	}

	// Counted fresh each tick from connActive (itself only touched by the
	// Connect/Accept/Disconnect drain above) rather than accumulated like
	// sent/recv/latency -- a connection count is a snapshot, not a rate, so
	// there's nothing to diff against elapsed time or reset afterward.
	connCounts := make(map[int32]int, len(w.connActive))
	for _, c := range w.connActive {
		connCounts[c.PID]++
	}

	results := make(map[int32]netIOStats, len(w.sentAccum)+len(w.latencyCount)+len(connCounts))
	for pid := range byPID {
		sent, hasSent := w.sentAccum[pid]
		recv, hasRecv := w.recvAccum[pid]
		latSum, hasLat := w.latencySum[pid]
		latCount := w.latencyCount[pid]
		connCount := connCounts[pid]
		if !hasSent && !hasRecv && !hasLat && connCount == 0 {
			continue
		}
		stats := netIOStats{
			NetSendKBs: float64(sent) / 1024 / elapsed,
			NetRecvKBs: float64(recv) / 1024 / elapsed,
			ConnCount:  connCount,
		}
		if latCount > 0 {
			stats.DiskLatencyMs = latSum / float64(latCount)
			stats.DiskLatencySamples = latCount
		}
		results[pid] = stats
	}
	w.results = results

	// Reset accumulators for the next window -- unlike sigWatcher's cache
	// (which keeps a result forever once known), a rate has to be recomputed
	// from zero each tick or it would just keep growing.
	w.sentAccum = make(map[int32]uint64)
	w.recvAccum = make(map[int32]uint64)
	w.latencySum = make(map[int32]float64)
	w.latencyCount = make(map[int32]int32)

	return w.results
}

// reset clears every accumulated/known figure -- called when the checkbox
// turns off (stopping the session) so stale numbers don't linger on screen
// looking live, and when it turns back on so the first post-restart tick
// doesn't compute a rate against a stale lastCheck timestamp. Connections
// are cleared too: the session that was tracking them just stopped, so
// there's no way to know when any of them actually close, and relisting
// them once the session restarts (as fresh Connect/Accept sightings won't
// happen for connections that were already established) would just be
// stale, confident-looking data.
func (w *netIOWatcher) reset() {
	w.sentAccum = make(map[int32]uint64)
	w.recvAccum = make(map[int32]uint64)
	w.latencySum = make(map[int32]float64)
	w.latencyCount = make(map[int32]int32)
	w.results = make(map[int32]netIOStats)
	w.connActive = make(map[connID]tcpConnection)
	w.knownConnMu.Lock()
	w.knownConn = make(map[connID]struct{})
	w.knownConnMu.Unlock()
	w.lastCheck = time.Time{}
}

// ConnectionsForPID returns a stable-ordered snapshot of pid's currently
// tracked TCP connections -- called from connectionsview.go every
// applySnapshot tick while a Connections window is open, reading what
// check() already maintained rather than triggering any fetch of its own.
func (w *netIOWatcher) ConnectionsForPID(pid int32) []tcpConnection {
	var out []tcpConnection
	for _, c := range w.connActive {
		if c.PID == pid {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RemoteAddr != out[j].RemoteAddr {
			return out[i].RemoteAddr < out[j].RemoteAddr
		}
		return out[i].RemotePort < out[j].RemotePort
	})
	return out
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
