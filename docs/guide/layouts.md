# Layouts

Every tab has its own layout mode, and the mode decides where a new pane goes and what resize and swap do. This page covers the three modes, switching between them, the layout commands, and moving panes between tabs.

The keys here are the defaults. A changed `leader_key` or `[keys]` table changes them; see [Keys](keys.md) for the full list and [Configuration](config.md#remapping-keys) for how to remap. Opening panes, focus and zoom are in [Agents side by side](agents.md).

## The three modes

| Mode | What it is | A new pane |
| --- | --- | --- |
| `bsp` | Panes in a tree of splits; each split divides its space in two. | `ctrl+space s` splits the focused pane and puts the new one below it, `ctrl+space v` and `ctrl+space n` put it to the right. It takes half of the focused pane. |
| `master-stack` | A master area and a stack, laid out from the order of the panes. | Added at the end of the order. The first `master_count` panes are the masters and the rest are the stack. With the default `master_grid`, one master on the left and four or more panes, all the panes form an equal grid instead. |
| `scrolling` | Columns of panes; only the columns that fit whole are shown. | `ctrl+space v` and `ctrl+space n` add a column after the focused one. `ctrl+space s` adds the pane to the focused column, below the focused pane. |

A new tab starts in the mode `default_mode` names. In a scrolling tab with more columns than fit, the bottom borders of the panes at the edges show how many are hidden. Moving focus left or right scrolls the focused column into view.

## Switch the layout

- To cycle the tab's layout, press `ctrl+space space`. The order is BSP, master-stack, scrolling, then BSP again.
- To set a mode directly, open the command palette with `ctrl+space :` and run "Layout: BSP", "Layout: master-stack" or "Layout: scrolling". These have no key; their ids are `layout.set.bsp`, `layout.set.master-stack` and `layout.set.scrolling`, listed in [Keys](keys.md#commands-without-a-default-key) if you want to bind them.

Either way the notice is `layout: <mode>`, such as `layout: master-stack`, also when the tab already had that mode and nothing changed.

A switch rebuilds the tab from the order of its panes. Every pane and process stays and focus stays, but split ratios and column widths reset, and zoom and any preselection are cleared. In BSP the panes are split in, one after the other, by the `auto_scheme` setting.

## Settings

The `[layout]` section of [`config.example.toml`](../../config.example.toml) holds `default_mode`, `auto_scheme`, `master_ratio`, `master_position`, `master_count`, `master_grid` and `column_width`. The example explains each one and what it accepts.

## Choose where the next pane goes

In a BSP tab you can say which side of the focused pane the next pane opens on, before you open it. This is a preselection.

- To set it, press `ctrl+space ;`, then `h`, `j`, `k` or `l` for left, below, above or right.
- The focused pane's top border shows a tag with an arrow and `next`.
- To clear it, press `ctrl+space ; esc`.
- The next pane that opens in the tab, however it opens (a split, an agent, a pane moved in), goes on that side of the focused pane at half its space, and the preselection is cleared. Moving focus keeps it; a layout switch clears it.

In a tab that is not BSP, `ctrl+space ;` shows `preselection works in BSP layouts` at once. The palette commands "Preselect left", "Preselect down", "Preselect up", "Preselect right" and "Clear preselection" show the same notice there.

## Rotate, equalize and promote

| To | Press | In |
| --- | --- | --- |
| Turn the split around the focused pane from side by side to stacked, or back | `ctrl+space r` | BSP |
| Make the panes equal | `ctrl+space =` | all modes |
| Swap the focused pane with the first master, and focus the new first pane | `ctrl+space enter` | master-stack |

- Rotate keeps the split's ratio. A lone pane has nothing to rotate: `nothing to rotate`. In another mode the notice is `rotate works in BSP layouts`.
- Equalize does what suits the mode: in BSP the panes become equal along each axis, in master-stack the tab goes back to `master_ratio`, and in scrolling every column goes back to `column_width`. With fewer than two panes nothing changes.
- Promote is dwm's zoom: the focused pane swaps places with the first pane, or with the second when it is the first. In another mode the notice is `promote works in master-stack layouts`.
- Each of them ends zoom only when it runs: in a mode it works in, with two or more panes.

## Resize and swap in each mode

The keys are the same in every mode: `ctrl+space H`, `J`, `K`, `L` resize the focused pane, and `ctrl+space ctrl+h`, `ctrl+j`, `ctrl+k`, `ctrl+l` swap it with its neighbour. What they do depends on the mode.

| Mode | Resize | Swap |
| --- | --- | --- |
| `bsp` | Moves the nearest divider on that axis, in steps of 5 percent. With no divider on that axis nothing happens. | Exchanges two panes; the splits keep their shape and sizes, so the moved pane takes the size of the place it lands in. |
| `master-stack` | Moves the divider between masters and stack, and only on the axis that divides them: `H` and `L` with the masters on the left, right or in the center, `K` and `J` with them on top or bottom. The other axis does nothing. In the grid, resize shows no change. | The focused pane and its neighbour exchange places in the order. |
| `scrolling` | `H` and `L` narrow and widen the focused column. `J` and `K` do nothing. | `ctrl+space ctrl+h` and `ctrl+l` exchange with the pane at the same position in the next column, `ctrl+j` and `ctrl+k` within the column. |

In master-stack, `L` and `J` grow the left or top region, so with the masters on the right or at the bottom they shrink the masters; with `master_position = "center"` `L` grows the middle column.

A swap ends zoom, and focus stays on the moved pane. With no neighbour in that direction nothing happens and zoom stays.

## Move panes between tabs

A moved pane keeps its process, its terminal and its history, and only changes size. It becomes the focused pane of the tab it lands in, and lands as that tab's mode places a new pane. In a BSP tab it goes to the preselection, which is then cleared, otherwise beside the focused pane, split by `auto_scheme`.

| To | Press |
| --- | --- |
| Move the focused pane to tab 1 to 9 and go there | `ctrl+space > 1` to `ctrl+space > 9` |
| Send it to tab 1 to 9 and stay where you are | `ctrl+space . 1` to `ctrl+space . 9` |
| Break it out into a new tab of its own and go there | `ctrl+space !` |

- The number is the tab's position in the workspace bar. A number one past the last tab creates a new tab at the end, in the `default_mode` layout.
- A source tab left with no panes is removed, and the tabs after it shift down by one.
- Sending keeps the current tab active, unless that tab was the one removed; then the target becomes active.
- Copy mode and any selection on the moved pane end.

These do nothing and show a notice:

| Notice | When |
| --- | --- |
| `the pane is already in tab 2` | You move it to the tab it is already in. |
| `there is no tab 5` | The number is more than one past the last tab. |
| `the pane is alone in its tab` | You break out the only pane of a tab, or move it to a new tab at the end. |

Moving the only pane of a tab to an existing tab works, and the tab it leaves is removed.
