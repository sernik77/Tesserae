// Package render draws styled text, boxes and glyphs onto a tcell screen.
package render

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/sernik77/tesserae/internal/config"
)

// ColorRoles lists palette roles in the order shown in the editor.
var ColorRoles = []string{"bg", "fg", "muted", "primary", "secondary", "accent", "ok", "warn", "crit", "border", "title", "select", "panel"}

// Presets are built-in palettes. "terminal" uses the terminal's own ANSI colors.
var Presets = map[string]map[string]string{
	"tesserae": {
		"bg": "#0f1117", "fg": "#c9d1e0", "muted": "#5c6582", "primary": "#ff5f87", "secondary": "#5fd7ff",
		"accent": "#b48cff", "ok": "#7ee787", "warn": "#f2cc60", "crit": "#ff5c57", "border": "#2c3247",
		"title": "#5fd7ff", "select": "#ffaf00", "panel": "#181b26",
	},
	"dracula": {
		"bg": "#282a36", "fg": "#f8f8f2", "muted": "#6272a4", "primary": "#ff79c6", "secondary": "#8be9fd",
		"accent": "#bd93f9", "ok": "#50fa7b", "warn": "#f1fa8c", "crit": "#ff5555", "border": "#44475a",
		"title": "#8be9fd", "select": "#ffb86c", "panel": "#343746",
	},
	"nord": {
		"bg": "#2e3440", "fg": "#d8dee9", "muted": "#616e88", "primary": "#88c0d0", "secondary": "#81a1c1",
		"accent": "#b48ead", "ok": "#a3be8c", "warn": "#ebcb8b", "crit": "#bf616a", "border": "#434c5e",
		"title": "#88c0d0", "select": "#d08770", "panel": "#3b4252",
	},
	"gruvbox": {
		"bg": "#282828", "fg": "#ebdbb2", "muted": "#928374", "primary": "#fb4934", "secondary": "#83a598",
		"accent": "#d3869b", "ok": "#b8bb26", "warn": "#fabd2f", "crit": "#fb4934", "border": "#504945",
		"title": "#fabd2f", "select": "#fe8019", "panel": "#3c3836",
	},
	"tokyonight": {
		"bg": "#1a1b26", "fg": "#c0caf5", "muted": "#565f89", "primary": "#7aa2f7", "secondary": "#7dcfff",
		"accent": "#bb9af7", "ok": "#9ece6a", "warn": "#e0af68", "crit": "#f7768e", "border": "#292e42",
		"title": "#7dcfff", "select": "#ff9e64", "panel": "#24283b",
	},
	"catppuccin": {
		"bg": "#1e1e2e", "fg": "#cdd6f4", "muted": "#6c7086", "primary": "#f5c2e7", "secondary": "#89dceb",
		"accent": "#cba6f7", "ok": "#a6e3a1", "warn": "#f9e2af", "crit": "#f38ba8", "border": "#313244",
		"title": "#89b4fa", "select": "#fab387", "panel": "#313244",
	},
	"terminal": {
		"bg": "default", "fg": "default", "muted": "gray", "primary": "red", "secondary": "aqua",
		"accent": "fuchsia", "ok": "green", "warn": "yellow", "crit": "red", "border": "gray",
		"title": "aqua", "select": "yellow", "panel": "black",
	},
}

// PresetNames returns preset names sorted, "tesserae" first.
func PresetNames() []string {
	names := slices.Sorted(maps.Keys(Presets))
	i := slices.Index(names, "tesserae")
	names = slices.Delete(names, i, i+1)
	return append([]string{"tesserae"}, names...)
}

// Theme is a resolved palette.
type Theme struct {
	colors map[string]tcell.Color
	Border string
}

// NewTheme resolves a config theme.
func NewTheme(t config.Theme) *Theme {
	p, ok := Presets[t.Preset]
	if !ok {
		p = Presets["tesserae"]
	}
	th := &Theme{colors: map[string]tcell.Color{}, Border: t.Border}
	for k, v := range p {
		th.colors[k] = parseColor(v)
	}
	for k, v := range t.Colors {
		if c, ok := th.lookup(v); ok {
			th.colors[strings.ToLower(k)] = c
		}
	}
	if th.Border == "" {
		th.Border = "rounded"
	}
	return th
}

// C returns a palette color by role.
func (t *Theme) C(role string) tcell.Color {
	if c, ok := t.colors[role]; ok {
		return c
	}
	return tcell.ColorDefault
}

// Color resolves a palette role, hex value, 0-255 index or tcell color name.
func (t *Theme) Color(s string) (tcell.Color, bool) {
	return t.lookup(s)
}

func (t *Theme) lookup(s string) (tcell.Color, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, false
	}
	if c, ok := t.colors[s]; ok {
		return c, true
	}
	if s == "default" {
		return tcell.ColorDefault, true
	}
	if s[0] == '#' {
		if len(s) != 7 && len(s) != 4 {
			return 0, false
		}
		c := tcell.GetColor(s)
		return c, c != tcell.ColorDefault
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n >= 0 && n <= 255 {
			return tcell.PaletteColor(n), true
		}
		return 0, false
	}
	if c, ok := tcell.ColorNames[s]; ok {
		return c, true
	}
	return 0, false
}

func parseColor(s string) tcell.Color {
	if s == "default" {
		return tcell.ColorDefault
	}
	if c, ok := tcell.ColorNames[s]; ok {
		return c
	}
	return tcell.GetColor(s)
}

// Base returns the default canvas style.
func (t *Theme) Base() tcell.Style {
	return tcell.StyleDefault.Background(t.C("bg")).Foreground(t.C("fg"))
}

// Style returns a style with role colors (empty role = keep).
func (t *Theme) Style(fg, bg string) tcell.Style {
	st := t.Base()
	if fg != "" {
		if c, ok := t.lookup(fg); ok {
			st = st.Foreground(c)
		}
	}
	if bg != "" {
		if c, ok := t.lookup(bg); ok {
			st = st.Background(c)
		}
	}
	return st
}
