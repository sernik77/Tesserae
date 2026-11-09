package data

import (
	"bufio"
	"os"
	"testing"

	"github.com/sernik77/tesserae/internal/config"
)

// TestOllamaExampleReplay feeds the captured llama.cpp log through the rules
// of the example dashboard and checks the values od.zsh would have shown.
func TestOllamaExampleReplay(t *testing.T) {
	f, err := os.Open("../../log.md")
	if err != nil {
		t.Skip("log.md sample not present")
	}
	defer f.Close()
	cfg, err := config.Load("../../configs/examples/ollama.toml")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	p, err := NewProcessor(*cfg.Source("log"), store)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		p.Line(sc.Text(), false)
	}
	want := map[string]string{
		"n_gen":          "866",
		"tg":             "14.92",
		"tg3s":           "14.88",
		"prompt_eval_ms": "379.08",
		"eval_ms":        "59606.53",
		"eval_tps":       "14.91",
		"total_ms":       "59985.62",
		"http_code":      "404",
		"http_path":      "/models",
		"state":          "idle",
		"ctx_used":       "58839",
	}
	for k, v := range want {
		if got, _ := store.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if n := len(store.Stream("events", 0)); n == 0 {
		t.Error("no events produced")
	}
}
