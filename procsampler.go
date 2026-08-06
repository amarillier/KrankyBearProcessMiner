package main

import (
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// SystemSnapshot is a single point-in-time read of system-wide resource usage.
type SystemSnapshot struct {
	Timestamp     time.Time
	CPUPercent    float64
	MemPercent    float64
	MemUsedBytes  uint64
	MemTotalBytes uint64
	DiskReadKBs   float64
	DiskWriteKBs  float64
	NetRecvKBs    float64
	NetSentKBs    float64
}

// ProcInfo describes one process as of the last process-list sample. Fields
// gopsutil couldn't read (permission-denied, exited mid-scan, OS doesn't
// expose it, etc.) are left at their zero value -- the UI renders those as
// "N/A" / "(unavailable)" rather than dropping the row or erroring.
type ProcInfo struct {
	PID, PPID    int32
	Name         string
	Username     string
	CPUPercent   float64
	MemPercent   float32
	RSSBytes     uint64
	DiskReadKBs  float64 // rate since the last sample; 0 on the first tick a PID is seen
	DiskWriteKBs float64
	// PrivateBytes is 0 ("N/A") on macOS: gopsutil's per-process VMS there is
	// reserved virtual address space (routinely >1TB for a Chrome renderer),
	// not real private memory, so showing it as "Private" would be actively
	// misleading. See platform notes in sampleProcesses.
	PrivateBytes uint64
}

// ProcDetail holds the process fields that are comparatively expensive to
// read -- on macOS, Status() forks a `ps` subprocess per call -- and are
// only ever shown for the single selected process. FetchProcessDetail reads
// these on demand instead of sampleProcesses fetching them for every
// process on every tick (previously the dominant CPU cost: one `ps` fork
// per process, per tick).
type ProcDetail struct {
	Cmdline    string
	CreateTime time.Time
	Status     string
}

// ProcessSnapshot is the full process list as of the last sample.
type ProcessSnapshot struct {
	Timestamp time.Time
	Procs     []ProcInfo
}

// Sampler periodically reads system and process stats in a single background
// goroutine and hands finished snapshots to caller-supplied callbacks. It
// never touches Fyne objects itself -- callers are expected to wrap their
// callbacks in fyne.Do (see CLAUDE.md's fyne.Do-is-mandatory rule).
type Sampler struct {
	sysTicker  *time.Ticker
	procTicker *time.Ticker
	refreshNow chan struct{}
	done       chan struct{}
	stopOnce   sync.Once

	onSystem  func(SystemSnapshot)
	onProcess func(ProcessSnapshot)

	// The following fields are only ever touched from loop(), which runs on
	// a single goroutine -- no mutex needed.
	prevSysTime                 time.Time
	sysBaselineSet              bool // false until the first sampleSystem call has recorded a baseline -- see its use below
	prevDiskRead, prevDiskWrite uint64
	prevNetRecv, prevNetSent    uint64
	procCache                   map[int32]*process.Process
	prevProcTime                time.Time
	prevProcIO                  map[int32]procIOBytes
}

// procIOBytes is the cumulative disk I/O byte counts gopsutil reported for a
// PID as of the last sample, so sampleProcesses can diff them into a rate
// the same way sampleSystem does for the system-wide totals.
type procIOBytes struct {
	read, write uint64
}

const (
	defaultSysInterval  = 1 * time.Second
	defaultProcInterval = 2 * time.Second
)

// NewSampler creates a Sampler with the default 1s system / 2s process-list
// intervals. Call Start to begin sampling.
func NewSampler() *Sampler {
	return &Sampler{
		sysTicker:  time.NewTicker(defaultSysInterval),
		procTicker: time.NewTicker(defaultProcInterval),
		refreshNow: make(chan struct{}, 1),
		done:       make(chan struct{}),
		procCache:  make(map[int32]*process.Process),
		prevProcIO: make(map[int32]procIOBytes),
	}
}

// OnSystemSnapshot sets the callback invoked after each system-wide sample.
// Must be called before Start.
func (s *Sampler) OnSystemSnapshot(fn func(SystemSnapshot)) { s.onSystem = fn }

// OnProcessSnapshot sets the callback invoked after each process-list sample.
// Must be called before Start.
func (s *Sampler) OnProcessSnapshot(fn func(ProcessSnapshot)) { s.onProcess = fn }

// Start begins sampling in a background goroutine.
func (s *Sampler) Start() {
	s.prevSysTime = time.Now()
	s.prevProcTime = time.Now()
	go s.loop()
}

// Stop halts the background goroutine. Safe to call multiple times. Must be
// called before app.Quit() -- see CLAUDE.md's teardown-order rule.
func (s *Sampler) Stop() {
	s.stopOnce.Do(func() { close(s.done) })
}

// RefreshProcessesNow requests an out-of-band process-list sample (e.g. after
// an End Process action) without waiting for the next tick. Non-blocking --
// a request already pending is not duplicated.
func (s *Sampler) RefreshProcessesNow() {
	select {
	case s.refreshNow <- struct{}{}:
	default:
	}
}

// SetProcessInterval changes how often the process list is resampled.
func (s *Sampler) SetProcessInterval(d time.Duration) {
	s.procTicker.Reset(d)
}

// EndProcess terminates pid. Errors (permission denied, already exited, etc.)
// are returned for the caller to surface, never panics.
func (s *Sampler) EndProcess(pid int32) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// FetchProcessDetail reads the expensive-to-fetch fields (Cmdline,
// CreateTime, Status) for a single pid, e.g. when the UI shows detail for
// the process the user has selected. Safe to call from any goroutine -- it
// opens its own process.Process handle and never touches procCache, which
// is otherwise only ever touched from loop().
func (s *Sampler) FetchProcessDetail(pid int32) (ProcDetail, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return ProcDetail{}, err
	}

	var d ProcDetail
	if cmdline, err := p.Cmdline(); err == nil {
		d.Cmdline = cmdline
	}
	if createMs, err := p.CreateTime(); err == nil {
		d.CreateTime = time.UnixMilli(createMs)
	}
	if statuses, err := p.Status(); err == nil && len(statuses) > 0 {
		d.Status = strings.Join(statuses, ",")
	}
	return d, nil
}

func (s *Sampler) loop() {
	for {
		select {
		case <-s.done:
			s.sysTicker.Stop()
			s.procTicker.Stop()
			return
		case <-s.sysTicker.C:
			s.sampleSystem()
		case <-s.procTicker.C:
			s.sampleProcesses()
		case <-s.refreshNow:
			s.sampleProcesses()
		}
	}
}

func clampDelta(cur, prev uint64) uint64 {
	if cur < prev {
		return 0 // counter reset/wrapped
	}
	return cur - prev
}

func (s *Sampler) sampleSystem() {
	now := time.Now()
	dt := now.Sub(s.prevSysTime).Seconds()
	if dt <= 0 {
		dt = defaultSysInterval.Seconds()
	}

	snap := SystemSnapshot{Timestamp: now}

	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		snap.CPUPercent = pcts[0]
	}

	if vm, err := mem.VirtualMemory(); err == nil {
		snap.MemPercent = vm.UsedPercent
		snap.MemUsedBytes = vm.Used
		snap.MemTotalBytes = vm.Total
	}

	// s.prevDiskRead etc. start at zero, indistinguishable from a genuine
	// previous reading -- without sysBaselineSet, the very first sample
	// would diff against 0 and report the *entire* cumulative bytes read
	// since boot as a one-tick "rate" (hundreds of GB / ~1s), an outlier
	// that then dominates the auto-scaled graph axis for as long as it
	// stays in the sparkline's buffer (2-5 minutes). Only record a baseline
	// on the first call; compute real deltas from the second call onward.
	var readBytes, writeBytes uint64
	if counters, err := disk.IOCounters(); err == nil {
		for _, c := range counters {
			readBytes += c.ReadBytes
			writeBytes += c.WriteBytes
		}
		if s.sysBaselineSet {
			snap.DiskReadKBs = float64(clampDelta(readBytes, s.prevDiskRead)) / 1024 / dt
			snap.DiskWriteKBs = float64(clampDelta(writeBytes, s.prevDiskWrite)) / 1024 / dt
		}
		s.prevDiskRead, s.prevDiskWrite = readBytes, writeBytes
	}

	if counters, err := net.IOCounters(false); err == nil && len(counters) > 0 {
		recv, sent := counters[0].BytesRecv, counters[0].BytesSent
		if s.sysBaselineSet {
			snap.NetRecvKBs = float64(clampDelta(recv, s.prevNetRecv)) / 1024 / dt
			snap.NetSentKBs = float64(clampDelta(sent, s.prevNetSent)) / 1024 / dt
		}
		s.prevNetRecv, s.prevNetSent = recv, sent
	}

	s.sysBaselineSet = true
	s.prevSysTime = now

	if s.onSystem != nil {
		s.onSystem(snap)
	}
}

func (s *Sampler) sampleProcesses() {
	procs, err := process.Processes()
	if err != nil {
		return
	}

	now := time.Now()
	dt := now.Sub(s.prevProcTime).Seconds()
	if dt <= 0 {
		dt = defaultProcInterval.Seconds()
	}

	newCache := make(map[int32]*process.Process, len(procs))
	newProcIO := make(map[int32]procIOBytes, len(procs))
	infos := make([]ProcInfo, 0, len(procs))

	for _, p := range procs {
		pid := p.Pid
		// Reuse the cached instance so stateful calls like CPUPercent() diff
		// against this process's own last sample rather than reporting a
		// meaningless since-start average on every tick.
		cached, ok := s.procCache[pid]
		if !ok {
			cached = p
		}
		newCache[pid] = cached

		name, err := cached.Name()
		if err != nil {
			continue // process exited mid-scan or is otherwise unreadable -- skip the row
		}
		ppid, err := cached.Ppid()
		if err != nil {
			continue
		}

		info := ProcInfo{PID: pid, PPID: ppid, Name: name}

		if cpuPct, err := cached.CPUPercent(); err == nil {
			info.CPUPercent = cpuPct
		}
		if memPct, err := cached.MemoryPercent(); err == nil {
			info.MemPercent = memPct
		}
		if mi, err := cached.MemoryInfo(); err == nil && mi != nil {
			info.RSSBytes = mi.RSS
			// On Windows gopsutil maps VMS to PagefileUsage -- genuinely
			// "Private Bytes" (Commit Charge), not virtual address space.
			if runtime.GOOS == "windows" {
				info.PrivateBytes = mi.VMS
			}
		}
		// linuxPrivateBytes is a no-op stub on non-Linux platforms (see
		// procsampler_other.go) -- not attempted on macOS: gopsutil's VMS
		// there is reserved virtual address space (often >1TB for a browser
		// renderer, see ProcInfo.PrivateBytes), and there's no cheap
		// smaps-equivalent gopsutil call to derive a real figure from.
		if pb := linuxPrivateBytes(cached); pb > 0 {
			info.PrivateBytes = pb
		}
		if user, err := cached.Username(); err == nil {
			info.Username = user
		}
		// IOCounters returns cumulative bytes since process start (like the
		// system-wide counters sampleSystem diffs) -- rate here, not gopsutil
		// diffing this itself. Permission-denied (e.g. a root-owned process
		// we don't own) leaves these at 0/"N/A", same as other restricted
		// fields.
		if counters, err := cached.IOCounters(); err == nil && counters != nil {
			if prev, ok := s.prevProcIO[pid]; ok {
				info.DiskReadKBs = float64(clampDelta(counters.DiskReadBytes, prev.read)) / 1024 / dt
				info.DiskWriteKBs = float64(clampDelta(counters.DiskWriteBytes, prev.write)) / 1024 / dt
			}
			newProcIO[pid] = procIOBytes{read: counters.DiskReadBytes, write: counters.DiskWriteBytes}
		}

		infos = append(infos, info)
	}

	sort.Slice(infos, func(i, j int) bool { return infos[i].PID < infos[j].PID })

	s.procCache = newCache
	s.prevProcIO = newProcIO
	s.prevProcTime = now

	if s.onProcess != nil {
		s.onProcess(ProcessSnapshot{Timestamp: time.Now(), Procs: infos})
	}
}
