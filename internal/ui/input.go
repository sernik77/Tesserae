package ui

import (
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// textBuf is an editable rune buffer with a cursor. Multi-line buffers keep
// '\n' runes; the view scrolls to keep the cursor visible.
type textBuf struct {
	r      []rune
	cur    int
	multi  bool
	scroll int // first visible column (single) or row (multi)
	hscrol int // first visible column in multi-line mode
}

func newBuf(s string, multi bool) *textBuf {
	r := []rune(s)
	return &textBuf{r: r, cur: len(r), multi: multi}
}

func (b *textBuf) String() string { return string(b.r) }

func (b *textBuf) set(s string) {
	b.r = []rune(s)
	b.cur = min(b.cur, len(b.r))
}

func (b *textBuf) insert(s string) {
	if !b.multi {
		s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", " ")
	}
	in := []rune(s)
	b.r = append(b.r[:b.cur], append(in, b.r[b.cur:]...)...)
	b.cur += len(in)
}

// lineBounds returns the start and end rune index of the line holding pos.
func (b *textBuf) lineBounds(pos int) (int, int) {
	s, e := pos, pos
	for s > 0 && b.r[s-1] != '\n' {
		s--
	}
	for e < len(b.r) && b.r[e] != '\n' {
		e++
	}
	return s, e
}

// rowCol returns the cursor row and column (in runes).
func (b *textBuf) rowCol() (int, int) {
	row, col := 0, 0
	for i := 0; i < b.cur; i++ {
		if b.r[i] == '\n' {
			row++
			col = 0
		} else {
			col++
		}
	}
	return row, col
}

func (b *textBuf) lines() []string {
	return strings.Split(string(b.r), "\n")
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// key applies an editing key. It returns false when the key is not an
// editing key (navigation between fields is handled by the caller).
func (b *textBuf) key(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyRune:
		b.insert(string(ev.Rune()))
	case tcell.KeyLeft:
		if ev.Modifiers()&tcell.ModCtrl != 0 {
			for b.cur > 0 && !isWord(b.r[b.cur-1]) {
				b.cur--
			}
			for b.cur > 0 && isWord(b.r[b.cur-1]) {
				b.cur--
			}
		} else if b.cur > 0 {
			b.cur--
		}
	case tcell.KeyRight:
		if ev.Modifiers()&tcell.ModCtrl != 0 {
			for b.cur < len(b.r) && !isWord(b.r[b.cur]) {
				b.cur++
			}
			for b.cur < len(b.r) && isWord(b.r[b.cur]) {
				b.cur++
			}
		} else if b.cur < len(b.r) {
			b.cur++
		}
	case tcell.KeyHome, tcell.KeyCtrlA:
		b.cur, _ = b.lineBounds(b.cur)
	case tcell.KeyEnd, tcell.KeyCtrlE:
		_, b.cur = b.lineBounds(b.cur)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if b.cur > 0 {
			b.r = append(b.r[:b.cur-1], b.r[b.cur:]...)
			b.cur--
		}
	case tcell.KeyDelete, tcell.KeyCtrlD:
		if b.cur < len(b.r) {
			b.r = append(b.r[:b.cur], b.r[b.cur+1:]...)
		}
	case tcell.KeyCtrlU:
		s, _ := b.lineBounds(b.cur)
		b.r = append(b.r[:s], b.r[b.cur:]...)
		b.cur = s
	case tcell.KeyCtrlK:
		_, e := b.lineBounds(b.cur)
		b.r = append(b.r[:b.cur], b.r[e:]...)
	case tcell.KeyCtrlW:
		s := b.cur
		for s > 0 && b.r[s-1] == ' ' {
			s--
		}
		for s > 0 && b.r[s-1] != ' ' && b.r[s-1] != '\n' {
			s--
		}
		b.r = append(b.r[:s], b.r[b.cur:]...)
		b.cur = s
	case tcell.KeyUp:
		if !b.multi {
			return false
		}
		s, _ := b.lineBounds(b.cur)
		if s == 0 {
			return false
		}
		col := b.cur - s
		ps, pe := b.lineBounds(s - 1)
		b.cur = min(ps+col, pe)
	case tcell.KeyDown:
		if !b.multi {
			return false
		}
		s, e := b.lineBounds(b.cur)
		if e >= len(b.r) {
			return false
		}
		col := b.cur - s
		ns, ne := b.lineBounds(e + 1)
		b.cur = min(ns+col, ne)
	case tcell.KeyEnter:
		if !b.multi {
			return false
		}
		b.insert("\n")
	default:
		return false
	}
	return true
}

// clickSingle places the cursor at visual column x of a single-line view.
func (b *textBuf) clickSingle(x int) {
	col := 0
	for i := b.scroll; i < len(b.r); i++ {
		w := runeWidth(b.r[i])
		if col+w > x {
			b.cur = i
			return
		}
		col += w
	}
	b.cur = len(b.r)
}

// clickMulti places the cursor at a visual row/column of a multi-line view.
func (b *textBuf) clickMulti(row, x int) {
	lines := b.lines()
	row += b.scroll
	if row >= len(lines) {
		b.cur = len(b.r)
		return
	}
	pos := 0
	for i := 0; i < row; i++ {
		pos += len([]rune(lines[i])) + 1
	}
	line := []rune(lines[row])
	col := 0
	for i := b.hscrol; i < len(line); i++ {
		w := runeWidth(line[i])
		if col+w > x {
			b.cur = pos + i
			return
		}
		col += w
	}
	b.cur = pos + len(line)
}

func runeWidth(r rune) int {
	w := uniseg.StringWidth(string(r))
	if w == 0 {
		return 1
	}
	return w
}

// fitScroll adjusts the horizontal scroll of a single-line view of width w and
// returns the visible text and the cursor's visual column.
func (b *textBuf) fitScroll(w int) (string, int) {
	if b.cur < b.scroll {
		b.scroll = b.cur
	}
	for {
		col := 0
		for i := b.scroll; i < b.cur; i++ {
			col += runeWidth(b.r[i])
		}
		if col < w || b.scroll >= b.cur {
			break
		}
		b.scroll++
	}
	var sb strings.Builder
	col, cx := 0, 0
	for i := b.scroll; i < len(b.r); i++ {
		if i == b.cur {
			cx = col
		}
		rw := runeWidth(b.r[i])
		if col+rw > w {
			break
		}
		sb.WriteRune(b.r[i])
		col += rw
	}
	if b.cur == len(b.r) {
		cx = col
	}
	return sb.String(), cx
}
