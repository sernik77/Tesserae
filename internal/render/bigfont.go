package render

import "github.com/rivo/uniseg"

// BigHeight is the height of big-font glyphs in rows.
const BigHeight = 3

// bigGlyphs is a 3-row block font. Every row of a glyph has the same width.
var bigGlyphs = map[rune][3]string{
	'0': {"█▀█", "█ █", "▀▀▀"},
	'1': {"▀█ ", " █ ", "▀▀▀"},
	'2': {"▀▀█", "█▀▀", "▀▀▀"},
	'3': {"▀▀█", " ▀█", "▀▀▀"},
	'4': {"█ █", "▀▀█", "  ▀"},
	'5': {"█▀▀", "▀▀█", "▀▀▀"},
	'6': {"█▀▀", "█▀█", "▀▀▀"},
	'7': {"▀▀█", "  █", "  ▀"},
	'8': {"█▀█", "█▀█", "▀▀▀"},
	'9': {"█▀█", "▀▀█", "▀▀▀"},
	'.': {" ", " ", "▀"},
	',': {" ", " ", "▀"},
	':': {"▄", " ", "▀"},
	'-': {"   ", "▀▀▀", "   "},
	'+': {" ▄ ", "▀█▀", "   "},
	'%': {"▀ █", " █ ", "█ ▄"},
	'/': {"  █", " █ ", "█  "},
	' ': {"  ", "  ", "  "},
	'A': {"█▀█", "█▀█", "▀ ▀"},
	'B': {"█▀▄", "█▀▄", "▀▀ "},
	'C': {"█▀▀", "█  ", "▀▀▀"},
	'D': {"█▀▄", "█ █", "▀▀ "},
	'E': {"█▀▀", "█▀▀", "▀▀▀"},
	'F': {"█▀▀", "█▀▀", "▀  "},
	'G': {"█▀▀", "█ █", "▀▀▀"},
	'H': {"█ █", "█▀█", "▀ ▀"},
	'I': {"▀█▀", " █ ", "▀▀▀"},
	'K': {"█ █", "█▀▄", "▀ ▀"},
	'L': {"█  ", "█  ", "▀▀▀"},
	'M': {"█▄█", "█ █", "▀ ▀"},
	'N': {"█▀█", "█ █", "▀ ▀"},
	'O': {"█▀█", "█ █", "▀▀▀"},
	'P': {"█▀█", "█▀▀", "▀  "},
	'R': {"█▀█", "█▀▄", "▀ ▀"},
	'S': {"█▀▀", "▀▀█", "▀▀▀"},
	'T': {"▀█▀", " █ ", " ▀ "},
	'U': {"█ █", "█ █", "▀▀▀"},
	'V': {"█ █", "█ █", " ▀ "},
	'W': {"█ █", "█ █", "▀▀▀"},
	'X': {"█ █", "▄▀▄", "▀ ▀"},
	'Y': {"█ █", "▀█▀", " ▀ "},
	'Z': {"▀▀█", "▄▀ ", "▀▀▀"},
	'°': {"▄", " ", " "},
}

// BigWidth returns the display width of text rendered in the big font.
func BigWidth(l Line) int {
	w := 0
	for _, sp := range l {
		g := uniseg.NewGraphemes(sp.Text)
		for g.Next() {
			w += bigGlyphWidth(g.Str(), g.Width()) + 1
		}
	}
	return max(0, w-1)
}

func bigGlyph(g string) ([3]string, bool) {
	rs := []rune(g)
	if len(rs) != 1 {
		return [3]string{}, false
	}
	r := rs[0]
	if r >= 'a' && r <= 'z' {
		r -= 'a' - 'A'
	}
	gl, ok := bigGlyphs[r]
	return gl, ok
}

func bigGlyphWidth(g string, w int) int {
	if gl, ok := bigGlyph(g); ok {
		return uniseg.StringWidth(gl[0])
	}
	return w
}

// BigLine draws a styled line in the big font at (x, y). Characters missing
// from the font are drawn at normal size on the middle row.
func (s Surface) BigLine(x, y int, l Line) {
	cx := x
	for _, sp := range l {
		g := uniseg.NewGraphemes(sp.Text)
		for g.Next() {
			if gl, ok := bigGlyph(g.Str()); ok {
				for row := 0; row < BigHeight; row++ {
					s.Text(cx, y+row, gl[row], sp.Style, -1)
				}
				cx += uniseg.StringWidth(gl[0]) + 1
				continue
			}
			s.Put(cx, y+1, g.Str(), g.Width(), sp.Style)
			cx += g.Width() + 1
		}
	}
}
