# Tesserae

Universal, fully configurable **TUI dashboards and control panels**, built and edited inside the terminal.

Tesserae tails logs, runs commands and polls HTTP endpoints. **Rules** parse that text into **variables**, and **widgets** show the variables at fixed places on the screen. Only the cells that change are redrawn, so numbers update in place and nothing jumps around. Start it with `--edit` to build the dashboard with the mouse: drag a rectangle anywhere to add a widget, right-click anything for its context menu.

```
╔══════════════════════════════════════════════════════════════════════════════════════════════╗
║ === [ GENESIS DAEMON CONTROL ] ===  │  UPTIME 00:12:31  │  API online  │  state GENERATING    ║
╚══════════════════════════════════════════════════════════════════════════════════════════════╝
╭─ [>] INFERENCE ENGINE (llama.cpp) ─────────────╮╭──── tokens / s ────╮╭─ generation speed ─────╮
│ Prompt     100% ████████████████   987.13 t/s  ││  ▀▀█ ▀▀█   ▀▀█     ││ now 23.31 avg3s 23.29  │
│ Generation    735 ->    23.31 t/s              ││  █▀▀  ▀█    ▀█     ││          ▃▅▆▇██████████│
│ Context    15858 / 131072 (12.1%)              ││  ▀▀▀ ▀▀▀ ▀ ▀▀▀     ││ ·········██████████████│
╰────────────────────────────────────────────────╯╰────────────────────╯╰────────────────────────╯
```

## Quick start

```bash
go build -o tesserae .

./tesserae configs/examples/demo.toml            # self-contained demo control panel (bash only)
./tesserae configs/examples/system.toml          # CPU / memory / disk / network / journal from /proc
./tesserae configs/examples/ollama.toml          # Ollama + llama.cpp + AMD GPU (port of od.zsh)

./tesserae --edit my.toml                        # build a new dashboard from scratch
./tesserae --probe 3s configs/examples/ollama.toml   # run sources headless, print every variable
./tesserae --check my.toml                       # validate
```

Without a file argument, Tesserae uses `./tesserae.toml` or `~/.config/tesserae/default.toml`.
In view mode the file is **hot-reloaded** when it changes on disk.

## How it works

```
 sources ──text──▶ rules ──▶ variables / event streams ──▶ widgets (templates)
 tail  exec          regex     {tg} {gpu.temp}  events       text gauge spark
 stream http         json/kv   history (sparklines)          bignum log box button
```

* A **source** produces text:
  * `tail` follows a file, safe across rotation and truncation;
  * `exec` runs a command every interval;
  * `stream` reads a long-running command line by line, e.g. `journalctl -f`;
  * `http` polls a URL.
* **Rules** are regexes applied to every line, or to the whole output. Named groups `(?P<name>…)` become variables. A rule can also `set` extra variables from templates, push an **event** to a stream, increment a **counter**, and `stop` the rule chain (the awk `else if` pattern).
* `format = "json"` flattens JSON into `prefix.a.b.0.c` variables. When a key disappears from the response, its variable is deleted too. `format = "kv"` parses `key=value` / `key: value` lines.
* **Widgets** are rectangles on a free canvas. Their text is a **template** that references variables.
* Every source also publishes `<name>.status` (`ok` / `error` / `waiting`), `<name>.error` and `<name>.lines`. When a command fails, its variables keep their last good value.

## Config reference

```toml
name    = "My panel"
refresh = "1s"          # redraw interval (data arrives asynchronously)
shell   = ""            # shell for commands; default bash, else sh
units   = "cells"       # unit for widgets created in edit mode: cells | percent

[theme]
preset = "tesserae"     # tesserae dracula nord gruvbox tokyonight catppuccin terminal
border = "rounded"      # rounded sharp double thick ascii dashed none
[theme.colors]          # optional overrides of palette roles
accent = "#ff8800"      # roles: bg fg muted primary secondary accent ok warn crit border title select panel

[vars]                  # initial values shown until a source sets them
state = "waiting"

[[source]]
name     = "log"
type     = "tail"       # tail | exec | stream | http
path     = "/var/log/app.log"
backfill = 300          # tail: replay existing lines at start (0 = 300, -1 = none)
# command = "…"         # exec / stream
# url = "…"  method = "GET"  body = "…"  headers = { Authorization = "Bearer …" }   # http
# interval = "1s"  timeout = "5s"                                                  # exec / http
# split = "lines"       # lines | whole  (tail/stream: lines, exec/http: whole)
# format = "text"       # text | json | kv
# prefix = ""           # json/kv variable prefix; default = source name, "-" = none
# var = "uptime"        # store the whole trimmed output (or each line) in a variable

[[source.rule]]
match = 'n_gen =\s+(?P<n_gen>\d+), tg =\s+(?P<tg>[\d.]+)'
set   = { state = "generating" }       # templates; {1} {name} are captures
event = "[ok]generated[-] {n_gen} tokens"
stream = "events"                      # default "events"
count = "gen_lines"                    # variable incremented on every match
stop  = true                           # skip later rules for this line

[[widget]]
id     = "speed"
type   = "gauge"        # text | gauge | spark | bignum | log | box | button
x = 0                   # cells; negative = from the right/bottom edge
y = "50%+1"             # percent of the screen with an optional cell offset
w = "50%"               # size: cells, percent, or 0 / -N = stretch to the edge (minus N)
h = 3
title = "Generation {tg|%.1f} t/s"     # titles are templates too
border = ""             # "" = theme default
var = "tg"              # gauge / spark / bignum value
min = 0.0
max = 40.0
thresholds = [10.0, 15.0]              # ok → warn → crit
invert = true           # low values are bad
text = "{tg|%.1f} t/s"  # content / label template
```

Widget-specific keys:

| type | keys |
|------|------|
| `text`, `box` | `text`, `align` (left/center/right), `valign` (top/middle/bottom), `wrap` |
| `gauge` | `var`, `min`, `max` (default 100), `thresholds`, `invert`, `text` (label). It turns vertical automatically when the box is tall and narrow. |
| `spark` | `var`, `min`/`max` (auto when unset), `thresholds`, `invert`, `text` (label row). Multi-row bars with 1/8-cell resolution, one sample per refresh tick. |
| `bignum` | `text` rendered in a 3-row block font, optional `var` + `thresholds` for color |
| `log` | `stream` (`events`, `actions`, any rule stream, or `source:<name>` for raw lines), `filter` (regex; `!regex` excludes), `text` line format (`{time}`, `{line}`), `max_lines`, `wrap` |
| `button` | `text`, `action` (shell command, templates expand), `key` (hotkey), `confirm`. Output goes to `{<id>.output}` / `{<id>.status}` and the `actions` stream. |
| all | `title`, `title_align`, `border`, `fg`, `bg`, `border_fg`, `title_fg`, `bold`, `group`, `locked` |

### Templates

`{name}` inserts a variable and `{name|filter|filter:arg}` transforms it. `{{` and `}}` are literal braces, and shell `${VAR}` is always left alone. Missing variables render as `—`.

| filter | example | result |
|--------|---------|--------|
| printf verb | `{tg\|%6.2f}` | ` 14.93` (keeps the width even for non-numbers) |
| pad / align | `{name\|<12}` `{n\|>6}` `{x\|^9}` | fixed-width columns |
| `default:x` | `{model\|default:none}` | fallback when missing or empty |
| `thr:a:b[:inv]` | `{temp\|thr:75:90}` | colored ok / warn / crit |
| `style:v=color:…:*=color` | `{status\|style:200=ok:*=crit}` | color by value |
| `map:v=text:*=text` | `{st\|map:ok=online:*=down}` | translate values |
| `mul` `div` `add` `sub` `round` | `{used\|div:total\|mul:100\|%.1f}` | math; args may be variable names |
| `bytes` `si` `dur` | `{size\|bytes}` `{n\|si}` `{secs\|dur}` | 1.5 GiB, 12.3k, 3h04m |
| `bar:w:max` | `{load\|bar:10:100}` | inline bar `███▊······` |
| `spark:w` | `{tg\|spark:20}` | inline sparkline from history |
| `trunc:n` `upper` `lower` `trim` `replace:a:b` | | |

Style markup: `[fg]`, `[fg:bg]`, `[fg:bg:attrs]`, `[::b]`. `[-]` pops one level, `[/]` resets, and `[[` is a literal `[`. A color is a palette role, `#rrggbb`, a number 0-255 or a color name. Attributes: `b` bold, `d` dim, `i` italic, `u` underline, `r` reverse, `s` strike. Values coming from logs are escaped, so they can never inject styles. Text like `[GIN]` that is not a valid tag prints as-is.

Built-in variables: `sys.time`, `sys.date`, `sys.datetime`, `sys.epoch`, `sys.uptime`, `sys.uptime_s`, `sys.host`, `sys.user`.

## Edit mode

`tesserae --edit file.toml` (or `Ctrl+E` while running). The dashboard keeps running live while you edit, so every change shows up with real data.

| action | how |
|--------|-----|
| context menu | **right-click** a widget (edit, select, group, lock, move, resize, copy, duplicate, remove, arrange, align, units…) or empty canvas (add, paste, select all, sources, variables, theme, undo, save…) |
| add a widget | **drag a rectangle on empty space**, like selecting text, then pick a type. Or `a` / right-click → Add. |
| select | click; right-click another widget → **Add to selection** (works in every terminal); `Ctrl`/`Alt`+click toggles; clicking a grouped widget selects its group; area menu → "Select widgets inside" |
| draw over widgets | `b` or menu → **Draw area…**: the next drag draws a rectangle even when it starts on a widget, which helps on full-screen dashboards |
| move | drag the body or title of a widget, arrows (`Ctrl` = 5 cells), or `m` / Move (follows the pointer until you click) |
| resize | drag the right / bottom / left edge or a corner of a selected widget, `Shift`+arrows, or `r` / Resize |
| properties | double-click, `Enter`, or menu → Edit. The form applies live, `Esc` reverts. |
| data | in the widget form, **Quick data source** runs a command for this widget. `s` opens all sources and their rules, with a live **regex tester** over recent lines. `v` opens the live variable inspector. `Ctrl+Space` in any field picks a variable. |
| other | `Ctrl+Z`/`Ctrl+Y` undo/redo, `Ctrl+C`/`V`/`X`/`D` clipboard, `g`/`G` group/ungroup, `l` lock, `[` `]` z-order, `#` grid, `t` dashboard & theme (presets preview live), `Ctrl+S` save, `q` quit |

Moving and resizing keep each coordinate's unit: percentages stay percentages, edge-anchored values stay anchored. Locked widgets cannot be moved, resized or removed. Leaving edit mode with `Ctrl+E` keeps unsaved changes: `Ctrl+S` still saves, quitting asks first, and hot-reload is paused. Saving rewrites the TOML in a stable, readable layout. The first save of a session keeps the previous file as `file.toml.bak`, and a config that would not parse is never written.

## View mode keys

`q` / `Esc` quit · `F5` reload · `?` help · `Ctrl+E` edit mode · click a button or press its `key`.

## Development

```bash
go test -race ./...
```

* `internal/app/app_test.go` drives the real UI on a tcell simulation screen:
  * rubber-band → add widget → live title → move → resize → right-click lock → undo → duplicate → group → save;
  * quick data source;
  * context-menu multi-select + group;
  * draw area over widgets;
  * unsaved-changes prompt.
* `internal/data/ollama_test.go` replays `log.md` through the example rules and checks the values `od.zsh` extracts.

| package | role |
|---------|------|
| `config` | schema, `Dim` units, defaults, validation, readable TOML encoder |
| `data` | variable store with history and streams, rule processor, tail/exec/stream/http engine |
| `tmpl` | `{var\|filter}` templates |
| `render` | themes, markup, clipped drawing surface, borders, big font |
| `elem` | widget renderers |
| `ui` | popup toolkit: context menus with submenus, forms, lists, dialogs |
| `app` | main loop, view mode, editor, property forms |
