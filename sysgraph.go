package main

import (
	"image"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

type sparklineScale int

const (
	scaleFixed0to100 sparklineScale = iota
	scaleAuto
)

const sparklineHistoryLen = 120 // ~2 minutes of history at the 1s system-sample cadence

// Sparkline is a small live strip-chart rendered as a filled area graph over
// a fixed-length sample history. One widget type serves every graph in the
// system strip (CPU/Mem fixed 0-100% scale, Disk/Net auto-scaled to the
// buffer's own max) -- only color and scale mode differ per instance.
type Sparkline struct {
	widget.BaseWidget
	col     color.Color
	scale   sparklineScale
	history []float64
	count   int // real samples pushed so far, capped at len(history)
}

func NewSparkline(col color.Color, scale sparklineScale) *Sparkline {
	s := &Sparkline{col: col, scale: scale, history: make([]float64, sparklineHistoryLen)}
	s.ExtendBaseWidget(s)
	return s
}

// Push appends a new sample, dropping the oldest, and repaints. Must be
// called on the main goroutine (i.e. from inside fyne.Do).
func (s *Sparkline) Push(v float64) {
	copy(s.history, s.history[1:])
	s.history[len(s.history)-1] = v
	if s.count < len(s.history) {
		s.count++
	}
	s.Refresh()
}

func (s *Sparkline) CreateRenderer() fyne.WidgetRenderer {
	raster := canvas.NewRaster(s.draw)
	return &sparklineRenderer{raster: raster}
}

func (s *Sparkline) draw(w, h int) image.Image {
	if w <= 0 || h <= 0 {
		return image.NewNRGBA(image.Rect(0, 0, 1, 1))
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	if s.count == 0 {
		return img // nothing pushed yet -- blank rather than a wall of stale zeros
	}

	// Only the real samples (the trailing s.count entries) are plotted, and
	// they're stretched across the full width from the first Push onward --
	// otherwise the fixed-length buffer's leading zeros dominate the chart
	// and the real data reads as a sliver crammed against the right edge
	// until the buffer fills up naturally (~2 minutes in).
	samples := s.history[len(s.history)-s.count:]
	n := s.count

	maxV := 100.0
	if s.scale == scaleAuto {
		maxV = 1.0 // floor avoids a divide-by-zero when the buffer is all-idle
		for _, v := range samples {
			if v > maxV {
				maxV = v
			}
		}
	}

	fillCol := translucent(s.col, 70)

	for x := 0; x < w; x++ {
		idx := x * n / w
		if idx >= n {
			idx = n - 1
		}
		frac := samples[idx] / maxV
		switch {
		case frac < 0:
			frac = 0
		case frac > 1:
			frac = 1
		}
		barTop := h - int(frac*float64(h))
		for y := 0; y < h; y++ {
			switch {
			case y == barTop:
				img.Set(x, y, s.col)
			case y > barTop:
				img.Set(x, y, fillCol)
			}
		}
	}
	return img
}

func translucent(c color.Color, alpha uint8) color.Color {
	r, g, b, _ := c.RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: alpha}
}

type sparklineRenderer struct {
	raster *canvas.Raster
}

func (r *sparklineRenderer) Destroy() {}

func (r *sparklineRenderer) Layout(size fyne.Size) {
	r.raster.Resize(size)
}

func (r *sparklineRenderer) MinSize() fyne.Size {
	return fyne.NewSize(120, 48)
}

func (r *sparklineRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.raster}
}

func (r *sparklineRenderer) Refresh() {
	r.raster.Refresh()
}
