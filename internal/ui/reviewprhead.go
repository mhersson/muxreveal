package ui

import (
	"strconv"
	"strings"

	"github.com/mhersson/muxreveal/internal/core"
)

// prHeaderMinDiff is the rows of diff the header leaves; a column too short for
// it and the header draws without it.
const prHeaderMinDiff = 3

// prHeaderRows is the rows of the header over the diff: the title, the meta row,
// one more when the worktree has unpushed commits or the pull request moved,
// and the rule.
func prHeaderRows(v *reviewView, pr core.PRInfo) int {
	rows := 3
	if v.unpushed > 0 || prMoved(v, pr) {
		rows++
	}

	return rows
}

// prMoved reports whether the poll's head is another commit than the one the
// diff was loaded at. A head not yet known on either side is no move.
func prMoved(v *reviewView, pr core.PRInfo) bool {
	return pr.HeadOID != "" && v.res.Head != "" && pr.HeadOID != v.res.Head
}

// prHeaderInfo is the pull request the view shows, as the poll last knew it.
func (a *App) prHeaderInfo(v *reviewView) (core.PRInfo, bool) {
	if v.pr == (core.ReviewPR{}) {
		return core.PRInfo{}, false
	}

	return prInfoByNumber(a.store.State(), v.pr.Repo, v.pr.Number)
}

// prHeaderH is the header's rows in a drawer innerH rows tall, 0 when the view
// shows no pull request the poll knows or the column is too short to keep some
// diff beside it.
func (a *App) prHeaderH(v *reviewView, innerH int) int {
	pr, ok := a.prHeaderInfo(v)
	if !ok {
		return 0
	}

	body := innerH
	if innerH >= hintsMinInner {
		body--
	}

	if rows := prHeaderRows(v, pr); body >= rows+prHeaderMinDiff {
		return rows
	}

	return 0
}

// diffColumn is row y of the diff column: the header's rows, then the diff.
func (d *drawCtx) diffColumn(y int) string {
	if y < d.lay.headH {
		return d.headerRow(y)
	}

	return d.diffRow(y - d.lay.headH)
}

// headerRow draws row y of the pull request header, cut with … to the column.
func (d *drawCtx) headerRow(y int) string {
	ui, pr, v := d.ui, d.pr, d.v
	p := railPainter{bg: ui.Bg}
	w := d.lay.diffW
	sp := func(n int) railRun { return railRun{text: strings.Repeat(" ", n), fg: ui.Fg} }

	switch {
	case y == 0:
		return p.line([]railRun{sp(markW), {text: prNumberText(pr.Number) + " " + printable(pr.Title), fg: ui.Fg, bold: true}}, w)
	case y == 1:
		return p.line(d.prMetaRuns(sp), w)
	case y == d.lay.headH-1:
		return p.line([]railRun{{text: strings.Repeat("─", w), fg: ui.Border}}, w)
	case prMoved(v, pr):
		return p.line([]railRun{sp(markW), {text: "the PR moved to " + pr.HeadOID[:min(len(pr.HeadOID), 7)] + " since this diff; r fetches it", fg: ui.Warning}}, w)
	case v.unpushed > 0:
		return p.line([]railRun{
			sp(markW),
			{text: "↑" + strconv.Itoa(v.unpushed) + " not pushed", fg: ui.Warning, bold: true},
			{text: ": the PR shows the pushed head; leader g p pushes", fg: ui.Muted},
		}, w)
	}

	return p.line(nil, w)
}

// prMetaRuns is the header's second row: branch and base, checks, review,
// author and age.
func (d *drawCtx) prMetaRuns(sp func(int) railRun) []railRun {
	ui, pr, v := d.ui, d.pr, d.v

	branch := pr.HeadBranch
	if branch == "" {
		branch = v.branch
	}

	base := v.prBase
	if base == "" {
		base = pr.BaseBranch
	}

	runs := []railRun{sp(markW)}
	if d.icons {
		runs = append(runs, railRun{text: iconGit + " ", fg: ui.Git})
	}

	runs = append(runs,
		railRun{text: printable(branch), fg: ui.Dim},
		railRun{text: " → ", fg: ui.Muted},
		railRun{text: printable(base), fg: ui.Dim},
	)

	if pr.State == prMergedState || pr.State == prClosedState {
		runs = append(runs, sp(3))
		runs = append(runs, prTextOf(pr, d.icons, ui).runs(prStage{counts: true})...)
	} else {
		if glyph, counts := prChecksOf(pr, d.icons, ui); glyph.text != "" {
			runs = append(runs, sp(3), glyph, sp(1), counts)
		}

		runs = append(runs, sp(3), prReviewWords(pr, ui))
	}

	if pr.Author != "" {
		runs = append(runs, railRun{text: "   @" + printable(pr.Author), fg: ui.Subtle})
	}

	if age := railAge(pr.UpdatedAt, d.now); age != "" {
		runs = append(runs, railRun{text: "   " + age, fg: ui.Subtle})
	}

	return runs
}
