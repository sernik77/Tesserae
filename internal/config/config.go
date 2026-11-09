package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Load reads a TOML dashboard from path, applies defaults and validates it.
func Load(path string) (*Dashboard, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes a TOML dashboard, applies defaults and validates it.
func Parse(data []byte) (*Dashboard, error) {
	var d Dashboard
	md, err := toml.Decode(string(data), &d)
	if err != nil {
		return nil, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, 0, len(und))
		for _, k := range und {
			keys = append(keys, k.String())
		}
		return nil, fmt.Errorf("unknown keys: %s", strings.Join(keys, ", "))
	}
	ApplyDefaults(&d)
	if err := Validate(&d); err != nil {
		return nil, err
	}
	return &d, nil
}

// New returns an empty dashboard with defaults applied.
func New(name string) *Dashboard {
	d := &Dashboard{Name: name}
	ApplyDefaults(d)
	return d
}

// ApplyDefaults fills in missing values. It is idempotent.
func ApplyDefaults(d *Dashboard) {
	if d.Refresh.Duration <= 0 {
		d.Refresh.Duration = time.Second
	}
	if d.Units == "" {
		d.Units = "cells"
	}
	if d.Theme.Preset == "" {
		d.Theme.Preset = "tesserae"
	}
	if d.Theme.Border == "" {
		d.Theme.Border = "rounded"
	}
	for i := range d.Sources {
		s := &d.Sources[i]
		if s.Type == "" {
			switch {
			case s.Path != "":
				s.Type = "tail"
			case s.URL != "":
				s.Type = "http"
			default:
				s.Type = "exec"
			}
		}
		if s.Split == "" {
			if s.Type == "tail" || s.Type == "stream" {
				s.Split = "lines"
			} else {
				s.Split = "whole"
			}
		}
		if s.Format == "" {
			s.Format = "text"
		}
		if (s.Type == "exec" || s.Type == "http") && s.Interval.Duration <= 0 {
			s.Interval.Duration = time.Second
		}
	}
	for i := range d.Elems {
		e := &d.Elems[i]
		if e.Type == "" {
			e.Type = "text"
		}
		if e.ID == "" {
			e.ID = UniqueID(d, e.Type)
		}
	}
}

var idRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// Validate checks the dashboard for errors that would break rendering.
func Validate(d *Dashboard) error {
	var errs []error
	seen := map[string]bool{}
	for i, s := range d.Sources {
		where := fmt.Sprintf("source #%d (%s)", i+1, s.Name)
		if s.Name == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", where))
		} else if seen["s:"+s.Name] {
			errs = append(errs, fmt.Errorf("%s: duplicate name", where))
		}
		seen["s:"+s.Name] = true
		if !slices.Contains(SourceTypes, s.Type) {
			errs = append(errs, fmt.Errorf("%s: unknown type %q", where, s.Type))
		}
		for j, r := range s.Rules {
			if _, err := regexp.Compile(r.Match); err != nil {
				errs = append(errs, fmt.Errorf("%s rule #%d: %v", where, j+1, err))
			}
		}
	}
	for i, e := range d.Elems {
		where := fmt.Sprintf("widget #%d (%s)", i+1, e.ID)
		if seen["e:"+e.ID] {
			errs = append(errs, fmt.Errorf("%s: duplicate id", where))
		}
		seen["e:"+e.ID] = true
		if !slices.Contains(ElementTypes, e.Type) {
			errs = append(errs, fmt.Errorf("%s: unknown type %q", where, e.Type))
		}
		if !slices.Contains(BorderStyles, e.Border) {
			errs = append(errs, fmt.Errorf("%s: unknown border %q", where, e.Border))
		}
		if e.Filter != "" {
			if _, err := regexp.Compile(strings.TrimPrefix(e.Filter, "!")); err != nil {
				errs = append(errs, fmt.Errorf("%s filter: %v", where, err))
			}
		}
	}
	return errors.Join(errs...)
}

// UniqueID returns base, base2, base3... not used by any element.
func UniqueID(d *Dashboard, base string) string {
	base = strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return '_'
		}
		return r
	}, base)
	if base == "" || !idRe.MatchString(base) {
		base = "widget"
	}
	used := map[string]bool{}
	for _, e := range d.Elems {
		used[e.ID] = true
	}
	if !used[base] {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s%d", base, n)
		if !used[id] {
			return id
		}
	}
}

// Index returns the index of the element with id, or -1.
func (d *Dashboard) Index(id string) int {
	for i := range d.Elems {
		if d.Elems[i].ID == id {
			return i
		}
	}
	return -1
}

// Source returns the source called name, or nil.
func (d *Dashboard) Source(name string) *Source {
	for i := range d.Sources {
		if d.Sources[i].Name == name {
			return &d.Sources[i]
		}
	}
	return nil
}

// Clone returns a deep copy of the dashboard.
func (d *Dashboard) Clone() *Dashboard {
	c := *d
	c.Vars = maps.Clone(d.Vars)
	c.Theme.Colors = maps.Clone(d.Theme.Colors)
	c.Sources = make([]Source, len(d.Sources))
	for i, s := range d.Sources {
		c.Sources[i] = s.Clone()
	}
	c.Elems = make([]Element, len(d.Elems))
	for i, e := range d.Elems {
		c.Elems[i] = e.Clone()
	}
	return &c
}

// Clone returns a deep copy of the source.
func (s Source) Clone() Source {
	s.Headers = maps.Clone(s.Headers)
	rules := make([]Rule, len(s.Rules))
	for i, r := range s.Rules {
		r.Set = maps.Clone(r.Set)
		rules[i] = r
	}
	s.Rules = rules
	return s
}

// Clone returns a deep copy of the element.
func (e Element) Clone() Element {
	e.Thresholds = slices.Clone(e.Thresholds)
	return e
}

// Save writes the dashboard atomically. The first save of a session keeps the
// previous file as path.bak when backup is true.
func Save(path string, d *Dashboard, backup bool) error {
	data := Encode(d)
	// Re-parse what we are about to write: never replace a good file with a
	// broken one.
	if _, err := Parse(data); err != nil {
		return fmt.Errorf("refusing to save, generated config does not parse: %w", err)
	}
	if backup {
		if old, err := os.ReadFile(path); err == nil {
			if err := os.WriteFile(path+".bak", old, 0o644); err != nil {
				return err
			}
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tesserae-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
