package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/sernik77/tesserae/internal/render"
	"github.com/sernik77/tesserae/internal/tmpl"
)

// FieldKind selects the editor used for a form field.
type FieldKind int

// Field kinds.
const (
	Text   FieldKind = iota // single-line input
	Area                    // multi-line input
	Select                  // one of Options
	Check                   // boolean
	Header                  // section title
	Info                    // read-only lines produced by Preview
)

// Field is one row (or block of rows) of a form.
type Field struct {
	Key     string
	Label   string
	Kind    FieldKind
	Value   string
	Options []string
	Labels  map[string]string // display names for options
	Checked bool
	Lines   int    // height of Area / Info fields
	Hint    string // shown in the footer while focused
	Picker  bool   // Ctrl+Space / F2 opens the variable picker
	// Preview renders Info fields from the current form values (markup lines).
	Preview func(f *Form) []string

	buf *textBuf
	row int // first row inside the form body
}

func (f *Field) focusable() bool { return f.Kind != Header && f.Kind != Info }

func (f *Field) height() int {
	switch f.Kind {
	case Area, Info:
		return max(1, f.Lines)
	}
	return 1
}

// Form is a modal dialog with labeled fields and buttons.
type Form struct {
	Title   string
	Fields  []*Field
	Buttons []string // default: OK, Cancel
	Width   int
	// Side places the form left or right of the screen ("left", "right" or
	// "" for centered), so a live-edited element stays visible.
	Side string

	OnChange func(f *Form)
	OnSubmit func(f *Form) error
	OnCancel func(f *Form)
	OnButton func(f *Form, name string) // buttons other than OK / Cancel
	OnPick   func(f *Form, field *Field, insert func(string))

	Err string

	focus  int // field index, or len(Fields)+i for button i
	scroll int
	rect   render.Rect
	body   render.Rect
	done   bool
	labelW int
	btnX   []int
}

// NewForm creates a form; call SetFields to populate it.
func NewForm(title string, fields []*Field) *Form {
	f := &Form{Title: title, Buttons: []string{"OK", "Cancel"}, Width: 84}
	f.SetFields(fields)
	return f
}

// SetFields replaces the fields, keeping the focused key when possible.
func (f *Form) SetFields(fields []*Field) {
	var key string
	if f.focus < len(f.Fields) {
		key = f.Fields[f.focus].Key
	}
	f.Fields = fields
	f.labelW = 0
	for _, fl := range fields {
		if fl.Kind == Text || fl.Kind == Area {
			fl.buf = newBuf(fl.Value, fl.Kind == Area)
		}
		if fl.Kind != Header {
			f.labelW = max(f.labelW, tmpl.Width(fl.Label))
		}
	}
	f.labelW = min(f.labelW+2, 20)
	f.focus = -1
	for i, fl := range fields {
		if fl.Key == key && fl.focusable() {
			f.focus = i
		}
	}
	if f.focus < 0 {
		f.focus = f.nextFocus(-1, 1)
	}
}

// Field returns the field with key, or nil.
func (f *Form) Field(key string) *Field {
	for _, fl := range f.Fields {
		if fl.Key == key {
			return fl
		}
	}
	return nil
}

// Get returns the current string value of a field.
func (f *Form) Get(key string) string {
	fl := f.Field(key)
	if fl == nil {
		return ""
	}
	if fl.buf != nil {
		return fl.buf.String()
	}
	return fl.Value
}

// Bool returns the state of a Check field.
func (f *Form) Bool(key string) bool {
	if fl := f.Field(key); fl != nil {
		return fl.Checked
	}
	return false
}

// Set changes the value of a field.
func (f *Form) Set(key, v string) {
	if fl := f.Field(key); fl != nil {
		fl.Value = v
		if fl.buf != nil {
			fl.buf.set(v)
		}
	}
}

// Values returns all current values (Check fields as "true"/"false").
func (f *Form) Values() map[string]string {
	m := map[string]string{}
	for _, fl := range f.Fields {
		switch fl.Kind {
		case Check:
			if fl.Checked {
				m[fl.Key] = "true"
			} else {
				m[fl.Key] = "false"
			}
		case Header, Info:
		default:
			m[fl.Key] = f.Get(fl.Key)
		}
	}
	return m
}

// Close closes the form without callbacks.
func (f *Form) Close() { f.done = true }

// Done implements Overlay.
func (f *Form) Done() bool { return f.done }

func (f *Form) nFocus() int { return len(f.Fields) + len(f.Buttons) }

func (f *Form) nextFocus(from, dir int) int {
	n := f.nFocus()
	for k := 1; k <= n; k++ {
		i := ((from+dir*k)%n + n) % n
		if i >= len(f.Fields) || f.Fields[i].focusable() {
			return i
		}
	}
	return 0
}

func (f *Form) layout(sw, sh int) {
	rows := 0
	for i, fl := range f.Fields {
		if fl.Kind == Header && i > 0 {
			rows++
		}
		fl.row = rows
		rows += fl.height()
	}
	w := min(f.Width, sw-2)
	// title border + body + blank + hint + error + buttons + border
	h := min(rows+6, sh-1)
	x := (sw - w) / 2
	switch f.Side {
	case "left":
		x = 1
	case "right":
		x = sw - w - 2
	}
	f.rect = clampRect(render.Rect{X: x, Y: max(0, (sh-h)/2), W: w, H: h}, sw, sh)
	f.body = render.Rect{X: f.rect.X + 2, Y: f.rect.Y + 1, W: f.rect.W - 4, H: f.rect.H - 6}
	// Keep the focused field visible.
	if f.focus < len(f.Fields) && f.focus >= 0 {
		fl := f.Fields[f.focus]
		if fl.row < f.scroll {
			f.scroll = fl.row
			if f.focus > 0 && f.Fields[f.focus-1].Kind == Header {
				f.scroll = f.Fields[f.focus-1].row
			}
		}
		if end := fl.row + fl.height(); end > f.scroll+f.body.H {
			f.scroll = end - f.body.H
		}
	}
	f.scroll = max(0, min(f.scroll, rows-f.body.H))
}

// Draw implements Overlay.
func (f *Form) Draw(s render.Surface, th *render.Theme) {
	f.layout(s.Clip.W, s.Clip.H)
	drawPanel(s, th, f.rect, f.Title, true)
	st := panelStyle(th)
	body := s.Sub(f.body)
	inputBg := th.C("bg")
	lx := f.body.X
	vx := f.body.X + f.labelW
	vw := f.body.W - f.labelW
	for i, fl := range f.Fields {
		y := f.body.Y + fl.row - f.scroll
		focused := i == f.focus
		switch fl.Kind {
		case Header:
			hst := st.Foreground(th.C("accent")).Bold(true)
			n := body.Text(lx, y, "── "+fl.Label+" ", hst, -1)
			body.Text(lx+n, y, strings.Repeat("─", max(0, f.body.W-n)), st.Foreground(th.C("border")), -1)
			continue
		case Info:
			lines := []string{}
			if fl.Preview != nil {
				lines = fl.Preview(f)
			}
			if fl.Label != "" {
				body.Text(lx, y, fl.Label, st.Foreground(th.C("muted")), f.labelW-1)
			}
			for j := 0; j < fl.height(); j++ {
				body.Fill(render.Rect{X: vx, Y: y + j, W: vw, H: 1}, st.Background(th.C("bg")))
				if j < len(lines) {
					l := render.Markup(lines[j], st.Background(th.C("bg")), th)[0]
					body.Line(vx+1, y+j, l, vw-2)
				}
			}
			continue
		}
		lst := st.Foreground(th.C("muted"))
		if focused {
			lst = st.Foreground(th.C("select")).Bold(true)
		}
		body.Text(lx, y, fl.Label, lst, f.labelW-1)
		ist := st.Background(inputBg)
		if focused {
			ist = ist.Foreground(th.C("fg"))
		}
		switch fl.Kind {
		case Text:
			body.Fill(render.Rect{X: vx, Y: y, W: vw, H: 1}, ist)
			txt, cx := fl.buf.fitScroll(vw - 2)
			body.Text(vx+1, y, txt, ist, vw-2)
			if focused && body.Clip.Contains(vx+1+cx, y) {
				s.S.ShowCursor(vx+1+cx, y)
			}
		case Area:
			f.drawArea(s, body, fl, render.Rect{X: vx, Y: y, W: vw, H: fl.height()}, ist, focused)
		case Select:
			body.Fill(render.Rect{X: vx, Y: y, W: vw, H: 1}, ist)
			label := fl.Value
			if l, ok := fl.Labels[fl.Value]; ok {
				label = l
			} else if label == "" {
				label = "(default)"
			}
			body.Text(vx+1, y, "◂ "+label+" ▸", ist, vw-2)
		case Check:
			mark := "[ ]"
			if fl.Checked {
				mark = "[✓]"
			}
			cst := st
			if focused {
				cst = cst.Foreground(th.C("select"))
			}
			body.Text(vx, y, mark, cst, -1)
		}
	}
	// Scroll indicator.
	if total := f.totalRows(); total > f.body.H {
		s.Text(f.rect.X+f.rect.W-2, f.body.Y, "▲", st.Foreground(th.C("muted")), 1)
		s.Text(f.rect.X+f.rect.W-2, f.body.Y+f.body.H-1, "▼", st.Foreground(th.C("muted")), 1)
	}
	// Footer: hint, error, buttons.
	fy := f.rect.Y + f.rect.H - 4
	hint := ""
	if f.focus >= 0 && f.focus < len(f.Fields) {
		hint = f.Fields[f.focus].Hint
		if f.Fields[f.focus].Picker {
			hint = strings.TrimSpace(hint + "  [Ctrl+Space: pick variable]")
		}
	}
	s.Text(f.rect.X+2, fy, strings.Repeat("─", f.rect.W-4), st.Foreground(th.C("border")), -1)
	if hint != "" {
		hl := render.Markup(hint, st.Foreground(th.C("muted")).Italic(true), th)[0]
		s.Line(f.rect.X+2, fy+1, hl, f.rect.W-4)
	}
	if f.Err != "" {
		s.Text(f.rect.X+2, fy+2, "✗ "+f.Err, st.Foreground(th.C("crit")).Bold(true), f.rect.W-4)
	}
	x := f.rect.X + f.rect.W - 2
	f.btnX = make([]int, len(f.Buttons))
	for i := len(f.Buttons) - 1; i >= 0; i-- {
		label := " " + f.Buttons[i] + " "
		x -= tmpl.Width(label) + 1
		f.btnX[i] = x
		bst := st.Background(th.C("border"))
		if f.focus == len(f.Fields)+i {
			bst = st.Background(th.C("select")).Foreground(th.C("bg")).Bold(true)
		}
		s.Text(x, f.rect.Y+f.rect.H-2, label, bst, -1)
	}
	keys := "Tab next · Enter OK · Esc cancel"
	if f.focus >= 0 && f.focus < len(f.Fields) && f.Fields[f.focus].Kind == Area {
		keys = "Tab next · Ctrl+S OK · Esc cancel"
	}
	s.Text(f.rect.X+2, f.rect.Y+f.rect.H-2, keys, st.Foreground(th.C("muted")), max(0, x-f.rect.X-3))
}

func (f *Form) totalRows() int {
	if len(f.Fields) == 0 {
		return 0
	}
	l := f.Fields[len(f.Fields)-1]
	return l.row + l.height()
}

func (f *Form) drawArea(s, body render.Surface, fl *Field, r render.Rect, st tcell.Style, focused bool) {
	b := fl.buf
	body.Fill(r, st)
	lines := b.lines()
	row, col := b.rowCol()
	if row < b.scroll {
		b.scroll = row
	}
	if row >= b.scroll+r.H {
		b.scroll = row - r.H + 1
	}
	inner := r.W - 2
	if col < b.hscrol {
		b.hscrol = col
	}
	if col >= b.hscrol+inner {
		b.hscrol = col - inner + 1
	}
	for i := 0; i < r.H; i++ {
		li := b.scroll + i
		if li >= len(lines) {
			break
		}
		lr := []rune(lines[li])
		if b.hscrol < len(lr) {
			body.Text(r.X+1, r.Y+i, string(lr[b.hscrol:]), st, inner)
		}
	}
	if len(lines) > r.H {
		body.Text(r.X+r.W-1, r.Y+r.H-1, "⋯", st.Foreground(tcell.ColorGray), 1)
	}
	if focused {
		lr := []rune(lines[row])
		cx := 0
		for i := b.hscrol; i < col && i < len(lr); i++ {
			cx += runeWidth(lr[i])
		}
		x, y := r.X+1+cx, r.Y+row-b.scroll
		if body.Clip.Contains(x, y) {
			s.S.ShowCursor(x, y)
		}
	}
}

func (f *Form) changed() {
	f.Err = ""
	if f.OnChange != nil {
		f.OnChange(f)
	}
}

func (f *Form) submit() {
	if f.OnSubmit != nil {
		if err := f.OnSubmit(f); err != nil {
			f.Err = err.Error()
			return
		}
	}
	f.done = true
}

func (f *Form) cancel() {
	if f.OnCancel != nil {
		f.OnCancel(f)
	}
	f.done = true
}

func (f *Form) press(i int) {
	switch name := f.Buttons[i]; name {
	case "OK", "Save", "Apply & close":
		f.submit()
	case "Cancel", "Close":
		f.cancel()
	default:
		if f.OnButton != nil {
			f.OnButton(f, name)
		}
	}
}

func (f *Form) cycle(fl *Field, dir int) {
	if len(fl.Options) == 0 {
		return
	}
	i := 0
	for j, o := range fl.Options {
		if o == fl.Value {
			i = j
		}
	}
	fl.Value = fl.Options[((i+dir)%len(fl.Options)+len(fl.Options))%len(fl.Options)]
	f.changed()
}

// Key implements Overlay.
func (f *Form) Key(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape:
		f.cancel()
		return true
	case tcell.KeyTab:
		f.focus = f.nextFocus(f.focus, 1)
		return true
	case tcell.KeyBacktab:
		f.focus = f.nextFocus(f.focus, -1)
		return true
	case tcell.KeyCtrlS:
		f.submit()
		return true
	case tcell.KeyPgDn:
		f.scroll += f.body.H / 2
		return true
	case tcell.KeyPgUp:
		f.scroll = max(0, f.scroll-f.body.H/2)
		return true
	}
	if f.focus >= len(f.Fields) {
		bi := f.focus - len(f.Fields)
		switch ev.Key() {
		case tcell.KeyEnter:
			f.press(bi)
		case tcell.KeyLeft, tcell.KeyUp:
			f.focus = f.nextFocus(f.focus, -1)
		case tcell.KeyRight, tcell.KeyDown:
			f.focus = f.nextFocus(f.focus, 1)
		case tcell.KeyRune:
			if ev.Rune() == ' ' {
				f.press(bi)
			}
		}
		return true
	}
	if f.focus < 0 {
		return true
	}
	fl := f.Fields[f.focus]
	isPick := ev.Key() == tcell.KeyF2 || ev.Key() == tcell.KeyCtrlSpace || (ev.Key() == tcell.KeyRune && ev.Rune() == ' ' && ev.Modifiers()&tcell.ModCtrl != 0)
	if isPick && fl.Picker && f.OnPick != nil && fl.buf != nil {
		f.OnPick(f, fl, func(s string) {
			fl.buf.insert(s)
			f.changed()
		})
		return true
	}
	switch fl.Kind {
	case Text, Area:
		before := fl.buf.String()
		if fl.buf.key(ev) {
			if fl.buf.String() != before {
				f.changed()
			}
			return true
		}
		switch ev.Key() {
		case tcell.KeyEnter:
			f.submit()
		case tcell.KeyUp:
			f.focus = f.nextFocus(f.focus, -1)
		case tcell.KeyDown:
			f.focus = f.nextFocus(f.focus, 1)
		}
	case Select:
		switch ev.Key() {
		case tcell.KeyLeft:
			f.cycle(fl, -1)
		case tcell.KeyRight:
			f.cycle(fl, 1)
		case tcell.KeyRune:
			if ev.Rune() == ' ' {
				f.cycle(fl, 1)
			}
		case tcell.KeyEnter:
			f.submit()
		case tcell.KeyUp:
			f.focus = f.nextFocus(f.focus, -1)
		case tcell.KeyDown:
			f.focus = f.nextFocus(f.focus, 1)
		}
	case Check:
		switch ev.Key() {
		case tcell.KeyRune:
			if ev.Rune() == ' ' || ev.Rune() == 'x' {
				fl.Checked = !fl.Checked
				f.changed()
			}
		case tcell.KeyEnter:
			f.submit()
		case tcell.KeyUp:
			f.focus = f.nextFocus(f.focus, -1)
		case tcell.KeyDown:
			f.focus = f.nextFocus(f.focus, 1)
		}
	}
	return true
}

// Paste inserts pasted text into the focused input.
func (f *Form) Paste(s string) {
	if f.focus >= 0 && f.focus < len(f.Fields) {
		if fl := f.Fields[f.focus]; fl.buf != nil {
			fl.buf.insert(s)
			f.changed()
		}
	}
}

// Mouse implements Overlay. Forms are modal: every event is consumed.
func (f *Form) Mouse(m Mouse) bool {
	switch m.Kind {
	case WheelUp:
		f.scroll = max(0, f.scroll-2)
		return true
	case WheelDown:
		f.scroll += 2
		return true
	case Press:
	default:
		return true
	}
	if m.Button != Left {
		return true
	}
	// Buttons.
	by := f.rect.Y + f.rect.H - 2
	if m.Y == by {
		for i, x := range f.btnX {
			if m.X >= x && m.X < x+tmpl.Width(f.Buttons[i])+2 {
				f.focus = len(f.Fields) + i
				f.press(i)
				return true
			}
		}
	}
	if !f.body.Contains(m.X, m.Y) {
		return true
	}
	row := m.Y - f.body.Y + f.scroll
	vx := f.body.X + f.labelW
	for i, fl := range f.Fields {
		if row < fl.row || row >= fl.row+fl.height() || !fl.focusable() {
			continue
		}
		f.focus = i
		switch fl.Kind {
		case Text:
			if m.X >= vx+1 {
				fl.buf.clickSingle(m.X - vx - 1)
			}
		case Area:
			if m.X >= vx+1 {
				fl.buf.clickMulti(row-fl.row, m.X-vx-1)
			}
		case Check:
			fl.Checked = !fl.Checked
			f.changed()
		case Select:
			if m.X >= vx {
				// Clicking the left arrow goes back, anything else forward.
				if m.X <= vx+2 {
					f.cycle(fl, -1)
				} else {
					f.cycle(fl, 1)
				}
			}
		}
		return true
	}
	return true
}
