# Tesserae — plan & status

## Architecture (v1.0, rewrite)

The first prototype placed tview TextViews on a fixed row/column grid, with
every widget grepping the whole log on its own every second. It could not
express free rectangles, so the rewrite is:

- **Free canvas**: widgets are rectangles (`x y w h`) in cells, percent
  (`50%+2`) or edge-anchored (`-3`, size `0` = stretch). Pure tcell rendering,
  no tview; tcell diffs cells so values change in place.
- **Shared data engine**: named sources (tail / exec / stream / http) →
  regex rules (named groups, set, event, count, stop) / json / kv →
  variable store with history + event streams.
- **Templates** with filters and safe color markup.
- **Editor** on top of the live dashboard: context menus, rubber-band add,
  drag move/resize, groups, locks, clipboard, align, z-order, units, undo/redo,
  live property forms, sources/rules editor with regex tester, variable
  inspector, theme editor, atomic save with backup.

## Done

- [x] Core engine, templates, 7 widget types, 7 theme presets
- [x] `--edit` mode with right-click context menus and all actions from the brief
      (select/deselect, group/ungroup, edit, lock/unlock, move, copy, remove, add)
- [x] Add flow: drag any rectangle on the empty canvas → type → property form
- [x] Per-widget quick data source (command + interval + regex) and full sources editor
- [x] `--check`, `--probe`, hot reload, buttons/actions with confirm
- [x] Examples: ollama (od.zsh port), system, demo
- [x] Tests: simulation-screen E2E, log replay, round trip, templates, dims, markup

## Ideas

- [ ] Multiple pages / tabs in one file
- [ ] Table widget with column templates
- [ ] Braille line chart for spark
- [ ] Snap-to-neighbour guides while dragging
