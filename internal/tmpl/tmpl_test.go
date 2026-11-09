package tmpl

import "testing"

type hist struct{ MapLookup }

func (hist) History(string) []float64 { return []float64{1, 2, 3, 4} }

func TestRender(t *testing.T) {
	l := hist{MapLookup{"tg": "14.932", "load": "95", "name": "qwen", "b": "1536", "log": "[GIN] x", "used": "50", "slot": "200", "st": "ok"}}
	cases := map[string]string{
		"{tg}":                      "14.932",
		"{tg|%.1f} t/s":             "14.9 t/s",
		"[{name|>6}]":               "[  qwen]",
		"{name|<6}|":                "qwen  |",
		"{name|^8}":                 "  qwen  ",
		"{missing}":                 "—",
		"{missing|default:n/a}":     "n/a",
		"{load|thr:60:90}":          "[crit]95[-]",
		"{tg|thr:10:15:inv}":        "[warn]14.932[-]",
		"{b|bytes}":                 "1.5 KiB",
		"{log}":                     "[[GIN] x",
		"{used|div:slot|mul:100}%":  "25%",
		"{st|style:ok=ok:*=crit}":   "[ok]ok[-]",
		"{st|map:ok=online:*=down}": "online",
		"{load|bar:4:100}":          "███▊",
		"{tg|spark:4}":              "▁▃▆█",
		"{{literal}}":               "{literal}",
		"{name|upper|trunc:3}":      "QW…",
		"{missing|%.2f}":            "—",
		"echo ${HOME:-/tmp} {name}": "echo ${HOME:-/tmp} qwen",
	}
	for src, want := range cases {
		if got := Render(src, l); got != want {
			t.Errorf("Render(%q) = %q, want %q", src, got, want)
		}
	}
}

func TestThresholdColor(t *testing.T) {
	for _, c := range []struct {
		x    float64
		inv  bool
		want string
	}{{10, false, "ok"}, {60, false, "warn"}, {95, false, "crit"}, {5, true, "crit"}, {12, true, "warn"}, {20, true, "ok"}} {
		if got := ThresholdColor(c.x, []float64{60, 90}, false); !c.inv && got != c.want {
			t.Errorf("%v: got %s want %s", c.x, got, c.want)
		}
		if got := ThresholdColor(c.x, []float64{10, 15}, true); c.inv && got != c.want {
			t.Errorf("inv %v: got %s want %s", c.x, got, c.want)
		}
	}
}
