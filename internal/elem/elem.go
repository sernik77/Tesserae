// Package elem draws dashboard elements into their rectangles.
package elem

import (
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/sernik77/tesserae/internal/config"
	"github.com/sernik77/tesserae/internal/data"
	"github.com/sernik77/tesserae/internal/render"
	"github.com/sernik77/tesserae/internal/tmpl"
)

// State is per-element runtime state owned by the app.
type State struct {
	Running bool      // button action in progress
	Flash   time.Time // last time the button was pressed
	Hover   bool
}

// Ctx carries everything a renderer needs.
type Ctx struct {
	Surf  render.Surface
	Theme *render.Theme
	Store *data.Store
	State *State
}

// Draw renders e into r.
func Draw(c Ctx, e *config.Element, r render.Rect) {
	if r.Empty() {
		return
	}
	s := c.Surf.Sub(r)
	base := c.Theme.Style(e.Fg, e.Bg)
	if e.Bold {
		base = base.Bold(true)
	}
	if e.Type == "button" {
		base = buttonStyle(c, e, base)
	}
	s.Fill(r, base)
	inner := frame(c, s, e, r, base)
	if inner.Empty() {
		return
	}
	s = s.Sub(inner)
	switch e.Type {
	case "gauge":
		drawGauge(c, s, e, inner, base)
	case "spark":
		drawSpark(c, s, e, inner, base)
	case "bignum":
		drawBig(c, s, e, inner, base)
	case "log":
		drawLog(c, s, e, inner, base)
	case "button":
		drawButton(c, s, e, inner, base)
	default:
		drawText(c, s, e, inner, base, e.Text)
	}
}

// BorderStyle returns the effective border style of e.
func BorderStyle(th *render.Theme, e *config.Element) string {
	if e.Border == "" {
		return th.Border
	}
	return e.Border
}

// frame draws border and title and returns the content rectangle.
func frame(c Ctx, s render.Surface, e *config.Element, r render.Rect, base tcell.Style) render.Rect {
	bst := base.Bold(false).Foreground(c.Theme.C("border"))
	if e.BorderFg != "" {
		if col, ok := c.Theme.Color(e.BorderFg); ok {
			bst = bst.Foreground(col)
		}
	}
	if e.Type == "button" {
		bst = bst.Foreground(fgOf(base))
	}
	bordered := s.Box(r, BorderStyle(c.Theme, e), bst)
	inner := r
	if bordered {
		inner = r.Inset(1)
		if inner.W >= 4 && e.Type != "bignum" {
			inner.X++
			inner.W -= 2
		}
	}
	if e.Title != "" {
		tst := base.Foreground(c.Theme.C("title")).Bold(true)
		if e.TitleFg != "" {
			if col, ok := c.Theme.Color(e.TitleFg); ok {
				tst = tst.Foreground(col)
			}
		}
		lines := render.Markup(" "+tmpl.Render(e.Title, c.Store)+" ", tst, c.Theme)
		l := lines[0]
		if bordered {
			avail := r.W - 4
			x := r.X + 2 + render.AlignX(e.TitleAlign, avail, l.Width())
			s.Line(x, r.Y, l, avail)
		} else if inner.H > 1 {
			x := r.X + render.AlignX(e.TitleAlign, r.W, l.Width()-2) - 1
			s.Sub(r).Line(x, r.Y, l, -1)
			inner.Y++
			inner.H--
		}
	}
	return inner
}

func fgOf(st tcell.Style) tcell.Color {
	fg, _, _ := st.Decompose()
	return fg
}

func drawText(c Ctx, s render.Surface, e *config.Element, r render.Rect, base tcell.Style, src string) {
	lines := render.Markup(tmpl.Render(src, c.Store), base, c.Theme)
	if e.Wrap {
		var wrapped []render.Line
		for _, l := range lines {
			wrapped = append(wrapped, render.Wrap(l, r.W)...)
		}
		lines = wrapped
	}
	y := r.Y + vOffset(e.VAlign, r.H, len(lines))
	for _, l := range lines {
		if y >= r.Y+r.H {
			break
		}
		x := r.X + render.AlignX(e.Align, r.W, l.Width())
		s.Line(x, y, l, r.W-(x-r.X))
		y++
	}
}

func vOffset(valign string, h, n int) int {
	switch valign {
	case "middle", "center":
		return max(0, (h-n)/2)
	case "bottom":
		return max(0, h-n)
	}
	return 0
}

// value returns the numeric value of the element's variable.
func value(c Ctx, e *config.Element) (float64, bool) {
	if e.Var == "" {
		return 0, false
	}
	v, ok := c.Store.Get(e.Var)
	if !ok {
		return 0, false
	}
	return tmpl.ParseNum(v)
}

func levelColor(c Ctx, e *config.Element, x float64, fallback string) tcell.Color {
	if len(e.Thresholds) == 0 {
		if e.Fg != "" {
			if col, ok := c.Theme.Color(e.Fg); ok {
				return col
			}
		}
		return c.Theme.C(fallback)
	}
	return c.Theme.C(tmpl.ThresholdColor(x, e.Thresholds, e.Invert))
}

var (
	hEighths = []rune(" ▏▎▍▌▋▊▉█")
	vEighths = []rune(" ▁▂▃▄▅▆▇█")
)

func drawGauge(c Ctx, s render.Surface, e *config.Element, r render.Rect, base tcell.Style) {
	x, ok := value(c, e)
	lo, hi := e.Min, e.Max
	if hi <= lo {
		hi = lo + 100
	}
	ratio := 0.0
	if ok {
		ratio = math.Max(0, math.Min(1, (x-lo)/(hi-lo)))
	}
	col := levelColor(c, e, x, "accent")
	fill := base.Bold(false).Foreground(col)
	track := base.Bold(false).Foreground(c.Theme.C("border"))

	label := e.Text
	if label == "" && e.Var != "" {
		label = "{" + e.Var + "|%.1f}"
	}
	var ll render.Line
	if label != "" {
		ll = render.Markup(tmpl.Render(label, c.Store), base, c.Theme)[0]
	}

	if r.H >= 3 && float64(r.W) < float64(r.H)*2.5 {
		// Vertical gauge: label on the bottom row, bar above it filling upward.
		bar := render.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}
		if ll != nil {
			bar.H--
			s.Line(r.X+render.AlignX("center", r.W, ll.Width()), r.Y+r.H-1, ll, r.W)
		}
		units := int(math.Round(ratio * float64(bar.H*8)))
		for row := 0; row < bar.H; row++ {
			y := bar.Y + bar.H - 1 - row
			u := min(max(units-row*8, 0), 8)
			for cx := bar.X; cx < bar.X+bar.W; cx++ {
				if u == 0 {
					s.Rune(cx, y, '░', track)
				} else {
					s.Rune(cx, y, vEighths[u], fill)
				}
			}
		}
		return
	}

	bar := r
	switch {
	case ll == nil:
	case r.H == 1:
		// One row: bar, then the label on the right.
		lw := ll.Width()
		bar.W = max(0, r.W-lw-1)
		s.Line(r.X+bar.W+1, r.Y, ll, lw)
	default:
		// Label row on top, bar fills the rows below (max 3).
		x0 := r.X + render.AlignX(orDefault(e.Align, "left"), r.W, ll.Width())
		s.Line(x0, r.Y, ll, r.W)
		bar.Y++
		bar.H = min(r.H-1, 3)
		bar.Y = r.Y + 1 + vOffset(e.VAlign, r.H-1, bar.H)
	}
	units := int(math.Round(ratio * float64(bar.W*8)))
	for i := 0; i < bar.W; i++ {
		u := min(max(units-i*8, 0), 8)
		for y := bar.Y; y < bar.Y+bar.H; y++ {
			if u == 0 {
				s.Rune(bar.X+i, y, '░', track)
			} else {
				s.Rune(bar.X+i, y, hEighths[u], fill)
			}
		}
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func drawSpark(c Ctx, s render.Surface, e *config.Element, r render.Rect, base tcell.Style) {
	h := c.Store.History(e.Var)
	chart := r
	if e.Text != "" && r.H >= 2 {
		ll := render.Markup(tmpl.Render(e.Text, c.Store), base, c.Theme)[0]
		s.Line(r.X+render.AlignX(e.Align, r.W, ll.Width()), r.Y, ll, r.W)
		chart.Y++
		chart.H--
	}
	if len(h) > chart.W {
		h = h[len(h)-chart.W:]
	}
	if len(h) == 0 {
		s.Text(chart.X, chart.Y+chart.H-1, strings.Repeat("·", chart.W), base.Foreground(c.Theme.C("border")), -1)
		return
	}
	lo, hi := e.Min, e.Max
	if hi <= lo {
		lo, hi = math.Inf(1), math.Inf(-1)
		for _, v := range h {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
		if lo > 0 {
			lo = 0
		}
		if hi <= lo {
			hi = lo + 1
		}
	}
	x0 := chart.X + chart.W - len(h)
	track := base.Bold(false).Foreground(c.Theme.C("border"))
	for i := chart.X; i < x0; i++ {
		s.Rune(i, chart.Y+chart.H-1, '·', track)
	}
	for i, v := range h {
		ratio := math.Max(0, math.Min(1, (v-lo)/(hi-lo)))
		units := int(math.Round(ratio * float64(chart.H*8)))
		if units == 0 && v > lo {
			units = 1
		}
		st := base.Bold(false).Foreground(levelColor(c, e, v, "secondary"))
		for row := 0; row < chart.H; row++ {
			u := min(max(units-row*8, 0), 8)
			y := chart.Y + chart.H - 1 - row
			if u > 0 {
				s.Rune(x0+i, y, vEighths[u], st)
			}
		}
	}
}

func drawBig(c Ctx, s render.Surface, e *config.Element, r render.Rect, base tcell.Style) {
	src := e.Text
	if src == "" && e.Var != "" {
		src = "{" + e.Var + "}"
	}
	if e.Var != "" && len(e.Thresholds) > 0 {
		if x, ok := value(c, e); ok {
			base = base.Foreground(levelColor(c, e, x, "fg"))
		}
	}
	lines := render.Markup(tmpl.Render(src, c.Store), base, c.Theme)
	l := lines[0]
	bw := render.BigWidth(l)
	if bw > r.W || r.H < render.BigHeight {
		drawText(c, s, e, r, base, src)
		return
	}
	x := r.X + render.AlignX(orDefault(e.Align, "center"), r.W, bw)
	y := r.Y + vOffset(orDefault(e.VAlign, "middle"), r.H, render.BigHeight)
	s.BigLine(x, y, l)
}

var (
	filterMu    sync.Mutex
	filterCache = map[string]*regexp.Regexp{}
)

func compiled(expr string) *regexp.Regexp {
	filterMu.Lock()
	defer filterMu.Unlock()
	if re, ok := filterCache[expr]; ok {
		return re
	}
	re, _ := regexp.Compile(expr)
	filterCache[expr] = re
	return re
}

// DefaultLogFormat is the line template of log elements.
const DefaultLogFormat = "[muted]{time}[-] {line}"

func drawLog(c Ctx, s render.Surface, e *config.Element, r render.Rect, base tcell.Style) {
	stream := orDefault(e.Stream, "events")
	limit := e.MaxLines
	if limit <= 0 || limit > r.H*4 {
		limit = r.H * 4
	}
	entries := c.Store.Stream(stream, 0)
	var re *regexp.Regexp
	exclude := strings.HasPrefix(e.Filter, "!")
	if expr := strings.TrimPrefix(e.Filter, "!"); expr != "" {
		re = compiled(expr)
	}
	format := orDefault(e.Text, DefaultLogFormat)
	pre, post, hasLine := strings.Cut(format, "{line}")
	if !hasLine {
		pre, post = format+" ", ""
	}
	var lines []render.Line
	for i := len(entries) - 1; i >= 0 && len(lines) < limit; i-- {
		en := entries[i]
		if re != nil && re.MatchString(render.StripMarkup(en.Text, c.Theme)) == exclude {
			continue
		}
		ts := "··:··:··"
		if !en.At.IsZero() {
			ts = en.At.Format("15:04:05")
		}
		lk := tmpl.Overlay{Top: map[string]string{"time": ts}, Base: c.Store}
		m := tmpl.Render(pre, lk) + en.Text + "[/]" + tmpl.Render(post, lk)
		l := render.Markup(m, base, c.Theme)[0]
		if e.Wrap {
			w := render.Wrap(l, r.W)
			for j := len(w) - 1; j >= 0; j-- {
				lines = append(lines, w[j])
			}
		} else {
			lines = append(lines, l)
		}
	}
	if len(lines) > limit {
		lines = lines[:limit]
	}
	if len(lines) == 0 {
		s.Text(r.X, r.Y, "waiting for "+stream+"…", base.Foreground(c.Theme.C("muted")).Italic(true), r.W)
		return
	}
	// Newest at the bottom; fill from the bottom up when there are few lines.
	y := r.Y + r.H - 1
	if e.VAlign != "bottom" {
		y = r.Y + min(len(lines), r.H) - 1
	}
	for _, l := range lines {
		if y < r.Y {
			break
		}
		s.Line(r.X, y, l, r.W)
		y--
	}
}

func buttonStyle(c Ctx, e *config.Element, base tcell.Style) tcell.Style {
	st := base
	if e.Bg == "" {
		st = st.Background(c.Theme.C("panel"))
	}
	if e.Fg == "" {
		st = st.Foreground(c.Theme.C("accent"))
	}
	if c.State != nil {
		if c.State.Hover {
			st = st.Background(c.Theme.C("border")).Bold(true)
		}
		if time.Since(c.State.Flash) < 250*time.Millisecond || c.State.Running {
			fg, bg, _ := st.Decompose()
			st = st.Foreground(bg).Background(fg)
		}
	}
	return st
}

func drawButton(c Ctx, s render.Surface, e *config.Element, r render.Rect, base tcell.Style) {
	label := e.Text
	if label == "" {
		label = e.ID
	}
	if c.State != nil && c.State.Running {
		label += " …"
	}
	if e.Key != "" {
		label += " [muted]" + tmpl.Escape("["+e.Key+"]") + "[-]"
	}
	el := *e
	el.Align = orDefault(e.Align, "center")
	el.VAlign = orDefault(e.VAlign, "middle")
	drawText(c, s, &el, r, base, label)
}
