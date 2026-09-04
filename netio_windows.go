//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/tekert/goetw/etw"
)

// netIOSupported gates procview.go's "Net Send"/"Net Recv"/"Disk Latency"
// columns and the "Show Network/Disk I/O" checkbox that drives them --
// ETW is Windows-only, same as the 4th Interference Watch signal
// (avmonitor_windows.go) and Capture Trace.
const netIOSupported = true

var (
	netIOMu         sync.Mutex
	netIOSession    *etw.RealTimeSession
	netIOConsumer   *etw.Consumer
	netIOCancel     context.CancelFunc
	netIOUsedLegacy bool // which session type is live -- see startNetIOMonitor

	netIOTidToPidMu sync.RWMutex
	netIOTidToPid   map[uint32]int32
)

// windows11BuildNumber is the first Windows 11 build (21996 was the last
// Insider-only Windows 10-labeled build; 22000 shipped as the public Windows
// 11 release) -- https://learn.microsoft.com/en-us/windows/release-health/windows11-release-information
const windows11BuildNumber = 22000

// isWindows11OrLater checks the real OS build via RtlGetVersion (already
// wrapped in golang.org/x/sys/windows, no hand-rolled syscall needed) rather
// than any version string gopsutil reports -- this gates which of the two
// session mechanisms startNetIOMonitor uses, so it needs the actual kernel
// build number, not a display string.
func isWindows11OrLater() bool {
	return windows.RtlGetVersion().BuildNumber >= windows11BuildNumber
}

// netIOUsingLegacySession reports whether the currently-running session (if
// any) is the legacy singleton "NT Kernel Logger" -- capture_windows.go's
// startCapture checks this before starting a WPR trace, since wpr.exe's own
// Network/DiskIO profiles use that exact same singleton session (confirmed in
// goetw's own NewKernelRealTimeSession doc comment: starting a new one stops
// whichever is already running). Always false on the modern per-provider
// session (Windows 11+), which isn't the singleton and so can't conflict.
func netIOUsingLegacySession() bool {
	netIOMu.Lock()
	defer netIOMu.Unlock()
	return netIOSession != nil && netIOUsedLegacy
}

// startNetIOMonitor begins consuming live network (TcpIp/UdpIp) and disk
// (DiskIo) kernel events, feeding globalNetIOWatcher via offerNet/
// offerDiskLatency for procview.go's "Show Network/Disk I/O" checkbox.
//
// Tiered by OS version (decided after confirming both mechanisms are fully
// supported by goetw -- see netio.go's package doc / ReleaseNotes.txt):
//   - Windows 11+: etw.NewSystemTraceSession + the modern SystemIoProviderGuid
//     (SYSTEM_IO_KW_DISK | SYSTEM_IO_KW_NETWORK). This is NOT the singleton NT
//     Kernel Logger session, so it can run alongside Capture Trace freely.
//   - Windows 10: falls back to the legacy etw.NewKernelRealTimeSession, the
//     same singleton session wpr.exe's own Network/DiskIO profiles use --
//     refused up front if a Capture Trace session is already running
//     (captureIsActive, capture_windows.go) rather than silently stopping it,
//     the same "surface a clear reason" spirit as wrapWPRError's admin-rights
//     message.
//
// NOTE (resolved 2026-09-04, real Windows 11 25H2 hardware): the modern
// SystemIoProviderGuid path's event Task/Opcode/property names were assumed
// to be an undocumented, possibly-different manifest-provider shape,
// confirmed instead to be byte-for-byte identical to the legacy path's --
// netio-debug.jsonl from a real capture showed every event still carrying
// ProviderGUID {9E814AAD-3204-11D2-9A82-006008A86939} (the classic "NT
// Kernel Logger" GUID) with the exact same TcpIp/UdpIp/DiskIo Task names,
// SendIPV4/RecvIPV4/ConnectIPV4/etc. Opcode names, and PID/size/daddr/saddr/
// dport/sport/connid/IssuingThreadId/HighResResponseTime properties
// netIOEventCallback already decodes. The modern SystemTraceSession
// mechanism turned out to be a different *session/enablement* API over the
// same underlying kernel provider, not a differently-shaped one -- so no
// decode changes were needed at all, and an end-to-end run confirmed real
// moving values in the Net Send/Net Recv/Disk Latency columns and
// Connections window. A prior attempt had found zero events on this same
// path; root cause of that not reproduced, but this run's evidence (a full
// netio-debug.jsonl capture plus working UI columns) is what actually
// shipping the modern path below is now based on -- not the earlier
// zero-event finding.
func startNetIOMonitor() error {
	netIOMu.Lock()
	defer netIOMu.Unlock()
	if netIOSession != nil {
		return nil // already running
	}

	// Windows 11+: the modern SystemIoProviderGuid session (not the singleton
	// NT Kernel Logger, so it runs alongside Capture Trace freely -- see the
	// NOTE above and netIOUsingLegacySession's doc comment). Windows 10:
	// falls back to the legacy singleton session, which conflicts with
	// Capture Trace and is refused up front below.
	legacy := !isWindows11OrLater()
	if legacy && captureIsActive() {
		return fmt.Errorf("Network/Disk I/O monitoring can't start while Capture Trace is running on this Windows version -- they use the same underlying kernel session. Stop the capture first, or use Windows 11+ where both can run together")
	}

	var s *etw.RealTimeSession
	if legacy {
		// NewKernelRealTimeSession only configures EnableFlags in memory --
		// unlike EnableProvider (used below for the modern path, and by
		// avmonitor_windows.go's proven-working AMFilter session), it has no
		// side effect that calls the real StartTrace API. Found via real-
		// world testing: without this explicit Start(), Consumer.Start()
		// below still succeeds (it only *opens a trace handle* to a session
		// by name -- see its own "// TODO: make is auto start the sessions"
		// comment in goetw's consumer.go) but there is no actual running "NT
		// Kernel Logger" session behind that handle, so zero events ever
		// arrive and nothing looks wrong until you check for data and find
		// none.
		s = etw.NewKernelRealTimeSession(etw.DiskIo, etw.TcpIp, etw.UdpIp)
		if err := s.Start(); err != nil {
			return err
		}
	} else {
		s = etw.NewSystemTraceSession("KrankyBearProcessMinerNetIO")
		provider := etw.Provider{
			GUID:            *etw.SystemIoProviderGuid,
			MatchAnyKeyword: etw.SYSTEM_IO_KW_DISK | etw.SYSTEM_IO_KW_NETWORK,
		}
		if err := s.EnableProvider(provider); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := etw.NewConsumer(ctx)
	c.FromSessions(s)
	c.EventCallback = netIOEventCallback

	if err := c.Start(); err != nil {
		cancel()
		s.Stop()
		return err
	}

	netIOSession = s
	netIOConsumer = c
	netIOCancel = cancel
	netIOUsedLegacy = legacy
	globalNetIOWatcher.reset()
	resetNetIODebugLog()
	return nil
}

// stopNetIOMonitor tears down the session/consumer -- called from quitApp
// (same "stop background work first" slot as stopAVMonitor) and from
// procview.go's checkbox when the user turns it back off. A no-op if nothing
// is running.
func stopNetIOMonitor() {
	netIOMu.Lock()
	defer netIOMu.Unlock()
	if netIOSession == nil {
		return
	}
	netIOCancel()
	netIOConsumer.Stop()
	netIOSession.Stop()
	netIOSession = nil
	netIOConsumer = nil
	netIOCancel = nil
	globalNetIOWatcher.reset()
}

// netIOEventCallback decodes TcpIp/UdpIp send/receive/connect/disconnect and
// DiskIo completion events and relays them to globalNetIOWatcher.
//
// Opcode names are matched by prefix, not exact string: real captured events
// (netio-debug.jsonl from a real Windows box -- see logNetIOEventShapeOnce)
// showed the real classic-provider opcode names carry an "IPV4"/"IPV6"
// suffix this code didn't originally expect -- "SendIPV4"/"SendIPV6",
// "RecvIPV4"/"RecvIPV6", "ConnectIPV4"/"ConnectIPV6",
// "DisconnectIPV4"/"DisconnectIPV6", "AcceptIPV4" (no IPv6 sighting yet, but
// AcceptIPV6 presumably exists the same way ConnectIPV6/DisconnectIPV6 do),
// vs. the bare "Send"/"Recv"/"Connect"/"Accept"/"Disconnect" this switch
// checked before that log existed. Prefix matching handles both address
// families with one code path instead of enumerating every suffix, and
// means IPv6 traffic decodes here too -- the property names and formatting
// (GetPropertyString on "daddr"/"saddr") are address-family-agnostic, this
// was never actually IPv4-only, just under-tested. DiskIo's "Read"/"Write"
// opcode names have no such suffix in the same capture, so that case is
// still matched exactly.
func netIOEventCallback(e *etw.EventRecordHelper) error {
	meta := e.System()
	logNetIOEventShapeOnce(e, meta)
	switch meta.Task.Name {
	case "TcpIp", "UdpIp":
		op := meta.Opcode.Name
		switch {
		case strings.HasPrefix(op, "Send"):
			pid, err := e.GetPropertyInt("PID")
			if err != nil {
				return nil
			}
			size, err := e.GetPropertyUint("size")
			if err != nil {
				return nil
			}
			globalNetIOWatcher.offerNet(int32(pid), size, true)
			if meta.Task.Name == "TcpIp" {
				offerTCPConnSightingIfNew(e)
			}
		case strings.HasPrefix(op, "Recv"):
			pid, err := e.GetPropertyInt("PID")
			if err != nil {
				return nil
			}
			size, err := e.GetPropertyUint("size")
			if err != nil {
				return nil
			}
			globalNetIOWatcher.offerNet(int32(pid), size, false)
			if meta.Task.Name == "TcpIp" {
				offerTCPConnSightingIfNew(e)
			}
		case strings.HasPrefix(op, "Connect"):
			offerTCPConnEvent(e, tcpConnOutbound)
		case strings.HasPrefix(op, "Accept"):
			offerTCPConnEvent(e, tcpConnInbound)
		case strings.HasPrefix(op, "Disconnect"):
			id, err := e.GetPropertyUint("connid")
			if err != nil {
				return nil
			}
			globalNetIOWatcher.offerDisconnect(connID(id))
		}
	case "DiskIo":
		switch meta.Opcode.Name {
		case "Read", "Write":
			tid, err := e.GetPropertyUint("IssuingThreadId")
			if err != nil {
				return nil
			}
			latencyTicks, err := e.GetPropertyUint("HighResResponseTime")
			if err != nil {
				return nil
			}
			pid, ok := lookupPidForTid(uint32(tid))
			if !ok {
				return nil // thread exited, or the snapshot hasn't caught up yet -- dropped silently, same best-effort precedent as avmonitor_windows.go's stale-session handling
			}
			// HighResResponseTime is documented in 100ns units, same as the
			// NT tick unit ntTicksToDuration (threads_windows.go) already
			// converts elsewhere in this codebase.
			latencyMs := float64(latencyTicks) / 10000
			globalNetIOWatcher.offerDiskLatency(pid, latencyMs)
		}
	}
	return nil
}

// offerTCPConnSightingIfNew is the Send/Recv-side half of the tcpConnExisting
// fallback (see that constant's doc comment in netio.go): it exists so a
// sustained high-throughput transfer -- Send/Recv fire once per network
// packet -- doesn't pay offerTCPConnEvent's full PID/address/port property
// extraction on every single packet for the rest of a connection's life.
// GetPropertyUint("connid") alone is a cheap, unavoidable check (it's how
// IsConnKnown even knows what to look up); the rest only runs the first time
// a given connid is seen.
func offerTCPConnSightingIfNew(e *etw.EventRecordHelper) {
	id, err := e.GetPropertyUint("connid")
	if err != nil || globalNetIOWatcher.IsConnKnown(connID(id)) {
		return
	}
	offerTCPConnEvent(e, tcpConnExisting)
}

// offerTCPConnEvent decodes a TcpIp Connect, Accept, Send, or Recv event --
// all four share the same PID/daddr/saddr/dport/sport/connid fields (Send/
// Recv carry a few extra byte-count/timing fields Connect/Accept don't, and
// vice versa, but every field this function reads is common to all of them),
// so one implementation covers both the real "a connection was just
// established" signal (direction is tcpConnOutbound/tcpConnInbound, from the
// Connect/Accept case) and the "this connection already existed" fallback
// signal (direction is tcpConnExisting, from the Send/Recv case -- see that
// constant's doc comment in netio.go for why this fallback exists at all).
func offerTCPConnEvent(e *etw.EventRecordHelper, direction tcpConnDirection) {
	pid, err := e.GetPropertyInt("PID")
	if err != nil {
		return
	}
	id, err := e.GetPropertyUint("connid")
	if err != nil {
		return
	}
	localAddr, err := e.GetPropertyString("saddr")
	if err != nil {
		return
	}
	localPort, err := e.GetPropertyUint("sport")
	if err != nil {
		return
	}
	remoteAddr, err := e.GetPropertyString("daddr")
	if err != nil {
		return
	}
	remotePort, err := e.GetPropertyUint("dport")
	if err != nil {
		return
	}
	globalNetIOWatcher.offerConn(connID(id), int32(pid), direction, localAddr, uint16(localPort), remoteAddr, uint16(remotePort))
}

// refreshTidToPidSnapshot rebuilds the TID->PID map netIOEventCallback uses
// to attribute DiskIo completions (which carry no PID at all, only
// IssuingThreadId -- see this file's package doc). Called once per
// netIOWatcher.check() cycle from the main goroutine; read from the ETW
// callback goroutine under netIOTidToPidMu, the one piece of shared state
// here that's genuinely touched from two goroutines.
//
// Adapts threads_windows.go's querySystemProcessInformation +
// SYSTEM_PROCESS_INFORMATION/systemThreadInformation buffer-walking loop
// (there scoped to one target PID via extractThreadSummary) into a full-buffer
// scan across every process -- same underlying syscall and struct layout,
// already verified against real hardware for the single-PID case.
func refreshTidToPidSnapshot() {
	buf, err := querySystemProcessInformation()
	if err != nil {
		return // leave the previous snapshot in place rather than clearing it on a transient failure
	}

	m := make(map[uint32]int32)
	const procInfoSize = unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{})
	const threadInfoSize = unsafe.Sizeof(systemThreadInformation{})
	offset := uint32(0)
	for {
		if int(offset)+int(procInfoSize) > len(buf) {
			break // malformed/truncated buffer -- stop rather than read out of bounds
		}
		proc := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[offset]))
		threadsOffset := offset + uint32(procInfoSize)
		for i := uint32(0); i < proc.NumberOfThreads; i++ {
			o := threadsOffset + i*uint32(threadInfoSize)
			if int(o)+int(threadInfoSize) > len(buf) {
				break
			}
			t := (*systemThreadInformation)(unsafe.Pointer(&buf[o]))
			m[uint32(t.UniqueThread)] = int32(proc.UniqueProcessID)
		}
		if proc.NextEntryOffset == 0 {
			break
		}
		offset += proc.NextEntryOffset
	}

	netIOTidToPidMu.Lock()
	netIOTidToPid = m
	netIOTidToPidMu.Unlock()
}

func lookupPidForTid(tid uint32) (int32, bool) {
	netIOTidToPidMu.RLock()
	defer netIOTidToPidMu.RUnlock()
	pid, ok := netIOTidToPid[tid]
	return pid, ok
}

// netIODebugMaxShapes bounds netio-debug.jsonl's growth -- one real-world
// session realistically has a handful of distinct (provider, task, opcode)
// shapes (TcpIp/Send, TcpIp/Recv, DiskIo/Read, DiskIo/Write, ...), so this
// cap is generous headroom, not a real limit expected to bind.
const netIODebugMaxShapes = 60

var (
	netIODebugMu   sync.Mutex
	netIODebugSeen map[string]bool
)

// resetNetIODebugLog clears the dedup set so a fresh startNetIOMonitor run
// (e.g. after switching legacy/modern, or just relaunching) logs its shapes
// again instead of assuming a previous run's are still representative.
func resetNetIODebugLog() {
	netIODebugMu.Lock()
	netIODebugSeen = make(map[string]bool)
	netIODebugMu.Unlock()
}

// netIODebugLogPath is where logNetIOEventShapeOnce writes -- same
// <UserConfigDir>/<appConfigDirName()>/ convention as the update checker's
// cache and the persisted watchlist (util.go/watchpersist.go).
func netIODebugLogPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, appConfigDirName(), "netio-debug.jsonl")
}

// logNetIOEventShapeOnce writes a full JSON dump of e.TraceInfo (provider/
// task/opcode names plus every property's name and type -- TraceEventInfo's
// own MarshalJSON, not hand-rolled) the first time a given
// (provider, task, opcode) combination is seen in this session, capped at
// netIODebugMaxShapes distinct shapes.
//
// This exists because netIOEventCallback's Task.Name/Opcode.Name/property-name
// checks are a best-effort guess -- the classic MOF fields are documented on
// learn.microsoft.com and cross-checked against goetw's own TDH-comparison
// test suite, but there was no way to verify them against this app's actual
// target hardware without running on it. If the columns come up blank, this
// log is real ground truth for what got captured instead of guessing again
// -- the same "verify against real captured events, not documentation or
// assumption" standard 0.5.0's AMFilter work already established for this
// project (see ReleaseNotes.txt).
func logNetIOEventShapeOnce(e *etw.EventRecordHelper, meta *etw.SystemMetadata) {
	key := meta.Provider.Name + "|" + meta.Task.Name + "|" + meta.Opcode.Name

	netIODebugMu.Lock()
	if netIODebugSeen == nil {
		netIODebugSeen = make(map[string]bool)
	}
	if netIODebugSeen[key] || len(netIODebugSeen) >= netIODebugMaxShapes {
		netIODebugMu.Unlock()
		return
	}
	netIODebugSeen[key] = true
	netIODebugMu.Unlock()

	path := netIODebugLogPath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	b, err := json.Marshal(e.TraceInfo)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(b)
	f.WriteString("\n")
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
