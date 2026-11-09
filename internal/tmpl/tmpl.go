// Package tmpl implements Tesserae value templates.
//
// A template is markup text with variable references:
//
//	[muted]GPU:[-] {gpu_temp|%.0f|thr:75:90}°C  {tg|>6}
//
// A reference is {name} or {name|filter|filter:arg:arg}. Use {{ and }} for
// literal braces; shell-style ${...} is always literal. Rendered output is markup (see package render); values are
// escaped so text coming from logs can never inject style tags.
package tmpl

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rivo/uniseg"
)

// Lookup resolves variables while rendering.
type Lookup interface {
	Get(name string) (string, bool)
	History(name string) []float64
}

// MapLookup adapts a plain map (no history).
type MapLookup map[string]string

// Get implements Lookup.
func (m MapLookup) Get(name string) (string, bool) { v, ok := m[name]; return v, ok }

// History implements Lookup.
func (m MapLookup) History(string) []float64 { return nil }

// Overlay looks names up in Top first, then in Base.
type Overlay struct {
	Top  map[string]string
	Base Lookup
}

// Get implements Lookup.
func (o Overlay) Get(name string) (string, bool) {
	if v, ok := o.Top[name]; ok {
		return v, true
	}
	if o.Base == nil {
		return "", false
	}
	return o.Base.Get(name)
}

// History implements Lookup.
func (o Overlay) History(name string) []float64 {
	if o.Base == nil {
		return nil
	}
	return o.Base.History(name)
}

type filter struct {
	name string
	args []string
}

type segment struct {
	lit     string // literal markup when ref == ""
	ref     string
	filters []filter
}

// Template is a parsed template.
type Template struct {
	segs []segment
	Vars []string // referenced variable names, in order of appearance
}

var cache sync.Map // string -> *Template

// Parse parses (and caches) a template.
func Parse(src string) *Template {
	if t, ok := cache.Load(src); ok {
		return t.(*Template)
	}
	t := parse(src)
	cache.Store(src, t)
	return t
}

func parse(src string) *Template {
	t := &Template{}
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			t.segs = append(t.segs, segment{lit: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '{' && i+1 < len(src) && src[i+1] == '{' {
			lit.WriteByte('{')
			i++
			continue
		}
		if c == '}' && i+1 < len(src) && src[i+1] == '}' {
			lit.WriteByte('}')
			i++
			continue
		}
		// "${...}" is shell syntax, never a template reference.
		if c != '{' || (i > 0 && src[i-1] == '$') {
			lit.WriteByte(c)
			continue
		}
		end := strings.IndexByte(src[i+1:], '}')
		if end < 0 {
			lit.WriteString(src[i:])
			break
		}
		body := src[i+1 : i+1+end]
		name, rest, _ := strings.Cut(body, "|")
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, " \t\n{") {
			lit.WriteString(src[i : i+2+end])
			i += end + 1
			continue
		}
		flush()
		seg := segment{ref: name}
		if rest != "" {
			for _, f := range splitFilters(rest) {
				parts := strings.Split(f, ":")
				seg.filters = append(seg.filters, filter{name: strings.TrimSpace(parts[0]), args: parts[1:]})
			}
		}
		t.segs = append(t.segs, seg)
		t.Vars = append(t.Vars, name)
		i += end + 1
	}
	flush()
	return t
}

// splitFilters splits on '|' but keeps printf verbs like "%-6s" intact.
func splitFilters(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "|") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Render renders a template to markup.
func Render(src string, l Lookup) string {
	return Parse(src).Render(l)
}

// RenderPlain renders a template and strips nothing: values are not escaped.
// Used for variable assignment templates and shell commands.
func RenderPlain(src string, l Lookup) string {
	return Parse(src).render(l, false)
}

// Render renders the template to markup.
func (t *Template) Render(l Lookup) string { return t.render(l, true) }

func (t *Template) render(l Lookup, markup bool) string {
	var b strings.Builder
	for _, s := range t.segs {
		if s.ref == "" {
			b.WriteString(s.lit)
			continue
		}
		v, ok := l.Get(s.ref)
		if !ok && !hasDefault(s.filters) {
			v = "—"
		}
		pre, post := "", ""
		for _, f := range s.filters {
			var p1, p2 string
			v, p1, p2 = apply(f, v, ok, s.ref, l)
			if p1 != "" {
				pre, post = p1, p2
			}
		}
		if markup {
			v = Escape(v)
		}
		b.WriteString(pre)
		b.WriteString(v)
		b.WriteString(post)
	}
	return b.String()
}

func hasDefault(fs []filter) bool {
	for _, f := range fs {
		if f.name == "default" || f.name == "or" {
			return true
		}
	}
	return false
}

// Escape makes text safe to embed in markup.
func Escape(s string) string {
	if !strings.Contains(s, "[") {
		return s
	}
	return strings.ReplaceAll(s, "[", "[[")
}

// ParseNum extracts a float from a value ("14.93", "93%", "  7 ").
func ParseNum(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "%")
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, true
	}
	// Leading number followed by a unit ("14.9 t/s", "512 MiB").
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.' || s[end] == '-' && end == 0) {
		end++
	}
	if end > 0 {
		if f, err := strconv.ParseFloat(s[:end], 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func argInt(args []string, i, def int) int {
	if i < len(args) {
		if n, err := strconv.Atoi(strings.TrimSpace(args[i])); err == nil {
			return n
		}
	}
	return def
}

// argNum reads a numeric argument that may also name a variable.
func argNum(args []string, i int, l Lookup) float64 {
	if i >= len(args) {
		return 0
	}
	a := strings.TrimSpace(args[i])
	if f, err := strconv.ParseFloat(a, 64); err == nil {
		return f
	}
	if l != nil {
		if v, ok := l.Get(a); ok {
			f, _ := ParseNum(v)
			return f
		}
	}
	return 0
}

func argFloat(args []string, i int, def float64) float64 {
	if i < len(args) {
		if f, err := strconv.ParseFloat(strings.TrimSpace(args[i]), 64); err == nil {
			return f
		}
	}
	return def
}

// apply runs one filter. It returns the new value and optional markup that
// wraps the value (pre/post), used by color filters.
func apply(f filter, v string, ok bool, name string, l Lookup) (string, string, string) {
	n := f.name
	switch {
	case strings.HasPrefix(n, "%"):
		return printf(n, v), "", ""
	case len(n) > 1 && (n[0] == '<' || n[0] == '>' || n[0] == '^'):
		w, err := strconv.Atoi(n[1:])
		if err != nil {
			return v, "", ""
		}
		return Pad(v, w, n[0]), "", ""
	}
	switch n {
	case "pad", "left":
		return Pad(v, argInt(f.args, 0, 0), '<'), "", ""
	case "lpad", "right":
		return Pad(v, argInt(f.args, 0, 0), '>'), "", ""
	case "center":
		return Pad(v, argInt(f.args, 0, 0), '^'), "", ""
	case "trunc":
		return Truncate(v, argInt(f.args, 0, 20)), "", ""
	case "upper":
		return strings.ToUpper(v), "", ""
	case "lower":
		return strings.ToLower(v), "", ""
	case "trim":
		return strings.TrimSpace(v), "", ""
	case "default", "or":
		if !ok || strings.TrimSpace(v) == "" {
			return strings.Join(f.args, ":"), "", ""
		}
		return v, "", ""
	case "replace":
		if len(f.args) >= 2 {
			return strings.ReplaceAll(v, f.args[0], f.args[1]), "", ""
		}
	case "mul", "div", "add", "sub", "round":
		x, isNum := ParseNum(v)
		if !isNum {
			return v, "", ""
		}
		a := argNum(f.args, 0, l)
		switch n {
		case "mul":
			x *= a
		case "div":
			if a != 0 {
				x /= a
			}
		case "add":
			x += a
		case "sub":
			x -= a
		case "round":
			p := math.Pow(10, a)
			x = math.Round(x*p) / p
			return strconv.FormatFloat(x, 'f', int(a), 64), "", ""
		}
		return strconv.FormatFloat(x, 'f', -1, 64), "", ""
	case "bytes":
		x, isNum := ParseNum(v)
		if !isNum {
			return v, "", ""
		}
		return HumanBytes(x * argFloat(f.args, 0, 1)), "", ""
	case "si":
		x, isNum := ParseNum(v)
		if !isNum {
			return v, "", ""
		}
		return HumanSI(x), "", ""
	case "dur":
		x, isNum := ParseNum(v)
		if !isNum {
			return v, "", ""
		}
		return HumanDuration(time.Duration(x * argFloat(f.args, 0, 1) * float64(time.Second))), "", ""
	case "thr":
		x, isNum := ParseNum(v)
		if !isNum {
			return v, "", ""
		}
		inv := len(f.args) > 2 && strings.HasPrefix(f.args[2], "inv")
		c := ThresholdColor(x, []float64{argFloat(f.args, 0, 60), argFloat(f.args, 1, 90)}, inv)
		return v, "[" + c + "]", "[-]"
	case "color":
		if len(f.args) > 0 {
			return v, "[" + strings.Join(f.args, ":") + "]", "[-]"
		}
	case "style":
		// style:value=color:value2=color:*=fallback
		for _, a := range f.args {
			k, c, found := strings.Cut(a, "=")
			if found && (k == v || k == "*") {
				return v, "[" + c + "]", "[-]"
			}
		}
		return v, "", ""
	case "map":
		// map:key=val:key2=val2:*=fallback
		for _, a := range f.args {
			k, val, found := strings.Cut(a, "=")
			if found && (k == v || k == "*") {
				return val, "", ""
			}
		}
	case "bar":
		x, _ := ParseNum(v)
		return InlineBar(x, argFloat(f.args, 1, 100), argInt(f.args, 0, 10)), "", ""
	case "spark":
		return InlineSpark(l.History(name), argInt(f.args, 0, 10)), "", ""
	}
	return v, "", ""
}

func printf(verb, v string) string {
	last := verb[len(verb)-1]
	switch last {
	case 'd', 'x', 'X', 'o', 'b', 'c':
		if x, ok := ParseNum(v); ok {
			return fmt.Sprintf(verb, int64(math.Round(x)))
		}
		return v
	case 'f', 'e', 'g', 'F', 'E', 'G':
		if x, ok := ParseNum(v); ok {
			return fmt.Sprintf(verb, x)
		}
		// Keep the column width stable even when the value is not numeric.
		return fmt.Sprintf(strings.TrimRight(verb, "fFeEgG.0123456789")+widthOf(verb)+"s", v)
	default:
		return fmt.Sprintf(verb, v)
	}
}

// widthOf extracts the field width from a printf verb ("%-8.2f" -> "8").
func widthOf(verb string) string {
	s := strings.TrimLeft(verb[1:], "-+ #0")
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	return s[:end]
}

// ThresholdColor returns ok/warn/crit for a value against two thresholds.
func ThresholdColor(x float64, t []float64, invert bool) string {
	if len(t) == 0 {
		return "ok"
	}
	lvl := 0
	if invert {
		// Low values are bad: below t[0] is critical, below t[1] a warning.
		lvl = 0
		switch {
		case x < t[0]:
			lvl = 2
		case len(t) > 1 && x < t[1]:
			lvl = 1
		}
	} else {
		for i, th := range t {
			if x >= th {
				lvl = i + 1
			}
		}
	}
	return [...]string{"ok", "warn", "crit", "crit"}[min(lvl, 3)]
}

// Width returns the display width of plain text.
func Width(s string) int { return uniseg.StringWidth(s) }

// Pad pads plain text to width w; align is '<', '>' or '^'.
func Pad(s string, w int, align byte) string {
	sw := Width(s)
	if sw >= w {
		return s
	}
	gap := w - sw
	switch align {
	case '>':
		return strings.Repeat(" ", gap) + s
	case '^':
		l := gap / 2
		return strings.Repeat(" ", l) + s + strings.Repeat(" ", gap-l)
	default:
		return s + strings.Repeat(" ", gap)
	}
}

// Truncate shortens plain text to at most w cells, ending with an ellipsis.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Width(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		cw := g.Width()
		if used+cw > w-1 {
			break
		}
		b.WriteString(g.Str())
		used += cw
	}
	b.WriteString("…")
	return b.String()
}

// HumanBytes formats a byte count with binary units.
func HumanBytes(x float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	i := 0
	for math.Abs(x) >= 1024 && i < len(units)-1 {
		x /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", x, units[i])
	}
	return fmt.Sprintf("%.1f %s", x, units[i])
}

// HumanSI formats a number with k/M/G suffixes.
func HumanSI(x float64) string {
	units := []string{"", "k", "M", "G", "T"}
	i := 0
	for math.Abs(x) >= 1000 && i < len(units)-1 {
		x /= 1000
		i++
	}
	if i == 0 {
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprintf("%.1f%s", x, units[i])
}

// HumanDuration formats a duration as 1d02h, 3h04m, 5m06s or 7.2s.
func HumanDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	s := int64(d / time.Second)
	switch {
	case d < time.Minute:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", s/3600, s%3600/60)
	default:
		return fmt.Sprintf("%dd%02dh", s/86400, s%86400/3600)
	}
}

var eighths = []rune(" ▏▎▍▌▋▊▉█")

// InlineBar renders a horizontal bar of width w with 1/8 cell resolution.
func InlineBar(x, max float64, w int) string {
	if w <= 0 {
		return ""
	}
	if max <= 0 {
		max = 100
	}
	r := math.Max(0, math.Min(1, x/max))
	units := int(math.Round(r * float64(w*8)))
	var b strings.Builder
	for i := 0; i < w; i++ {
		u := min(max0(units-i*8), 8)
		if u == 0 {
			b.WriteRune('·')
		} else {
			b.WriteRune(eighths[u])
		}
	}
	return b.String()
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// InlineSpark renders the last w samples as a one-line sparkline.
func InlineSpark(h []float64, w int) string {
	if w <= 0 {
		return ""
	}
	if len(h) > w {
		h = h[len(h)-w:]
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range h {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", w-len(h)))
	for _, v := range h {
		i := 0
		if hi > lo {
			i = int(math.Round((v - lo) / (hi - lo) * 7))
		}
		b.WriteRune(sparkRunes[i])
	}
	return b.String()
}
