package ui

import (
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/sernik77/tesserae/internal/render"
	"github.com/sernik77/tesserae/internal/tmpl"
)

// Item is a menu entry. An item with neither Action nor Sub is a header when
// Header is set, a separator when Label is empty.
type Item struct {
	Label    string
	Hint     string // right-aligned shortcut hint
	Action   func()
	Sub      []Item
	Disabled bool
	Checked  bool
	Header   bool
}

// Sep is a separator item.
var Sep = Item{}

// Menu is a context menu with optional nested submenus.
type Menu struct {
	Items   []Item
	X, Y    int
	Title   string
	OnClose func()

	sel    int
	off    int // first visible item when the menu is taller than the screen
	child  *Menu
	parent *Menu
	rect   render.Rect
	done   bool
}

// NewMenu creates a menu whose top-left corner is near (x, y).
func NewMenu(x, y int, title string, items []Item) *Menu {
	m := &Menu{Items: items, X: x, Y: y, Title: title, sel: -1}
	m.sel = m.next(-1, 1)
	return m
}

func (m *Menu) selectable(i int) bool {
	it := m.Items[i]
	return !it.Disabled && !it.Header && (it.Action != nil || it.Sub != nil)
}

func (m *Menu) next(from, dir int) int {
	n := len(m.Items)
	for k := 1; k <= n; k++ {
		i := ((from+dir*k)%n + n) % n
		if m.selectable(i) {
			return i
		}
	}
	return -1
}

// Done implements Overlay.
func (m *Menu) Done() bool { return m.done }

func (m *Menu) root() *Menu {
	r := m
	for r.parent != nil {
		r = r.parent
	}
	return r
}

func (m *Menu) close() {
	r := m.root()
	r.done = true
	if r.OnClose != nil {
		r.OnClose()
	}
}

func (m *Menu) width() int {
	lw, hw := tmpl.Width(m.Title)+2, 0
	for _, it := range m.Items {
		w := tmpl.Width(it.Label) + 4
		if it.Sub != nil {
			w += 2
		}
		lw = max(lw, w)
		hw = max(hw, tmpl.Width(it.Hint))
	}
	if hw > 0 {
		hw += 2
	}
	return lw + hw + 2
}

func (m *Menu) layout(sw, sh int) {
	w, h := m.width(), len(m.Items)+2
	x, y := m.X, m.Y
	if m.parent != nil {
		// Open to the right of the parent item, or to the left if no room.
		pr := m.parent.rect
		x = pr.X + pr.W - 1
		if x+w > sw {
			x = pr.X - w + 1
		}
	} else if x+w > sw {
		x = max(0, x-w)
	}
	if y+h > sh {
		y = max(0, sh-h)
	}
	m.rect = clampRect(render.Rect{X: x, Y: y, W: w, H: h}, sw, sh)
	vis := m.rect.H - 2
	if m.sel >= 0 && m.sel < m.off {
		m.off = m.sel
	}
	if m.sel >= m.off+vis {
		m.off = m.sel - vis + 1
	}
	m.off = max(0, min(m.off, len(m.Items)-vis))
}

// Draw implements Overlay.
func (m *Menu) Draw(s render.Surface, th *render.Theme) {
	m.layout(s.Clip.W, s.Clip.H)
	r := m.rect
	drawPanel(s, th, r, m.Title, m.child == nil)
	st := panelStyle(th)
	for i, it := range m.Items {
		y := r.Y + 1 + i - m.off
		if i < m.off {
			continue
		}
		if y >= r.Y+r.H-1 {
			break
		}
		x := r.X + 1
		w := r.W - 2
		switch {
		case it.Header:
			s.Text(x+1, y, it.Label, st.Foreground(th.C("muted")).Italic(true), w-1)
			continue
		case it.Label == "" && it.Action == nil && it.Sub == nil:
			s.Text(r.X, y, "├"+strings.Repeat("─", r.W-2)+"┤", st.Foreground(th.C("muted")), -1)
			continue
		}
		ist := st
		if it.Disabled {
			ist = ist.Foreground(th.C("muted"))
		}
		if i == m.sel {
			ist = ist.Background(th.C("select")).Foreground(th.C("bg")).Bold(true)
		}
		s.Fill(render.Rect{X: x, Y: y, W: w, H: 1}, ist)
		mark := " "
		if it.Checked {
			mark = "✓"
		}
		s.Text(x, y, mark, ist, 1)
		s.Text(x+2, y, it.Label, ist, w-3)
		right := x + w - 1
		if it.Sub != nil {
			s.Text(right, y, "▸", ist, 1)
			right -= 2
		}
		if it.Hint != "" {
			hst := ist
			if i != m.sel {
				hst = hst.Foreground(th.C("muted"))
			}
			hw := tmpl.Width(it.Hint)
			s.Text(right-hw+1, y, it.Hint, hst, hw)
		}
	}
	if m.off > 0 {
		s.Text(r.X+r.W-3, r.Y, "▲", st.Foreground(th.C("select")), 1)
	}
	if m.off+r.H-2 < len(m.Items) {
		s.Text(r.X+r.W-3, r.Y+r.H-1, "▼", st.Foreground(th.C("select")), 1)
	}
	if m.child != nil {
		m.child.Draw(s, th)
	}
}

func (m *Menu) openChild() {
	if m.sel < 0 || m.Items[m.sel].Sub == nil {
		return
	}
	c := NewMenu(0, 0, "", m.Items[m.sel].Sub)
	c.parent = m
	c.Y = m.rect.Y + m.sel - m.off
	m.child = c
}

func (m *Menu) activate() {
	if m.sel < 0 || !m.selectable(m.sel) {
		return
	}
	it := m.Items[m.sel]
	if it.Sub != nil {
		m.openChild()
		return
	}
	m.close()
	if it.Action != nil {
		it.Action()
	}
}

// Key implements Overlay.
func (m *Menu) Key(ev *tcell.EventKey) bool {
	if m.child != nil {
		return m.child.Key(ev)
	}
	switch ev.Key() {
	case tcell.KeyUp, tcell.KeyBacktab:
		m.sel = m.next(m.sel, -1)
	case tcell.KeyDown, tcell.KeyTab:
		m.sel = m.next(m.sel, 1)
	case tcell.KeyHome:
		m.sel = m.next(-1, 1)
	case tcell.KeyEnd:
		m.sel = m.next(len(m.Items), -1)
	case tcell.KeyEnter:
		m.activate()
	case tcell.KeyRight:
		if m.sel >= 0 && m.Items[m.sel].Sub != nil {
			m.openChild()
		}
	case tcell.KeyLeft, tcell.KeyEscape:
		if m.parent != nil {
			m.parent.child = nil
		} else {
			m.close()
		}
	case tcell.KeyRune:
		if ev.Rune() == ' ' {
			m.activate()
			break
		}
		// Jump to the next item starting with the typed letter.
		r := unicode.ToLower(ev.Rune())
		for k := 1; k <= len(m.Items); k++ {
			i := (m.sel + k) % len(m.Items)
			if lab := []rune(strings.ToLower(m.Items[i].Label)); m.selectable(i) && len(lab) > 0 && lab[0] == r {
				m.sel = i
				break
			}
		}
	}
	return true
}

func (m *Menu) hit(x, y int) (*Menu, int) {
	if m.child != nil {
		if mm, i := m.child.hit(x, y); mm != nil {
			return mm, i
		}
	}
	if !m.rect.Contains(x, y) {
		return nil, -1
	}
	i := y - m.rect.Y - 1 + m.off
	if y == m.rect.Y || y == m.rect.Y+m.rect.H-1 || i < 0 || i >= len(m.Items) || x == m.rect.X || x == m.rect.X+m.rect.W-1 {
		return m, -1
	}
	return m, i
}

// Mouse implements Overlay.
func (m *Menu) Mouse(ev Mouse) bool {
	mm, i := m.hit(ev.X, ev.Y)
	switch ev.Kind {
	case Move, Drag:
		if mm != nil && i >= 0 && mm.selectable(i) {
			if mm.sel != i || mm.child == nil {
				mm.sel = i
				mm.child = nil
				if mm.Items[i].Sub != nil {
					mm.openChild()
				}
			}
		}
	case Press:
		if mm == nil {
			// Clicking outside closes the menu and swallows the click.
			m.close()
			return true
		}
		if i >= 0 && mm.selectable(i) {
			mm.sel = i
			mm.activate()
		}
	case WheelUp:
		if mm != nil {
			mm.sel = mm.next(mm.sel, -1)
		}
	case WheelDown:
		if mm != nil {
			mm.sel = mm.next(mm.sel, 1)
		}
	}
	return true
}
