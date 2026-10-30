package ui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// statusMark is a file's status letter and its colour.
func (d *drawCtx) statusMark(f git.FileDiff) (string, theme.Hex) {
	switch {
	case f.Untracked || f.Status == git.Added:
		return "A", d.ui.Success
	case f.Status == git.Deleted:
		return "D", d.ui.Error
	case f.Status == git.Renamed:
		return "R", d.ui.Warning
	case f.Status == git.Copied:
		return "C", d.ui.Warning
	}

	return "M", d.ui.Info
}

func (d *drawCtx) hiddenFlag() string {
	if !d.icons {
		return "!"
	}

	return iconBlocked
}

// treeFile is the file the tree marks: the selected one while the tree has the
// keyboard, else the one under the diff's cursor; -1 for none.
func (d *drawCtx) treeFile() int {
	if d.tree {
		return d.v.treeSel
	}

	if d.v.cursor >= 0 && d.v.cursor < len(d.v.rows) {
		return d.v.rows[d.v.cursor].file
	}

	return -1
}

// treeTop is the first tree item shown, so the marked file shows, centred when
// the tree scrolls.
func (d *drawCtx) treeTop(room int) int {
	items := d.c.tree
	if len(items) <= room || room <= 0 {
		return 0
	}

	at := slices.IndexFunc(items, func(it treeItem) bool { return !it.dir && it.file == d.treeFile() })
	if at < 0 {
		return 0
	}

	return min(max(at-room/2, 0), len(items)-room)
}

// treeRow draws row y of the tree column: the mode row, the totals row, a
// blank row, then the items.
func (d *drawCtx) treeRow(y int) string {
	w, ui := d.lay.treeW, d.ui
	p := railPainter{bg: ui.Bg}

	switch y {
	case 0:
		return p.line(d.modeRuns(), w)
	case 1:
		return p.line(d.totalRuns(), w)
	case 2:
		return p.line(nil, w)
	}

	room := d.lay.bodyH - 3
	i := d.treeTop(room) + y - 3

	if i < 0 || i >= len(d.c.tree) {
		return p.line(nil, w)
	}

	it := d.c.tree[i]
	name, _ := cleanText(it.name)
	indent := strings.Repeat(" ", it.depth*2)

	if it.dir {
		runs := []railRun{{text: " " + indent}}
		if d.icons {
			runs = append(runs, railRun{text: iconFolder + " ", fg: ui.Muted})
		}

		return p.line(append(runs, railRun{text: name + "/", fg: ui.Muted}), w)
	}

	f := d.v.res.Files[it.file]
	bg, mark, picked := ui.Bg, " ", it.file == d.treeFile()

	switch {
	case picked && d.tree:
		bg, mark = ui.Selection, markGlyph
	case picked && d.diffKeys():
		bg = ui.Surface
	}

	letter, letterFg := d.statusMark(f)

	var right []railRun

	if it.file < len(d.c.hidden) && d.c.hidden[it.file] {
		right = append(right, railRun{text: d.hiddenFlag() + " ", fg: ui.Warning, bold: true})
	}

	if f.Binary {
		right = append(right, railRun{text: "bin", fg: ui.Subtle})
	} else {
		right = append(right, railRun{text: "+" + strconv.Itoa(f.Added), fg: ui.Success}, railRun{text: " "}, railRun{text: "-" + strconv.Itoa(f.Removed), fg: ui.Error})
	}

	lead := 1 + it.depth*2 + 2
	room2 := w - lead - 2 - runsWidth(right) - 1

	name = ansi.Truncate(name, max(room2, 0), "…")
	gap := max(w-lead-ansi.StringWidth(name)-runsWidth(right)-1, 1)

	runs := []railRun{
		{text: mark, fg: ui.Accent},
		{text: indent},
		{text: letter, fg: letterFg, bold: true},
		{text: " "},
		{text: name, fg: ui.Fg, bold: picked},
		{text: strings.Repeat(" ", gap)},
	}

	return railPainter{bg: bg}.line(append(runs, right...), w)
}

// modeParts are the pieces of the mode row: the icon (empty with icons off),
// the bold word and the rest. All are empty before the view has a source.
func modeParts(v *reviewView, icons bool) (icon, word, rest string) {
	switch src := v.src.(type) {
	case git.BranchSource:
		base := v.base
		if base == "" {
			base = src.Base
		}

		if base == "" {
			base = "the default branch"
		}

		base, _ = cleanText(base)
		icon, word, rest = iconBranchMode, "branch", " vs "+base
	case git.TurnSource:
		n, m := v.shownTurn(), v.turns

		icon, word, rest = iconTurn, "turn "+strconv.Itoa(n), " of "+strconv.Itoa(m)+" in this worktree"

		switch {
		case v.noSnapshot:
			rest += ", no snapshot"
		case n == m:
			rest += ", live"
		}
	default:
		return "", "", ""
	}

	if !icons {
		icon = ""
	}

	return icon, word, rest
}

// modeWidth is the width of the mode row with its margins.
func modeWidth(v *reviewView, icons bool) int {
	icon, word, rest := modeParts(v, icons)
	w := treePad + len(word) + ansi.StringWidth(rest)

	if icon != "" {
		w += 2
	}

	return w
}

// modeRuns is the mode row of the tree column.
func (d *drawCtx) modeRuns() []railRun {
	icon, word, rest := modeParts(d.v, d.icons)
	if word == "" {
		return nil
	}

	runs := []railRun{{text: " "}}
	if icon != "" {
		runs = []railRun{{text: " " + icon + " ", fg: d.ui.Accent}}
	}

	return append(runs, railRun{text: word, fg: d.ui.Fg, bold: true}, railRun{text: rest, fg: d.ui.Muted})
}

// totalRuns is the totals row: the files and the lines added and removed.
func (d *drawCtx) totalRuns() []railRun {
	files := d.v.res.Files
	added, removed := 0, 0

	for _, f := range files {
		added += f.Added
		removed += f.Removed
	}

	noun := " files"
	if len(files) == 1 {
		noun = " file"
	}

	return []railRun{
		{text: " " + strconv.Itoa(len(files)) + noun + "  ", fg: d.ui.Muted},
		{text: "+" + strconv.Itoa(added), fg: d.ui.Success},
		{text: " -" + strconv.Itoa(removed), fg: d.ui.Error},
	}
}
