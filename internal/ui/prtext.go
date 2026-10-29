package ui

import (
	"slices"
	"strconv"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/github"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// Pull request states as core holds them; prOpenState is in prs.go.
const (
	prMergedState = "MERGED"
	prClosedState = "CLOSED"
)

// prRepoOf is the repository a resolution answered for the working directory
// and branch the pane's head probe last found. Until that answer arrives (after
// a move, a switch or a failed resolution), for a detached HEAD, a pane no head
// probe answered for and a remote that is not on GitHub, the pane has none.
func (a *App) prRepoOf(id core.PaneID) (github.Repo, bool) {
	rp, pp := a.rail.panes[id], a.prs.panes[id]

	switch {
	case rp == nil || pp == nil || !pp.ok:
		return github.Repo{}, false
	case !rp.probe.known || !rp.probe.repo || rp.probe.detached || rp.probe.branch == "":
		return github.Repo{}, false
	case pp.answered != prKey{cwd: rp.probe.headCwd, branch: rp.probe.branch}:
		return github.Repo{}, false
	}

	return pp.repo, true
}

// prOfPane is the pull request of the branch a pane is on: its open one, else
// the one a lookup found.
func (a *App) prOfPane(st core.State, id core.PaneID) (core.PRInfo, bool) {
	repo, ok := a.prRepoOf(id)
	if !ok {
		return core.PRInfo{}, false
	}

	return st.PRFor(repo.String(), a.rail.panes[id].probe.branch)
}

// prText is a pull request as a card and the tray write it, in the pieces a
// card drops one by one when short of room. An empty chip means nothing to show.
type prText struct {
	space  railRun
	chip   []railRun // the icon and the number
	word   railRun   // draft, merged or closed; empty for an open pull request
	glyph  railRun   // the checks' icon, or "ci" without icons; empty with no checks
	counts railRun   // done/total, or how many failed
	review railRun   // the review decision; empty with none
}

// prStage is which of the pieces after the chip show. counts stands for the
// text after the glyph or the number: the check counts, or the word of a draft,
// merged or closed pull request.
type prStage struct{ review, counts, glyph bool }

func prTextOf(pr core.PRInfo, icons bool, ui theme.UI) prText {
	num := "#" + strconv.Itoa(pr.Number)
	t := prText{space: railRun{text: " ", fg: ui.Fg}}

	// A draft, merged or closed PR is one colour with its word and no checks.
	plain := func(icon, word string, fg theme.Hex) prText {
		t.chip, t.word = []railRun{{text: num, fg: fg}}, railRun{text: word, fg: fg}
		if icons {
			t.chip = []railRun{{text: icon, fg: fg}, t.space, t.chip[0]}
		}

		return t
	}

	switch {
	case pr.State == prMergedState:
		return plain(iconMerge, "merged", ui.Accent)
	case pr.State == prClosedState:
		return plain(iconPRClosed, "closed", ui.Muted)
	case pr.State != prOpenState:
		return prText{}
	case pr.Draft:
		return plain(iconPRDraft, "draft", ui.Muted)
	}

	t.chip = []railRun{{text: num, fg: ui.Fg, bold: true}}
	if icons {
		t.chip = []railRun{{text: iconPR, fg: ui.Info}, t.space, t.chip[0]}
	}

	t.glyph, t.counts = prChecksOf(pr, icons, ui)
	t.review = prReviewOf(pr, icons, ui)

	return t
}

// prChecksOf is the checks' icon and counts. A failed check outranks one still
// running, which outranks all passed; a pull request with no checks shows none.
func prChecksOf(pr core.PRInfo, icons bool, ui theme.UI) (glyph, counts railRun) {
	var (
		icon, text string
		fg         theme.Hex
		bold       bool
	)

	switch {
	case pr.ChecksFailed > 0:
		icon, text, fg, bold = iconFailed, strconv.Itoa(pr.ChecksFailed)+" failed", ui.Error, true
	case pr.ChecksTotal == 0:
		return railRun{}, railRun{}
	case pr.ChecksDone < pr.ChecksTotal:
		icon, text, fg = iconRunning, strconv.Itoa(pr.ChecksDone)+"/"+strconv.Itoa(pr.ChecksTotal), ui.Warning
	default:
		icon, text, fg = iconPassed, strconv.Itoa(pr.ChecksTotal)+"/"+strconv.Itoa(pr.ChecksTotal), ui.Success
	}

	if !icons {
		icon = "ci"
	}

	return railRun{text: icon, fg: fg, bold: bold}, railRun{text: text, fg: fg, bold: bold}
}

func prReviewOf(pr core.PRInfo, icons bool, ui theme.UI) railRun {
	switch pr.ReviewDecision {
	case "APPROVED":
		if icons {
			return railRun{text: iconApproved, fg: ui.Success}
		}

		return railRun{text: "approved", fg: ui.Success}
	case "CHANGES_REQUESTED":
		if icons {
			return railRun{text: iconChanges, fg: ui.Warning}
		}

		return railRun{text: "changes", fg: ui.Warning}
	}

	return railRun{}
}

// runs is the chip and the pieces of stage s that exist, a space apart. The
// check counts show only beside the glyph.
func (p prText) runs(s prStage) []railRun {
	out := slices.Clone(p.chip)

	if s.counts && p.word.text != "" {
		out = append(out, p.space, p.word)
	}

	if s.glyph && p.glyph.text != "" {
		out = append(out, p.space, p.glyph)

		if s.counts {
			out = append(out, p.space, p.counts)
		}
	}

	if s.review && p.review.text != "" {
		out = append(out, p.space, p.review)
	}

	return out
}

// railRightEnds are the right ends of a card's second line, fullest first: the
// commits ahead, then the pull request. Each end drops one more part than the
// one before: the review, the commits ahead, the check counts (or the word of a
// draft, merged or closed pull request), the check glyph. pinned means the last
// end shows however little room the branch is left, since the number always stays.
func railRightEnds(ui theme.UI, c railCard, icons bool) (ends [][]railRun, pinned bool) {
	var ahead []railRun
	if c.ahead > 0 {
		ahead = []railRun{{text: "↑" + strconv.Itoa(c.ahead), fg: ui.Info, bold: true}}
	}

	var pr prText
	if c.hasPR {
		pr = prTextOf(c.pr, icons, ui)
	}

	if len(pr.chip) == 0 {
		if ahead == nil {
			return nil, false
		}

		return [][]railRun{ahead}, false
	}

	stages := [...]struct {
		ahead bool
		prStage
	}{
		{true, prStage{review: true, counts: true, glyph: true}},
		{true, prStage{counts: true, glyph: true}},
		{false, prStage{counts: true, glyph: true}},
		{false, prStage{glyph: true}},
		{false, prStage{}},
	}

	for _, s := range stages {
		var right []railRun
		if s.ahead && ahead != nil {
			right = append(right, ahead[0], railRun{text: "  ", fg: ui.Fg})
		}

		right = append(right, pr.runs(s.prStage)...)

		// A stage that drops nothing the card has is the same end again.
		if n := len(ends); n > 0 && runsWidth(ends[n-1]) == runsWidth(right) {
			continue
		}

		ends = append(ends, right)
	}

	return ends, true
}
