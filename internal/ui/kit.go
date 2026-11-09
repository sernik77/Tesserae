// Package ui is a small popup toolkit for Tesserae: context menus, forms,
// list dialogs and message boxes drawn directly with tcell.
package ui

import (
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/sernik77/tesserae/internal/render"
)

// MouseKind classifies a normalized mouse event.
type MouseKind int

// Mouse event kinds.
const (
	Move MouseKind = iota
	Press
	Release
	Drag
	WheelUp
	WheelDown
)

// Button identifies a mouse button.
type Button int

// Mouse buttons.
const (
	NoButton Button = iota
	Left
	Right
	Middle
)

// Mouse is a normalized mouse event with press/release/drag transitions.
type Mouse struct {
	X, Y   int
	Kind   MouseKind
	Button Button
	Mod    tcell.ModMask
	Double bool
}

// MouseTracker converts tcell button state snapshots into transitions.
type MouseTracker struct {
	prev      tcell.ButtonMask
	lastPress time.Time
	lastX     int
	lastY     int
}

// Translate converts one tcell mouse event into zero or more Mouse events.
func (t *MouseTracker) Translate(ev *tcell.EventMouse) []Mouse {
	x, y := ev.Position()
	b := ev.Buttons()
	mod := ev.Modifiers()
	var out []Mouse
	if b&tcell.WheelUp != 0 {
		out = append(out, Mouse{X: x, Y: y, Kind: WheelUp, Mod: mod})
	}
	if b&tcell.WheelDown != 0 {
		out = append(out, Mouse{X: x, Y: y, Kind: WheelDown, Mod: mod})
	}
	b &= tcell.Button1 | tcell.Button2 | tcell.Button3
	buttons := []struct {
		mask tcell.ButtonMask
		btn  Button
	}{{tcell.Button1, Left}, {tcell.Button2, Right}, {tcell.Button3, Middle}}
	moved := false
	for _, bt := range buttons {
		now, was := b&bt.mask != 0, t.prev&bt.mask != 0
		switch {
		case now && !was:
			m := Mouse{X: x, Y: y, Kind: Press, Button: bt.btn, Mod: mod}
			if bt.btn == Left {
				if time.Since(t.lastPress) < 400*time.Millisecond && x == t.lastX && y == t.lastY {
					m.Double = true
					t.lastPress = time.Time{}
				} else {
					t.lastPress = time.Now()
				}
				t.lastX, t.lastY = x, y
			}
			out = append(out, m)
		case !now && was:
			out = append(out, Mouse{X: x, Y: y, Kind: Release, Button: bt.btn, Mod: mod})
		case now && was:
			if x != t.lastX || y != t.lastY {
				// A drag in between is not part of a double click.
				t.lastPress = time.Time{}
			}
			if !moved {
				out = append(out, Mouse{X: x, Y: y, Kind: Drag, Button: bt.btn, Mod: mod})
				moved = true
			}
		}
	}
	if b == 0 && t.prev == 0 && len(out) == 0 {
		out = append(out, Mouse{X: x, Y: y, Kind: Move, Mod: mod})
	}
	t.prev = b
	return out
}

// Overlay is a popup layer. The topmost overlay receives input first.
type Overlay interface {
	Draw(s render.Surface, th *render.Theme)
	Key(ev *tcell.EventKey) bool
	Mouse(m Mouse) bool
	Done() bool
}

// Host is implemented by the application to let overlays open other overlays.
type Host interface {
	Push(o Overlay)
	Toast(msg string, isErr bool)
}

// panelStyle is the background style of popups.
func panelStyle(th *render.Theme) tcell.Style {
	return tcell.StyleDefault.Background(th.C("panel")).Foreground(th.C("fg"))
}

// drawPanel draws a popup frame with a title and drop shadow.
func drawPanel(s render.Surface, th *render.Theme, r render.Rect, title string, active bool) {
	st := panelStyle(th)
	s.Fill(r, st)
	bc := th.C("muted")
	if active {
		bc = th.C("select")
	}
	s.Box(r, "rounded", st.Foreground(bc))
	if title != "" {
		l := render.Markup(" "+title+" ", st.Foreground(th.C("title")).Bold(true), th)[0]
		s.Line(r.X+2, r.Y, l, r.W-4)
	}
	s.Shadow(r, th.C("bg"))
}

// clampRect keeps r on screen.
func clampRect(r render.Rect, sw, sh int) render.Rect {
	r.W, r.H = min(r.W, sw), min(r.H, sh)
	r.X = max(0, min(r.X, sw-r.W))
	r.Y = max(0, min(r.Y, sh-r.H))
	return r
}
