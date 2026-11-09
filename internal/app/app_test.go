package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/sernik77/tesserae/internal/config"
	"github.com/sernik77/tesserae/internal/ui"
)

type harness struct {
	t   *testing.T
	a   *App
	sim tcell.SimulationScreen
	err chan error
}

func start(t *testing.T, path string, edit bool) *harness {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	a, err := New(Options{Path: path, Edit: edit, Screen: sim})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, a: a, sim: sim, err: make(chan error, 1)}
	go func() { h.err <- a.Run() }()
	a.Sync(func() { sim.SetSize(120, 40) })
	h.sync()
	return h
}

func (h *harness) sync() {
	// Two round trips: the first lets queued input events drain, the second
	// guarantees a redraw happened after them.
	time.Sleep(20 * time.Millisecond)
	h.a.Sync(func() {})
	h.a.Sync(func() {})
}

func (h *harness) screen() string {
	// Read the simulated screen on the UI goroutine: tcell's simulation
	// buffer is not safe for concurrent access.
	var out string
	h.a.Sync(func() { out = h.contents() })
	return out
}

func (h *harness) contents() string {
	cells, w, ht := h.sim.GetContents()
	var b strings.Builder
	for y := 0; y < ht; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteString(string(c.Runes))
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (h *harness) waitFor(cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("condition not met; screen:\n%s", h.screen())
}

func (h *harness) expect(text string) {
	h.t.Helper()
	h.sync()
	h.waitFor(func() bool { return strings.Contains(h.screen(), text) })
}

func (h *harness) mouse(x, y int, b tcell.ButtonMask, mod tcell.ModMask) {
	h.sim.InjectMouse(x, y, b, mod)
	time.Sleep(5 * time.Millisecond)
}

func (h *harness) click(x, y int, b tcell.ButtonMask) {
	h.mouse(x, y, b, 0)
	h.mouse(x, y, tcell.ButtonNone, 0)
	h.sync()
}

func (h *harness) key(k tcell.Key, r rune, mod tcell.ModMask) {
	h.sim.InjectKey(k, r, mod)
	h.sync()
}

func (h *harness) cfg() *config.Dashboard {
	var c *config.Dashboard
	h.a.Sync(func() { c = h.a.cfg.Clone() })
	return c
}

func (h *harness) quit() {
	h.a.Sync(func() { h.a.quit = true })
	select {
	case err := <-h.err:
		if err != nil {
			h.t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		h.t.Fatal("app did not quit")
	}
}

// findText returns the screen position of text.
func (h *harness) findText(text string) (int, int) {
	h.t.Helper()
	for y, line := range strings.Split(h.screen(), "\n") {
		if i := strings.Index(line, text); i >= 0 {
			return len([]rune(line[:i])), y
		}
	}
	h.t.Fatalf("%q not on screen:\n%s", text, h.screen())
	return 0, 0
}

func TestEditModeEndToEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.toml")
	h := start(t, path, true)

	// 1. Select a rectangle on the empty canvas like selecting text.
	h.mouse(5, 5, tcell.Button1, 0)
	h.mouse(20, 9, tcell.Button1, 0)
	h.mouse(34, 12, tcell.Button1, 0)
	h.mouse(34, 12, tcell.ButtonNone, 0)
	h.expect("Area 30×8 at 5,5")

	// 2. Pick "Add gauge bar" from the area menu.
	x, y := h.findText("Add gauge bar")
	h.click(x, y, tcell.Button1)
	h.expect("New Gauge bar")
	// The property form is live: typing in Title updates the canvas.
	h.key(tcell.KeyTab, 0, 0) // id -> type
	h.key(tcell.KeyTab, 0, 0) // type -> title
	h.key(tcell.KeyCtrlU, 0, 0)
	for _, r := range "CPU load" {
		h.key(tcell.KeyRune, r, 0)
	}
	h.expect("─ CPU load ─")
	h.key(tcell.KeyCtrlS, 0, 0)

	c := h.cfg()
	if len(c.Elems) != 1 {
		t.Fatalf("want 1 widget, got %d", len(c.Elems))
	}
	e := c.Elems[0]
	if e.Type != "gauge" || e.Title != "CPU load" || e.X.Off != 5 || e.Y.Off != 5 || e.W.Off != 30 || e.H.Off != 8 {
		t.Fatalf("unexpected widget: %+v", e)
	}

	// 3. Drag it by its body to a new place.
	h.mouse(15, 8, tcell.Button1, 0)
	h.mouse(25, 10, tcell.Button1, 0)
	h.mouse(25, 10, tcell.ButtonNone, 0)
	h.sync()
	if e := h.cfg().Elems[0]; e.X.Off != 15 || e.Y.Off != 7 {
		t.Fatalf("move failed: x=%v y=%v", e.X, e.Y)
	}

	// 4. Resize from the bottom-right corner.
	h.mouse(44, 14, tcell.Button1, 0)
	h.mouse(49, 16, tcell.Button1, 0)
	h.mouse(49, 16, tcell.ButtonNone, 0)
	h.sync()
	if e := h.cfg().Elems[0]; e.W.Off != 35 || e.H.Off != 10 {
		t.Fatalf("resize failed: w=%v h=%v", e.W, e.H)
	}

	// 5. Right-click → context menu → Lock.
	h.click(20, 10, tcell.Button2)
	h.expect("Edit properties…")
	x, y = h.findText("Lock")
	h.click(x, y, tcell.Button1)
	if !h.cfg().Elems[0].Locked {
		t.Fatal("lock failed")
	}
	// Locked widgets do not move.
	h.mouse(20, 10, tcell.Button1, 0)
	h.mouse(30, 12, tcell.Button1, 0)
	h.mouse(30, 12, tcell.ButtonNone, 0)
	h.sync()
	if e := h.cfg().Elems[0]; e.X.Off != 15 {
		t.Fatalf("locked widget moved to %v", e.X)
	}

	// 6. Undo the lock, duplicate, group both.
	h.key(tcell.KeyCtrlZ, 'z', tcell.ModCtrl)
	if h.cfg().Elems[0].Locked {
		t.Fatal("undo did not unlock")
	}
	h.click(20, 10, tcell.Button1)
	h.key(tcell.KeyCtrlD, 'd', tcell.ModCtrl)
	h.key(tcell.KeyCtrlA, 'a', tcell.ModCtrl)
	h.key(tcell.KeyRune, 'g', 0)
	c = h.cfg()
	if len(c.Elems) != 2 || c.Elems[0].Group == "" || c.Elems[0].Group != c.Elems[1].Group {
		t.Fatalf("duplicate+group failed: %+v", c.Elems)
	}

	// 7. Save and reload from disk.
	h.key(tcell.KeyCtrlS, 's', tcell.ModCtrl)
	h.quit()
	d, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Elems) != 2 || d.Elems[0].Title != "CPU load" {
		t.Fatalf("saved config wrong: %+v", d.Elems)
	}
}

func TestViewModeRendersExamples(t *testing.T) {
	files, _ := filepath.Glob("../../configs/examples/*.toml")
	if len(files) == 0 {
		t.Skip("no examples")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			h := start(t, f, false)
			h.sync()
			if s := h.screen(); strings.TrimSpace(s) == "" {
				t.Fatal("blank screen")
			}
			h.quit()
		})
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestQuickDataSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quick.toml")
	h := start(t, path, true)
	h.mouse(2, 2, tcell.Button1, 0)
	h.mouse(30, 6, tcell.Button1, 0)
	h.mouse(30, 6, tcell.ButtonNone, 0)
	h.expect("Add text / template")
	x, y := h.findText("Add text / template")
	h.click(x, y, tcell.Button1)
	h.expect("New Text / template")
	h.a.Sync(func() {
		f := h.a.top().(*ui.Form)
		f.Set("q.cmd", "echo answer=42")
		f.Set("q.regex", `answer=(\d+)`)
	})
	h.key(tcell.KeyCtrlS, 's', tcell.ModCtrl)
	h.waitFor(func() bool { v, _ := h.a.Store().Get("text"); return v == "42" })
	h.expect("42")
	c := h.cfg()
	if s := c.Source("w.text"); s == nil || s.Rules[0].Set["text"] != "{1}" || c.Elems[0].Text != "{text}" {
		t.Fatalf("quick source not wired: %+v / %q", c.Sources, c.Elems[0].Text)
	}
	h.quit()
}

func TestSelectionMenuAndDirtyQuit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sel.toml")
	d := config.New("sel")
	d.Elems = []config.Element{
		{ID: "a", Type: "text", X: config.Cells(0), Y: config.Cells(0), W: config.Cells(20), H: config.Cells(5), Text: "A"},
		{ID: "b", Type: "text", X: config.Cells(30), Y: config.Cells(0), W: config.Cells(20), H: config.Cells(5), Text: "B"},
	}
	if err := config.Save(path, d, false); err != nil {
		t.Fatal(err)
	}
	h := start(t, path, true)

	// Right-click A → Select, right-click B → Add to selection → Group.
	h.click(5, 2, tcell.Button2)
	h.findText("Deselect") // A got selected by the right-click itself
	h.key(tcell.KeyEscape, 0, 0)
	h.click(35, 2, tcell.Button2)
	x, y := h.findText("Add to selection")
	h.click(x, y, tcell.Button1)
	h.click(35, 2, tcell.Button2)
	x, y = h.findText("Group selection")
	h.click(x, y, tcell.Button1)
	c := h.cfg()
	if c.Elems[0].Group == "" || c.Elems[0].Group != c.Elems[1].Group {
		t.Fatalf("group via context menu failed: %q %q", c.Elems[0].Group, c.Elems[1].Group)
	}

	// "Draw area…" lets a band start on top of a widget.
	h.click(70, 20, tcell.Button2)
	x, y = h.findText("Draw area…")
	h.click(x, y, tcell.Button1)
	h.mouse(0, 0, tcell.Button1, 0)
	h.mouse(60, 10, tcell.Button1, 0)
	h.mouse(60, 10, tcell.ButtonNone, 0)
	h.expect("Select widgets inside (2)")
	h.key(tcell.KeyEscape, 0, 0)

	// Leave edit mode with unsaved changes: q must ask, not quit.
	h.key(tcell.KeyCtrlE, 'e', tcell.ModCtrl)
	h.key(tcell.KeyRune, 'q', 0)
	h.expect("Unsaved changes")
	h.key(tcell.KeyRune, 's', 0) // Save
	select {
	case err := <-h.err:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("app did not quit after Save")
	}
	saved, err := config.Load(path)
	if err != nil || saved.Elems[0].Group == "" {
		t.Fatalf("group not saved: %v", err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Fatalf("file mode changed to %v", fi.Mode().Perm())
	}
}
