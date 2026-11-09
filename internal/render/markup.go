package render

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// Span is a run of text sharing one style.
type Span struct {
	Text  string
	Style tcell.Style
}

// Line is a sequence of spans.
type Line []Span

// Width returns the display width of the line.
func (l Line) Width() int {
	w := 0
	for _, s := range l {
		w += uniseg.StringWidth(s.Text)
	}
	return w
}

// Plain returns the line text without styles.
func (l Line) Plain() string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.Text)
	}
	return b.String()
}

// Markup parses style tags into lines of spans.
//
//	[fg]  [fg:bg]  [fg:bg:attrs]  [::b]  [-] pops one tag  [/] resets  [[ is a literal [
//
// Colors are palette roles (ok, warn, muted…), #rrggbb, 0-255 or color names.
// Attributes: b bold, d dim, i italic, u underline, r reverse, s strike, l blink.
// A bracket group that is not a valid tag is printed literally, so log text
// such as "[GIN]" survives.
func Markup(src string, base tcell.Style, th *Theme) []Line {
	stack := []tcell.Style{base}
	cur := base
	var lines []Line
	var line Line
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			line = append(line, Span{Text: text.String(), Style: cur})
			text.Reset()
		}
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch c {
		case '\n':
			flush()
			lines = append(lines, line)
			line = nil
			continue
		case '\t':
			text.WriteString("    ")
			continue
		case '[':
			if i+1 < len(src) && src[i+1] == '[' {
				text.WriteByte('[')
				i++
				continue
			}
			end := strings.IndexByte(src[i+1:], ']')
			if end < 0 || end > 40 {
				break
			}
			tag := src[i+1 : i+1+end]
			if st, op, ok := parseTag(tag, cur, base, th); ok {
				flush()
				switch op {
				case '-':
					if len(stack) > 1 {
						stack = stack[:len(stack)-1]
					}
					cur = stack[len(stack)-1]
				case '/':
					stack = stack[:1]
					cur = base
				default:
					stack = append(stack, st)
					cur = st
				}
				i += end + 1
				continue
			}
		}
		text.WriteByte(c)
	}
	flush()
	lines = append(lines, line)
	return lines
}

// parseTag interprets a tag body. op is '-' (pop), '/' (reset) or 0 (push).
func parseTag(tag string, cur, base tcell.Style, th *Theme) (tcell.Style, byte, bool) {
	switch tag {
	case "-":
		return cur, '-', true
	case "/":
		return cur, '/', true
	case "":
		return cur, 0, false
	}
	parts := strings.Split(tag, ":")
	if len(parts) > 3 {
		return cur, 0, false
	}
	st := cur
	bfg, bbg, _ := base.Decompose()
	if fg := parts[0]; fg != "" {
		if fg == "-" {
			st = st.Foreground(bfg)
		} else if c, ok := th.Color(fg); ok {
			st = st.Foreground(c)
		} else {
			return cur, 0, false
		}
	}
	if len(parts) > 1 && parts[1] != "" {
		if parts[1] == "-" {
			st = st.Background(bbg)
		} else if c, ok := th.Color(parts[1]); ok {
			st = st.Background(c)
		} else {
			return cur, 0, false
		}
	}
	if len(parts) > 2 {
		for _, a := range parts[2] {
			switch a {
			case 'b':
				st = st.Bold(true)
			case 'd':
				st = st.Dim(true)
			case 'i':
				st = st.Italic(true)
			case 'u':
				st = st.Underline(true)
			case 'r':
				st = st.Reverse(true)
			case 's':
				st = st.StrikeThrough(true)
			case 'l':
				st = st.Blink(true)
			case '-':
				_, _, attrs := base.Decompose()
				st = st.Attributes(attrs)
			default:
				return cur, 0, false
			}
		}
	}
	if len(parts) == 1 && parts[0] == "" {
		return cur, 0, false
	}
	return st, 0, true
}

// StripMarkup returns the plain text of markup.
func StripMarkup(src string, th *Theme) string {
	var b strings.Builder
	for i, l := range Markup(src, tcell.StyleDefault, th) {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l.Plain())
	}
	return b.String()
}

// Wrap breaks a line into lines of at most w cells, preferring spaces.
func Wrap(l Line, w int) []Line {
	if w <= 0 || l.Width() <= w {
		return []Line{l}
	}
	type cell struct {
		g  string
		st tcell.Style
		w  int
	}
	var cells []cell
	for _, s := range l {
		g := uniseg.NewGraphemes(s.Text)
		for g.Next() {
			cells = append(cells, cell{g.Str(), s.Style, g.Width()})
		}
	}
	var out []Line
	for len(cells) > 0 {
		used, cut := 0, 0
		for cut < len(cells) && used+cells[cut].w <= w {
			used += cells[cut].w
			cut++
		}
		if cut < len(cells) {
			// Break at the last space when there is one in the second half.
			for j := cut - 1; j > cut/2; j-- {
				if cells[j].g == " " {
					cut = j + 1
					break
				}
			}
		}
		if cut == 0 {
			cut = 1
		}
		var line Line
		for _, c := range cells[:cut] {
			if n := len(line); n > 0 && line[n-1].Style == c.st {
				line[n-1].Text += c.g
			} else {
				line = append(line, Span{c.g, c.st})
			}
		}
		out = append(out, line)
		cells = cells[cut:]
	}
	return out
}
