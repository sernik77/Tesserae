package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestDim(t *testing.T) {
	cases := []struct {
		pos, size string
		extent    int
		x, w      int
	}{
		{"0", "20", 100, 0, 20},
		{"-10", "5", 100, 90, 5},
		{"50%", "50%", 101, 51, 50},
		{"50%+2", "-2", 100, 52, 46},
		{"10", "0", 100, 10, 90},
		{"25%", "25%", 10, 3, 2},
	}
	for _, c := range cases {
		p, err := ParseDim(c.pos)
		if err != nil {
			t.Fatal(err)
		}
		s, err := ParseDim(c.size)
		if err != nil {
			t.Fatal(err)
		}
		x, w := ResolveSpan(p, s, c.extent)
		if x != c.x || w != c.w {
			t.Errorf("%s/%s@%d = %d,%d want %d,%d", c.pos, c.size, c.extent, x, w, c.x, c.w)
		}
		// Rebase must reproduce the same cells and keep the unit kind.
		p2, s2 := p.Rebase(x, c.extent), s.RebaseSize(p.Rebase(x, c.extent), x, w, c.extent)
		if p2.IsPct != p.IsPct || s2.IsPct != s.IsPct {
			t.Errorf("%s/%s: unit kind changed", c.pos, c.size)
		}
		if x2, w2 := ResolveSpan(p2, s2, c.extent); x2 != x || w2 != w {
			t.Errorf("%s/%s rebased to %s/%s → %d,%d", c.pos, c.size, p2, s2, x2, w2)
		}
		if back, _ := ParseDim(p.String()); back != p {
			t.Errorf("String round trip %s → %s", c.pos, p)
		}
	}
}

// TestRoundTrip catches fields silently dropped by the encoder.
func TestRoundTrip(t *testing.T) {
	files, _ := filepath.Glob("../../configs/examples/*.toml")
	if len(files) == 0 {
		t.Skip("no examples")
	}
	for _, f := range files {
		a, err := Load(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		b, err := Parse(Encode(a))
		if err != nil {
			t.Fatalf("%s: re-parse: %v\n%s", f, err, Encode(a))
		}
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: round trip differs", f)
		}
	}
}

func TestQuote(t *testing.T) {
	d := New("q")
	d.Elems = []Element{{ID: "a", Type: "text", Text: "line '1'\nline \"2\" \\d+", Title: `regex \d+ "x"`, Action: "a'''b\nc"}}
	b, err := Parse(Encode(d))
	if err != nil {
		t.Fatal(err)
	}
	if b.Elems[0].Text != d.Elems[0].Text || b.Elems[0].Title != d.Elems[0].Title || b.Elems[0].Action != d.Elems[0].Action {
		t.Fatalf("quote round trip failed: %q %q %q", b.Elems[0].Text, b.Elems[0].Title, b.Elems[0].Action)
	}
}
