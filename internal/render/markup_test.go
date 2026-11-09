package render

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/silverx/tesserae/internal/config"
)

func TestMarkup(t *testing.T) {
	th := NewTheme(config.Theme{Preset: "dracula"})
	base := th.Base()
	lines := Markup("[GIN] a[ok]b[::b]c[-]d[-]e[[x]\nz", base, th)
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	if got := lines[0].Plain(); got != "[GIN] abcde[x]" {
		t.Fatalf("plain = %q", got)
	}
	style := func(ch string) tcell.Style {
		for _, s := range lines[0] {
			if s.Text == ch {
				return s.Style
			}
		}
		t.Fatalf("span %q missing", ch)
		return base
	}
	fgB, _, _ := style("b").Decompose()
	if fgB != th.C("ok") {
		t.Error("b should be ok-colored")
	}
	_, _, attrs := style("c").Decompose()
	if attrs&tcell.AttrBold == 0 {
		t.Error("c should be bold")
	}
	fgD, _, attrsD := style("d").Decompose()
	if fgD != th.C("ok") || attrsD&tcell.AttrBold != 0 {
		t.Error("[-] should pop one level back to plain ok")
	}
	if style("e[x]") != base {
		t.Error("second [-] should return to base")
	}
}
