package main

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type sparklineScale int

const (
	scaleFixed0to100 sparklineScale = iota
	scaleAuto
)

const sparklineHistoryLen = 120 // ~2 minutes of history at the 1s system-sample cadence

// sparklineAxisDivisions is how many gridlines/labels each axis gets when
// ShowAxes is true, beyond the 0-fraction one -- 4 divisions means 5
// gridlines per axis (0%, 25%, 50%, 75%, 100%-style spacing).
const sparklineAxisDivisions = 4

// sparklineAxisTextSize is deliberately smaller than the theme default --
// these are compact axis labels, not body text.
const sparklineAxisTextSize = 11

// Sparkline is a small live strip-chart rendered as a filled area graph over
// a fixed-length sample history. One widget type serves every graph in the
// system strip (CPU/Mem fixed 0-100% scale, Disk/Net auto-scaled to the
// buffer's own max) -- only color, scale mode, and history length differ per
// instance (the Resource Details window's big graphs use a longer history
// than the top-strip minis, via historyLen in NewSparkline).
type Sparkline struct {
	widget.BaseWidget
	col      color.Color
	scale    sparklineScale
	history  []float64
	count    int // real samples pushed so far, capped at len(history)
	OnTapped func()

	// ShowAxes, Unit, and SampleInterval only matter for the Resource
	// Details window's big graphs (see resourceview.go) -- the top-strip
	// minis leave these at their zero values and render exactly as before.
	ShowAxes       bool
	Unit           string        // value-axis label suffix, e.g. "%" or " KB/s"
	SampleInterval time.Duration // real time one Push represents, for the time axis
}

func NewSparkline(col color.Color, scale sparklineScale, historyLen int) *Sparkline {
	s := &Sparkline{col: col, scale: scale, history: make([]float64, historyLen)}
	s.ExtendBaseWidget(s)
	return s
}

// Tapped lets a Sparkline open a bigger view of itself when OnTapped is set
// (see sysview.go's mini graphs -> resourceview.go's Resource Details
// window). A nil OnTapped (e.g. a Sparkline used only inside the Resource
// Details window itself) makes this a no-op, not a non-tappable widget --
// Fyne only shows the pointer cursor via Cursor() below regardless.
func (s *Sparkline) Tapped(*fyne.PointEvent) {
	if s.OnTapped != nil {
		s.OnTapped()
	}
}

// Cursor hints that this widget is clickable when OnTapped is set (the
// top-strip minis), and falls back to the normal cursor otherwise (the
// Resource Details window's own big graphs, which aren't themselves
// clickable).
func (s *Sparkline) Cursor() desktop.Cursor {
	if s.OnTapped != nil {
		return desktop.PointerCursor
	}
	return desktop.DefaultCursor
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

// visibleMax returns the value-axis ceiling for the currently visible
// samples -- always 100 for the fixed 0-100% scale, or the buffer's own
// running max for auto-scale. Shared by draw() and the axis-label
// rendering so the two can never disagree about the scale in use.
func (s *Sparkline) visibleMax() float64 {
	if s.scale != scaleAuto || s.count == 0 {
		return 100
	}
	maxV := 1.0 // floor avoids a divide-by-zero when the buffer is all-idle
	for _, v := range s.history[len(s.history)-s.count:] {
		if v > maxV {
			maxV = v
		}
	}
	return maxV
}

func (s *Sparkline) CreateRenderer() fyne.WidgetRenderer {
	raster := canvas.NewRaster(s.draw)
	r := &sparklineRenderer{s: s, raster: raster}
	if s.ShowAxes {
		gridColor := theme.Color(theme.ColorNameDisabled)
		textColor := theme.Color(theme.ColorNameForeground)
		for i := 0; i <= sparklineAxisDivisions; i++ {
			r.valueLines = append(r.valueLines, canvas.NewLine(gridColor))
			r.timeLines = append(r.timeLines, canvas.NewLine(gridColor))

			vLabel := canvas.NewText("", textColor)
			vLabel.TextSize = sparklineAxisTextSize
			r.valueLabels = append(r.valueLabels, vLabel)

			tLabel := canvas.NewText("", textColor)
			tLabel.TextSize = sparklineAxisTextSize
			r.timeLabels = append(r.timeLabels, tLabel)
		}
	}
	return r
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

	maxV := s.visibleMax()
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

// formatAxisTime renders a time-ago label for the time axis: "now" at the
// right edge (elapsed 0), "-M:SS" further left.
func formatAxisTime(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	d = d.Round(time.Second)
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("-%d:%02d", m, s)
}

type sparklineRenderer struct {
	s        *Sparkline
	raster   *canvas.Raster
	lastSize fyne.Size

	// Populated only when s.ShowAxes; nil (and skipped) otherwise -- the
	// top-strip minis pay no cost for axes they don't use.
	valueLines  []*canvas.Line
	valueLabels []*canvas.Text
	timeLines   []*canvas.Line
	timeLabels  []*canvas.Text
}

func (r *sparklineRenderer) Destroy() {}

func (r *sparklineRenderer) Layout(size fyne.Size) {
	r.raster.Resize(size)
	r.lastSize = size
	r.refreshAxes(size)
}

func (r *sparklineRenderer) MinSize() fyne.Size {
	return fyne.NewSize(120, 48)
}

func (r *sparklineRenderer) Objects() []fyne.CanvasObject {
	objects := []fyne.CanvasObject{r.raster}
	for _, l := range r.valueLines {
		objects = append(objects, l)
	}
	for _, l := range r.timeLines {
		objects = append(objects, l)
	}
	for _, t := range r.valueLabels {
		objects = append(objects, t)
	}
	for _, t := range r.timeLabels {
		objects = append(objects, t)
	}
	return objects
}

func (r *sparklineRenderer) Refresh() {
	r.raster.Refresh()
	r.refreshAxes(r.lastSize)
}

// refreshAxes recomputes gridline positions and label text/position from
// the current data and widget size. Cheap enough to call on every Push
// (called via Refresh): at most sparklineAxisDivisions+1 lines/labels per
// axis, not per-pixel work like draw().
func (r *sparklineRenderer) refreshAxes(size fyne.Size) {
	if !r.s.ShowAxes || size.Width <= 0 || size.Height <= 0 {
		return
	}
	w, h := size.Width, size.Height
	textSize := float32(sparklineAxisTextSize)
	style := fyne.TextStyle{}

	maxV := r.s.visibleMax()
	for i, line := range r.valueLines {
		frac := float32(i) / float32(sparklineAxisDivisions)
		y := frac * h
		line.Position1 = fyne.NewPos(0, y)
		line.Position2 = fyne.NewPos(w, y)
		line.Refresh()

		label := r.valueLabels[i]
		if i == sparklineAxisDivisions {
			// Skip the bottom (0-value) label: it collides with the
			// leftmost time-axis label in the same corner, and 0 is the
			// obvious baseline of a filled area chart anyway.
			label.Text = ""
			label.Refresh()
			continue
		}
		value := maxV * float64(1-frac)
		label.Text = fmt.Sprintf("%.0f%s", value, r.s.Unit)
		ty := y - textSize/2
		switch {
		case ty < 0:
			ty = 0
		case ty+textSize > h:
			ty = h - textSize
		}
		label.Move(fyne.NewPos(2, ty))
		label.Refresh()
	}

	total := time.Duration(r.s.count) * r.s.SampleInterval
	for i, line := range r.timeLines {
		frac := float32(i) / float32(sparklineAxisDivisions)
		x := frac * w
		line.Position1 = fyne.NewPos(x, 0)
		line.Position2 = fyne.NewPos(x, h)
		line.Refresh()

		elapsed := time.Duration(float64(total) * float64(1-frac))
		label := r.timeLabels[i]
		label.Text = formatAxisTime(elapsed)
		tw := fyne.MeasureText(label.Text, textSize, style).Width
		tx := x - tw/2
		switch {
		case tx < 0:
			tx = 0
		case tx+tw > w:
			tx = w - tw
		}
		label.Move(fyne.NewPos(tx, h-textSize-2))
		label.Refresh()
	}
}
