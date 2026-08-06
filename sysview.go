package main

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

var (
	graphColorCPU  = color.NRGBA{R: 0x3d, G: 0x8b, B: 0xfd, A: 0xff} // blue
	graphColorMem  = color.NRGBA{R: 0x9b, G: 0x59, B: 0xd6, A: 0xff} // purple
	graphColorDisk = color.NRGBA{R: 0xf3, G: 0x9c, B: 0x12, A: 0xff} // orange
	graphColorNet  = color.NRGBA{R: 0x2e, G: 0xcc, B: 0x71, A: 0xff} // green
)

// newSystemGraphsView builds the top strip of live CPU/Memory/Disk/Network
// graphs. onTap is called when the user clicks a mini graph, to open the
// bigger single-resource view (see resourceview.go's Resource Details
// window) already showing that resource. The returned update func must only
// be called from the main goroutine (i.e. from inside fyne.Do) since it
// calls Sparkline.Push.
func newSystemGraphsView(onTap func(resourceKind)) (view fyne.CanvasObject, update func(SystemSnapshot)) {
	cpuGraph := NewSparkline(graphColorCPU, scaleFixed0to100, sparklineHistoryLen)
	memGraph := NewSparkline(graphColorMem, scaleFixed0to100, sparklineHistoryLen)
	diskGraph := NewSparkline(graphColorDisk, scaleAuto, sparklineHistoryLen)
	netGraph := NewSparkline(graphColorNet, scaleAuto, sparklineHistoryLen)

	cpuGraph.OnTapped = func() { onTap(resCPU) }
	memGraph.OnTapped = func() { onTap(resMem) }
	diskGraph.OnTapped = func() { onTap(resDisk) }
	netGraph.OnTapped = func() { onTap(resNet) }

	cpuLabel := widget.NewLabel("CPU: --")
	memLabel := widget.NewLabel("Memory: --")
	diskLabel := widget.NewLabel("Disk: --")
	netLabel := widget.NewLabel("Network: --")

	view = container.NewGridWithColumns(4,
		container.NewBorder(cpuLabel, nil, nil, nil, cpuGraph),
		container.NewBorder(memLabel, nil, nil, nil, memGraph),
		container.NewBorder(diskLabel, nil, nil, nil, diskGraph),
		container.NewBorder(netLabel, nil, nil, nil, netGraph),
	)

	update = func(s SystemSnapshot) {
		cpuGraph.Push(s.CPUPercent)
		cpuLabel.SetText(fmt.Sprintf("CPU: %.0f%%", s.CPUPercent))

		memGraph.Push(s.MemPercent)
		memLabel.SetText(fmt.Sprintf("Memory: %.0f%% (%s / %s)", s.MemPercent, formatBytes(s.MemUsedBytes), formatBytes(s.MemTotalBytes)))

		diskTotal := s.DiskReadKBs + s.DiskWriteKBs
		diskGraph.Push(diskTotal)
		diskLabel.SetText(fmt.Sprintf("Disk: %.0f KB/s (R %.0f / W %.0f)", diskTotal, s.DiskReadKBs, s.DiskWriteKBs))

		netTotal := s.NetRecvKBs + s.NetSentKBs
		netGraph.Push(netTotal)
		netLabel.SetText(fmt.Sprintf("Network: %.0f KB/s (↓ %.0f / ↑ %.0f)", netTotal, s.NetRecvKBs, s.NetSentKBs))
	}

	return view, update
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
