package app

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sernik77/tesserae/internal/config"
	"github.com/sernik77/tesserae/internal/data"
	"github.com/sernik77/tesserae/internal/render"
	"github.com/sernik77/tesserae/internal/tmpl"
	"github.com/sernik77/tesserae/internal/ui"
)

const (
	hintTemplate = "template: {var} {var|%.1f} {var|>8} {var|thr:60:90} · colors: [[ok]]…[[-]] [[#ff8800::b]]…[[-]]"
	hintDim      = "cells (12), from far edge (-3), percent (50%, 50%+2); size 0/-N stretches to the edge"
	hintColor    = "palette role (ok warn crit muted primary accent…), #rrggbb, 0-255 or a color name"
)

func opt(values ...string) []string { return values }

// ── widget properties ────────────────────────────────────────────────────────

// elementFields builds the property form of e. q is the widget's quick data
// source ("w.<id>"), or nil.
func elementFields(e *config.Element, th *render.Theme, q *config.Source) []*ui.Field {
	text := func(key, label, val, hint string) *ui.Field {
		return &ui.Field{Key: key, Label: label, Kind: ui.Text, Value: val, Hint: hint}
	}
	num := func(f float64) string {
		if f == 0 {
			return ""
		}
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	borderLabels := map[string]string{"": "(theme: " + th.Border + ")"}
	alignOpts := opt("", "left", "center", "right")
	valignOpts := opt("", "top", "middle", "bottom")
	defLabels := map[string]string{"": "(default)"}

	fs := []*ui.Field{
		{Label: "Widget", Kind: ui.Header},
		text("id", "ID", e.ID, "unique name; also used for <id>.output / <id>.status of buttons"),
		{Key: "type", Label: "Type", Kind: ui.Select, Value: e.Type, Options: config.ElementTypes, Labels: config.ElementTypeLabels},
		{Key: "title", Label: "Title", Kind: ui.Text, Value: e.Title, Hint: "shown in the top border; templates allowed", Picker: true},
		{Key: "title_align", Label: "Title align", Kind: ui.Select, Value: e.TitleAlign, Options: alignOpts, Labels: defLabels},
		{Key: "border", Label: "Border", Kind: ui.Select, Value: e.Border, Options: config.BorderStyles, Labels: borderLabels},
		{Label: "Placement", Kind: ui.Header},
		text("x", "X", e.X.String(), hintDim),
		text("y", "Y", e.Y.String(), hintDim),
		text("w", "Width", e.W.String(), hintDim),
		text("h", "Height", e.H.String(), hintDim),
		{Label: "Content", Kind: ui.Header},
	}
	varField := &ui.Field{Key: "var", Label: "Variable", Kind: ui.Text, Value: e.Var, Picker: true, Hint: "numeric variable to visualize"}
	thr := make([]string, len(e.Thresholds))
	for i, t := range e.Thresholds {
		thr[i] = strconv.FormatFloat(t, 'f', -1, 64)
	}
	thrField := text("thresholds", "Thresholds", strings.Join(thr, ", "), "warn, crit levels (e.g. 60, 90): colors ok → warn → crit")
	invert := &ui.Field{Key: "invert", Label: "Low is bad", Kind: ui.Check, Checked: e.Invert, Hint: "invert thresholds: values below them are warnings"}
	align := &ui.Field{Key: "align", Label: "Align", Kind: ui.Select, Value: e.Align, Options: alignOpts, Labels: defLabels}
	valign := &ui.Field{Key: "valign", Label: "Vertical", Kind: ui.Select, Value: e.VAlign, Options: valignOpts, Labels: defLabels}
	now := &ui.Field{Key: "now", Label: "Live value", Kind: ui.Info, Lines: 1}

	switch e.Type {
	case "gauge", "spark":
		label := "Label"
		hint := "template; default {var|%.1f}"
		if e.Type == "spark" {
			hint = "template shown above the chart (optional)"
		}
		fs = append(fs, varField, now,
			text("min", "Min", num(e.Min), "scale minimum"),
			text("max", "Max", num(e.Max), "scale maximum (gauge default 100, spark: auto)"),
			thrField, invert,
			&ui.Field{Key: "text", Label: label, Kind: ui.Text, Value: e.Text, Hint: hint, Picker: true},
			align)
		if e.Type == "gauge" {
			fs = append(fs, valign)
		}
	case "bignum":
		fs = append(fs,
			&ui.Field{Key: "text", Label: "Text", Kind: ui.Text, Value: e.Text, Picker: true, Hint: "template rendered in big digits, e.g. {tg|%.1f}"},
			varField, now, thrField, invert, align, valign)
	case "log":
		fs = append(fs,
			&ui.Field{Key: "stream", Label: "Stream", Kind: ui.Text, Value: e.Stream, Hint: "event stream name (events, actions) or source:<name> for raw lines"},
			&ui.Field{Key: "streams", Label: "Known", Kind: ui.Info, Lines: 1},
			text("filter", "Filter", e.Filter, "only show lines matching this regex; !regex hides matching lines"),
			&ui.Field{Key: "text", Label: "Line format", Kind: ui.Text, Value: e.Text, Picker: true, Hint: "default: " + tmpl.Escape(elemDefaultLog) + " — {line} is the entry, {time} its timestamp"},
			text("max_lines", "Max lines", strconv.Itoa(e.MaxLines), "0 = as many as fit"),
			&ui.Field{Key: "wrap", Label: "Wrap", Kind: ui.Check, Checked: e.Wrap},
			valign)
	case "button":
		fs = append(fs,
			&ui.Field{Key: "text", Label: "Label", Kind: ui.Text, Value: e.Text, Picker: true, Hint: hintTemplate},
			&ui.Field{Key: "action", Label: "Command", Kind: ui.Area, Lines: 3, Value: e.Action, Picker: true, Hint: "shell command run on click; templates expand first; output → {" + e.ID + ".output}"},
			text("key", "Hotkey", e.Key, "single key that triggers the button in view mode"),
			&ui.Field{Key: "confirm", Label: "Confirm", Kind: ui.Check, Checked: e.Confirm, Hint: "ask before running"},
			align, valign)
	default: // text, box
		fs = append(fs,
			&ui.Field{Key: "text", Label: "Text", Kind: ui.Area, Lines: 6, Value: e.Text, Picker: true, Hint: hintTemplate},
			align, valign,
			&ui.Field{Key: "wrap", Label: "Wrap", Kind: ui.Check, Checked: e.Wrap})
	}
	if e.Type != "box" && e.Type != "button" {
		qcmd, qevery, qregex := "", "1s", ""
		if q != nil {
			qcmd, qevery = q.Command, config.FormatDuration(q.Interval.Duration)
			if len(q.Rules) > 0 {
				qregex = q.Rules[0].Match
			}
		}
		fs = append(fs,
			&ui.Field{Label: "Quick data source", Kind: ui.Header},
			&ui.Field{Key: "q.cmd", Label: "Command", Kind: ui.Area, Lines: 2, Value: qcmd,
				Hint: "shell command run every interval; its output becomes {" + e.ID + "} (creates source w." + e.ID + ")"},
			text("q.every", "Every", qevery, "interval, e.g. 1s, 500ms, 5s"),
			text("q.regex", "Extract", qregex, "optional regex: group 1 (or named groups) instead of the whole output"))
	}
	fs = append(fs,
		&ui.Field{Label: "Style", Kind: ui.Header},
		text("fg", "Text color", e.Fg, hintColor),
		text("bg", "Background", e.Bg, hintColor),
		text("border_fg", "Border color", e.BorderFg, hintColor),
		text("title_fg", "Title color", e.TitleFg, hintColor),
		&ui.Field{Key: "bold", Label: "Bold", Kind: ui.Check, Checked: e.Bold},
		&ui.Field{Label: "Edit", Kind: ui.Header},
		text("group", "Group", e.Group, "widgets in one group are selected and moved together"),
		&ui.Field{Key: "locked", Label: "Locked", Kind: ui.Check, Checked: e.Locked, Hint: "locked widgets cannot be moved, resized or removed"},
	)
	return fs
}

const elemDefaultLog = "[muted]{time}[-] {line}"

// formElement builds an element from form values on top of base.
func formElement(f *ui.Form, base config.Element) (config.Element, error) {
	e := base.Clone()
	v := f.Values()
	get := func(k string, dst *string) {
		if val, ok := v[k]; ok {
			*dst = val
		}
	}
	get("id", &e.ID)
	get("type", &e.Type)
	get("title", &e.Title)
	get("title_align", &e.TitleAlign)
	get("border", &e.Border)
	get("text", &e.Text)
	get("var", &e.Var)
	get("align", &e.Align)
	get("valign", &e.VAlign)
	get("stream", &e.Stream)
	get("filter", &e.Filter)
	get("fg", &e.Fg)
	get("bg", &e.Bg)
	get("border_fg", &e.BorderFg)
	get("title_fg", &e.TitleFg)
	get("action", &e.Action)
	get("key", &e.Key)
	get("group", &e.Group)
	e.ID = strings.TrimSpace(e.ID)
	e.Var = strings.Trim(strings.TrimSpace(e.Var), "{}")
	e.Group = strings.TrimSpace(e.Group)
	for _, b := range []struct {
		k   string
		dst *bool
	}{{"invert", &e.Invert}, {"wrap", &e.Wrap}, {"bold", &e.Bold}, {"confirm", &e.Confirm}, {"locked", &e.Locked}} {
		if val, ok := v[b.k]; ok {
			*b.dst = val == "true"
		}
	}
	var errs []error
	dim := func(k string, dst *config.Dim) {
		if val, ok := v[k]; ok {
			d, err := config.ParseDim(val)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %v", k, err))
				return
			}
			*dst = d
		}
	}
	dim("x", &e.X)
	dim("y", &e.Y)
	dim("w", &e.W)
	dim("h", &e.H)
	float := func(k string, dst *float64) {
		if val, ok := v[k]; ok {
			if strings.TrimSpace(val) == "" {
				*dst = 0
				return
			}
			x, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: not a number", k))
				return
			}
			*dst = x
		}
	}
	float("min", &e.Min)
	float("max", &e.Max)
	if val, ok := v["thresholds"]; ok {
		e.Thresholds = nil
		for _, p := range strings.FieldsFunc(val, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
			x, err := strconv.ParseFloat(p, 64)
			if err != nil {
				errs = append(errs, fmt.Errorf("thresholds: %q is not a number", p))
				continue
			}
			e.Thresholds = append(e.Thresholds, x)
		}
	}
	if val, ok := v["max_lines"]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(orStr(val, "0")))
		if err != nil {
			errs = append(errs, errors.New("max lines: not a number"))
		}
		e.MaxLines = n
	}
	if e.Filter != "" {
		if _, err := regexp.Compile(strings.TrimPrefix(e.Filter, "!")); err != nil {
			errs = append(errs, fmt.Errorf("filter: %v", err))
		}
	}
	if len([]rune(e.Key)) > 1 {
		errs = append(errs, errors.New("hotkey must be a single key"))
	}
	return e, errors.Join(errs...)
}

func (a *App) editElement(i int, isNew bool) {
	a.editElementFrom(i, isNew, a.cfg.Clone())
}

// editElementFrom opens the property form. Changes apply live; Cancel restores
// before (which, for new widgets, does not contain the widget at all).
func (a *App) editElementFrom(i int, isNew bool, before *config.Dashboard) {
	id := a.cfg.Elems[i].ID
	cur := func() int { return a.cfg.Index(id) }
	title := "Widget · " + id
	if isNew {
		title = "New " + config.ElementTypeLabels[a.cfg.Elems[i].Type]
	}
	origQuick := ""
	var quick *config.Source
	if q := a.cfg.Source("w." + id); q != nil {
		origQuick = q.Name
		c := q.Clone()
		quick = &c
	}
	f := ui.NewForm(title, elementFields(&a.cfg.Elems[i], a.th, quick))
	f.Buttons = []string{"OK", "Cancel"}
	a.layout()
	if r := a.rects[i]; r.X+r.W/2 > a.width()/2 {
		f.Side = "left"
	} else {
		f.Side = "right"
	}
	f.Width = 78
	lastType := a.cfg.Elems[i].Type
	preview := func(f *ui.Form) {
		if fl := f.Field("now"); fl != nil {
			fl.Preview = func(f *ui.Form) []string {
				name := strings.Trim(f.Get("var"), "{} ")
				if name == "" {
					return []string{"[muted](no variable)[-]"}
				}
				v, ok := a.store.Get(name)
				if !ok {
					return []string{"[warn]" + tmpl.Escape(name) + " is not set (yet)[-] — see Variables… (v)"}
				}
				return []string{tmpl.Escape(tmpl.Truncate(v, 40)) + "  [muted]" + tmpl.InlineSpark(a.store.History(name), 20) + "[-]"}
			}
		}
		if fl := f.Field("streams"); fl != nil {
			fl.Preview = func(*ui.Form) []string {
				return []string{"[muted]" + tmpl.Escape(strings.Join(a.store.StreamNames(), "  ")) + "[-]"}
			}
		}
	}
	preview(f)
	f.OnChange = func(f *ui.Form) {
		k := cur()
		if k < 0 {
			return
		}
		e, err := formElement(f, a.cfg.Elems[k])
		if err != nil {
			f.Err = firstLine(err.Error())
			return
		}
		if e.ID != id && (e.ID == "" || a.cfg.Index(e.ID) >= 0) {
			f.Err = "id is empty or already used"
			return
		}
		if e.ID != id {
			delete(a.ed.sel, id)
			a.ed.sel[e.ID] = true
			if a.ed.primary == id {
				a.ed.primary = e.ID
			}
			id = e.ID
		}
		a.cfg.Elems[k] = e
		if e.Type != lastType {
			lastType = e.Type
			f.SetFields(elementFields(&a.cfg.Elems[k], a.th, quickFrom(f)))
			preview(f)
		}
	}
	f.OnSubmit = func(f *ui.Form) error {
		f.OnChange(f)
		if f.Err != "" {
			return errors.New(f.Err)
		}
		if err := a.applyQuick(f, origQuick, id); err != nil {
			return err
		}
		if err := config.Validate(a.cfg); err != nil {
			return errors.New(firstLine(err.Error()))
		}
		a.commit(before)
		return nil
	}
	f.OnCancel = func(*ui.Form) {
		a.cfg = before
		if isNew {
			a.ed.clear()
		}
	}
	f.OnPick = a.picker
	a.Push(f)
}

// quickFrom reads the quick data source fields of a widget form.
func quickFrom(f *ui.Form) *config.Source {
	if f.Field("q.cmd") == nil {
		return nil
	}
	d, _ := parseDur(f.Get("q.every"))
	q := &config.Source{Command: f.Get("q.cmd"), Interval: d}
	if rx := f.Get("q.regex"); rx != "" {
		q.Rules = []config.Rule{{Match: rx}}
	}
	return q
}

// applyQuick creates, updates or removes the widget's own exec source
// "w.<id>" from the Quick data source fields and wires its variable in.
func (a *App) applyQuick(f *ui.Form, origName, id string) error {
	q := quickFrom(f)
	k := a.cfg.Index(id)
	if q == nil || k < 0 {
		return nil
	}
	name := "w." + id
	cmd := strings.TrimSpace(q.Command)
	changed := false
	if origName != "" && (cmd == "" || origName != name) {
		for i, s := range a.cfg.Sources {
			if s.Name == origName {
				a.cfg.Sources = slices.Delete(a.cfg.Sources, i, i+1)
				changed = true
				break
			}
		}
	}
	if cmd != "" {
		src := config.Source{Name: name, Type: "exec", Command: cmd, Interval: q.Interval}
		if len(q.Rules) > 0 {
			re, err := regexp.Compile(q.Rules[0].Match)
			if err != nil {
				return fmt.Errorf("extract: %v", err)
			}
			r := config.Rule{Match: q.Rules[0].Match}
			named := slices.ContainsFunc(re.SubexpNames(), func(n string) bool { return n != "" })
			switch {
			case named:
			case re.NumSubexp() > 0:
				r.Set = map[string]string{id: "{1}"}
			default:
				r.Set = map[string]string{id: "{0}"}
			}
			src.Rules = []config.Rule{r}
		} else {
			src.Var = id
		}
		d := &config.Dashboard{Sources: []config.Source{src}}
		config.ApplyDefaults(d)
		if old := a.cfg.Source(name); old != nil {
			*old = d.Sources[0]
		} else {
			a.cfg.Sources = append(a.cfg.Sources, d.Sources[0])
		}
		changed = true
		e := &a.cfg.Elems[k]
		ref := "{" + id + "}"
		switch e.Type {
		case "gauge", "spark":
			if e.Var == "" {
				e.Var = id
			}
		case "bignum":
			if e.Var == "" {
				e.Var = id
			}
			if e.Text == "" || e.Text == newElement("bignum").Text {
				e.Text = ref
			}
		case "text":
			if strings.TrimSpace(e.Text) == "" || e.Text == newElement("text").Text {
				e.Text = ref
			}
		case "log":
			e.Stream = "source:" + name
		}
	}
	if changed {
		a.startData()
	}
	return nil
}

func (a *App) width() int { w, _ := a.size(); return w }

// picker opens the variable inspector to insert a variable into a field.
func (a *App) picker(f *ui.Form, field *ui.Field, insert func(string)) {
	bare := field.Key == "var"
	a.inspector(func(name string) {
		if bare {
			f.Set(field.Key, name)
			if f.OnChange != nil {
				f.OnChange(f)
			}
			return
		}
		insert("{" + name + "}")
	})
}

// ── variables inspector ──────────────────────────────────────────────────────

func (a *App) inspector(pick func(string)) {
	rows := func(q string) []ui.Row {
		q = strings.ToLower(q)
		var out []ui.Row
		for _, n := range a.store.Names() {
			if q != "" && !strings.Contains(strings.ToLower(n), q) {
				continue
			}
			v, _ := a.store.Get(n)
			name := tmpl.Pad(n, 28, '<')
			col := "fg"
			if strings.HasPrefix(n, "sys.") {
				col = "muted"
			}
			out = append(out, ui.Row{Key: n, Text: "[accent]" + tmpl.Escape(name) + "[-] [" + col + "]" + tmpl.Escape(tmpl.Truncate(strings.ReplaceAll(v, "\n", "⏎"), 60)) + "[-]"})
		}
		return out
	}
	var l *ui.List
	if pick != nil {
		l = ui.NewList("Pick a variable", rows, []string{"Insert", "Cancel"}, func(b string, r *ui.Row) bool {
			if b == "Insert" && r != nil {
				pick(r.Key)
				return true
			}
			return b == "Cancel"
		})
		l.Default = "Insert"
	} else {
		l = ui.NewList("Variables (live)", rows, []string{"Text", "Gauge", "Spark", "Big", "Close"}, func(b string, r *ui.Row) bool {
			if r == nil {
				return b == "Close"
			}
			typ := map[string]string{"Text": "text", "Gauge": "gauge", "Spark": "spark", "Big": "bignum"}[b]
			if typ == "" {
				return true
			}
			a.addElement(typ, render.Rect{X: a.mx, Y: a.my, W: 1, H: 1}, r.Key)
			return true
		})
		l.Default = "Text"
		l.Footer = "type to filter · Enter / Text, Gauge, Spark, Big: add a widget showing the variable"
	}
	l.Filter = true
	l.Width = 100
	l.Height = 30
	a.Push(l)
}

// ── dashboard settings ───────────────────────────────────────────────────────

func (a *App) settingsForm() {
	before := a.cfg.Clone()
	d := a.cfg
	fs := []*ui.Field{
		{Label: "Dashboard", Kind: ui.Header},
		{Key: "name", Label: "Name", Kind: ui.Text, Value: d.Name},
		{Key: "refresh", Label: "Refresh", Kind: ui.Text, Value: config.FormatDuration(d.Refresh.Duration), Hint: "redraw interval, e.g. 1s, 500ms"},
		{Key: "shell", Label: "Shell", Kind: ui.Text, Value: d.Shell, Hint: "shell for commands; empty = bash (or sh)"},
		{Key: "units", Label: "New widgets", Kind: ui.Select, Value: d.Units, Options: opt("cells", "percent"), Labels: map[string]string{"cells": "fixed cells", "percent": "percent (responsive)"}},
		{Label: "Theme", Kind: ui.Header},
		{Key: "preset", Label: "Preset", Kind: ui.Select, Value: d.Theme.Preset, Options: render.PresetNames(), Hint: "←/→ to preview themes live"},
		{Key: "tborder", Label: "Border", Kind: ui.Select, Value: d.Theme.Border, Options: config.BorderStyles[1:]},
	}
	for _, role := range render.ColorRoles {
		fs = append(fs, &ui.Field{Key: "c." + role, Label: role, Kind: ui.Text, Value: d.Theme.Colors[role], Hint: "override; empty = preset · " + hintColor})
	}
	fs = append(fs,
		&ui.Field{Label: "Initial variables", Kind: ui.Header},
		&ui.Field{Key: "vars", Label: "Vars", Kind: ui.Area, Lines: 4, Value: kvLines(d.Vars, " = "), Hint: "one per line: name = value (shown until a source sets them)"},
	)
	f := ui.NewForm("Dashboard & theme", fs)
	apply := func(f *ui.Form) error {
		c := a.cfg
		c.Name = f.Get("name")
		if r, err := time.ParseDuration(strings.TrimSpace(f.Get("refresh"))); err != nil || r < 50*time.Millisecond {
			return errors.New("refresh: use a duration ≥ 50ms like 1s or 500ms")
		} else {
			c.Refresh.Duration = r
		}
		c.Shell = strings.TrimSpace(f.Get("shell"))
		c.Units = f.Get("units")
		c.Theme.Preset = f.Get("preset")
		c.Theme.Border = f.Get("tborder")
		c.Theme.Colors = map[string]string{}
		for _, role := range render.ColorRoles {
			if v := strings.TrimSpace(f.Get("c." + role)); v != "" {
				if _, ok := a.th.Color(v); !ok && !strings.HasPrefix(v, "#") {
					return fmt.Errorf("%s: unknown color %q", role, v)
				}
				c.Theme.Colors[role] = v
			}
		}
		if len(c.Theme.Colors) == 0 {
			c.Theme.Colors = nil
		}
		vars, err := parseKV(f.Get("vars"), "=")
		if err != nil {
			return err
		}
		c.Vars = vars
		a.th = render.NewTheme(c.Theme)
		return nil
	}
	f.OnChange = func(f *ui.Form) {
		if err := apply(f); err != nil {
			f.Err = err.Error()
		}
	}
	f.OnSubmit = func(f *ui.Form) error {
		if err := apply(f); err != nil {
			return err
		}
		for k, v := range a.cfg.Vars {
			a.store.SetDefault(k, v)
		}
		a.eng.Shell = a.cfg.Shell
		a.commit(before)
		return nil
	}
	f.OnCancel = func(*ui.Form) {
		a.cfg = before
		a.th = render.NewTheme(before.Theme)
	}
	f.Side = "right"
	f.Width = 72
	a.Push(f)
}

func kvLines(m map[string]string, sep string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + sep + m[k] + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// parseKV parses "name <sep> value" lines.
func parseKV(s, sep string) (map[string]string, error) {
	var m map[string]string
	for n, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, ok := strings.Cut(line, sep)
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("line %d: expected name %s value", n+1, strings.TrimSpace(sep))
		}
		if m == nil {
			m = map[string]string{}
		}
		m[k] = strings.TrimSpace(v)
	}
	return m, nil
}

// ── sources ──────────────────────────────────────────────────────────────────

func (a *App) sourceStatus(name string) string {
	st, _ := a.store.Get(name + ".status")
	switch st {
	case "ok":
		return "[ok]●[-]"
	case "error":
		return "[crit]●[-]"
	}
	return "[warn]○[-]"
}

func sourceTarget(s config.Source) string {
	switch s.Type {
	case "tail":
		return s.Path
	case "http":
		return s.URL
	}
	return strings.ReplaceAll(s.Command, "\n", " ")
}

func (a *App) sourcesDialog() {
	var l *ui.List
	rows := func(string) []ui.Row {
		var out []ui.Row
		for _, s := range a.cfg.Sources {
			lines, _ := a.store.Get(s.Name + ".lines")
			errText, _ := a.store.Get(s.Name + ".error")
			extra := fmt.Sprintf("[muted]%d rules · %s lines[-]", len(s.Rules), orStr(lines, "0"))
			if errText != "" {
				extra = "[crit]" + tmpl.Escape(tmpl.Truncate(errText, 30)) + "[-]"
			}
			out = append(out, ui.Row{Key: s.Name, Text: fmt.Sprintf("%s [accent]%s[-] [secondary]%-6s[-] %s  %s",
				a.sourceStatus(s.Name), tmpl.Escape(tmpl.Pad(s.Name, 12, '<')), s.Type, tmpl.Escape(tmpl.Truncate(sourceTarget(s), 38)), extra)})
		}
		return out
	}
	l = ui.NewList("Sources & parsing", rows, []string{"Add", "Edit", "Rules", "Remove", "Up", "Down", "Close"}, func(b string, r *ui.Row) bool {
		idx := -1
		if r != nil {
			for i, s := range a.cfg.Sources {
				if s.Name == r.Key {
					idx = i
				}
			}
		}
		switch b {
		case "Add":
			a.sourceForm(-1)
		case "Edit":
			if idx >= 0 {
				a.sourceForm(idx)
			}
		case "Rules":
			if idx >= 0 {
				a.rulesDialog(a.cfg.Sources[idx].Name)
			}
		case "Remove":
			if idx >= 0 {
				a.checkpoint()
				a.cfg.Sources = slices.Delete(a.cfg.Sources, idx, idx+1)
				a.startData()
				a.toast("source removed — Ctrl+Z to undo", false)
			}
		case "Up", "Down":
			j := idx - 1
			if b == "Down" {
				j = idx + 1
			}
			if idx >= 0 && j >= 0 && j < len(a.cfg.Sources) {
				a.checkpoint()
				a.cfg.Sources[idx], a.cfg.Sources[j] = a.cfg.Sources[j], a.cfg.Sources[idx]
				l.Select(a.cfg.Sources[j].Name)
			}
		case "Close":
			return true
		}
		return false
	})
	l.Default = "Edit"
	l.Width = 104
	l.Height = 18
	l.Footer = "a source produces text → its rules turn it into variables/events → widgets show them\nEnter: edit · R: rules · Del: remove"
	a.Push(l)
}

func sourceFields(s *config.Source) []*ui.Field {
	text := func(key, label, val, hint string) *ui.Field {
		return &ui.Field{Key: key, Label: label, Kind: ui.Text, Value: val, Hint: hint}
	}
	dur := func(d config.Duration) string { return config.FormatDuration(d.Duration) }
	fs := []*ui.Field{
		{Label: "Source", Kind: ui.Header},
		text("name", "Name", s.Name, "unique; status vars: <name>.status, <name>.error, <name>.lines"),
		{Key: "type", Label: "Type", Kind: ui.Select, Value: s.Type, Options: config.SourceTypes, Labels: map[string]string{
			"tail": "tail — follow a log file", "exec": "exec — run a command periodically",
			"stream": "stream — read a long-running command", "http": "http — poll a URL",
		}},
	}
	switch s.Type {
	case "tail":
		fs = append(fs,
			text("path", "File", s.Path, "log file to follow (rotation-safe); ~ and $VARS expand"),
			text("backfill", "Backfill", strconv.Itoa(s.Backfill), "existing lines replayed at start (0 = 300, -1 = none)"))
	case "exec", "stream":
		hint := "shell command; its output is parsed every interval"
		if s.Type == "stream" {
			hint = "long-running command; every stdout line is parsed (e.g. journalctl -f)"
		}
		fs = append(fs, &ui.Field{Key: "command", Label: "Command", Kind: ui.Area, Lines: 3, Value: s.Command, Hint: hint})
		if s.Type == "exec" {
			fs = append(fs,
				text("interval", "Interval", dur(s.Interval), "e.g. 1s, 500ms, 1m"),
				text("timeout", "Timeout", dur(s.Timeout), "kill slow runs (default max(10s, 3×interval))"),
				&ui.Field{Key: "split", Label: "Split", Kind: ui.Select, Value: s.Split, Options: opt("whole", "lines"), Labels: map[string]string{"whole": "whole output at once", "lines": "line by line"}})
		}
	case "http":
		fs = append(fs,
			text("url", "URL", s.URL, "e.g. http://127.0.0.1:11434/api/ps"),
			text("method", "Method", s.Method, "GET (default) / POST / …"),
			&ui.Field{Key: "body", Label: "Body", Kind: ui.Area, Lines: 2, Value: s.Body},
			&ui.Field{Key: "headers", Label: "Headers", Kind: ui.Area, Lines: 2, Value: kvLines(s.Headers, ": "), Hint: "one per line: Name: value"},
			text("interval", "Interval", dur(s.Interval), "e.g. 1s, 5s"),
			text("timeout", "Timeout", dur(s.Timeout), "default 5s"))
	}
	fs = append(fs,
		&ui.Field{Label: "Parsing", Kind: ui.Header},
		&ui.Field{Key: "format", Label: "Format", Kind: ui.Select, Value: s.Format, Options: opt("text", "json", "kv"), Labels: map[string]string{
			"text": "text — rules only", "json": "json — flatten into <prefix>.a.b.0.c", "kv": "kv — key=value / key: value lines",
		}},
		text("prefix", "Prefix", s.Prefix, "variable prefix for json/kv; empty = source name, - = none"),
		&ui.Field{Key: "var", Label: "Whole → var", Kind: ui.Text, Value: s.Var, Hint: "store the full trimmed output (or the last line) in this variable"},
		&ui.Field{Key: "rules", Label: "Rules", Kind: ui.Info, Lines: 1},
		&ui.Field{Label: "Live", Kind: ui.Header},
		&ui.Field{Key: "live", Label: "", Kind: ui.Info, Lines: 7},
	)
	return fs
}

func parseDur(s string) (config.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return config.Duration{}, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return config.Duration{}, fmt.Errorf("invalid duration %q", s)
	}
	return config.Duration{Duration: d}, nil
}

func formSource(f *ui.Form, base config.Source) (config.Source, error) {
	s := base.Clone()
	v := f.Values()
	for k, dst := range map[string]*string{"name": &s.Name, "type": &s.Type, "path": &s.Path, "command": &s.Command,
		"url": &s.URL, "method": &s.Method, "body": &s.Body, "split": &s.Split, "format": &s.Format, "prefix": &s.Prefix, "var": &s.Var} {
		if val, ok := v[k]; ok {
			*dst = strings.TrimSpace(val)
		}
	}
	var errs []error
	if val, ok := v["backfill"]; ok {
		n, err := strconv.Atoi(orStr(strings.TrimSpace(val), "0"))
		if err != nil {
			errs = append(errs, errors.New("backfill: not a number"))
		}
		s.Backfill = n
	}
	for k, dst := range map[string]*config.Duration{"interval": &s.Interval, "timeout": &s.Timeout} {
		if val, ok := v[k]; ok {
			d, err := parseDur(val)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %v", k, err))
			}
			*dst = d
		}
	}
	if val, ok := v["headers"]; ok {
		h, err := parseKV(val, ":")
		if err != nil {
			errs = append(errs, fmt.Errorf("headers: %v", err))
		}
		s.Headers = h
	}
	if s.Name == "" {
		errs = append(errs, errors.New("name is required"))
	}
	return s, errors.Join(errs...)
}

func (a *App) sourceForm(idx int) {
	isNew := idx < 0
	src := config.Source{Type: "exec", Interval: config.Duration{Duration: time.Second}}
	if isNew {
		n := 1
		for a.cfg.Source(fmt.Sprintf("src%d", n)) != nil {
			n++
		}
		src.Name = fmt.Sprintf("src%d", n)
		src.Command = "date +%s"
		config.ApplyDefaults(&config.Dashboard{Sources: []config.Source{src}})
	} else {
		src = a.cfg.Sources[idx].Clone()
	}
	work := src
	f := ui.NewForm("Source · "+src.Name, sourceFields(&work))
	if isNew {
		f.Title = "New source"
	}
	f.Width = 92
	setPreview := func(f *ui.Form) {
		if fl := f.Field("rules"); fl != nil {
			fl.Preview = func(*ui.Form) []string {
				return []string{fmt.Sprintf("[accent]%d[-] rule(s) — edit them with [accent]Rules…[-]", len(work.Rules))}
			}
		}
		if fl := f.Field("live"); fl != nil {
			fl.Preview = func(*ui.Form) []string {
				name := work.Name
				out := []string{}
				st, ok := a.store.Get(name + ".status")
				if !ok {
					out = append(out, "[muted]not running yet — press OK to start it[-]")
				} else {
					lines, _ := a.store.Get(name + ".lines")
					errText, _ := a.store.Get(name + ".error")
					out = append(out, fmt.Sprintf("%s %s · %s lines %s", a.sourceStatus(name), st, orStr(lines, "0"), "[crit]"+tmpl.Escape(errText)+"[-]"))
				}
				for _, e := range a.store.Stream("source:"+name, 6) {
					out = append(out, "[muted]›[-] "+e.Text)
				}
				return out
			}
		}
	}
	setPreview(f)
	f.Buttons = []string{"OK", "Rules…", "Cancel"}
	f.OnChange = func(f *ui.Form) {
		s, err := formSource(f, work)
		if err != nil {
			f.Err = firstLine(err.Error())
			return
		}
		typeChanged := s.Type != work.Type
		if typeChanged {
			s.Split = ""
			d := &config.Dashboard{Sources: []config.Source{s}}
			config.ApplyDefaults(d)
			s = d.Sources[0]
		}
		work = s
		if typeChanged {
			f.SetFields(sourceFields(&work))
			setPreview(f)
		}
	}
	commit := func(f *ui.Form) error {
		s, err := formSource(f, work)
		if err != nil {
			return errors.New(firstLine(err.Error()))
		}
		for i, o := range a.cfg.Sources {
			if o.Name == s.Name && i != idx {
				return fmt.Errorf("a source named %q already exists", s.Name)
			}
		}
		d := &config.Dashboard{Sources: []config.Source{s}}
		config.ApplyDefaults(d)
		if err := config.Validate(d); err != nil {
			return errors.New(firstLine(err.Error()))
		}
		a.checkpoint()
		if isNew {
			a.cfg.Sources = append(a.cfg.Sources, d.Sources[0])
			idx = len(a.cfg.Sources) - 1
			isNew = false
		} else {
			a.cfg.Sources[idx] = d.Sources[0]
		}
		work = d.Sources[0]
		a.startData()
		return nil
	}
	f.OnSubmit = commit
	f.OnButton = func(f *ui.Form, name string) {
		if name == "Rules…" {
			if err := commit(f); err != nil {
				f.Err = err.Error()
				return
			}
			f.Close()
			a.rulesDialog(work.Name)
		}
	}
	a.Push(f)
}

// ── rules ────────────────────────────────────────────────────────────────────

func ruleSummary(r config.Rule) string {
	var parts []string
	if re, err := regexp.Compile(r.Match); err == nil {
		for _, n := range re.SubexpNames() {
			if n != "" {
				parts = append(parts, n)
			}
		}
	}
	for k := range r.Set {
		parts = append(parts, k)
	}
	s := ""
	if len(parts) > 0 {
		s = "[ok]→ " + tmpl.Escape(strings.Join(parts, " ")) + "[-]"
	}
	if r.Event != "" {
		s += " [accent]⚑ event[-]"
	}
	if r.Count != "" {
		s += " [secondary]+1 " + tmpl.Escape(r.Count) + "[-]"
	}
	if r.Stop {
		s += " [muted]stop[-]"
	}
	return s
}

func (a *App) rulesDialog(source string) {
	var l *ui.List
	src := func() *config.Source { return a.cfg.Source(source) }
	rows := func(string) []ui.Row {
		s := src()
		if s == nil {
			return nil
		}
		var out []ui.Row
		for i, r := range s.Rules {
			out = append(out, ui.Row{Key: strconv.Itoa(i), Text: fmt.Sprintf("[muted]%2d[-] %s  %s", i+1, tmpl.Escape(tmpl.Truncate(r.Match, 52)), ruleSummary(r))})
		}
		return out
	}
	l = ui.NewList("Rules · "+source, rows, []string{"Add", "Edit", "Remove", "Up", "Down", "Close"}, func(b string, row *ui.Row) bool {
		s := src()
		if s == nil {
			return true
		}
		idx := -1
		if row != nil {
			idx, _ = strconv.Atoi(row.Key)
		}
		switch b {
		case "Add":
			a.ruleForm(source, -1)
		case "Edit":
			if idx >= 0 {
				a.ruleForm(source, idx)
			}
		case "Remove":
			if idx >= 0 {
				a.checkpoint()
				s = src()
				s.Rules = slices.Delete(s.Rules, idx, idx+1)
				a.startData()
			}
		case "Up", "Down":
			j := idx - 1
			if b == "Down" {
				j = idx + 1
			}
			if idx >= 0 && j >= 0 && j < len(s.Rules) {
				a.checkpoint()
				s = src()
				s.Rules[idx], s.Rules[j] = s.Rules[j], s.Rules[idx]
				a.startData()
				l.Select(strconv.Itoa(j))
			}
		case "Close":
			return true
		}
		return false
	})
	l.Default = "Edit"
	l.Width = 104
	l.Height = 20
	l.Footer = "rules run top to bottom on every line (or whole output); a rule with [accent]stop[-] ends the chain\nnamed groups (?P<name>…) become variables"
	a.Push(l)
}

func (a *App) ruleForm(source string, idx int) {
	s := a.cfg.Source(source)
	if s == nil {
		return
	}
	isNew := idx < 0
	r := config.Rule{Match: `value = (?P<value>[0-9.]+)`}
	if !isNew {
		r = s.Rules[idx]
		r.Set = maps.Clone(r.Set)
	}
	whole := s.Split == "whole"
	fs := []*ui.Field{
		{Label: "Match", Kind: ui.Header},
		{Key: "match", Label: "Regex", Kind: ui.Text, Value: r.Match, Hint: "Go regexp; (?P<name>…) named groups become variables; (?i) ignore case"},
		{Key: "test", Label: "Test", Kind: ui.Info, Lines: 9},
		{Label: "Outputs", Kind: ui.Header},
		{Key: "set", Label: "Set vars", Kind: ui.Area, Lines: 3, Value: kvLines(r.Set, " = "), Picker: true, Hint: "name = template, one per line; {1} {name} are captures, other {vars} work too"},
		{Key: "event", Label: "Event", Kind: ui.Text, Value: r.Event, Picker: true, Hint: "push a line to an event stream, e.g. [[ok]]Model loaded[[-]] {model}"},
		{Key: "stream", Label: "Stream", Kind: ui.Text, Value: r.Stream, Hint: "event stream name (default: events)"},
		{Key: "count", Label: "Counter", Kind: ui.Text, Value: r.Count, Hint: "variable incremented on every match"},
		{Key: "stop", Label: "Stop", Kind: ui.Check, Checked: r.Stop, Hint: "do not evaluate later rules when this one matches"},
	}
	f := ui.NewForm("Rule · "+source, fs)
	f.Width = 100
	var (
		lastExpr string
		lastRe   *regexp.Regexp
		lastErr  error
	)
	f.Field("test").Preview = func(f *ui.Form) []string {
		expr := f.Get("match")
		if expr != lastExpr {
			lastExpr = expr
			lastRe, lastErr = regexp.Compile(expr)
		}
		if lastErr != nil {
			return []string{"[crit]" + tmpl.Escape(lastErr.Error()) + "[-]"}
		}
		entries := a.store.Stream("source:"+source, 0)
		if len(entries) == 0 {
			return []string{"[muted]no input from " + tmpl.Escape(source) + " yet — the test uses its latest lines[-]"}
		}
		var texts []string
		for _, e := range entries {
			texts = append(texts, strings.ReplaceAll(e.Text, "[[", "["))
		}
		if whole {
			texts = []string{strings.Join(texts, "\n")}
		}
		var hits []string
		n := 0
		for i := len(texts) - 1; i >= 0; i-- {
			m := lastRe.FindStringSubmatchIndex(texts[i])
			if m == nil {
				continue
			}
			n++
			if len(hits) < 8 {
				caps := data.Captures(lastRe, texts[i], m)
				var cs []string
				for j, name := range lastRe.SubexpNames() {
					if j > 0 && m[2*j] >= 0 {
						if name == "" {
							name = strconv.Itoa(j)
						}
						cs = append(cs, "[ok]"+tmpl.Escape(name)+"[-]="+tmpl.Escape(tmpl.Truncate(caps[name], 24)))
					}
				}
				line := strings.ReplaceAll(texts[i][m[0]:m[1]], "\n", "⏎")
				hits = append(hits, "[muted]"+tmpl.Escape(tmpl.Truncate(line, 46))+"[-]  "+strings.Join(cs, " "))
			}
		}
		head := fmt.Sprintf("[accent]%d[-] of %d recent lines match", n, len(texts))
		if whole {
			head = "latest output: " + map[bool]string{true: "[ok]matches[-]", false: "[warn]no match[-]"}[n > 0]
		}
		return append([]string{head}, hits...)
	}
	build := func(f *ui.Form) (config.Rule, error) {
		nr := config.Rule{
			Match:  f.Get("match"),
			Event:  strings.TrimSpace(f.Get("event")),
			Stream: strings.TrimSpace(f.Get("stream")),
			Count:  strings.TrimSpace(f.Get("count")),
			Stop:   f.Bool("stop"),
		}
		if _, err := regexp.Compile(nr.Match); err != nil {
			return nr, fmt.Errorf("regex: %v", err)
		}
		set, err := parseKV(f.Get("set"), "=")
		if err != nil {
			return nr, fmt.Errorf("set: %v", err)
		}
		nr.Set = set
		return nr, nil
	}
	f.OnChange = func(f *ui.Form) {
		if _, err := build(f); err != nil {
			f.Err = err.Error()
		}
	}
	f.OnSubmit = func(f *ui.Form) error {
		nr, err := build(f)
		if err != nil {
			return err
		}
		a.checkpoint()
		s := a.cfg.Source(source)
		if s == nil {
			return errors.New("source disappeared")
		}
		if isNew {
			s.Rules = append(s.Rules, nr)
		} else {
			s.Rules[idx] = nr
		}
		a.startData()
		return nil
	}
	f.OnPick = a.picker
	a.Push(f)
}
