// Package config defines the Tesserae dashboard schema and its TOML I/O.
package config

import (
	"fmt"
	"time"
)

// Dashboard is the top-level configuration for a Tesserae dashboard.
type Dashboard struct {
	Name    string            `toml:"name"`
	Refresh Duration          `toml:"refresh"`
	Shell   string            `toml:"shell"` // shell used for exec sources and button actions
	Units   string            `toml:"units"` // cells | percent: unit for elements created in edit mode
	Theme   Theme             `toml:"theme"`
	Vars    map[string]string `toml:"vars"` // initial variable values
	Sources []Source          `toml:"source"`
	Elems   []Element         `toml:"widget"`
}

// Theme selects a preset palette and allows per-color overrides.
type Theme struct {
	Preset string            `toml:"preset"`
	Border string            `toml:"border"` // default border style for elements
	Colors map[string]string `toml:"colors"` // overrides: bg fg muted primary secondary accent ok warn crit border title select
}

// Source is a named producer of raw text that is parsed into variables.
type Source struct {
	Name     string            `toml:"name"`
	Type     string            `toml:"type"` // tail | exec | stream | http
	Path     string            `toml:"path"`
	Command  string            `toml:"command"`
	URL      string            `toml:"url"`
	Method   string            `toml:"method"`
	Body     string            `toml:"body"`
	Headers  map[string]string `toml:"headers"`
	Interval Duration          `toml:"interval"`
	Timeout  Duration          `toml:"timeout"`
	Split    string            `toml:"split"`    // lines | whole
	Format   string            `toml:"format"`   // text | json | kv
	Prefix   string            `toml:"prefix"`   // variable prefix for json/kv flattening; default: source name
	Var      string            `toml:"var"`      // store the whole trimmed output in this variable
	Backfill int               `toml:"backfill"` // tail: replay this many existing lines on start (-1 = none)
	Rules    []Rule            `toml:"rule"`
}

// Rule matches raw text and turns it into variables, events and counters.
type Rule struct {
	Match  string            `toml:"match"`  // regex; named groups become variables
	Set    map[string]string `toml:"set"`    // extra variables; values are templates over captures
	Event  string            `toml:"event"`  // template pushed to an event stream
	Stream string            `toml:"stream"` // event stream name (default "events")
	Count  string            `toml:"count"`  // variable incremented on every match
	Stop   bool              `toml:"stop"`   // stop evaluating further rules for this input
}

// Element is a rectangle on the canvas that renders something.
type Element struct {
	ID         string    `toml:"id"`
	Type       string    `toml:"type"` // text | gauge | spark | log | bignum | box | button
	X          Dim       `toml:"x"`
	Y          Dim       `toml:"y"`
	W          Dim       `toml:"w"`
	H          Dim       `toml:"h"`
	Title      string    `toml:"title"`
	TitleAlign string    `toml:"title_align"` // left | center | right
	Border     string    `toml:"border"`      // "" (theme default) | none | rounded | sharp | double | thick | ascii | dashed
	Text       string    `toml:"text"`        // template: content / label
	Var        string    `toml:"var"`         // numeric variable for gauge / spark
	Min        float64   `toml:"min"`
	Max        float64   `toml:"max"`
	Thresholds []float64 `toml:"thresholds"` // ok < t[0] <= warn < t[1] <= crit
	Invert     bool      `toml:"invert"`     // low values are bad
	Align      string    `toml:"align"`      // left | center | right
	VAlign     string    `toml:"valign"`     // top | middle | bottom
	Stream     string    `toml:"stream"`     // log: event stream name or "source:<name>"
	Filter     string    `toml:"filter"`     // log: regex filter
	MaxLines   int       `toml:"max_lines"`
	Wrap       bool      `toml:"wrap"`
	Fg         string    `toml:"fg"`
	Bg         string    `toml:"bg"`
	BorderFg   string    `toml:"border_fg"`
	TitleFg    string    `toml:"title_fg"`
	Bold       bool      `toml:"bold"`
	Action     string    `toml:"action"`  // button: shell command
	Key        string    `toml:"key"`     // button: keyboard shortcut (single key)
	Confirm    bool      `toml:"confirm"` // button: ask before running
	Group      string    `toml:"group"`
	Locked     bool      `toml:"locked"`
}

// ElementTypes lists all element types in menu order.
var ElementTypes = []string{"text", "gauge", "spark", "bignum", "log", "box", "button"}

// ElementTypeLabels are human readable names for ElementTypes.
var ElementTypeLabels = map[string]string{
	"text":   "Text / template",
	"gauge":  "Gauge bar",
	"spark":  "Sparkline",
	"bignum": "Big number",
	"log":    "Log / event feed",
	"box":    "Box / frame",
	"button": "Button / action",
}

// SourceTypes lists all source types.
var SourceTypes = []string{"tail", "exec", "stream", "http"}

// BorderStyles lists all border styles; "" means the theme default.
var BorderStyles = []string{"", "none", "rounded", "sharp", "double", "thick", "ascii", "dashed"}

// Duration wraps time.Duration for TOML strings ("1s", "500ms").
type Duration struct {
	time.Duration
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		d.Duration = 0
		return nil
	}
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("invalid duration %q", text)
	}
	d.Duration = v
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(FormatDuration(d.Duration)), nil
}

// FormatDuration renders a duration compactly ("1s", "500ms", "1m30s").
func FormatDuration(d time.Duration) string {
	if d == 0 {
		return ""
	}
	s := d.String()
	// time.Duration prints "1m0s" — trim the zero tails.
	for _, suf := range []string{"m0s", "h0m"} {
		if len(s) > len(suf) && s[len(s)-len(suf):] == suf {
			s = s[:len(s)-len(suf)+1]
		}
	}
	return s
}
