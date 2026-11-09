package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/sernik77/tesserae/internal/config"
	"github.com/sernik77/tesserae/internal/elem"
	"github.com/sernik77/tesserae/internal/render"
	"github.com/sernik77/tesserae/internal/tmpl"
	"github.com/sernik77/tesserae/internal/ui"
)

type dragKind int

const (
	dragNone   dragKind = iota
	dragMove            // mouse drag of the selection
	dragResize          // mouse drag of an edge
	dragBand            // rubber-band rectangle on empty canvas
	followMove          // "Move" command: selection follows the pointer until click
	followSize          // "Resize" command: bottom-right corner follows the pointer
)

// Edge bits for resizing.
const (
	edgeL = 1 << iota
	edgeR
	edgeT
	edgeB
)

const maxUndo = 200

type editor struct {
	sel     map[string]bool
	primary string
	hover   int

	drag     dragKind
	edges    int
	sx, sy   int
	orig     map[string]render.Rect
	before   *config.Dashboard
	changed  bool
	band     render.Rect
	showBand bool
	armBand  bool // next left press starts a band, even over widgets

	undo, redo []*config.Dashboard
	clip       []config.Element
	grid       bool
	dirty      bool
	lastNudge  time.Time
}

func (ed *editor) init() {
	ed.sel = map[string]bool{}
	ed.hover = -1
	ed.grid = true
}

func (ed *editor) clear() {
	ed.sel = map[string]bool{}
	ed.primary = ""
	ed.drag = dragNone
	ed.showBand = false
	ed.armBand = false
}

// ── undo ─────────────────────────────────────────────────────────────────────

// checkpoint records the current state before a mutation.
func (a *App) checkpoint() {
	a.ed.undo = append(a.ed.undo, a.cfg.Clone())
	if len(a.ed.undo) > maxUndo {
		a.ed.undo = a.ed.undo[1:]
	}
	a.ed.redo = nil
	a.ed.dirty = true
}

// commit records a snapshot taken before an interactive change, unless
// nothing actually changed.
func (a *App) commit(before *config.Dashboard) {
	if before == nil || string(config.Encode(before)) == string(config.Encode(a.cfg)) {
		return
	}
	a.ed.undo = append(a.ed.undo, before)
	if len(a.ed.undo) > maxUndo {
		a.ed.undo = a.ed.undo[1:]
	}
	a.ed.redo = nil
	a.ed.dirty = true
}

func (a *App) restore(d *config.Dashboard) {
	sourcesChanged := string(config.Encode(&config.Dashboard{Sources: d.Sources})) != string(config.Encode(&config.Dashboard{Sources: a.cfg.Sources}))
	a.cfg = d
	a.th = render.NewTheme(d.Theme)
	if sourcesChanged {
		a.startData()
	}
	for id := range a.ed.sel {
		if a.cfg.Index(id) < 0 {
			delete(a.ed.sel, id)
		}
	}
	if a.cfg.Index(a.ed.primary) < 0 {
		a.ed.primary = ""
	}
	a.ed.dirty = true
}

func (a *App) undo() {
	if len(a.ed.undo) == 0 {
		a.toast("nothing to undo", true)
		return
	}
	prev := a.ed.undo[len(a.ed.undo)-1]
	a.ed.undo = a.ed.undo[:len(a.ed.undo)-1]
	a.ed.redo = append(a.ed.redo, a.cfg.Clone())
	a.restore(prev)
	a.toast(fmt.Sprintf("undo (%d left)", len(a.ed.undo)), false)
}

func (a *App) redo() {
	if len(a.ed.redo) == 0 {
		a.toast("nothing to redo", true)
		return
	}
	next := a.ed.redo[len(a.ed.redo)-1]
	a.ed.redo = a.ed.redo[:len(a.ed.redo)-1]
	a.ed.undo = append(a.ed.undo, a.cfg.Clone())
	a.restore(next)
	a.toast("redo", false)
}

// ── selection ────────────────────────────────────────────────────────────────

// members returns the ids of e's group (or just e).
func (a *App) members(i int) []string {
	e := a.cfg.Elems[i]
	if e.Group == "" {
		return []string{e.ID}
	}
	var ids []string
	for _, o := range a.cfg.Elems {
		if o.Group == e.Group {
			ids = append(ids, o.ID)
		}
	}
	return ids
}

func (a *App) selectOnly(i int) {
	a.ed.sel = map[string]bool{}
	for _, id := range a.members(i) {
		a.ed.sel[id] = true
	}
	a.ed.primary = a.cfg.Elems[i].ID
}

func (a *App) toggleSel(i int) {
	ids := a.members(i)
	on := !a.ed.sel[a.cfg.Elems[i].ID]
	for _, id := range ids {
		if on {
			a.ed.sel[id] = true
		} else {
			delete(a.ed.sel, id)
		}
	}
	if on {
		a.ed.primary = a.cfg.Elems[i].ID
	} else if a.ed.primary == a.cfg.Elems[i].ID {
		a.ed.primary = ""
		for id := range a.ed.sel {
			a.ed.primary = id
		}
	}
}

func (a *App) selectAll() {
	for _, e := range a.cfg.Elems {
		a.ed.sel[e.ID] = true
	}
	if a.ed.primary == "" && len(a.cfg.Elems) > 0 {
		a.ed.primary = a.cfg.Elems[len(a.cfg.Elems)-1].ID
	}
}

// selected returns selected element indices in z-order.
func (a *App) selected() []int {
	var out []int
	for i, e := range a.cfg.Elems {
		if a.ed.sel[e.ID] {
			out = append(out, i)
		}
	}
	return out
}

func (a *App) primaryIndex() int {
	if i := a.cfg.Index(a.ed.primary); i >= 0 && a.ed.sel[a.ed.primary] {
		return i
	}
	if s := a.selected(); len(s) > 0 {
		return s[len(s)-1]
	}
	return -1
}

// ── geometry ─────────────────────────────────────────────────────────────────

// setRect changes an element's geometry, keeping the unit kind of each value.
func (a *App) setRect(i int, r render.Rect) {
	w, h := a.size()
	e := &a.cfg.Elems[i]
	e.X = e.X.Rebase(r.X, w)
	e.Y = e.Y.Rebase(r.Y, h)
	e.W = e.W.RebaseSize(e.X, r.X, r.W, w)
	e.H = e.H.RebaseSize(e.Y, r.Y, r.H, h)
}

func (a *App) rectsOf(ids map[string]bool) map[string]render.Rect {
	a.layout()
	m := map[string]render.Rect{}
	for i, e := range a.cfg.Elems {
		if ids[e.ID] {
			m[e.ID] = a.rects[i]
		}
	}
	return m
}

// moveBy shifts the movable part of the selection from its original rects,
// clamping so the whole selection stays on screen.
func (a *App) moveBy(orig map[string]render.Rect, dx, dy int) {
	w, h := a.size()
	x0, y0, x1, y1 := w, h, 0, 0
	for id, r := range orig {
		if a.cfg.Elems[a.cfg.Index(id)].Locked {
			continue
		}
		x0, y0 = min(x0, r.X), min(y0, r.Y)
		x1, y1 = max(x1, r.X+r.W), max(y1, r.Y+r.H)
	}
	if x0 > x1 {
		return
	}
	dx = max(-x0, min(dx, w-x1))
	dy = max(-y0, min(dy, h-y1))
	for id, r := range orig {
		i := a.cfg.Index(id)
		if i < 0 || a.cfg.Elems[i].Locked {
			continue
		}
		r.X += dx
		r.Y += dy
		a.setRect(i, r)
	}
}

func minSize(e *config.Element, th *render.Theme) (int, int) {
	if elem.BorderStyle(th, e) != "none" {
		return 3, 2
	}
	return 1, 1
}

// resizeTo applies edge deltas to an original rect.
func (a *App) resizeTo(i int, o render.Rect, edges, dx, dy int) {
	w, h := a.size()
	mw, mh := minSize(&a.cfg.Elems[i], a.th)
	r := o
	if edges&edgeL != 0 {
		nx := max(0, min(o.X+dx, o.X+o.W-mw))
		r.W = o.X + o.W - nx
		r.X = nx
	}
	if edges&edgeR != 0 {
		r.W = max(mw, min(o.W+dx, w-o.X))
	}
	if edges&edgeT != 0 {
		ny := max(0, min(o.Y+dy, o.Y+o.H-mh))
		r.H = o.Y + o.H - ny
		r.Y = ny
	}
	if edges&edgeB != 0 {
		r.H = max(mh, min(o.H+dy, h-o.Y))
	}
	a.setRect(i, r)
}

// edgeAt returns resize edges under (x, y) of a selected element's rect.
func edgeAt(r render.Rect, x, y int) int {
	e := 0
	if x == r.X && r.W >= 3 {
		e |= edgeL
	}
	if x == r.X+r.W-1 && r.W >= 2 {
		e |= edgeR
	}
	if y == r.Y+r.H-1 && r.H >= 2 {
		e |= edgeB
	}
	if y == r.Y && r.H >= 3 && e != 0 {
		// The top border moves the widget; only its corners resize.
		e |= edgeT
	}
	return e
}

// ── mouse ────────────────────────────────────────────────────────────────────

func (a *App) editMouse(m ui.Mouse) {
	a.layout()
	ed := &a.ed
	switch ed.drag {
	case followMove, followSize:
		a.followMouse(m)
		return
	}
	switch m.Kind {
	case ui.Move:
		ed.hover = a.hit(m.X, m.Y)
	case ui.Press:
		ed.showBand = false
		i := a.hit(m.X, m.Y)
		switch m.Button {
		case ui.Left:
			if ed.armBand {
				// "Draw area" was chosen: start a band even over widgets.
				ed.armBand = false
				ed.sx, ed.sy = m.X, m.Y
				ed.drag = dragBand
				ed.band = render.Rect{X: m.X, Y: m.Y, W: 1, H: 1}
				return
			}
			a.leftPress(m, i)
		case ui.Right:
			ed.armBand = false
			if i >= 0 {
				// Keep an existing selection so the menu can add to it;
				// with nothing selected, select the clicked widget.
				if len(ed.sel) == 0 {
					a.selectOnly(i)
				}
				a.elementMenu(m.X, m.Y, i)
			} else {
				a.canvasMenu(m.X, m.Y)
			}
		}
	case ui.Drag:
		if m.Button != ui.Left {
			return
		}
		dx, dy := m.X-ed.sx, m.Y-ed.sy
		switch ed.drag {
		case dragMove:
			if dx != 0 || dy != 0 {
				ed.changed = true
			}
			a.moveBy(ed.orig, dx, dy)
		case dragResize:
			if i := a.primaryIndex(); i >= 0 {
				ed.changed = true
				a.resizeTo(i, ed.orig[a.cfg.Elems[i].ID], ed.edges, dx, dy)
			}
		case dragBand:
			ed.band = bandRect(ed.sx, ed.sy, m.X, m.Y)
			ed.showBand = true
		}
	case ui.Release:
		if m.Button != ui.Left {
			return
		}
		switch ed.drag {
		case dragMove, dragResize:
			if ed.changed {
				a.commit(ed.before)
			}
		case dragBand:
			ed.band = bandRect(ed.sx, ed.sy, m.X, m.Y)
			if ed.band.W >= 2 && ed.band.H >= 1 && (ed.band.W > 2 || ed.band.H > 1) {
				ed.showBand = true
				a.areaMenu(m.X, m.Y, ed.band)
			} else {
				ed.showBand = false
			}
		}
		ed.drag = dragNone
	}
}

func bandRect(x0, y0, x1, y1 int) render.Rect {
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	return render.Rect{X: x0, Y: y0, W: x1 - x0 + 1, H: y1 - y0 + 1}
}

func (a *App) leftPress(m ui.Mouse, i int) {
	ed := &a.ed
	ed.sx, ed.sy = m.X, m.Y
	ed.changed = false
	if i < 0 {
		if m.Mod&(tcell.ModShift|tcell.ModCtrl|tcell.ModAlt) == 0 {
			ed.clear()
		}
		ed.drag = dragBand
		ed.band = render.Rect{X: m.X, Y: m.Y, W: 1, H: 1}
		return
	}
	e := &a.cfg.Elems[i]
	if m.Double {
		a.selectOnly(i)
		a.editElement(i, false)
		return
	}
	if m.Mod&(tcell.ModShift|tcell.ModCtrl|tcell.ModAlt) != 0 {
		a.toggleSel(i)
		return
	}
	// Resize when grabbing an edge of the (already) selected primary widget.
	if ed.sel[e.ID] && !e.Locked {
		if edges := edgeAt(a.rects[i], m.X, m.Y); edges != 0 {
			ed.primary = e.ID
			ed.drag = dragResize
			ed.edges = edges
			ed.before = a.cfg.Clone()
			ed.orig = map[string]render.Rect{e.ID: a.rects[i]}
			return
		}
	}
	if !ed.sel[e.ID] {
		a.selectOnly(i)
	} else {
		ed.primary = e.ID
	}
	ed.drag = dragMove
	ed.before = a.cfg.Clone()
	ed.orig = a.rectsOf(ed.sel)
}

// armBand makes the next left drag draw a selection rectangle, even when it
// starts on top of a widget (full-screen dashboards have no empty space).
func (a *App) armBand() {
	a.ed.armBand = true
	a.toast("draw area: drag a rectangle anywhere · Esc cancels", false)
}

// startFollow begins a keyboard/pointer-driven move or resize.
func (a *App) startFollow(kind dragKind) {
	i := a.primaryIndex()
	if i < 0 {
		return
	}
	if a.cfg.Elems[i].Locked {
		a.toast(a.cfg.Elems[i].ID+" is locked", true)
		return
	}
	ed := &a.ed
	ed.drag = kind
	ed.before = a.cfg.Clone()
	ed.orig = a.rectsOf(ed.sel)
	r := a.rects[i]
	if kind == followMove {
		ed.sx, ed.sy = a.mx, a.my
		a.toast("move: point and click to drop · arrows nudge · Enter done · Esc cancel", false)
	} else {
		ed.orig = map[string]render.Rect{a.cfg.Elems[i].ID: r}
		ed.primary = a.cfg.Elems[i].ID
		ed.sx, ed.sy = r.X+r.W-1, r.Y+r.H-1
		a.scr.ShowCursor(ed.sx, ed.sy)
		a.toast("resize: point the new corner and click · arrows adjust · Enter done · Esc cancel", false)
	}
}

func (a *App) followTo(x, y int) {
	ed := &a.ed
	dx, dy := x-ed.sx, y-ed.sy
	if ed.drag == followMove {
		a.moveBy(ed.orig, dx, dy)
		return
	}
	if i := a.primaryIndex(); i >= 0 {
		a.resizeTo(i, ed.orig[a.cfg.Elems[i].ID], edgeR|edgeB, dx, dy)
	}
}

func (a *App) followMouse(m ui.Mouse) {
	switch m.Kind {
	case ui.Move, ui.Drag:
		a.followTo(m.X, m.Y)
	case ui.Press:
		if m.Button == ui.Left {
			a.followTo(m.X, m.Y)
			a.finishFollow(true)
		} else {
			a.finishFollow(false)
		}
	}
}

func (a *App) finishFollow(keep bool) {
	ed := &a.ed
	if keep {
		a.commit(ed.before)
	} else {
		a.cfg = ed.before
	}
	ed.drag = dragNone
}

// ── keyboard ─────────────────────────────────────────────────────────────────

func (a *App) editKey(ev *tcell.EventKey) {
	ed := &a.ed
	if ed.drag == followMove || ed.drag == followSize {
		a.followKey(ev)
		return
	}
	ctrl := ev.Modifiers()&tcell.ModCtrl != 0
	shift := ev.Modifiers()&tcell.ModShift != 0
	switch ev.Key() {
	case tcell.KeyEscape:
		if ed.showBand || len(ed.sel) > 0 {
			ed.clear()
		}
	case tcell.KeyCtrlS:
		a.save()
	case tcell.KeyCtrlZ:
		a.undo()
	case tcell.KeyCtrlY:
		a.redo()
	case tcell.KeyCtrlC:
		a.copySel()
	case tcell.KeyCtrlX:
		a.copySel()
		a.removeSel()
	case tcell.KeyCtrlV:
		a.pasteAt(a.mx, a.my)
	case tcell.KeyCtrlD:
		a.duplicate()
	case tcell.KeyCtrlA:
		a.selectAll()
	case tcell.KeyCtrlG:
		a.group()
	case tcell.KeyCtrlE:
		if ed.dirty {
			a.toast("unsaved changes kept in memory — Ctrl+S to save", false)
		}
		a.setEdit(false)
	case tcell.KeyDelete, tcell.KeyBackspace, tcell.KeyBackspace2:
		a.removeSel()
	case tcell.KeyEnter:
		if i := a.primaryIndex(); i >= 0 {
			a.editElement(i, false)
		}
	case tcell.KeyTab, tcell.KeyBacktab:
		a.cycleSel(ev.Key() == tcell.KeyTab)
	case tcell.KeyF1:
		a.help()
	case tcell.KeyF10:
		a.requestQuit()
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight:
		dx, dy := arrow(ev.Key())
		switch {
		case shift:
			a.nudgeSize(dx, dy)
		case ctrl:
			a.nudge(dx*5, dy*5)
		default:
			a.nudge(dx, dy)
		}
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q':
			a.requestQuit()
		case '?':
			a.help()
		case 'a', 'A':
			a.addMenu(a.mx, a.my, a.defaultArea())
		case 'e':
			if i := a.primaryIndex(); i >= 0 {
				a.editElement(i, false)
			}
		case 'x':
			a.removeSel()
		case 'g':
			a.group()
		case 'G':
			a.ungroup()
		case 'l', 'L':
			a.toggleLock()
		case 'm', 'M':
			a.startFollow(followMove)
		case 'r', 'R':
			a.startFollow(followSize)
		case 's', 'S':
			a.sourcesDialog()
		case 'v', 'V':
			a.inspector(nil)
		case 't', 'T':
			a.settingsForm()
		case '#':
			ed.grid = !ed.grid
		case 'b', 'B':
			a.armBand()
		case 'u':
			a.undo()
		case ']':
			a.arrange("forward")
		case '[':
			a.arrange("backward")
		}
	}
}

func arrow(k tcell.Key) (int, int) {
	switch k {
	case tcell.KeyUp:
		return 0, -1
	case tcell.KeyDown:
		return 0, 1
	case tcell.KeyLeft:
		return -1, 0
	}
	return 1, 0
}

func (a *App) followKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape:
		a.finishFollow(false)
	case tcell.KeyEnter:
		a.finishFollow(true)
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight:
		dx, dy := arrow(ev.Key())
		if ev.Modifiers()&tcell.ModCtrl != 0 {
			dx, dy = dx*5, dy*5
		}
		// Arrow keys move the virtual pointer.
		if a.ed.drag == followMove {
			a.mx += dx
			a.my += dy
		} else {
			a.mx, a.my = a.ed.sx+dx, a.ed.sy+dy
			if i := a.primaryIndex(); i >= 0 {
				r := a.rectsOf(map[string]bool{a.cfg.Elems[i].ID: true})[a.cfg.Elems[i].ID]
				a.mx, a.my = r.X+r.W-1+dx, r.Y+r.H-1+dy
			}
		}
		a.followTo(a.mx, a.my)
	}
}

// nudgeCheckpoint coalesces rapid keyboard nudges into one undo step.
func (a *App) nudgeCheckpoint() {
	if time.Since(a.ed.lastNudge) > 1200*time.Millisecond {
		a.checkpoint()
	}
	a.ed.lastNudge = time.Now()
}

func (a *App) nudge(dx, dy int) {
	if len(a.ed.sel) == 0 {
		return
	}
	a.nudgeCheckpoint()
	a.moveBy(a.rectsOf(a.ed.sel), dx, dy)
}

func (a *App) nudgeSize(dx, dy int) {
	i := a.primaryIndex()
	if i < 0 || a.cfg.Elems[i].Locked {
		return
	}
	a.nudgeCheckpoint()
	a.layout()
	a.resizeTo(i, a.rects[i], edgeR|edgeB, dx, dy)
}

func (a *App) cycleSel(fwd bool) {
	n := len(a.cfg.Elems)
	if n == 0 {
		return
	}
	i := a.cfg.Index(a.ed.primary)
	if fwd {
		i = (i + 1) % n
	} else {
		i = (i - 1 + n) % n
		if i < 0 {
			i = n - 1
		}
	}
	a.selectOnly(i)
}

// defaultArea is where keyboard-added widgets go: at the pointer, 30×6.
func (a *App) defaultArea() render.Rect {
	w, h := a.size()
	r := render.Rect{X: a.mx, Y: a.my, W: 30, H: 6}
	r.X = max(0, min(r.X, w-r.W))
	r.Y = max(0, min(r.Y, h-r.H))
	return r
}

// ── commands ─────────────────────────────────────────────────────────────────

// defaultSize is the size of a widget added without a selected area.
var defaultSize = map[string][2]int{
	"text": {30, 5}, "gauge": {30, 3}, "spark": {40, 6}, "bignum": {26, 5},
	"log": {60, 10}, "box": {30, 8}, "button": {16, 3},
}

func newElement(typ string) config.Element {
	e := config.Element{Type: typ}
	switch typ {
	case "text":
		e.Title = "Text"
		e.Text = "[muted]time[-] {sys.time}\n[muted]up[-]   {sys.uptime}"
	case "gauge":
		e.Title = "Gauge"
		e.Max = 100
		e.Thresholds = []float64{60, 90}
	case "spark":
		e.Title = "Trend"
	case "bignum":
		e.Text = "{sys.time}"
	case "log":
		e.Title = "Events"
		e.Stream = "events"
	case "box":
		e.Title = "Section"
	case "button":
		e.Text = "Run"
		e.Action = "echo hello from {sys.host}"
	}
	return e
}

// addElement creates a widget of typ in r and opens its properties.
func (a *App) addElement(typ string, r render.Rect, withVar string) {
	before := a.cfg.Clone()
	e := newElement(typ)
	e.ID = config.UniqueID(a.cfg, typ)
	if withVar != "" {
		e.Var = withVar
		switch typ {
		case "text":
			e.Title = withVar
			e.Text = "{" + withVar + "}"
		case "bignum":
			e.Text = "{" + withVar + "}"
		default:
			e.Title = withVar
		}
	}
	// Seed the unit kind; setRect below keeps it while computing the values.
	e.W, e.H = config.Cells(1), config.Cells(1)
	if a.cfg.Units == "percent" {
		e.X, e.Y, e.W, e.H = config.Percent(0), config.Percent(0), config.Percent(10), config.Percent(10)
	}
	if r.W <= 2 && r.H <= 1 {
		sz := defaultSize[typ]
		w, h := a.size()
		r = render.Rect{X: max(0, min(r.X, w-sz[0])), Y: max(0, min(r.Y, h-sz[1])), W: sz[0], H: sz[1]}
	}
	a.cfg.Elems = append(a.cfg.Elems, e)
	i := len(a.cfg.Elems) - 1
	a.setRect(i, r)
	a.selectOnly(i)
	a.ed.showBand = false
	a.editElementFrom(i, true, before)
}

func (a *App) removeSel() {
	ids := a.selected()
	if len(ids) == 0 {
		return
	}
	var keep []config.Element
	removed, locked := 0, 0
	for _, e := range a.cfg.Elems {
		if a.ed.sel[e.ID] && !e.Locked {
			removed++
			continue
		}
		if a.ed.sel[e.ID] {
			locked++
		}
		keep = append(keep, e)
	}
	if removed == 0 {
		a.toast("selection is locked", true)
		return
	}
	a.checkpoint()
	a.cfg.Elems = keep
	a.ed.clear()
	msg := fmt.Sprintf("removed %d widget(s) — Ctrl+Z to undo", removed)
	if locked > 0 {
		msg += fmt.Sprintf(", %d locked kept", locked)
	}
	a.toast(msg, false)
}

func (a *App) copySel() {
	a.ed.clip = nil
	a.layout()
	for _, i := range a.selected() {
		e := a.cfg.Elems[i].Clone()
		// Store absolute geometry so paste lands exactly where intended.
		r := a.rects[i]
		e.X, e.Y, e.W, e.H = config.Cells(r.X), config.Cells(r.Y), config.Cells(r.W), config.Cells(r.H)
		a.ed.clip = append(a.ed.clip, e)
	}
	if len(a.ed.clip) > 0 {
		a.toast(fmt.Sprintf("copied %d widget(s)", len(a.ed.clip)), false)
	}
}

// paste_ inserts the clipboard with its top-left corner at (x, y).
func (a *App) pasteAt(x, y int) {
	if len(a.ed.clip) == 0 {
		a.toast("clipboard is empty", true)
		return
	}
	a.checkpoint()
	minX, minY := 1<<30, 1<<30
	for _, e := range a.ed.clip {
		minX, minY = min(minX, e.X.Off), min(minY, e.Y.Off)
	}
	w, h := a.size()
	groups := map[string]string{}
	a.ed.sel = map[string]bool{}
	for _, c := range a.ed.clip {
		e := c.Clone()
		e.ID = config.UniqueID(a.cfg, strings.TrimRight(c.ID, "0123456789"))
		e.Locked = false
		if e.Group != "" {
			if _, ok := groups[e.Group]; !ok {
				groups[e.Group] = a.newGroupName()
			}
			e.Group = groups[e.Group]
		}
		r := render.Rect{X: x + e.X.Off - minX, Y: y + e.Y.Off - minY, W: e.W.Off, H: e.H.Off}
		r.X = max(0, min(r.X, w-r.W))
		r.Y = max(0, min(r.Y, h-r.H))
		a.cfg.Elems = append(a.cfg.Elems, e)
		a.setRect(len(a.cfg.Elems)-1, r)
		a.ed.sel[e.ID] = true
		a.ed.primary = e.ID
	}
	a.toast(fmt.Sprintf("pasted %d widget(s)", len(a.ed.clip)), false)
}

func (a *App) duplicate() {
	sel := a.selected()
	if len(sel) == 0 {
		return
	}
	saved := a.ed.clip
	a.copySel()
	x, y := 1<<30, 1<<30
	for _, e := range a.ed.clip {
		x, y = min(x, e.X.Off), min(y, e.Y.Off)
	}
	a.pasteAt(x+2, y+1)
	a.ed.clip = saved
}

func (a *App) newGroupName() string {
	used := map[string]bool{}
	for _, e := range a.cfg.Elems {
		used[e.Group] = true
	}
	for n := 1; ; n++ {
		if g := fmt.Sprintf("group%d", n); !used[g] {
			return g
		}
	}
}

func (a *App) group() {
	sel := a.selected()
	if len(sel) < 2 {
		a.toast("select at least two widgets to group (Shift+click)", true)
		return
	}
	a.checkpoint()
	g := a.newGroupName()
	for _, i := range sel {
		a.cfg.Elems[i].Group = g
	}
	a.toast(fmt.Sprintf("grouped %d widgets as %s", len(sel), g), false)
}

func (a *App) ungroup() {
	sel := a.selected()
	n := 0
	for _, i := range sel {
		if a.cfg.Elems[i].Group != "" {
			n++
		}
	}
	if n == 0 {
		a.toast("selection is not grouped", true)
		return
	}
	a.checkpoint()
	for _, i := range sel {
		a.cfg.Elems[i].Group = ""
	}
	a.toast(fmt.Sprintf("ungrouped %d widgets", n), false)
}

func (a *App) toggleLock() {
	sel := a.selected()
	if len(sel) == 0 {
		return
	}
	lock := false
	for _, i := range sel {
		if !a.cfg.Elems[i].Locked {
			lock = true
		}
	}
	a.checkpoint()
	for _, i := range sel {
		a.cfg.Elems[i].Locked = lock
	}
	if lock {
		a.toast(fmt.Sprintf("locked %d widget(s)", len(sel)), false)
	} else {
		a.toast(fmt.Sprintf("unlocked %d widget(s)", len(sel)), false)
	}
}

// arrange changes the z-order of the selection.
func (a *App) arrange(how string) {
	sel := a.selected()
	if len(sel) == 0 {
		return
	}
	a.checkpoint()
	var in, out []config.Element
	for _, e := range a.cfg.Elems {
		if a.ed.sel[e.ID] {
			in = append(in, e)
		} else {
			out = append(out, e)
		}
	}
	switch how {
	case "front":
		a.cfg.Elems = append(out, in...)
	case "back":
		a.cfg.Elems = append(in, out...)
	case "forward", "backward":
		el := a.cfg.Elems
		if how == "forward" {
			for k := len(el) - 2; k >= 0; k-- {
				if a.ed.sel[el[k].ID] && !a.ed.sel[el[k+1].ID] {
					el[k], el[k+1] = el[k+1], el[k]
				}
			}
		} else {
			for k := 1; k < len(el); k++ {
				if a.ed.sel[el[k].ID] && !a.ed.sel[el[k-1].ID] {
					el[k], el[k-1] = el[k-1], el[k]
				}
			}
		}
	}
}

// align lines up the selection with the primary widget.
func (a *App) align(how string) {
	sel := a.selected()
	p := a.primaryIndex()
	if len(sel) < 2 || p < 0 {
		a.toast("select two or more widgets; the last clicked one is the reference", true)
		return
	}
	a.checkpoint()
	a.layout()
	ref := a.rects[p]
	for _, i := range sel {
		if i == p || a.cfg.Elems[i].Locked {
			continue
		}
		r := a.rects[i]
		switch how {
		case "left":
			r.X = ref.X
		case "right":
			r.X = ref.X + ref.W - r.W
		case "top":
			r.Y = ref.Y
		case "bottom":
			r.Y = ref.Y + ref.H - r.H
		case "hcenter":
			r.X = ref.X + (ref.W-r.W)/2
		case "vcenter":
			r.Y = ref.Y + (ref.H-r.H)/2
		case "width":
			r.W = ref.W
		case "height":
			r.H = ref.H
		}
		a.setRect(i, r)
	}
}

// convertUnits rewrites the selection's geometry in cells or percent.
func (a *App) convertUnits(pct bool) {
	sel := a.selected()
	if len(sel) == 0 {
		return
	}
	a.checkpoint()
	a.layout()
	for _, i := range sel {
		e := &a.cfg.Elems[i]
		r := a.rects[i]
		if pct {
			e.X, e.Y, e.W, e.H = config.Percent(0), config.Percent(0), config.Percent(0), config.Percent(0)
		} else {
			e.X, e.Y, e.W, e.H = config.Cells(1), config.Cells(1), config.Cells(1), config.Cells(1)
		}
		a.setRect(i, r)
	}
	unit := "cells"
	if pct {
		unit = "percent (scales with the terminal)"
	}
	a.toast(fmt.Sprintf("%d widget(s) now use %s", len(sel), unit), false)
}

// selectInside selects every widget fully inside r.
func (a *App) selectInside(r render.Rect) int {
	a.layout()
	a.ed.sel = map[string]bool{}
	n := 0
	for i, e := range a.cfg.Elems {
		if er := a.rects[i]; er.Intersect(r) == er {
			a.ed.sel[e.ID] = true
			a.ed.primary = e.ID
			n++
		}
	}
	return n
}

// ── menus ────────────────────────────────────────────────────────────────────

func (a *App) typeItems(r render.Rect) []ui.Item {
	var items []ui.Item
	for _, t := range config.ElementTypes {
		items = append(items, ui.Item{Label: config.ElementTypeLabels[t], Action: func() { a.addElement(t, r, "") }})
	}
	return items
}

func (a *App) addMenu(x, y int, r render.Rect) {
	a.Push(ui.NewMenu(x, y, "Add widget", a.typeItems(r)))
}

func (a *App) areaMenu(x, y int, r render.Rect) {
	inside := 0
	for _, er := range a.rects {
		if er.Intersect(r) == er {
			inside++
		}
	}
	items := []ui.Item{{Label: fmt.Sprintf("Area %d×%d at %d,%d", r.W, r.H, r.X, r.Y), Header: true}}
	for _, it := range a.typeItems(r) {
		it.Label = "Add " + strings.ToLower(it.Label)
		items = append(items, it)
	}
	items = append(items, ui.Sep)
	items = append(items, ui.Item{Label: fmt.Sprintf("Select widgets inside (%d)", inside), Disabled: inside == 0, Action: func() {
		a.selectInside(r)
	}})
	if len(a.ed.clip) > 0 {
		items = append(items, ui.Item{Label: "Paste here", Hint: "Ctrl+V", Action: func() { a.pasteAt(r.X, r.Y) }})
	}
	items = append(items, ui.Item{Label: "Cancel", Hint: "Esc", Action: func() {}})
	m := ui.NewMenu(x+1, y, "", items)
	m.OnClose = func() { a.ed.showBand = false }
	a.Push(m)
}

func (a *App) commonItems(x, y int) []ui.Item {
	return []ui.Item{
		{Label: "Sources & parsing…", Hint: "s", Action: a.sourcesDialog},
		{Label: "Variables…", Hint: "v", Action: func() { a.inspector(nil) }},
		{Label: "Dashboard & theme…", Hint: "t", Action: a.settingsForm},
		ui.Sep,
		{Label: "Undo", Hint: "Ctrl+Z", Disabled: len(a.ed.undo) == 0, Action: a.undo},
		{Label: "Redo", Hint: "Ctrl+Y", Disabled: len(a.ed.redo) == 0, Action: a.redo},
		{Label: "Save", Hint: "Ctrl+S", Action: func() { a.save() }},
		{Label: "Grid", Hint: "#", Checked: a.ed.grid, Action: func() { a.ed.grid = !a.ed.grid }},
		{Label: "Help", Hint: "?", Action: a.help},
		ui.Sep,
		{Label: "Leave edit mode", Hint: "Ctrl+E", Action: func() { a.setEdit(false) }},
		{Label: "Quit", Hint: "q", Action: a.requestQuit},
	}
}

func (a *App) canvasMenu(x, y int) {
	r := render.Rect{X: x, Y: y, W: 1, H: 1}
	items := []ui.Item{
		{Label: "Add widget", Hint: "a", Sub: a.typeItems(r)},
		{Label: "Draw area…", Hint: "b", Action: a.armBand},
		{Label: "Paste", Hint: "Ctrl+V", Disabled: len(a.ed.clip) == 0, Action: func() { a.pasteAt(x, y) }},
		{Label: "Select all", Hint: "Ctrl+A", Disabled: len(a.cfg.Elems) == 0, Action: a.selectAll},
		{Label: "Deselect", Hint: "Esc", Disabled: len(a.ed.sel) == 0, Action: a.ed.clear},
		ui.Sep,
	}
	a.Push(ui.NewMenu(x+1, y, "Canvas", append(items, a.commonItems(x, y)...)))
}

func (a *App) elementMenu(x, y, i int) {
	e := a.cfg.Elems[i]
	sel := a.selected()
	multi := len(sel) > 1 || (len(sel) == 1 && !a.ed.sel[e.ID])
	lockLabel := "Lock"
	if e.Locked {
		lockLabel = "Unlock"
	}
	selLabel, selHint := "Deselect", "Ctrl+click"
	switch {
	case !a.ed.sel[e.ID] && len(sel) > 0:
		selLabel = "Add to selection"
	case !a.ed.sel[e.ID]:
		selLabel = "Select"
	}
	// Actions other than selection work on the clicked widget: when it is
	// not part of the selection, select it first.
	only := func(f func()) func() {
		return func() {
			if !a.ed.sel[e.ID] {
				a.selectOnly(i)
			}
			f()
		}
	}
	title := fmt.Sprintf("%s · %s", e.ID, e.Type)
	if multi {
		title = fmt.Sprintf("%d selected", len(sel))
	}
	if e.Locked {
		title += " · locked"
	}
	r := render.Rect{X: x, Y: y, W: 1, H: 1}
	items := []ui.Item{
		{Label: "Edit properties…", Hint: "Enter", Action: only(func() { a.editElement(i, false) })},
		{Label: selLabel, Hint: selHint, Action: func() { a.toggleSel(i) }},
		{Label: "Group selection", Hint: "g", Disabled: !multi, Action: a.group},
		{Label: "Ungroup", Hint: "G", Disabled: e.Group == "", Action: only(a.ungroup)},
		{Label: lockLabel, Hint: "l", Action: only(a.toggleLock)},
		{Label: "Move", Hint: "m", Disabled: e.Locked, Action: only(func() { a.startFollow(followMove) })},
		{Label: "Resize", Hint: "r", Disabled: e.Locked, Action: only(func() { a.startFollow(followSize) })},
		{Label: "Copy", Hint: "Ctrl+C", Action: only(a.copySel)},
		{Label: "Duplicate", Hint: "Ctrl+D", Action: only(a.duplicate)},
		{Label: "Remove", Hint: "Del", Disabled: e.Locked && !multi, Action: only(a.removeSel)},
		ui.Sep,
		{Label: "Arrange", Sub: []ui.Item{
			{Label: "Bring to front", Action: func() { a.arrange("front") }},
			{Label: "Bring forward", Hint: "]", Action: func() { a.arrange("forward") }},
			{Label: "Send backward", Hint: "[", Action: func() { a.arrange("backward") }},
			{Label: "Send to back", Action: func() { a.arrange("back") }},
		}},
		{Label: "Align to " + a.ed.primary, Disabled: !multi, Sub: []ui.Item{
			{Label: "Left edges", Action: func() { a.align("left") }},
			{Label: "Right edges", Action: func() { a.align("right") }},
			{Label: "Top edges", Action: func() { a.align("top") }},
			{Label: "Bottom edges", Action: func() { a.align("bottom") }},
			{Label: "Centers horizontally", Action: func() { a.align("hcenter") }},
			{Label: "Centers vertically", Action: func() { a.align("vcenter") }},
			{Label: "Same width", Action: func() { a.align("width") }},
			{Label: "Same height", Action: func() { a.align("height") }},
		}},
		{Label: "Units", Sub: []ui.Item{
			{Label: "Cells (fixed size)", Checked: !e.X.IsPct && !e.W.IsPct, Action: func() { a.convertUnits(false) }},
			{Label: "Percent (responsive)", Checked: e.X.IsPct && e.W.IsPct, Action: func() { a.convertUnits(true) }},
		}},
		ui.Sep,
		{Label: "Add widget here", Sub: a.typeItems(r)},
		{Label: "Draw area…", Hint: "b", Action: a.armBand},
		{Label: "Paste", Hint: "Ctrl+V", Disabled: len(a.ed.clip) == 0, Action: func() { a.pasteAt(x, y) }},
		ui.Sep,
	}
	common := a.commonItems(x, y)
	items = append(items, common[:3]...)
	items = append(items, ui.Item{Label: "More", Sub: common[4:]})
	a.Push(ui.NewMenu(x+1, y, title, items))
}

// ── drawing ──────────────────────────────────────────────────────────────────

func (a *App) drawGrid(s render.Surface) {
	w, h := a.size()
	st := a.th.Base().Foreground(a.th.C("border"))
	for y := 0; y < h; y += 2 {
		for x := 0; x < w; x += 4 {
			s.Rune(x, y, '·', st)
		}
	}
}

// outline recolors (or draws) the frame of r.
func outline(s render.Surface, r render.Rect, c tcell.Color, bordered bool) {
	recolor := func(st tcell.Style) tcell.Style { return st.Foreground(c).Bold(true) }
	if bordered {
		s.Restyle(render.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, recolor)
		s.Restyle(render.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1}, recolor)
		s.Restyle(render.Rect{X: r.X, Y: r.Y, W: 1, H: r.H}, recolor)
		s.Restyle(render.Rect{X: r.X + r.W - 1, Y: r.Y, W: 1, H: r.H}, recolor)
		return
	}
	corner := func(x, y int, ch rune) {
		_, _, st, _ := s.S.GetContent(x, y)
		s.Rune(x, y, ch, st.Foreground(c).Bold(true))
	}
	if r.W >= 2 && r.H >= 2 {
		corner(r.X, r.Y, '┌')
		corner(r.X+r.W-1, r.Y, '┐')
		corner(r.X, r.Y+r.H-1, '└')
		corner(r.X+r.W-1, r.Y+r.H-1, '┘')
	} else {
		s.Restyle(r, func(st tcell.Style) tcell.Style { return st.Underline(true).Foreground(c) })
	}
}

func (a *App) drawEdit(s render.Surface) {
	ed := &a.ed
	w, h := a.size()
	for i, e := range a.cfg.Elems {
		r := a.rects[i]
		bordered := elem.BorderStyle(a.th, &e) != "none"
		switch {
		case ed.sel[e.ID]:
			c := a.th.C("select")
			if e.Locked {
				c = a.th.C("crit")
			}
			outline(s, r, c, bordered)
			if e.ID != ed.primary {
				continue
			}
			tag := " " + e.ID + " "
			if e.Locked {
				tag = " 🔒" + tag
			}
			if e.Group != "" {
				tag += "◇" + e.Group + " "
			}
			tw := tmpl.Width(tag)
			st := tcell.StyleDefault.Background(c).Foreground(a.th.C("bg")).Bold(true)
			ty := r.Y + r.H - 1
			if r.H < 2 {
				ty = r.Y
			}
			s.Text(r.X+max(1, r.W-tw-2), ty, tag, st, max(0, r.W-2))
			if !e.Locked && r.W >= 2 && r.H >= 2 {
				s.Rune(r.X+r.W-1, r.Y+r.H-1, '◢', tcell.StyleDefault.Background(a.th.C("bg")).Foreground(c))
			}
		case i == ed.hover && ed.drag == dragNone:
			outline(s, r, a.th.C("accent"), bordered)
		case !bordered:
			outline(s, r, a.th.C("border"), false)
		}
	}
	if ed.showBand || ed.drag == dragBand {
		b := ed.band
		s.Restyle(b, func(st tcell.Style) tcell.Style { return st.Background(a.th.C("panel")) })
		s.Box(b, "dashed", tcell.StyleDefault.Background(a.th.C("panel")).Foreground(a.th.C("select")))
		label := fmt.Sprintf(" %d×%d @ %d,%d ", b.W, b.H, b.X, b.Y)
		ly := b.Y - 1
		if ly < 0 {
			ly = b.Y + b.H
		}
		s.Text(b.X, ly, label, tcell.StyleDefault.Background(a.th.C("select")).Foreground(a.th.C("bg")).Bold(true), -1)
	}
	a.drawStatus(s, w, h)
}

func (a *App) drawStatus(s render.Surface, w, h int) {
	ed := &a.ed
	st := tcell.StyleDefault.Background(a.th.C("panel")).Foreground(a.th.C("fg"))
	y := h - 1
	s.Fill(render.Rect{X: 0, Y: y, W: w, H: 1}, st)
	x := s.Text(0, y, " ✎ EDIT ", st.Background(a.th.C("select")).Foreground(a.th.C("bg")).Bold(true), -1)
	file := " " + a.path
	if ed.dirty {
		file += " ●"
	}
	x += s.Text(x, y, file+" ", st.Foreground(a.th.C("muted")), -1)
	info := ""
	switch {
	case ed.drag == dragBand || ed.showBand:
		info = fmt.Sprintf("area %d×%d @ %d,%d", ed.band.W, ed.band.H, ed.band.X, ed.band.Y)
	case len(ed.sel) > 0:
		if i := a.primaryIndex(); i >= 0 {
			e := a.cfg.Elems[i]
			r := a.rects[i]
			info = fmt.Sprintf("%s (%s) %d,%d %d×%d  [%s %s %s %s]", e.ID, e.Type, r.X, r.Y, r.W, r.H, e.X, e.Y, e.W, e.H)
			if len(ed.sel) > 1 {
				info = fmt.Sprintf("%d selected · ", len(ed.sel)) + info
			}
		}
	default:
		info = fmt.Sprintf("%d widgets · %d sources", len(a.cfg.Elems), len(a.cfg.Sources))
	}
	x += s.Text(x, y, "│ "+info+" ", st.Foreground(a.th.C("accent")), -1)
	hint := "right-click: menu · drag empty: add · ? help · ^S save"
	if ed.armBand {
		hint = "DRAW AREA — drag a rectangle anywhere · Esc cancel"
	}
	switch ed.drag {
	case followMove:
		hint = "MOVE — click to drop · Esc cancel"
	case followSize:
		hint = "RESIZE — click the new corner · Esc cancel"
	case dragResize:
		hint = "resizing"
	case dragMove:
		hint = "moving"
	}
	if hw := tmpl.Width(hint); x+hw+1 < w {
		s.Text(w-hw-1, y, hint, st.Foreground(a.th.C("muted")), -1)
	}
	if ed.drag == followSize {
		a.scr.ShowCursor(a.mx, a.my)
	}
}
