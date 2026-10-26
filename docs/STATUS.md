# Status

Update this file at the end of every session: tick what's done, set the next step, and log decisions.

## Current milestone: M1 Terminal core

Goal: PTYs, the `tuios.dev/vt` adapter, compositor, splits, tabs, shell panes, copy mode, mouse and clipboard, hook socket, state badges, persistence and resume. Covers AG-1 to AG-5, AG-7, AG-8 (interface), AG-9, PR-1 to PR-4, UX-4 to UX-6. It builds on the spike findings below.

Gate: six busy Claude panes stay smooth at 60 fps, and a reboot brings back the same layout with every session resumed.

M1 ships as four plans, one PR each.

### M1a Panes (done, PR #7)

Live shell and `claude` panes in splits, with focus, resize, zoom, swap and close, full key and paste forwarding, and a replay bench. Covers AG-1, AG-2, AG-3, AG-7.

- [x] VT adapter on `tuios.dev/vt`
- [x] Key encoder and paste
- [x] PTY processes and pane environment
- [x] Layout state, actions and geometry
- [x] Live panes with row cache and sync hold
- [x] Compositor
- [x] Input routing
- [x] Pane commands and lifecycle
- [x] Replay bench and gate

Gate: the app hosts shell and `claude` panes in splits, and the replay bench shows six busy Claude panes inside the frame budget.

#### M1a gate results

Gate: six-pane bench p99 <= 8 ms and idle CPU <= 1 % of one core. **Both met.** The bench gate rests on runs a, b and c; in b and c every pane's window lies in a busy stretch, and a is the spike's gate configuration (see below); the mixed run is supplementary.

Bench: the real `App` with replay panes on a virtual clock at 60 FPS, 240x64, median of 3 runs, feed + update + view + present per drawn frame (`go test -tags bench ./internal/ui -run 'TestBench$'` with `MUXREVEAL_BENCH_RECORDINGS`, `_START`, `_LENGTH`, `_STAGGER`). Pane i replays [start + i x stagger, + length). Each row is the run whose p99 is the median of the 3. The component p99s are per-component and do not sum to the frame p99.

| Run | Recording | Start | Length | Stagger |
| --- | --- | --- | --- | --- |
| a | spike `pane-2.rec` (its gate window, like-for-like with the spike) | 91 s | 30 s | 5 s |
| b | new `p3.rec` | 72 s | 7 s | 0.5 s |
| c | new `p2.rec` | 45 s | 4 s | 0.5 s |

| Run | Panes | Frames | Bytes fed | p50 | p95 | p99 | max | CPU % | feed / update+view / present p99 | GCs |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| a | 2 | 238 | 386,215 | 2.38 ms | 4.86 ms | 5.83 ms | 6.11 ms | 2.3 | 0.11 / 1.31 / 5.10 ms | 6 |
| a | 4 | 420 | 781,212 | 2.32 ms | 4.67 ms | 5.58 ms | 7.66 ms | 3.8 | 0.13 / 0.94 / 4.45 ms | 8 |
| a | 6 | 593 | 1,127,049 | 2.35 ms | 4.84 ms | **6.06 ms** | 7.89 ms | 5.6 | 0.18 / 1.14 / 4.64 ms | 13 |
| b | 2 | 61 | 111,141 | 2.35 ms | 2.95 ms | 4.59 ms | 4.59 ms | 2.1 | 0.07 / 0.69 / 3.93 ms | 1 |
| b | 4 | 104 | 225,640 | 2.19 ms | 2.83 ms | 4.59 ms | 5.62 ms | 3.5 | 0.09 / 0.72 / 3.86 ms | 2 |
| b | 6 | 128 | 330,780 | 2.37 ms | 4.74 ms | **5.47 ms** | 5.50 ms | 4.9 | 0.13 / 0.99 / 4.37 ms | 3 |
| c | 2 | 44 | 91,262 | 3.24 ms | 5.12 ms | 5.68 ms | 5.68 ms | 3.8 | 0.18 / 1.40 / 5.08 ms | 1 |
| c | 4 | 80 | 188,627 | 2.21 ms | 4.89 ms | 5.67 ms | 5.67 ms | 6.0 | 0.27 / 1.10 / 4.30 ms | 1 |
| c | 6 | 107 | 284,741 | 2.55 ms | 5.56 ms | **6.51 ms** | 6.74 ms | 8.5 | 0.24 / 1.21 / 5.00 ms | 2 |

Busy rule (the spike's): a second is busy when its bytes exceed 25 % of that recording's peak per-second rate. By it `p2.rec` (peak 14,633 B/s at 49 s) is busy 45-51 s (7 s) and `p3.rec` (peak 13,582 B/s at 42 s) is busy 36-43 s (8 s) and 72-82 s (11 s), so Claude's text answers are short bursts and runs b (pane 6 at 74.5-81.5 s) and c (pane 6 at 47.5-51.5 s) are short truecolor windows with every pane inside a busy stretch. `pane-2.rec` (peak 12,591 B/s at 134 s) is busy 93-135 s and 140-145 s. Run a is the spike's gate configuration: the sustained 30 s reply, in 256 colour (no truecolor SGRs), with windows [91 + 5i, 121 + 5i) s. Two exceptions: pane 0 starts with a 91-93 s spinner lead-in, and panes 4 and 5 cross the quiet 136-140 s. Run a is like-for-like with the spike's gate (593 frames, 1,127,049 bytes at six panes; spike p99 5.04 ms on the headless model, 6.06 ms here through the real `App`). Present is again the cost centre. GC pause totals stay under 1 ms per run.

Supplementary, mixed (partly idle: recordings `p2`, `p3`, `pane-2` in turn, start 37 s, length 10 s, stagger 0.5 s, the pooled-busiest window; some panes sit at a spinner or idle in it): 2 panes 94 frames, p99 7.01 ms; 4 panes 208 frames, p99 4.89 ms; 6 panes 270 frames, 215,690 bytes, p99 5.52 ms, CPU 7.4 %.

Caveat (a one-off scratch measurement, not a gate run): the bench builds one `View` per 60 FPS step, while the app calls `View` after every message. On run a's workload (six panes, 240x64) update+view p99 was 1.18 ms with one `View` per step and 2.08 ms with a `View` per dirty message, which would put the six-pane frame p99 near 7 ms, still under 8 ms.

Idle: six shell panes each running `sleep 3600`, 240x64 in a private tmux, 30 s after a 5 s warm-up, `/proc/<pid>/stat` utime+stime: **0.30 %** of one core.

Inputs: `p2.rec` (136 s, file size 94,552 bytes) and `p3.rec` (117 s, file size 204,497 bytes) are new recordings of two live `claude` panes (Claude Code 2.1.310, no `TMUX` in its environment, text-only prompts), recorded at 79x31 in a 159x33 tmux; both carry `38;2;` truecolor SGRs. `pane-2.rec` is the spike's recording.

Fidelity limits of the replay: the recordings are 79x31, but the bench lays the panes out in the 240x64 grid, so they replay at 77-83 columns by 30-31 rows (119-120 columns by 62 rows at 2 panes, 30-31 rows at 4 panes). Recorded resizes are skipped; none fall inside any window.

### M1b Tabs

Tabs, each with its own split layout, focus and zoom, with new, rename, close, jump and cycle keys; framed panes with titles and Nerd Font icons; a workspace bar on the top row with the tabs and a tray (clock, agents, git, load); a restyled status bar, which-key, palette, confirm and rename. Covers AG-2, AG-9 and UX-1.

- [x] Tabs in the core state
- [x] Framed panes with titles and icons
- [x] Workspace bar tabs, per-tab layout and status bar
- [ ] Tab commands, rename and close
- [ ] Grouped which-key, palette restyle and bench check
- [ ] Tray modules

Gate: hidden tabs keep their panes running at their own size, the six-pane bench p99 stays at most 8 ms and idle CPU at most 1 % of one core with the tray on.

### M1c Copy, mouse and clipboard

Part 1 (new files only) is done on its own branch and runs alongside M1b; Part 2 rebases onto `main` after M1b merges. Covers AG-4, UX-4, UX-5, UX-6.

### M1d Agents and persistence

After M1b and M1c. Saves the tabs and adds the AG-5 state badges to pane frames and tabs. Covers AG-5, AG-7 shim, AG-8, PR-1 to PR-4.

## Spike - VT (done)

Goal: prove that `tuios.dev/vt` + Bubble Tea v2 can host several busy Claude Code panes smoothly, before any real package depends on it. The spike code is kept on a local branch and is not on GitHub; M1 ports what it needs from it.

- [x] Read the `tuios.dev/vt` docs and source: list which features it exposes (scrollback, Kitty graphics, Kitty keyboard protocol, OSC 133, synchronized output, mouse modes) and which backend it uses (pure Go or ghostty)
- [x] Open two PTYs with `creack/pty`, each running `claude`, feeding a `tuios.dev/vt` emulator per pane
- [x] Render both side by side with Bubble Tea v2, with resize handling and event-driven redraws (no fixed tick). On a window resize, a same-size resize is skipped and a resized pane re-renders at once, so an idle pane keeps its screen (tested on both backends).
- [x] Forward keys to the focused pane, including Kitty keyboard protocol and bracketed paste (checked live on tuios, where Claude turns on Kitty flags 5 and modifyOtherKeys 2 at startup: Left went through tuios's `EncodeKeyCSIu`, letters and Backspace fell through to the legacy encoder; bracketed paste checked live; the legacy encoder's cursor, function-key and modifier paths are unit-tested only; a pass through a Kitty-protocol terminal is in Later)
- [x] Measure frame time and CPU with both panes busy (e.g. both generating long output), then with four and six panes; also idle CPU
- [x] Run the same harness with `charmbracelet/x/vt` for comparison
- [x] Write the findings below: API fit, missing features, numbers, rendering bugs seen, recommendation

Gate: a go/no-go on `tuios.dev/vt` vs `charmbracelet/x/vt`, with numbers.

### Findings

**Result: GO on `tuios.dev/vt` v0.3.1 (pure-Go backend).** It passes every gate line, and `charmbracelet/x/vt` is no faster while missing P0 features.

#### Gate, line by line

| Gate line | Measured (6 panes) | Result |
| --- | --- | --- |
| tuios builds clean with `GOTOOLCHAIN=go1.26.0` | `go build ./...` and `go vet ./...` clean on go1.26.0 | pass |
| Bench median p99 frame cost <= 8 ms | tuios 5.04 ms (x/vt 4.69 ms) | pass |
| Interactive CPU <= 25 % of one core | tuios 10.2 % (x/vt 10.4 %) | pass |
| Fidelity sweep finds no blocking rendering bug | 0 mismatches at 3249 checkpoints (tuios descends from x/vt, so this cannot catch a bug they share); both final screens match tmux (text only); live captures clean | pass |
| Pick x/vt if tuios fails build or fidelity, or is > 2x slower with no correctness edge | tuios / x/vt: p99 1.07x, bench CPU 1.07x, interactive CPU 0.98x; tuios has the correctness edge (Kitty keyboard, sync output Claude uses, OSC 52, OSC 133, Kitty graphics) | does not apply |
| Idle CPU <= 1 % of one core (non-blocking) | tuios 0.35 %, x/vt 0.31 % | met |

#### Machine

- AMD Ryzen 9 3950X, 16 cores / 32 threads, 31 GiB; Arch Linux, kernel 7.2.11.
- go1.27.3; the toolchain floor was checked with go1.26.0.
- tmux 3.8 for the interactive runs and the reference screens; sessions started with `-f /dev/null`.

#### Workload and cut

- Recordings from two live `claude` panes (v2.1.310, Opus 5.5, 79x31), recorded on tuios. `pane-2.rec` streams a real reply from 93.5 s to 145.9 s (peak 71.6 KB per 10 s). `pane-1.rec` is 7m54s of spinner only (about 4.6 KB per 10 s); Claude never replied in that pane.
- **Gate configuration:** `pane-2.rec` in all 6 panes, start 91 s, length 30 s, stagger 5 s. Pane i replays [91 + 5i, 121 + 5i) s, so the six windows span 91-146 s, inside the busy 10 s buckets (1m30s-2m20s, all above 25 % of the peak). The reply streams for 52.4 s, 2.6 s short of the 55 s six such windows need, so pane 0 starts with 2.5 s of pre-reply spinner; every other window is pure streaming.
- **Mixed configuration** (realistic, not gating): panes alternate `pane-1.rec` (spinner) and `pane-2.rec` (streaming), same start, length and stagger.
- Interactive runs replay the gate configuration with `-start 86s -warmup 5s -duration 30s`, so they measure the same recording windows as the bench (1,127,049 bytes fed in both).

#### Capabilities (spike caps probe)

| Capability | tuios (pure-Go) | x/vt |
|---|---|---|
| truecolor | yes `fg {1 2 3 255}` | yes `fg {1 2 3 255}` |
| wide chars | yes `width 2` | yes `width 2` |
| OSC 8 links | yes `url "http://a"` | yes `url "http://a"` |
| alt screen 1049 | yes `cell 0,0 = "m" after round trip` | yes `cell 0,0 = "m" after round trip` |
| scrollback | yes `26 lines` | yes `26 lines` |
| sync output 2026 | yes `"\x1b[?2026;2$y"` | no `"\x1b[?2026;0$y"` |
| bracketed paste 2004 | yes `"\x1b[?2004;2$y"` | yes `"\x1b[?2004;2$y"` |
| SGR mouse 1006 | yes `"\x1b[?1006;2$y"` | yes `"\x1b[?1006;2$y"` |
| focus reports 1004 | yes `"\x1b[?1004;2$y"` | yes `"\x1b[?1004;2$y"` |
| Kitty keyboard | yes `"\x1b[?1u"` | no `no reply` |
| Kitty graphics | yes `"\x1b_Gi=31;OK\x1b\\"` | no `no reply` |
| OSC 133 marks | yes `1 markers` | no `no API` |
| OSC 52 clipboard | yes `ClipboardSet("c", "hi")` | no `no API` |
| DA1 | yes `"\x1b[?62;22c"` | yes `"\x1b[?62;1;6;22c"` |
| XTVERSION | yes `"\x1bP>\|vt\x1b\\"` | no `no reply` |
| OSC 10/11 query | yes `"\x1b]11;rgb:0000/0000/0000\a"` | yes `"\x1b]11;rgb:0000/0000/0000\a"` |
| cursor shape DECSCUSR | yes `style 2 steady false` | yes `style 2 blink true` |

The published module builds the pure-Go emulator by default. Its ghostty backend (libghostty-vt through cgo) sits behind a `ghostty` build tag and was not used.

#### API fit for M1's `VT` interface

Mapped cleanly on both: write, resize, size, `CellAt` (both return ultraviolet `uv.Cell`), cursor and visibility, scrollback length, and replies read from the emulator as an `io.Reader`. The harness needs one adapter of about 70 lines (tuios) or 90 lines (x/vt).

Needed host code:

- **Legacy keys:** tuios `EncodePaneKey` returns "" unless Kitty flags or modifyOtherKeys are on, and its `EncodeKeyCSIu` returns "" for unmodified text, Enter, Tab and Backspace, so the adapter falls back to a legacy encoder ported from x/vt (MIT, about 140 lines). Claude pushes Kitty flags 5 and modifyOtherKeys 2 (`ESC[<u ESC[>5u ESC[>4;2m`) at 0.53 s in both recordings and keeps them on to the end (two pops of at most 0.15 s around the trust prompt, each re-pushed; last re-push at 473.29 s in `pane-1.rec`), so for Claude on tuios the port only sends text, Enter, Tab and Backspace.
- **The legacy port is not fit for M1 as is** (the spike's x/vt-derived legacy encoder): ctrl, shift or meta with an arrow, Home/End, PgUp/PgDn or a function key returns "" and sends nothing (alt only gets an ESC prefix), where xterm sends `CSI 1;<mod>X`; a key without text it has no entry for, such as F13 and up or Begin, sends `string(Code)`, a private-use rune (x/vt's own encoder has the same F13 bug); and a key with `IsRepeat` set skips every table, so a held arrow sends a private-use rune too.
- **Paste:** tuios offers `SanitizePaste` and `BracketedPasteEnabled`; the host wraps the text. x/vt's `Paste` wraps but does not sanitise.
- **Sync hold:** tuios's `IsSyncActive` and `SyncUpdate` expire on the wall clock, so the host tracks DEC 2026 from the mode callbacks and applies its own 1 s hold on an injectable clock.
- **Damage:** tuios has no damage API, so a dirty pane re-renders every row into a per-row string cache and only changed rows count. x/vt reports touched rows, but the adapter must treat an alt-screen switch or resize as all dirty and clear the list itself.
- **Callbacks:** tuios `SetCallbacks` replaces the whole set, so every feature that needs a callback (2026 tracking, OSC 52) shares one struct.

#### Numbers

Bench (gate configuration, `pane-2.rec` in every pane), **bench: feed+update+view+present**, median of 3 runs on a virtual clock at 60 FPS:

| Panes | Size | Frames/run | tuios p50 | tuios p99 | tuios CPU % | x/vt p50 | x/vt p99 | x/vt CPU % |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 2 | 159x32 | 238 | 1.34 ms | 2.53 ms | 1.0 | 0.95 ms | 2.39 ms | 0.9 |
| 4 | 159x64 | 420 | 1.46 ms | 3.77 ms | 2.6 | 1.46 ms | 3.22 ms | 2.2 |
| 6 | 239x64 | 593 | 1.99 ms | 5.04 ms | 4.5 | 2.02 ms | 4.69 ms | 4.2 |

Worst single frame in any 6-pane run: tuios 10.6 ms, x/vt 6.7 ms, both inside the 16.7 ms budget of 60 FPS.

Bench (mixed configuration, not gating), **bench: feed+update+view+present**, median of 3 runs:

| Panes | Frames/run | tuios p99 | tuios CPU % | x/vt p99 | x/vt CPU % |
| --- | --- | --- | --- | --- | --- |
| 2 | 290 | 1.95 ms | 0.9 | 1.96 ms | 0.8 |
| 4 | 555 | 3.20 ms | 3.0 | 3.15 ms | 2.6 |
| 6 | 796 | 4.69 ms | 5.8 | 3.89 ms | 5.2 |

Interactive (gate configuration, 6 panes, real time in a 240x64 tmux session, 30 s window, one run per backend), **interactive: update+view**; CPU is the whole spike process as a share of one core, including Bubble Tea's flush:

| Backend | Frames | p50 | p95 | p99 | max | CPU % |
| --- | --- | --- | --- | --- | --- | --- |
| tuios | 771 | 0.75 ms | 0.84 ms | 1.11 ms | 1.79 ms | 10.2 |
| x/vt | 771 | 0.70 ms | 0.84 ms | 0.98 ms | 1.20 ms | 10.4 |

Idle (CPU % of one core, 30 s window after 5 s warmup, one run per backend):

| Panes | tuios | x/vt |
| --- | --- | --- |
| 6 x `sleep 3600` | 0.35 | 0.31 |
| 2 x `claude` at the folder-trust prompt | 0.33 | 0.34 |

#### Where frame time goes

| Panes | tuios feed / render / present p99 | x/vt feed / render / present p99 | Present share of summed frame time (tuios, x/vt) |
| --- | --- | --- | --- |
| 2 | 0.15 / 0.33 / 2.04 ms | 0.30 / 0.15 / 1.90 ms | 85.5 %, 82.7 % |
| 4 | 0.15 / 0.38 / 3.30 ms | 0.28 / 0.21 / 2.83 ms | 90.1 %, 87.9 % |
| 6 | 0.16 / 0.41 / 4.53 ms | 0.43 / 0.25 / 4.14 ms | 92.0 %, 90.7 % |

The p99 columns are medians of 3 runs. The share is not derived from them: it is present's time summed over every drawn frame of a run divided by the summed frame time (feed + render + present) of that run, then the median of 3 runs, taken from the `Total` fields of the bench JSON (`bench.md` does not print them).

- **Present** (Bubble Tea's path: parse the whole view string into a cell buffer, diff it, write) takes 83-92 % of summed frame time in the gate configuration, and 85-95 % in the mixed one by the same method. Bubble Tea v2.1's `View.Content` is a string, so every changed frame is re-parsed in full. Inside present, timed on the 593 frames of a 6-pane gate run, the string parse (`NewStyledString` + `Draw`) is about 48 %, `TerminalRenderer.Render` (cell diff and escape generation) about 52 %, and `Flush` to `io.Discard` about 0 %.
- **Render** (pane rows to strings, compose): x/vt's touched rows save about 0.15 ms p99 at 6 panes. tuios re-renders all 31 rows of a dirty pane.
- **Feed** (emulator parse): tuios parses 2-3x faster than x/vt.
- The emulator choice moves less than 0.4 ms of a 5 ms p99. Bubble Tea's presenter is the cost centre, split about evenly between the string re-parse and the cell diff. The frames are byte-identical across backends, so the present p99 gap between tuios and x/vt (0.39 ms at 6 panes) is not presenter work; it is GC landing in present: a 6-pane bench run has 14 GC cycles on tuios and 6 on x/vt.
- Interactive CPU (10 %) is about twice bench CPU (4.5 %); the difference is not attributed, and the bench already includes present. Candidates: Bubble Tea calls `View` after every message (771 frame builds in the interactive window against 593 bench frames); the 6-pane grid of 79x31 panes is 239 columns wide in a 240-column terminal, so every frame line takes a `fit()` pass (this is also inside the interactive update+view numbers; the bench terminal is exactly 239 wide); Bubble Tea's 60 FPS ticker and the replay goroutines; and real writes to tmux instead of `io.Discard`.
- Caveats, identical for both backends: the bench builds the frame once per 60 FPS step while Bubble Tea calls `View` after every message, so absolute render cost is slightly under-counted; interactive frame timing skips frames rebuilt only by a hold expiry.
- Workload caveats: the gate workload is one recording (`pane-2.rec`) replayed six times 5 s apart, so the panes differ in offset but not in content, which may flatter the per-row cache. Every replay feeds bytes Claude produced while talking to tuios with `TMUX` set (2026 brackets, tuios's Kitty and XTVERSION replies, 256 colours; see Agent environment), so the x/vt performance and fidelity numbers describe x/vt on a tuios-shaped stream; x/vt's own unbracketed live stream was probed only at a trust-prompt redraw. The conclusion stands.

#### Fidelity sweep

- **Replay diff** (every cell's content, width, style and link plus the cursor, after every chunk): `pane-2.rec` 618 checkpoints, `pane-1.rec` 2631, none skipped (each Claude frame arrives in one chunk with its 2026 brackets). 0 mismatches in every class.
- **tmux reference** (79x31, status off): the final screens of both backends match tmux for both recordings.
- **What the diff can catch:** tuios.dev/vt's NOTICE says it started as a copy of `charmbracelet/x/vt` and that its parser is a modified copy of the `x/ansi` parser; both use ultraviolet cells and width tables. "0 mismatches at 3249 checkpoints" therefore cannot catch a bug tuios inherited. The independent evidence is the two tmux final-screen comparisons (text only) and the visual captures; references at more checkpoints are in Later.
- **Live captures:** the two backends' 6-pane interactive captures at the 30 s mark are byte-identical apart from the status label. No stale rows; wide characters (emoji in Claude's status line) keep the pane borders aligned. The live captures of the trust prompt, input box, streaming reply and spinner are clean.
- **Blocking rendering bugs: none** on either backend.
- **OSC 8 on x/vt:** x/vt keeps the trust prompt's "Security guide" link. Claude sends `ESC ] 8 ; id=zaxmda ; https://... BEL`, x/vt splits the payload into exactly three parts and keeps the link, the `-e` captures of both backends carry the identical OSC 8 sequence, and the replay diff shows no link mismatch.
- **Claude does not use synchronized output on x/vt** (live, no tokens: a trust-prompt redraw forced with Down then Up, never answered). At startup Claude sends XTVERSION, a Kitty keyboard query, OSC 7501 and DA1. tuios answers XTVERSION and Kitty, Claude then asks DECRQM 2026 and wraps each redraw in `?2026h`/`?2026l` (2 brackets). x/vt answers only DA1; Claude never asks about 2026 and sends the same redraw bytes unbracketed (0 brackets). With and without `TMUX` in Claude's environment (79x31, same Down then Up, full-stream counts): tuios 2 brackets after a DECRQM 2026 query, x/vt 0 brackets and no DECRQM 2026 query. So on x/vt the host cannot hold half-drawn frames. The replays cannot show this because they replay tuios-recorded bytes. Whether it visibly tears during streaming on x/vt is untested.
- **Agent environment:** the recordings, the idle `claude` runs and the first probes were made with `TMUX` inherited from the outer tmux, so Claude believed it ran inside tmux: it wrapped its OSC 11 query in tmux passthrough and drew in 256 colours (neither recording holds a truecolor SGR). Without `TMUX`, Claude sends OSC 11 plainly, draws in truecolor and, on tuios, also queries Kitty graphics; a tuios control run with `TMUX` set reproduces the old behaviour. The replays therefore exercised Claude's 256-colour output, not its truecolor output (truecolor itself is covered by the caps probe). The spike harness strips `TMUX` and `TMUX_PANE` from pane environments; MuxReveal is the agent's terminal, so M1 must not pass them to agent PTYs either.

#### Library issues found

tuios v0.3.1:

- `IsSyncActive` and `SyncUpdate` expire on the wall clock (`time.Now`), so the hold cannot run on a test or bench clock.
- No damage API.
- `EncodePaneKey` returns "" for legacy keys.
- `SetCallbacks` replaces the whole callback set.

x/vt v0.1.0:

- `Emulator.Close` writes the `closed` field that `Read` reads without a lock: a data race. The adapter closes the input pipe and waits for its reader before calling `Close`.
- Entering the alt screen clears it together with its touched-row list, and leaving it marks nothing, so a screen switch never shows up as touched rows. There is also no public way to clear the list; the adapter clears the aliased slice and treats a switch as all dirty.
- DECRQM 2026 answers 0 (not recognised). No Kitty keyboard, Kitty graphics, OSC 133, OSC 52 API or XTVERSION.
- `Paste` does not sanitise an embedded `ESC[201~`, so pasted text can end bracketed paste early.
- The key encoder sends nothing for shift+enter (Claude's newline key) and a private-use rune for unmapped keys such as F13 (the spike's legacy port inherits the F13 bug; see API fit).
- OSC 8 is dropped when the URL contains `;` (the handler requires exactly three `;`-separated parts). Claude's links do not hit this.

Spike harness bugs, fixed in the spike code (no effect on the numbers):

- `run` read `pty.Getsize` as (width, height), but it returns (rows, cols). Until Bubble Tea's first resize message the layout was transposed, and PTY children started at the transposed size too (the first probes' recordings say 64x239). Replay panes have a fixed size and the stats report 240x64. The spike's recorder requires an explicit pane size: the recording format stores only the initial size and has no resize records, which matters if M1 reuses it for VT fixtures.
- A window resize blanked every pane's row cache and nothing re-rendered it, so an idle or exited pane lost its screen until it wrote again. A same-size resize is skipped and a resized pane re-renders at once.
- Pane children inherited `TMUX` and `TMUX_PANE` (see Agent environment); the spike harness strips them.

#### Recommendation

Build M1's terminal layer on `tuios.dev/vt` v0.3.1 (pure Go) behind the `VT` interface, with the host code listed under API fit. Keep `charmbracelet/x/vt` as the fallback, but note that it is only performance-equal: on x/vt, Claude Code loses Kitty keyboard (shift+enter), OSC 52 and OSC 133, and it stops using synchronized output (with or without `TMUX` in its environment). Do not reuse the spike's legacy key port as is (see API fit).

## Next

M1b Tabs on `feat/m1b-tabs`. Then M1c Part 2 (Copy, mouse and clipboard) after M1b merges, then M1d Agents and persistence.

## Done

- Spike - VT: GO on `tuios.dev/vt` v0.3.1; findings in the Spike - VT section above. The spike code is on a local branch, not on GitHub.
- M0 Skeleton: app shell, config, Catppuccin themes, commands, which-key, palette and live theme picker (PRs #4, #5, #6)

## Decisions log

| Date | Decision | Why |
| --- | --- | --- |
| 2026-10-26 | Requirements, architecture and milestones settled in DESIGN.md | Initial design |
| 2026-10-26 | Go 1.26 on `tuios.dev/vt` instead of Rust on `ghostty-vt` | A proven Go emulator removes the reason for Rust; Go is the main language |
| 2026-10-26 | Use only the emulator from TUIOS, not `pkg/tuios` | `pkg/tuios` is pre-1.0 and can't host our own panes as tiles |
| 2026-10-26 | GO on `tuios.dev/vt` v0.3.1 (pure Go) for the terminal layer; `charmbracelet/x/vt` stays the fallback | Passes every gate line at 6 panes (bench p99 5.0 ms, interactive CPU 10.2 %, no rendering bugs, builds on go1.26.0); x/vt is no faster (4.7 ms, 10.4 %) and lacks Kitty keyboard and the sync output Claude Code uses |
| 2026-10-26 | Adopt the VT spike findings into DESIGN.md: per-row string cache, near-zero idle CPU, own sync hold, own legacy key encoder, pane PTY environment, x/vt fallback gaps, cell-level render path as the lever | Measured in the spike; M1 builds on them |
| 2026-10-26 | Target Go 1.27.3 (go.mod `go 1.27.3`) | Latest Go release; the local toolchain, golangci-lint and goimports are built with it; `tuios.dev/vt` needs only 1.26 |
| 2026-10-26 | Every key without the leader goes to the focused pane, `ctrl+c` included; `ctrl+space q` still runs `app.quit` | Terminal panes need every key, `ctrl+c` for the agent above all |
| 2026-10-26 | M1 ships as several plans and PRs, Panes first, with two workers running plans in parallel where their files do not overlap | Keeps each PR reviewable and uses both workers |
| 2026-10-26 | Tabs join M1 as M1b; copy/mouse/clipboard becomes M1c, agents and persistence M1d | Tabs change the state shape that persistence saves |

## Later

Ideas and requests that came up outside the current milestone go here.

- Build and run the spike on the operator's Mac; all numbers so far are from Linux.
- Kitty and Ghostty key pass by the operator: Kitty keyboard keys (shift+enter, ctrl combinations) through a real Kitty-protocol terminal, which tmux cannot carry.
- tuios's `ghostty` build-tag backend (libghostty-vt through cgo, built with Zig).
- Mouse forwarding to panes (not built in the spike).
- Live x/vt streaming check, only if x/vt ever becomes the backend: does Claude's unsynchronized output tear?
- A cell-level render path that skips Bubble Tea's string re-parse (about half of present), if M1's frame cost grows.
- Fidelity references outside the x/vt lineage: tmux `capture-pane -e` (styles included) at several checkpoints of each recording, not only the final screen.
- Pass the guest's cursor shape (DECSCUSR) through to the outer terminal; panes show the default cursor today.
- The palette overlay scrolls by a fixed `MaxRows` and does not use the height left below its top, so on terminals shorter than about 15 rows the last commands are cut off.
- On a terminal whose Backspace sends 0x08 (or Enter sends 0x0a) without the Kitty keyboard protocol, leader then Backspace swaps left (and leader then that Enter swaps down); Ghostty and Kitty are unaffected.
