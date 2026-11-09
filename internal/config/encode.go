package config

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Encode renders the dashboard as hand-written-looking TOML: stable key order,
// zero values omitted, multi-line templates as ”' literals.
func Encode(d *Dashboard) []byte {
	var b bytes.Buffer
	w := &tw{b: &b}

	b.WriteString("# Tesserae dashboard — edit by hand or with: tesserae --edit <file>\n\n")
	w.str("name", d.Name)
	w.str("refresh", FormatDuration(d.Refresh.Duration))
	w.str("shell", d.Shell)
	if d.Units != "cells" {
		w.str("units", d.Units)
	}

	b.WriteString("\n[theme]\n")
	w.str("preset", d.Theme.Preset)
	w.str("border", d.Theme.Border)
	if len(d.Theme.Colors) > 0 {
		b.WriteString("\n[theme.colors]\n")
		w.strMap(d.Theme.Colors)
	}
	if len(d.Vars) > 0 {
		b.WriteString("\n[vars]\n")
		w.strMap(d.Vars)
	}

	for _, s := range d.Sources {
		b.WriteString("\n# " + strings.Repeat("─", 70) + "\n[[source]]\n")
		w.str("name", s.Name)
		w.str("type", s.Type)
		w.str("path", s.Path)
		w.str("command", s.Command)
		w.str("url", s.URL)
		w.str("method", s.Method)
		w.str("body", s.Body)
		w.str("interval", FormatDuration(s.Interval.Duration))
		w.str("timeout", FormatDuration(s.Timeout.Duration))
		defSplit := "whole"
		if s.Type == "tail" || s.Type == "stream" {
			defSplit = "lines"
		}
		if s.Split != defSplit {
			w.str("split", s.Split)
		}
		if s.Format != "text" {
			w.str("format", s.Format)
		}
		w.str("prefix", s.Prefix)
		w.str("var", s.Var)
		w.int("backfill", s.Backfill)
		if len(s.Headers) > 0 {
			w.inlineMap("headers", s.Headers)
		}
		for _, r := range s.Rules {
			b.WriteString("\n[[source.rule]]\n")
			w.str("match", r.Match)
			if len(r.Set) > 0 {
				w.inlineMap("set", r.Set)
			}
			w.str("event", r.Event)
			w.str("stream", r.Stream)
			w.str("count", r.Count)
			w.bool("stop", r.Stop)
		}
	}

	for _, e := range d.Elems {
		b.WriteString("\n# " + strings.Repeat("─", 70) + "\n[[widget]]\n")
		w.str("id", e.ID)
		w.str("type", e.Type)
		fmt.Fprintf(&b, "x = %s\ny = %s\nw = %s\nh = %s\n", dimTOML(e.X), dimTOML(e.Y), dimTOML(e.W), dimTOML(e.H))
		w.str("title", e.Title)
		w.str("title_align", e.TitleAlign)
		w.str("border", e.Border)
		w.str("text", e.Text)
		w.str("var", e.Var)
		w.float("min", e.Min)
		w.float("max", e.Max)
		if len(e.Thresholds) > 0 {
			parts := make([]string, len(e.Thresholds))
			for i, t := range e.Thresholds {
				parts[i] = fmtFloat(t)
			}
			fmt.Fprintf(&b, "thresholds = [%s]\n", strings.Join(parts, ", "))
		}
		w.bool("invert", e.Invert)
		w.str("align", e.Align)
		w.str("valign", e.VAlign)
		w.str("stream", e.Stream)
		w.str("filter", e.Filter)
		w.int("max_lines", e.MaxLines)
		w.bool("wrap", e.Wrap)
		w.str("fg", e.Fg)
		w.str("bg", e.Bg)
		w.str("border_fg", e.BorderFg)
		w.str("title_fg", e.TitleFg)
		w.bool("bold", e.Bold)
		w.str("action", e.Action)
		w.str("key", e.Key)
		w.bool("confirm", e.Confirm)
		w.str("group", e.Group)
		w.bool("locked", e.Locked)
	}
	return b.Bytes()
}

func dimTOML(d Dim) string {
	v, _ := d.MarshalTOML()
	return string(v)
}

type tw struct{ b *bytes.Buffer }

func (w *tw) str(k, v string) {
	if v == "" {
		return
	}
	fmt.Fprintf(w.b, "%s = %s\n", k, quote(v))
}

func (w *tw) int(k string, v int) {
	if v != 0 {
		fmt.Fprintf(w.b, "%s = %d\n", k, v)
	}
}

func (w *tw) float(k string, v float64) {
	if v != 0 {
		fmt.Fprintf(w.b, "%s = %s\n", k, fmtFloat(v))
	}
}

func (w *tw) bool(k string, v bool) {
	if v {
		fmt.Fprintf(w.b, "%s = true\n", k)
	}
}

func (w *tw) strMap(m map[string]string) {
	for _, k := range sortedKeys(m) {
		fmt.Fprintf(w.b, "%s = %s\n", key(k), quote(m[k]))
	}
}

func (w *tw) inlineMap(name string, m map[string]string) {
	parts := make([]string, 0, len(m))
	for _, k := range sortedKeys(m) {
		parts = append(parts, key(k)+" = "+basic(m[k]))
	}
	fmt.Fprintf(w.b, "%s = { %s }\n", name, strings.Join(parts, ", "))
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func fmtFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// key quotes a TOML key when it is not a bare key.
func key(k string) string {
	for _, r := range k {
		if !(r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return basic(k)
		}
	}
	if k == "" {
		return `""`
	}
	return k
}

// quote picks the most readable TOML string form: literal strings for regexes
// and backslash-heavy text, multi-line literals for templates.
func quote(s string) string {
	if strings.Contains(s, "\n") && !strings.Contains(s, "'''") && literalSafe(s, true) {
		return "'''\n" + s + "'''"
	}
	if !strings.Contains(s, "'") && literalSafe(s, false) && (strings.ContainsAny(s, `\"`)) {
		return "'" + s + "'"
	}
	return basic(s)
}

func literalSafe(s string, multiline bool) bool {
	for _, r := range s {
		if r == '\n' && multiline {
			continue
		}
		if r < 0x20 && r != '\t' || r == 0x7f {
			return false
		}
	}
	return true
}

// basic renders a TOML basic string.
func basic(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
