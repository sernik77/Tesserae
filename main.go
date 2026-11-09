// Tesserae — universal, fully configurable TUI dashboards and control panels.
//
//	tesserae [file.toml]            run a dashboard
//	tesserae --edit [file.toml]     build / edit it in the TUI (creates the file if missing)
//	tesserae --check file.toml      validate a config
//	tesserae --probe 5s file.toml   run the sources headless and print all variables
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sernik77/tesserae/internal/app"
	"github.com/sernik77/tesserae/internal/config"
	"github.com/sernik77/tesserae/internal/data"
	"github.com/sernik77/tesserae/internal/render"
	"github.com/sernik77/tesserae/internal/tmpl"
)

const version = "1.0.0"

const usage = `Tesserae %s — universal TUI dashboards and control panels

Usage:
  tesserae [options] [dashboard.toml]

Options:
  -e, --edit          edit mode: right-click menus, drag to add/move/resize
                      (a missing file is created on first save)
      --check         validate the config and exit
      --probe DUR     run all sources for DUR (e.g. 3s) without a TUI and
                      print every variable — handy for writing parse rules
      --themes        list theme presets
  -v, --version       print version
  -h, --help          this help

Without a file, ./tesserae.toml and ~/.config/tesserae/default.toml are tried.
Inside the TUI press ? for key bindings.
`

func main() {
	fs := flag.NewFlagSet("tesserae", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, usage, version) }
	var edit, check, themes, ver bool
	var probe time.Duration
	fs.BoolVar(&edit, "edit", false, "")
	fs.BoolVar(&edit, "e", false, "")
	fs.BoolVar(&check, "check", false, "")
	fs.DurationVar(&probe, "probe", 0, "")
	fs.BoolVar(&themes, "themes", false, "")
	fs.BoolVar(&ver, "version", false, "")
	fs.BoolVar(&ver, "v", false, "")
	// Allow flags after the file name too: tesserae dash.toml --edit
	args := os.Args[1:]
	var files []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			if err == flag.ErrHelp {
				return
			}
			os.Exit(2)
		}
		args = fs.Args()
		if len(args) > 0 {
			files = append(files, args[0])
			args = args[1:]
		}
	}
	switch {
	case ver:
		fmt.Println("tesserae", version)
		return
	case themes:
		for _, n := range render.PresetNames() {
			fmt.Println(n)
		}
		return
	}
	if len(files) > 1 {
		fail("expected one dashboard file, got %d", len(files))
	}
	path := ""
	if len(files) == 1 {
		path = files[0]
	} else if path = findDefault(); path == "" {
		if !edit {
			fs.Usage()
			fail("no dashboard file given and none found")
		}
		path = "tesserae.toml"
	}

	switch {
	case check:
		d, err := config.Load(path)
		if err != nil {
			fail("%s: %v", path, err)
		}
		rules := 0
		for _, s := range d.Sources {
			rules += len(s.Rules)
		}
		fmt.Printf("%s: OK — %d sources, %d rules, %d widgets\n", path, len(d.Sources), rules, len(d.Elems))
		return
	case probe > 0:
		runProbe(path, probe)
		return
	}

	a, err := app.New(app.Options{Path: path, Edit: edit})
	if err != nil {
		fail("%s: %v", path, err)
	}
	if err := a.Run(); err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "tesserae: "+format+"\n", args...)
	os.Exit(1)
}

func findDefault() string {
	cands := []string{"tesserae.toml"}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(home, ".config", "tesserae", "default.toml"))
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func runProbe(path string, d time.Duration) {
	cfg, err := config.Load(path)
	if err != nil {
		fail("%s: %v", path, err)
	}
	store := data.NewStore()
	for k, v := range cfg.Vars {
		store.SetDefault(k, v)
	}
	eng := data.NewEngine(store, cfg.Shell)
	eng.Start(cfg.Sources)
	time.Sleep(d)
	eng.Stop()
	names := store.Names()
	width := 0
	for _, n := range names {
		width = max(width, len(n))
	}
	fmt.Printf("── variables after %s ──\n", d)
	for _, n := range names {
		v, _ := store.Get(n)
		fmt.Printf("%-*s  %s\n", width, n, strings.ReplaceAll(tmpl.Truncate(v, 100), "\n", "⏎"))
	}
	fmt.Println("── streams ──")
	th := render.NewTheme(cfg.Theme)
	for _, s := range store.StreamNames() {
		entries := store.Stream(s, 0)
		fmt.Printf("%s (%d entries)\n", s, len(entries))
		if !strings.HasPrefix(s, "source:") {
			for _, e := range entries[max(0, len(entries)-5):] {
				fmt.Println("   ", render.StripMarkup(e.Text, th))
			}
		}
	}
}
