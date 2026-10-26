# Status

Update this file at the end of every session: tick what's done, set the next step, and log decisions.

## Current milestone: Spike — VT

Goal: prove that `tuios.dev/vt` + Bubble Tea v2 can host several busy Claude Code panes smoothly, before any real package depends on it. Work in `spikes/vt` only; this code may be thrown away.

- [ ] Read the `tuios.dev/vt` docs and source: list which features it exposes (scrollback, Kitty graphics, Kitty keyboard protocol, OSC 133, synchronized output, mouse modes) and which backend it uses (pure Go or ghostty)
- [ ] Open two PTYs with `creack/pty`, each running `claude`, feeding a `tuios.dev/vt` emulator per pane
- [ ] Render both side by side with Bubble Tea v2, with resize handling and event-driven redraws (no fixed tick)
- [ ] Forward keys to the focused pane, including Kitty keyboard protocol and bracketed paste
- [ ] Measure frame time and CPU with both panes busy (e.g. both generating long output), then with four and six panes; also idle CPU
- [ ] Run the same harness with `charmbracelet/x/vt` for comparison
- [ ] Write the findings below: API fit, missing features, numbers, rendering bugs seen, recommendation

Gate: a go/no-go on `tuios.dev/vt` vs `charmbracelet/x/vt`, with numbers.

### Findings

_Not started._

## Next

M0 Skeleton: module layout from DESIGN.md, config, theme engine with the four Catppuccin flavors, command palette, empty app shell (TH-1, TH-2, UX-1, UX-2).

## Done

_Nothing yet._

## Decisions log

| Date | Decision | Why |
| --- | --- | --- |
| 2026-10-26 | Requirements, architecture and milestones settled in DESIGN.md | Initial design |
| 2026-10-26 | Go 1.26 on `tuios.dev/vt` instead of Rust on `ghostty-vt` | A proven Go emulator removes the reason for Rust; Go is the main language |
| 2026-10-26 | Use only the emulator from TUIOS, not `pkg/tuios` | `pkg/tuios` is pre-1.0 and can't host our own panes as tiles |

## Later

Ideas and requests that came up outside the current milestone go here.
