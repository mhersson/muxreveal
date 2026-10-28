<!-- Describe what changed and how to test it. -->

# Review drawer

`leader d` slides a **review drawer** in from the right over half the body and shows the focused pane's diff: an agent's *last turn*, or its branch against the base. See the [design notes](https://github.com/mhersson/muxreveal/blob/main/docs/DESIGN.md) for the keys; it replaces ~~the old layout pane~~. Press <kbd>esc</kbd> to hand the keyboard back.

> [!NOTE]
> The panes under the drawer keep their size, so no agent redraws when it opens or closes.

## What changed

- The ported tuios `diffview` draws the lines, with highlighting from the theme
- One watcher per worktree, shared by every agent in it
  - the turn gate skips unchanged snapshots
  - the agents rail re-probes on a branch switch
    - which fixes the stale `main` on the cards
- `leader n` picks a worktree for a new agent
  - ![The drawer in a list](./docs/drawer-small.png)
  - <details>
    <summary>Keys in the list</summary>

    Step with `,` and `.`; press <kbd>esc</kbd> to leave.

    </details>

### Checklist

- [x] `make check` passes
- [x] Goldens in Mocha and Latte
- [ ] The macOS CI step for the watcher fix

## How to test

1. Open three agents in master-stack: `leader n` three times
2. Run a prompt in the master, then press `leader d`
3. Step through the turns with `,` and `.`

```sh
make check
go test -tags bench ./internal/ui -run 'TestBench$' -v  # quiet machine only
```

```go
func (a *App) openReview(ctx context.Context, id core.PaneID) tea.Cmd {
	if r, ok := a.reviews[id]; ok {
		return a.focus(r.pane) // already open
	}
	return a.load(ctx, id)
}
```

| Gate                                                         | Limit | Measured |
| :----------------------------------------------------------- | ----: | -------: |
| Save to drawer (median)                                      | 300 ms |   187 ms |
| Six-pane bench p99 with the drawer open over a busy agent    |  8 ms |   7.1 ms |
| Scroll a 10,000-line diff, p99                               |  8 ms |   5.4 ms |
| Idle CPU, drawer open                                        |   1 % |    0.6 % |

> [!WARNING]
> Drawer keys never reach a covered pane; `leader h` goes back to the master.

> Reviewing is reading. The drawer should never make you wait for it.
>
> - the first rule of review
> - ![A quoted screenshot](https://example.com/shots/quote.png)
>
> <details open>
> <summary>Quoted notes</summary>
>
> Open by default, inside a quote.
>
> </details>

> [!TIP]
> Open the drawer before the agent finishes to watch the diff grow.

> [!IMPORTANT]
> Merging always needs the reviewed head.

> [!CAUTION]
> The drawer reads the worktree, so unsaved edits do not show.

![The drawer over two workers](https://github.com/user-attachments/assets/3f1c-drawer.png)

<details>
<summary>Bench output</summary>

six panes, run a: p50 2.75 ms, p95 5.76 ms, p99 7.03 ms;<br>
run b: p99 7.19 ms; run c: p99 6.12 ms

</details>

---

Closes [#18](https://github.com/mhersson/muxreveal/issues/18). Thanks to the tuios project for diffview[^1].

[^1]: Ported at commit `aaaa128`, MIT.
