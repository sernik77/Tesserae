package render

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// Rect is a screen rectangle in cells.
type Rect struct{ X, Y, W, H int }

// Contains reports whether the cell (x, y) is inside r.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H
}

// Intersect returns the overlap of r and o.
func (r Rect) Intersect(o Rect) Rect {
	x0, y0 := max(r.X, o.X), max(r.Y, o.Y)
	x1, y1 := min(r.X+r.W, o.X+o.W), min(r.Y+r.H, o.Y+o.H)
	if x1 <= x0 || y1 <= y0 {
		return Rect{x0, y0, 0, 0}
	}
	return Rect{x0, y0, x1 - x0, y1 - y0}
}

// Overlaps reports whether r and o share at least one cell.
func (r Rect) Overlaps(o Rect) bool {
	i := r.Intersect(o)
	return i.W > 0 && i.H > 0
}

// Inset shrinks r by n cells on every side.
func (r Rect) Inset(n int) Rect {
	r = Rect{r.X + n, r.Y + n, r.W - 2*n, r.H - 2*n}
	r.W, r.H = max(r.W, 0), max(r.H, 0)
	return r
}

// Empty reports whether r has no cells.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Surface draws onto a screen, clipped to a rectangle.
type Surface struct {
	S    tcell.Screen
	Clip Rect
}

// NewSurface returns a surface covering the whole screen.
func NewSurface(s tcell.Screen) Surface {
	w, h := s.Size()
	return Surface{S: s, Clip: Rect{0, 0, w, h}}
}

// Sub returns a surface clipped to r (intersected with the current clip).
func (s Surface) Sub(r Rect) Surface {
	return Surface{S: s.S, Clip: s.Clip.Intersect(r)}
}

// Put draws one grapheme cluster of the given width.
func (s Surface) Put(x, y int, g string, w int, st tcell.Style) {
	if y < s.Clip.Y || y >= s.Clip.Y+s.Clip.H || x < s.Clip.X || x+w > s.Clip.X+s.Clip.W {
		// A wide glyph that does not fit is replaced by blanks.
		for i := 0; i < w; i++ {
			if s.Clip.Contains(x+i, y) {
				s.S.SetContent(x+i, y, ' ', nil, st)
			}
		}
		return
	}
	rs := []rune(g)
	if len(rs) == 0 {
		rs = []rune{' '}
	}
	s.S.SetContent(x, y, rs[0], rs[1:], st)
}

// Rune draws a single-width rune.
func (s Surface) Rune(x, y int, r rune, st tcell.Style) {
	if s.Clip.Contains(x, y) {
		s.S.SetContent(x, y, r, nil, st)
	}
}

// Fill paints r with spaces in style st.
func (s Surface) Fill(r Rect, st tcell.Style) {
	r = s.Clip.Intersect(r)
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			s.S.SetContent(x, y, ' ', nil, st)
		}
	}
}

// Text draws plain text and returns the width used. maxW < 0 means unlimited.
func (s Surface) Text(x, y int, text string, st tcell.Style, maxW int) int {
	used := 0
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		w := g.Width()
		if maxW >= 0 && used+w > maxW {
			break
		}
		s.Put(x+used, y, g.Str(), w, st)
		used += w
	}
	return used
}

// Line draws a styled line and returns the width used.
func (s Surface) Line(x, y int, l Line, maxW int) int {
	used := 0
	for _, sp := range l {
		rem := -1
		if maxW >= 0 {
			rem = maxW - used
			if rem <= 0 {
				break
			}
		}
		used += s.Text(x+used, y, sp.Text, sp.Style, rem)
	}
	return used
}

// Restyle changes the style of existing cells in r using fn.
func (s Surface) Restyle(r Rect, fn func(tcell.Style) tcell.Style) {
	r = s.Clip.Intersect(r)
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			m, c, st, _ := s.S.GetContent(x, y)
			s.S.SetContent(x, y, m, c, fn(st))
		}
	}
}

// Shadow darkens the cells right of and below r (a drop shadow for popups).
func (s Surface) Shadow(r Rect, c tcell.Color) {
	dim := func(st tcell.Style) tcell.Style { return st.Background(c).Foreground(tcell.ColorGray).Dim(true) }
	s.Restyle(Rect{r.X + r.W, r.Y + 1, 1, r.H}, dim)
	s.Restyle(Rect{r.X + 1, r.Y + r.H, r.W, 1}, dim)
}

// BorderSet is the set of runes used to draw a box.
type BorderSet struct{ TL, TR, BL, BR, H, V rune }

// Borders maps style names to rune sets.
var Borders = map[string]BorderSet{
	"rounded": {'╭', '╮', '╰', '╯', '─', '│'},
	"sharp":   {'┌', '┐', '└', '┘', '─', '│'},
	"double":  {'╔', '╗', '╚', '╝', '═', '║'},
	"thick":   {'┏', '┓', '┗', '┛', '━', '┃'},
	"ascii":   {'+', '+', '+', '+', '-', '|'},
	"dashed":  {'┌', '┐', '└', '┘', '╌', '╎'},
}

// Box draws a border around r. Unknown styles (including "none") draw nothing.
func (s Surface) Box(r Rect, style string, st tcell.Style) bool {
	b, ok := Borders[style]
	if !ok || r.W < 2 || r.H < 2 {
		return false
	}
	x1, y1 := r.X+r.W-1, r.Y+r.H-1
	for x := r.X + 1; x < x1; x++ {
		s.Rune(x, r.Y, b.H, st)
		s.Rune(x, y1, b.H, st)
	}
	for y := r.Y + 1; y < y1; y++ {
		s.Rune(r.X, y, b.V, st)
		s.Rune(x1, y, b.V, st)
	}
	s.Rune(r.X, r.Y, b.TL, st)
	s.Rune(x1, r.Y, b.TR, st)
	s.Rune(r.X, y1, b.BL, st)
	s.Rune(x1, y1, b.BR, st)
	return true
}

// AlignX returns the x offset of content of width cw inside width w.
func AlignX(align string, w, cw int) int {
	switch align {
	case "center":
		return max(0, (w-cw)/2)
	case "right":
		return max(0, w-cw)
	}
	return 0
}
