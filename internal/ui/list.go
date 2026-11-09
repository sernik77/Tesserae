package ui

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/sernik77/tesserae/internal/render"
	"github.com/sernik77/tesserae/internal/tmpl"
)

// Row is one entry of a List. Text is markup.
type Row struct {
	Key  string
	Text string
}

// List is a modal list dialog with buttons and an optional type-to-filter.
type List struct {
	Title   string
	Rows    func(filter string) []Row // called on every draw, so rows can be live
	Buttons []string
	Default string // button triggered by Enter / double-click
	Filter  bool   // typing filters rows
	Width   int
	Height  int
	Footer  string // markup shown above the buttons

	// OnButton is called with the button name and the selected row (nil when
	// the list is empty). Return true to close the dialog.
	OnButton func(name string, row *Row) bool

	sel    int
	scroll int
	query  textBuf
	rows   []Row
	rect   render.Rect
	listR  render.Rect
	btnX   []int
	focusB int // -1: list focused, else button index
	done   bool
}

// NewList creates a list dialog.
func NewList(title string, rows func(string) []Row, buttons []string, onButton func(string, *Row) bool) *List {
	return &List{Title: title, Rows: rows, Buttons: buttons, OnButton: onButton, Width: 80, Height: 22, focusB: -1}
}

// Done implements Overlay.
func (l *List) Done() bool { return l.done }

// Select moves the selection to the row with key.
func (l *List) Select(key string) {
	for i, r := range l.Rows(l.query.String()) {
		if r.Key == key {
			l.sel = i
		}
	}
}

// Selected returns the selected row, or nil.
func (l *List) Selected() *Row {
	if l.sel >= 0 && l.sel < len(l.rows) {
		r := l.rows[l.sel]
		return &r
	}
	return nil
}

func (l *List) press(name string) {
	if name == "Close" || name == "Cancel" {
		if l.OnButton != nil {
			l.OnButton(name, l.Selected())
		}
		l.done = true
		return
	}
	if l.OnButton != nil && l.OnButton(name, l.Selected()) {
		l.done = true
	}
}

// Draw implements Overlay.
func (l *List) Draw(s render.Surface, th *render.Theme) {
	sw, sh := s.Clip.W, s.Clip.H
	w, h := min(l.Width, sw-2), min(l.Height, sh-1)
	l.rect = clampRect(render.Rect{X: (sw - w) / 2, Y: (sh - h) / 2, W: w, H: h}, sw, sh)
	drawPanel(s, th, l.rect, l.Title, true)
	st := panelStyle(th)
	l.rows = l.Rows(l.query.String())
	if l.sel >= len(l.rows) {
		l.sel = len(l.rows) - 1
	}
	if l.sel < 0 && len(l.rows) > 0 {
		l.sel = 0
	}
	top := l.rect.Y + 1
	if l.Filter {
		q, cx := l.query.fitScroll(l.rect.W - 8)
		fst := st.Background(th.C("bg"))
		s.Fill(render.Rect{X: l.rect.X + 2, Y: top, W: l.rect.W - 4, H: 1}, fst)
		s.Text(l.rect.X+3, top, "⌕ "+q, fst, l.rect.W-6)
		if l.focusB < 0 {
			s.S.ShowCursor(l.rect.X+5+cx, top)
		}
		top += 2
	}
	footerH := 2
	if l.Footer != "" {
		footerH += strings.Count(l.Footer, "\n") + 1
	}
	l.listR = render.Rect{X: l.rect.X + 1, Y: top, W: l.rect.W - 2, H: l.rect.Y + l.rect.H - 1 - footerH - top}
	if l.sel < l.scroll {
		l.scroll = l.sel
	}
	if l.sel >= l.scroll+l.listR.H {
		l.scroll = l.sel - l.listR.H + 1
	}
	l.scroll = max(0, l.scroll)
	ls := s.Sub(l.listR)
	for i := 0; i < l.listR.H; i++ {
		ri := l.scroll + i
		if ri >= len(l.rows) {
			break
		}
		rst := st
		if ri == l.sel {
			rst = st.Background(th.C("border"))
			if l.focusB < 0 {
				rst = st.Background(th.C("select")).Foreground(th.C("bg"))
			}
		}
		y := l.listR.Y + i
		ls.Fill(render.Rect{X: l.listR.X, Y: y, W: l.listR.W, H: 1}, rst)
		line := render.Markup(l.rows[ri].Text, rst, th)[0]
		if ri == l.sel && l.focusB < 0 {
			// Keep selected rows readable: force the selection colors.
			for j := range line {
				line[j].Style = rst.Bold(true)
			}
		}
		ls.Line(l.listR.X+1, y, line, l.listR.W-2)
	}
	if len(l.rows) == 0 {
		s.Text(l.listR.X+2, l.listR.Y, "(empty)", st.Foreground(th.C("muted")).Italic(true), -1)
	}
	if len(l.rows) > l.listR.H {
		info := " " + itoa(l.sel+1) + "/" + itoa(len(l.rows)) + " "
		s.Text(l.rect.X+l.rect.W-2-tmpl.Width(info), l.rect.Y+l.rect.H-1-footerH, info, st.Foreground(th.C("muted")), -1)
	}
	fy := l.rect.Y + l.rect.H - footerH
	if l.Footer != "" {
		for i, fl := range render.Markup(l.Footer, st.Foreground(th.C("muted")), th) {
			s.Line(l.rect.X+2, fy+i, fl, l.rect.W-4)
		}
	}
	x := l.rect.X + l.rect.W - 2
	l.btnX = make([]int, len(l.Buttons))
	for i := len(l.Buttons) - 1; i >= 0; i-- {
		label := " " + l.Buttons[i] + " "
		x -= tmpl.Width(label) + 1
		l.btnX[i] = x
		bst := st.Background(th.C("border"))
		if l.focusB == i {
			bst = st.Background(th.C("select")).Foreground(th.C("bg")).Bold(true)
		} else if l.Buttons[i] == l.Default {
			bst = bst.Bold(true)
		}
		s.Text(x, l.rect.Y+l.rect.H-2, label, bst, -1)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// Key implements Overlay.
func (l *List) Key(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape:
		l.press("Close")
		return true
	case tcell.KeyUp:
		l.focusB = -1
		l.sel = max(0, l.sel-1)
		return true
	case tcell.KeyDown:
		l.focusB = -1
		l.sel = min(len(l.rows)-1, l.sel+1)
		return true
	case tcell.KeyPgUp:
		l.sel = max(0, l.sel-l.listR.H)
		return true
	case tcell.KeyPgDn:
		l.sel = min(len(l.rows)-1, l.sel+l.listR.H)
		return true
	case tcell.KeyTab:
		l.focusB++
		if l.focusB >= len(l.Buttons) {
			l.focusB = -1
		}
		return true
	case tcell.KeyBacktab:
		l.focusB--
		if l.focusB < -1 {
			l.focusB = len(l.Buttons) - 1
		}
		return true
	case tcell.KeyEnter:
		if l.focusB >= 0 {
			l.press(l.Buttons[l.focusB])
		} else if l.Default != "" {
			l.press(l.Default)
		}
		return true
	case tcell.KeyDelete:
		for _, b := range l.Buttons {
			if b == "Remove" || b == "Delete" {
				l.press(b)
			}
		}
		return true
	}
	if l.focusB >= 0 {
		switch ev.Key() {
		case tcell.KeyLeft:
			l.focusB = max(0, l.focusB-1)
		case tcell.KeyRight:
			l.focusB = min(len(l.Buttons)-1, l.focusB+1)
		}
		return true
	}
	if l.Filter {
		if l.query.key(ev) {
			l.sel, l.scroll = 0, 0
		}
		return true
	}
	// Without a filter, letters trigger the button with that initial.
	if ev.Key() == tcell.KeyRune {
		r := strings.ToLower(string(ev.Rune()))
		for _, b := range l.Buttons {
			if strings.HasPrefix(strings.ToLower(b), r) {
				l.press(b)
				break
			}
		}
	}
	return true
}

// Mouse implements Overlay.
func (l *List) Mouse(m Mouse) bool {
	switch m.Kind {
	case WheelUp:
		l.sel = max(0, l.sel-1)
	case WheelDown:
		l.sel = min(len(l.rows)-1, l.sel+1)
	case Press:
		if m.Button != Left {
			return true
		}
		if m.Y == l.rect.Y+l.rect.H-2 {
			for i, x := range l.btnX {
				if m.X >= x && m.X < x+tmpl.Width(l.Buttons[i])+2 {
					l.focusB = i
					l.press(l.Buttons[i])
					return true
				}
			}
		}
		if l.listR.Contains(m.X, m.Y) {
			i := l.scroll + m.Y - l.listR.Y
			if i < len(l.rows) {
				l.focusB = -1
				l.sel = i
				if m.Double && l.Default != "" {
					l.press(l.Default)
				}
			}
		}
	}
	return true
}

// Paste implements text paste into the filter.
func (l *List) Paste(s string) {
	if l.Filter {
		l.query.insert(s)
	}
}

// Message is a modal box with text and buttons.
type Message struct {
	Title    string
	Text     string // markup
	Buttons  []string
	OnButton func(name string)
	Width    int

	rect render.Rect
	btnX []int
	sel  int
	done bool
}

// NewConfirm creates a message box. The first button is focused.
func NewConfirm(title, text string, buttons []string, onButton func(string)) *Message {
	return &Message{Title: title, Text: text, Buttons: buttons, OnButton: onButton, Width: 64}
}

// Done implements Overlay.
func (m *Message) Done() bool { return m.done }

// Draw implements Overlay.
func (m *Message) Draw(s render.Surface, th *render.Theme) {
	st := panelStyle(th)
	sw, sh := s.Clip.W, s.Clip.H
	w := min(m.Width, sw-2)
	var lines []render.Line
	for _, l := range render.Markup(m.Text, st, th) {
		lines = append(lines, render.Wrap(l, w-4)...)
	}
	h := min(len(lines)+5, sh-1)
	m.rect = clampRect(render.Rect{X: (sw - w) / 2, Y: (sh - h) / 2, W: w, H: h}, sw, sh)
	drawPanel(s, th, m.rect, m.Title, true)
	for i, l := range lines {
		if i >= h-5 {
			break
		}
		s.Line(m.rect.X+2, m.rect.Y+2+i, l, w-4)
	}
	x := m.rect.X + m.rect.W - 2
	m.btnX = make([]int, len(m.Buttons))
	for i := len(m.Buttons) - 1; i >= 0; i-- {
		label := " " + m.Buttons[i] + " "
		x -= tmpl.Width(label) + 1
		m.btnX[i] = x
		bst := st.Background(th.C("border"))
		if i == m.sel {
			bst = st.Background(th.C("select")).Foreground(th.C("bg")).Bold(true)
		}
		s.Text(x, m.rect.Y+m.rect.H-2, label, bst, -1)
	}
}

func (m *Message) press(i int) {
	m.done = true
	if m.OnButton != nil {
		m.OnButton(m.Buttons[i])
	}
}

// Key implements Overlay.
func (m *Message) Key(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape:
		m.done = true
		if m.OnButton != nil {
			m.OnButton("")
		}
	case tcell.KeyLeft, tcell.KeyBacktab:
		m.sel = max(0, m.sel-1)
	case tcell.KeyRight, tcell.KeyTab:
		m.sel = min(len(m.Buttons)-1, m.sel+1)
	case tcell.KeyEnter:
		m.press(m.sel)
	case tcell.KeyRune:
		r := strings.ToLower(string(ev.Rune()))
		for i, b := range m.Buttons {
			if strings.HasPrefix(strings.ToLower(b), r) {
				m.press(i)
				break
			}
		}
	}
	return true
}

// Mouse implements Overlay.
func (m *Message) Mouse(ev Mouse) bool {
	if ev.Kind == Press && ev.Button == Left && ev.Y == m.rect.Y+m.rect.H-2 {
		for i, x := range m.btnX {
			if ev.X >= x && ev.X < x+tmpl.Width(m.Buttons[i])+2 {
				m.press(i)
			}
		}
	}
	return true
}
