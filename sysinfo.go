package main

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	fynetooltip "github.com/dweymouth/fyne-tooltip"
	ttwidget "github.com/dweymouth/fyne-tooltip/widget"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// SystemInfo is a point-in-time snapshot of mostly-static system info
// (computer name, OS, CPU, memory, disks, network adapters), gathered on
// demand when the System Info window opens -- unlike Sampler's continuous
// per-second polling, none of this needs to be live.
type SystemInfo struct {
	Hostname        string
	OS              string // e.g. "darwin", "windows", "linux"
	Platform        string // e.g. "darwin", "Microsoft Windows 11 Pro", "ubuntu"
	PlatformVersion string
	KernelArch      string // e.g. "arm64", "x86_64"

	CPUModel   string
	CPUCores   int     // physical
	CPUThreads int     // logical
	CPUMhz     float64 // meaningless (not necessarily 0) if unavailable -- see formatCPUSpeed

	MemTotal uint64

	Disks       []DiskVolumeInfo
	NetAdapters []NetAdapterInfo
}

// DiskVolumeInfo is a mounted volume, not a raw physical disk -- gopsutil
// has no cross-platform physical-disk enumeration (that needs deeper
// per-platform APIs: diskutil/WMI/smartctl), so this is the pragmatic
// gopsutil-based proxy, same granularity as "Disk 0 (C:)" vs. a raw device
// would be if we went further than what's readily available.
type DiskVolumeInfo struct {
	Device     string
	Mountpoint string
	TotalBytes uint64
}

func gatherSystemInfo() SystemInfo {
	var info SystemInfo

	if hi, err := host.Info(); err == nil {
		info.Hostname = hi.Hostname
		info.OS = hi.OS
		info.Platform = hi.Platform
		info.PlatformVersion = hi.PlatformVersion
		info.KernelArch = hi.KernelArch
	}

	if n, err := cpu.Counts(false); err == nil {
		info.CPUCores = n
	}
	if n, err := cpu.Counts(true); err == nil {
		info.CPUThreads = n
	}
	if stats, err := cpu.Info(); err == nil && len(stats) > 0 {
		info.CPUModel = stats[0].ModelName
		info.CPUMhz = stats[0].Mhz
	}

	if vm, err := mem.VirtualMemory(); err == nil {
		info.MemTotal = vm.Total
	}

	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			usage, err := disk.Usage(p.Mountpoint)
			if err != nil {
				continue
			}
			info.Disks = append(info.Disks, DiskVolumeInfo{
				Device:     p.Device,
				Mountpoint: p.Mountpoint,
				TotalBytes: usage.Total,
			})
		}
	}

	info.NetAdapters = gatherNetAdapters()

	return info
}

var sysInfoWindow fyne.Window

// sysInfoOpen tracks whether the System Info window should be considered
// "open" for hide-all/show-all purposes (windows.go) -- see aboutOpen in
// about.go.
var sysInfoOpen bool

// sysInfoGathering guards against a second background gather starting if
// the user closes and reopens the window while the first one is still in
// flight (gatherSystemInfo can take 10+ seconds -- see below).
var sysInfoGathering bool

// showSystemInfo opens the window immediately with a loading placeholder,
// then fills in the real content once gatherSystemInfo finishes in the
// background. That gather is re-run fresh each time the window goes from
// closed to open (it's a point-in-time summary, not a live view), and can
// legitimately take 10+ seconds: on macOS, WiFi signal strength shells out
// to system_profiler, which alone takes ~8s on this machine regardless of
// -detailLevel, and there's no faster non-privileged alternative (wdutil
// info is near-instant but requires sudo; the legacy airport binary that
// used to be fast no longer exists on current macOS). Without this loading
// state, that delay reads as the app being frozen.
func showSystemInfo(a fyne.App) {
	if sysInfoWindow != nil && sysInfoOpen {
		sysInfoWindow.Show()
		sysInfoWindow.RequestFocus()
		return
	}

	if sysInfoWindow == nil {
		sysInfoWindow = a.NewWindow(appName + " - System Info")
		sysInfoWindow.SetIcon(resourceKrankyBearProcessMinerPng)
		sysInfoWindow.SetCloseIntercept(func() {
			sysInfoOpen = false
			sysInfoWindow.Hide()
		})
	}

	sysInfoWindow.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewPadded(sysInfoLoadingContent()), sysInfoWindow.Canvas()))
	sysInfoWindow.Resize(fyne.NewSize(460, 200))
	sysInfoOpen = true
	sysInfoWindow.Show()
	sysInfoWindow.RequestFocus()

	if sysInfoGathering {
		return // already fetching from a previous open -- don't start a second one
	}
	sysInfoGathering = true
	go func() {
		info := gatherSystemInfo()
		fyne.Do(func() {
			sysInfoGathering = false
			if sysInfoWindow == nil {
				return // quit() or similar tore it down while this was in flight
			}
			renderSystemInfo(sysInfoWindow, info)
		})
	}()
}

func sysInfoLoadingContent() fyne.CanvasObject {
	msg := widget.NewLabel("Collecting system info…\nThis can take 10-20 seconds or a little more, depending on your hardware.")
	msg.Wrapping = fyne.TextWrapWord
	msg.Alignment = fyne.TextAlignCenter
	// Not wrapped in container.NewCenter: Center sizes to the child's own
	// MinSize, and an unconstrained wrapping Label's MinSize degenerates to
	// almost nothing -- it wraps character-by-character instead of at real
	// word boundaries. A VBox gives each child the container's full width
	// (only height is minimized), which is what wrapping needs; Alignment
	// above still centers the text itself within that width.
	return container.NewVBox(msg, widget.NewProgressBarInfinite())
}

func renderSystemInfo(win fyne.Window, info SystemInfo) {
	bold := fyne.TextStyle{Bold: true}

	computerLabel := widget.NewLabel(fmt.Sprintf("%s\n%s %s (%s)",
		orNA(info.Hostname), orNA(info.Platform), info.PlatformVersion, orNA(info.KernelArch)))
	computerLabel.Wrapping = fyne.TextWrapWord

	cpuLabel := widget.NewLabel(fmt.Sprintf("%s\n%d cores / %d logical processors @ %s",
		orNA(info.CPUModel), info.CPUCores, info.CPUThreads, formatCPUSpeed(info.CPUMhz)))
	cpuLabel.Wrapping = fyne.TextWrapWord

	memLabel := widget.NewLabel(formatBytes(info.MemTotal) + " total")

	const copyLabel = "Copy to Clipboard"
	var copyBtn *ttwidget.Button
	copyBtn = ttwidget.NewButton(copyLabel, func() {
		win.Clipboard().SetContent(formatSystemInfoText(info))
		// Clicking a button gives no other feedback that anything happened,
		// so flip the label to confirm the copy actually worked, then
		// revert after a couple seconds. AfterFunc's callback runs on its
		// own goroutine, hence fyne.Do (CLAUDE.md's fyne.Do-is-mandatory
		// rule).
		copyBtn.SetText("Copied to Clipboard ✓")
		copyBtn.Importance = widget.SuccessImportance
		copyBtn.Refresh()
		time.AfterFunc(2*time.Second, func() {
			fyne.Do(func() {
				copyBtn.SetText(copyLabel)
				copyBtn.Importance = widget.MediumImportance
				copyBtn.Refresh()
			})
		})
	})
	copyBtn.SetToolTip("Copy this whole summary as plain text -- handy for a bug report or comparing against another machine")

	content := container.NewVBox(
		widget.NewLabelWithStyle("Computer", fyne.TextAlignLeading, bold),
		computerLabel,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("CPU", fyne.TextAlignLeading, bold),
		cpuLabel,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Memory", fyne.TextAlignLeading, bold),
		memLabel,
		widget.NewSeparator(),
		widget.NewLabelWithStyle(fmt.Sprintf("Disks (%d)", len(info.Disks)), fyne.TextAlignLeading, bold),
		diskRows(info.Disks),
		widget.NewSeparator(),
		widget.NewLabelWithStyle(fmt.Sprintf("Network Adapters (%d)", len(info.NetAdapters)), fyne.TextAlignLeading, bold),
		netAdapterRows(info.NetAdapters),
	)

	scroll := container.NewVScroll(content)
	scroll.SetMinSize(fyne.NewSize(420, 420))

	win.SetContent(fynetooltip.AddWindowToolTipLayer(container.NewBorder(nil, container.NewPadded(copyBtn), nil, nil, container.NewPadded(scroll)), win.Canvas()))
	win.Resize(fyne.NewSize(460, 560))
}

// formatCPUSpeed shows N/A rather than a misleading speed reading. gopsutil
// doesn't report real clock speed on Apple Silicon, but not as a clean 0 --
// empirically it returns Mhz=4 on an M4 Pro (some other, meaningless
// counter), which formatted as "0.00 GHz" and slipped past a naive `<= 0`
// check. No real desktop/laptop CPU has run below 100MHz in decades, so
// that's used as the "not a real reading" floor instead of exactly 0.
func formatCPUSpeed(mhz float64) string {
	if mhz < 100 {
		return "N/A"
	}
	return fmt.Sprintf("%.2f GHz", mhz/1000)
}

// formatSystemInfoText renders the same information as renderSystemInfo
// into plain text, for the "Copy to Clipboard" button -- e.g. to paste into
// a bug report or a message comparing this against Task Manager/Process
// Explorer/Activity Monitor on another machine.
func formatSystemInfoText(info SystemInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s - System Info\n\n", appName)
	fmt.Fprintf(&b, "Computer: %s\n", orNA(info.Hostname))
	fmt.Fprintf(&b, "OS: %s %s (%s)\n\n", orNA(info.Platform), info.PlatformVersion, orNA(info.KernelArch))
	fmt.Fprintf(&b, "CPU: %s\n", orNA(info.CPUModel))
	fmt.Fprintf(&b, "%d cores / %d logical processors @ %s\n\n", info.CPUCores, info.CPUThreads, formatCPUSpeed(info.CPUMhz))
	fmt.Fprintf(&b, "Memory: %s total\n\n", formatBytes(info.MemTotal))

	fmt.Fprintf(&b, "Disks (%d):\n", len(info.Disks))
	if len(info.Disks) == 0 {
		b.WriteString("N/A\n")
	}
	for _, d := range info.Disks {
		fmt.Fprintf(&b, "- %s (%s) — %s\n", d.Device, d.Mountpoint, formatBytes(d.TotalBytes))
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "Network Adapters (%d):\n", len(info.NetAdapters))
	if len(info.NetAdapters) == 0 {
		b.WriteString("N/A\n")
	}
	for _, a := range info.NetAdapters {
		status := "Disconnected"
		if a.Connected {
			status = "Connected"
		}
		line := fmt.Sprintf("- %s — %s, %s", a.Name, a.Type, status)
		if a.HasSignal {
			line += fmt.Sprintf(", Signal %d%%", a.SignalPercent)
		}
		b.WriteString(line + "\n")
	}

	return b.String()
}

func diskRows(disks []DiskVolumeInfo) fyne.CanvasObject {
	if len(disks) == 0 {
		return widget.NewLabel("N/A")
	}
	rows := make([]fyne.CanvasObject, len(disks))
	for i, d := range disks {
		label := widget.NewLabel(fmt.Sprintf("%s (%s) — %s", d.Device, d.Mountpoint, formatBytes(d.TotalBytes)))
		label.Wrapping = fyne.TextWrapWord
		rows[i] = label
	}
	return container.NewVBox(rows...)
}

func netAdapterRows(adapters []NetAdapterInfo) fyne.CanvasObject {
	if len(adapters) == 0 {
		return widget.NewLabel("N/A")
	}
	rows := make([]fyne.CanvasObject, len(adapters))
	for i, a := range adapters {
		status := "Disconnected"
		if a.Connected {
			status = "Connected"
		}
		text := fmt.Sprintf("%s — %s, %s", a.Name, a.Type, status)
		if a.HasSignal {
			text += fmt.Sprintf(", Signal %d%%", a.SignalPercent)
		}
		label := widget.NewLabel(text)
		label.Wrapping = fyne.TextWrapWord
		rows[i] = label
	}
	return container.NewVBox(rows...)
}

// "Now this is not the end. It is not even the beginning of the end. But it is, perhaps, the end of the beginning." Winston Churchill, November 10, 1942
