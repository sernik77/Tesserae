// Package app wires configuration, data engine, renderer and editor together.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/sernik77/tesserae/internal/config"
	"github.com/sernik77/tesserae/internal/data"
	"github.com/sernik77/tesserae/internal/elem"
	"github.com/sernik77/tesserae/internal/render"
	"github.com/sernik77/tesserae/internal/tmpl"
	"github.com/sernik77/tesserae/internal/ui"
)

// Options configure a run.
type Options struct {
	Path   string
	Edit   bool
	Screen tcell.Screen // optional; tests pass a simulation screen
}

// App is a running dashboard.
type App struct {
	path  string
	cfg   *config.Dashboard
	th    *render.Theme
	store *data.Store
	eng   *data.Engine
	scr   tcell.Screen

	edit     bool
	overlays []ui.Overlay
	states   map[string]*elem.State
	rects    []render.Rect

	toastMsg string
	toastErr bool
	toastAt  time.Time

	mouse   ui.MouseTracker
	mx, my  int
	quit    bool
	modTime time.Time
	backup  bool // a .bak has been written this session

	hoverID  string // button under the pointer (view mode)
	skipDraw bool   // the last event batch changed nothing visible

	pasting bool
	pasteB  strings.Builder

	calls  chan func()
	ticker *time.Ticker
	tickIv time.Duration
	ctx    context.Context
	cancel context.CancelFunc

	ed editor
}

// New loads (or creates, in edit mode) the dashboard at opts.Path.
func New(opts Options) (*App, error) {
	cfg, err := config.Load(opts.Path)
	if err != nil {
		if !os.IsNotExist(err) || !opts.Edit {
			return nil, err
		}
		name := strings.TrimSuffix(filepath.Base(opts.Path), filepath.Ext(opts.Path))
		cfg = config.New(name)
	}
	a := &App{
		path:   opts.Path,
		cfg:    cfg,
		th:     render.NewTheme(cfg.Theme),
		store:  data.NewStore(),
		edit:   opts.Edit,
		states: map[string]*elem.State{},
		calls:  make(chan func(), 64),
		scr:    opts.Screen,
	}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.eng = data.NewEngine(a.store, cfg.Shell)
	a.ed.init()
	if fi, err := os.Stat(opts.Path); err == nil {
		a.modTime = fi.ModTime()
	} else if opts.Edit {
		a.ed.dirty = true
		a.toast("New dashboard — drag on the canvas to add a widget, right-click for the menu", false)
	}
	return a, nil
}

// Sync runs f on the UI goroutine and waits for it (used by tests).
func (a *App) Sync(f func()) {
	done := make(chan struct{})
	a.post(func() { f(); close(done) })
	select {
	case <-done:
	case <-a.ctx.Done():
	}
}

// Store exposes the variable store (used by tests).
func (a *App) Store() *data.Store { return a.store }

// Config exposes the current configuration (used by tests).
func (a *App) Config() *config.Dashboard { return a.cfg }

// Run starts the TUI and blocks until the user quits.
func (a *App) Run() error {
	if a.scr == nil {
		s, err := tcell.NewScreen()
		if err != nil {
			return err
		}
		a.scr = s
	}
	if err := a.scr.Init(); err != nil {
		return err
	}
	defer a.scr.Fini()
	a.scr.EnableMouse(tcell.MouseMotionEvents)
	a.scr.EnablePaste()
	a.scr.SetTitle("tesserae — " + a.cfg.Name)

	a.startData()
	defer a.eng.Stop()
	defer a.cancel()

	events := make(chan tcell.Event, 256)
	quit := make(chan struct{})
	go a.scr.ChannelEvents(events, quit)
	defer close(quit)

	a.resetTicker()
	defer a.ticker.Stop()
	a.draw()
	for !a.quit {
		select {
		case ev := <-events:
			a.handle(ev)
			skip := a.skipDraw
			// Coalesce bursts (drags, pastes) into one redraw.
			for drained := false; !drained && !a.quit; {
				select {
				case ev := <-events:
					a.handle(ev)
					skip = skip && a.skipDraw
				default:
					drained = true
				}
			}
			a.skipDraw = skip
		case f := <-a.calls:
			f()
		case <-a.ticker.C:
			a.store.Sample()
			a.checkReload()
		}
		if a.cfg.Refresh.Duration != a.tickIv {
			a.resetTicker()
		}
		if a.skipDraw {
			a.skipDraw = false
			continue
		}
		a.draw()
	}
	return nil
}

func (a *App) resetTicker() {
	if a.ticker != nil {
		a.ticker.Stop()
	}
	a.tickIv = a.cfg.Refresh.Duration
	a.ticker = time.NewTicker(a.tickIv)
}

// startData seeds initial variables and (re)starts all sources.
func (a *App) startData() {
	for k, v := range a.cfg.Vars {
		a.store.SetDefault(k, v)
	}
	a.eng.Shell = a.cfg.Shell
	a.eng.Start(a.cfg.Sources)
}

// post runs f on the UI goroutine.
func (a *App) post(f func()) {
	select {
	case a.calls <- f:
	case <-a.ctx.Done():
	}
}

func (a *App) toast(msg string, isErr bool) {
	a.toastMsg, a.toastErr, a.toastAt = msg, isErr, time.Now()
}

// Toast implements ui.Host.
func (a *App) Toast(msg string, isErr bool) { a.toast(msg, isErr) }

// Push implements ui.Host.
func (a *App) Push(o ui.Overlay) { a.overlays = append(a.overlays, o) }

func (a *App) top() ui.Overlay {
	if len(a.overlays) == 0 {
		return nil
	}
	return a.overlays[len(a.overlays)-1]
}

func (a *App) prune() {
	out := a.overlays[:0]
	for _, o := range a.overlays {
		if !o.Done() {
			out = append(out, o)
		}
	}
	a.overlays = out
}

func (a *App) state(id string) *elem.State {
	st, ok := a.states[id]
	if !ok {
		st = &elem.State{}
		a.states[id] = st
	}
	return st
}

func (a *App) size() (int, int) { return a.scr.Size() }

// layout resolves element rectangles against the current screen size.
func (a *App) layout() {
	w, h := a.size()
	a.rects = a.rects[:0]
	for i := range a.cfg.Elems {
		a.rects = append(a.rects, resolve(&a.cfg.Elems[i], w, h))
	}
}

func resolve(e *config.Element, w, h int) render.Rect {
	x, rw := config.ResolveSpan(e.X, e.W, w)
	y, rh := config.ResolveSpan(e.Y, e.H, h)
	return render.Rect{X: x, Y: y, W: rw, H: rh}
}

// hit returns the index of the topmost element at (x, y), or -1.
func (a *App) hit(x, y int) int {
	for i := len(a.rects) - 1; i >= 0; i-- {
		if a.rects[i].Contains(x, y) {
			return i
		}
	}
	return -1
}

func (a *App) draw() {
	a.scr.HideCursor()
	s := render.NewSurface(a.scr)
	w, h := a.size()
	s.Fill(s.Clip, a.th.Base())
	if w < 20 || h < 5 {
		s.Text(0, 0, "terminal too small", a.th.Base(), -1)
		a.scr.Show()
		return
	}
	a.layout()
	if a.edit && a.ed.grid {
		a.drawGrid(s)
	}
	for i := range a.cfg.Elems {
		e := &a.cfg.Elems[i]
		elem.Draw(elem.Ctx{Surf: s, Theme: a.th, Store: a.store, State: a.state(e.ID)}, e, a.rects[i])
	}
	if len(a.cfg.Elems) == 0 && !a.edit {
		msg := "Empty dashboard — run with --edit to build it"
		s.Text((w-tmpl.Width(msg))/2, h/2, msg, a.th.Base().Foreground(a.th.C("muted")), -1)
	}
	if a.edit {
		a.drawEdit(s)
	}
	for _, o := range a.overlays {
		o.Draw(s, a.th)
	}
	a.drawToast(s)
	a.scr.Show()
}

func (a *App) drawToast(s render.Surface) {
	if a.toastMsg == "" {
		return
	}
	life := 3 * time.Second
	if a.toastErr {
		life = 7 * time.Second
	}
	if time.Since(a.toastAt) > life {
		a.toastMsg = ""
		return
	}
	w, h := a.size()
	msg := " " + a.toastMsg + " "
	if a.toastErr {
		msg = " ✗" + msg
	} else {
		msg = " ✓" + msg
	}
	tw := min(tmpl.Width(msg), w-2)
	y := h - 2
	if !a.edit {
		y = h - 1
	}
	st := tcell.StyleDefault.Background(a.th.C("panel")).Foreground(a.th.C("ok")).Bold(true)
	if a.toastErr {
		st = st.Foreground(a.th.C("crit"))
	}
	s.Text(w-tw-1, y, msg, st, tw)
}

func (a *App) handle(ev tcell.Event) {
	a.skipDraw = false
	switch ev := ev.(type) {
	case *tcell.EventResize:
		a.scr.Sync()
	case *tcell.EventPaste:
		if ev.Start() {
			a.pasting = true
			a.pasteB.Reset()
		} else {
			a.pasting = false
			a.paste(a.pasteB.String())
		}
	case *tcell.EventKey:
		if a.pasting {
			switch ev.Key() {
			case tcell.KeyRune:
				a.pasteB.WriteRune(ev.Rune())
			case tcell.KeyEnter:
				a.pasteB.WriteByte('\n')
			case tcell.KeyTab:
				a.pasteB.WriteByte('\t')
			}
			return
		}
		a.key(ev)
	case *tcell.EventMouse:
		for _, m := range a.mouse.Translate(ev) {
			a.mouseEv(m)
		}
	}
}

func (a *App) paste(s string) {
	type paster interface{ Paste(string) }
	if p, ok := a.top().(paster); ok {
		p.Paste(s)
	}
}

func (a *App) key(ev *tcell.EventKey) {
	if o := a.top(); o != nil {
		o.Key(ev)
		a.prune()
		return
	}
	if a.edit {
		a.editKey(ev)
		return
	}
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		a.requestQuit()
	case tcell.KeyCtrlE:
		a.setEdit(true)
	case tcell.KeyCtrlS:
		if a.ed.dirty {
			a.save()
		}
	case tcell.KeyRune:
		switch r := ev.Rune(); r {
		case 'q', 'Q':
			a.requestQuit()
		case '?':
			a.help()
		default:
			for i := range a.cfg.Elems {
				if e := &a.cfg.Elems[i]; e.Type == "button" && e.Key == string(r) {
					a.press(e)
				}
			}
		}
	case tcell.KeyF5:
		if a.ed.dirty {
			a.toast("unsaved edits — Ctrl+S to save first", true)
			break
		}
		a.reload()
	}
}

func (a *App) mouseEv(m ui.Mouse) {
	a.mx, a.my = m.X, m.Y
	if o := a.top(); o != nil {
		o.Mouse(m)
		a.prune()
		return
	}
	if a.edit {
		a.editMouse(m)
		return
	}
	a.layout()
	i := a.hit(m.X, m.Y)
	hover := ""
	if i >= 0 && a.cfg.Elems[i].Type == "button" {
		hover = a.cfg.Elems[i].ID
	}
	if m.Kind == ui.Move && hover == a.hoverID {
		// Plain pointer motion over the same widget: nothing to redraw.
		a.skipDraw = true
		return
	}
	a.hoverID = hover
	for id, st := range a.states {
		st.Hover = id == hover
	}
	if hover != "" {
		a.state(hover).Hover = true
		if m.Kind == ui.Press && m.Button == ui.Left {
			a.press(&a.cfg.Elems[i])
		}
	}
}

// press runs a button action, asking first when the button wants confirmation.
func (a *App) press(e *config.Element) {
	if e.Action == "" {
		a.toast(e.ID+": no action configured", true)
		return
	}
	id := e.ID
	command := tmpl.RenderPlain(e.Action, a.store)
	run := func() {
		st := a.state(id)
		if st.Running {
			return
		}
		st.Running, st.Flash = true, time.Now()
		go func() {
			out, err := data.RunCommand(a.ctx, a.cfg.Shell, command, 2*time.Minute)
			a.post(func() {
				st.Running = false
				a.store.Set(id+".output", out)
				first := out
				if i := strings.IndexByte(first, '\n'); i >= 0 {
					first = first[:i]
				}
				if err != nil {
					a.store.Set(id+".status", "error")
					a.store.Push("actions", "[crit]✗[-] "+tmpl.Escape(id+": "+err.Error()), time.Now())
					a.toast(id+": "+orStr(first, err.Error()), true)
					return
				}
				a.store.Set(id+".status", "ok")
				a.store.Push("actions", "[ok]✓[-] "+tmpl.Escape(id+": "+first), time.Now())
				a.toast(id+": "+orStr(first, "done"), false)
			})
		}()
	}
	if e.Confirm {
		a.Push(ui.NewConfirm("Run action?", "[muted]"+tmpl.Escape(command)+"[-]", []string{"Run", "Cancel"}, func(b string) {
			if b == "Run" {
				run()
			}
		}))
		return
	}
	run()
}

func orStr(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// checkReload reloads the config when the file changed on disk (view mode).
func (a *App) checkReload() {
	// Never replace unsaved edits with the file on disk.
	if a.edit || a.ed.dirty {
		return
	}
	fi, err := os.Stat(a.path)
	if err != nil || fi.ModTime().Equal(a.modTime) {
		return
	}
	a.reload()
}

func (a *App) reload() {
	cfg, err := config.Load(a.path)
	if fi, err := os.Stat(a.path); err == nil {
		a.modTime = fi.ModTime()
	}
	if err != nil {
		a.toast("reload failed: "+firstLine(err.Error()), true)
		return
	}
	a.cfg = cfg
	a.th = render.NewTheme(cfg.Theme)
	a.startData()
	a.toast("reloaded "+filepath.Base(a.path), false)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

func (a *App) setEdit(on bool) {
	a.edit = on
	a.ed.clear()
	if on {
		a.toast("edit mode — right-click anything, drag on empty space to add", false)
	}
}

// requestQuit leaves the app, asking about unsaved changes first.
func (a *App) requestQuit() {
	if !a.ed.dirty {
		a.quit = true
		return
	}
	a.Push(ui.NewConfirm("Unsaved changes", fmt.Sprintf("Save changes to [accent]%s[-] before quitting?", tmpl.Escape(a.path)),
		[]string{"Save", "Discard", "Cancel"}, func(b string) {
			switch b {
			case "Save":
				if a.save() {
					a.quit = true
				}
			case "Discard":
				a.quit = true
			}
		}))
}

func (a *App) save() bool {
	if err := config.Save(a.path, a.cfg, !a.backup); err != nil {
		a.toast("save failed: "+firstLine(err.Error()), true)
		return false
	}
	a.backup = true
	a.ed.dirty = false
	if fi, err := os.Stat(a.path); err == nil {
		a.modTime = fi.ModTime()
	}
	a.toast("saved "+a.path, false)
	return true
}

const helpText = `[title::b]VIEW MODE[-]
  [accent]q[-] / [accent]Esc[-]        quit                  [accent]F5[-]   reload config
  [accent]Ctrl+E[-]          enter edit mode        [accent]?[-]    this help
  click a [accent]button[-] widget or press its key to run its action

[title::b]EDIT MODE[-]   (start with [accent]tesserae --edit file.toml[-])
  [accent]right-click[-]     context menu on a widget or on empty canvas
  [accent]drag empty[-]      select a rectangle → add a widget there / select inside
  [accent]click / drag[-]    select / move ([accent]Ctrl[-]/[accent]Alt[-]+click or menu → Add to selection)
  [accent]b[-] / Draw area   next drag draws a rectangle even over widgets
  [accent]drag edge[-]       resize a selected widget (right, bottom, left, corners)
  [accent]double-click[-]    edit properties        [accent]Enter[-]  edit properties
  [accent]arrows[-]          move 1 cell            [accent]Shift+arrows[-] resize
  [accent]Ctrl+arrows[-]     move 5 cells           [accent]Tab[-]  next widget
  [accent]a[-] add   [accent]Del[-] remove   [accent]Ctrl+C/V/X/D[-] copy/paste/cut/duplicate
  [accent]g[-] group   [accent]G[-] ungroup   [accent]l[-] lock/unlock   [accent]m[-] move   [accent]r[-] resize
  [accent]Ctrl+Z / Ctrl+Y[-] undo / redo           [accent]Ctrl+A[-] select all
  [accent]s[-] sources & parsing   [accent]v[-] variables   [accent]t[-] dashboard & theme
  [accent]#[-] toggle grid   [accent]Ctrl+S[-] save   [accent]Ctrl+E[-] leave edit mode   [accent]q[-] quit

[title::b]TEMPLATES[-]
  {var}  {var|%.1f}  {var|>8}  {var|thr:60:90}  {var|bytes}  {var|bar:10:100}  {var|spark:20}
  [[muted]text[[-]  [[#ff8800::b]text[[-]  — colors: palette roles, #hex, 0-255, names`

func (a *App) help() {
	m := ui.NewConfirm("Tesserae — help", helpText, []string{"Close"}, nil)
	m.Width = 92
	a.Push(m)
}
