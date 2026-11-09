package data

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/silverx/tesserae/internal/config"
	"github.com/silverx/tesserae/internal/tmpl"
)

const maxFlatten = 2000

type rule struct {
	cfg config.Rule
	re  *regexp.Regexp
}

// Processor applies a source's parsing configuration to raw text.
type Processor struct {
	src   config.Source
	rules []rule
	store *Store
	lines int
	keys  map[string]bool // variables produced by the last structured parse
}

// NewProcessor compiles the rules of src.
func NewProcessor(src config.Source, store *Store) (*Processor, error) {
	p := &Processor{src: src, store: store}
	for i, r := range src.Rules {
		re, err := regexp.Compile(r.Match)
		if err != nil {
			return nil, fmt.Errorf("rule #%d: %w", i+1, err)
		}
		p.rules = append(p.rules, rule{cfg: r, re: re})
	}
	return p, nil
}

func (p *Processor) prefix() string {
	switch p.src.Prefix {
	case "":
		return p.src.Name + "."
	case "-":
		return ""
	}
	return strings.TrimSuffix(p.src.Prefix, ".") + "."
}

// Line processes one line of a line-oriented source.
func (p *Processor) Line(line string, backfill bool) {
	line = strings.TrimRight(line, "\r")
	at := time.Now()
	if backfill {
		at = time.Time{}
	}
	p.lines++
	p.store.Push("source:"+p.src.Name, tmpl.Escape(line), at)
	p.apply(line, at)
	if p.src.Var != "" {
		p.store.Set(p.src.Var, line)
	}
	if p.src.Format == "json" || p.src.Format == "kv" {
		p.structured(line)
	}
}

// Whole processes the complete output of a polling source.
func (p *Processor) Whole(text string) {
	text = strings.TrimRight(text, "\r\n ")
	at := time.Now()
	if p.src.Split == "lines" {
		for _, l := range strings.Split(text, "\n") {
			p.Line(l, false)
		}
		return
	}
	lines := strings.Split(text, "\n")
	entries := make([]Entry, len(lines))
	for i, l := range lines {
		entries[i] = Entry{At: at, Text: tmpl.Escape(strings.TrimRight(l, "\r"))}
	}
	p.store.Replace("source:"+p.src.Name, entries)
	p.lines += len(lines)
	if p.src.Var != "" {
		p.store.Set(p.src.Var, strings.TrimSpace(text))
	}
	p.structured(text)
	p.apply(text, at)
}

func (p *Processor) structured(text string) {
	seen := map[string]bool{}
	defer func() {
		// Whole outputs describe the full state: drop keys that disappeared
		// (e.g. models.0.* after a model is unloaded).
		if p.src.Split != "whole" || len(seen) == 0 {
			return
		}
		var gone []string
		for k := range p.keys {
			if !seen[k] {
				gone = append(gone, k)
			}
		}
		if len(gone) > 0 {
			p.store.Delete(gone...)
		}
		p.keys = seen
	}()
	switch p.src.Format {
	case "json":
		var v any
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			return
		}
		n := 0
		Flatten(strings.TrimSuffix(p.prefix(), "."), v, func(k, val string) {
			if n < maxFlatten {
				p.store.Set(k, val)
				seen[k] = true
				n++
			}
		})
	case "kv":
		pre := p.prefix()
		for _, l := range strings.Split(text, "\n") {
			k, v, ok := cutKV(l)
			if ok {
				p.store.Set(pre+k, v)
				seen[pre+k] = true
			}
		}
	}
}

func cutKV(l string) (string, string, bool) {
	i := strings.IndexAny(l, "=:")
	if i <= 0 {
		return "", "", false
	}
	k := strings.TrimSpace(l[:i])
	if k == "" || strings.ContainsAny(k, " \t{}|") {
		return "", "", false
	}
	return strings.ReplaceAll(k, " ", "_"), strings.Trim(strings.TrimSpace(l[i+1:]), `"`), true
}

// Flatten walks a decoded JSON value and emits dotted paths ("a.b.0.c").
func Flatten(prefix string, v any, emit func(k, v string)) {
	join := func(k string) string {
		if prefix == "" {
			return k
		}
		return prefix + "." + k
	}
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			Flatten(join(k), val, emit)
		}
	case []any:
		if prefix != "" {
			emit(prefix+".len", strconv.Itoa(len(t)))
		}
		for i, val := range t {
			Flatten(join(strconv.Itoa(i)), val, emit)
		}
	case string:
		emit(prefix, t)
	case float64:
		emit(prefix, strconv.FormatFloat(t, 'f', -1, 64))
	case bool:
		emit(prefix, strconv.FormatBool(t))
	case nil:
		emit(prefix, "")
	}
}

func (p *Processor) apply(text string, at time.Time) {
	for _, r := range p.rules {
		m := r.re.FindStringSubmatchIndex(text)
		if m == nil {
			continue
		}
		caps := Captures(r.re, text, m)
		for i, name := range r.re.SubexpNames() {
			if name != "" && m[2*i] >= 0 {
				p.store.Set(name, caps[name])
			}
		}
		lk := tmpl.Overlay{Top: caps, Base: p.store}
		for k, t := range r.cfg.Set {
			p.store.Set(k, tmpl.RenderPlain(t, lk))
		}
		if r.cfg.Count != "" {
			p.store.Inc(r.cfg.Count)
		}
		if r.cfg.Event != "" {
			stream := r.cfg.Stream
			if stream == "" {
				stream = "events"
			}
			p.store.Push(stream, tmpl.Render(r.cfg.Event, lk), at)
		}
		if r.cfg.Stop {
			return
		}
	}
}

// Captures returns named and numbered groups of a match ("0" is the match).
func Captures(re *regexp.Regexp, text string, m []int) map[string]string {
	caps := map[string]string{}
	for i, name := range re.SubexpNames() {
		if m[2*i] < 0 {
			continue
		}
		v := text[m[2*i]:m[2*i+1]]
		caps[strconv.Itoa(i)] = v
		if name != "" {
			caps[name] = v
		}
	}
	return caps
}
