package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/github"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// mergeTimeout bounds one merge.
const mergeTimeout = 60 * time.Second

// maxMergeWidth caps the merge box.
const maxMergeWidth = 80

// The merge box's notices.
const (
	noticeMerging     = "merging #%d…"
	noticeMergeBusy   = "#%d is already being merged"
	noticeMergeClosed = "#%d is %s"
	noticeMergeNoPane = "merge: not a git repository"
)

// mergeView is the open merge box. Everything it sends is frozen when it opens:
// the repository, the pull request, the repository's settings and the head the
// drawer's diff loaded. The box draws the poll's later state of the pull
// request but never sends it.
type mergeView struct {
	repo   github.Repo
	pr     core.PRInfo
	info   github.RepoInfo
	sha    string
	method github.MergeMethod
	del    bool
}

// mergeKey identifies a pull request with a merge in flight.
type mergeKey struct {
	repo   github.Repo
	number int
}

// mergedMsg answers a merge.
type mergedMsg struct {
	key    mergeKey
	method github.MergeMethod
	branch string
	res    github.Merged
	err    error
}

// mergeMethods lists the methods in the order the chips show them.
func mergeMethods() []github.MergeMethod {
	return []github.MergeMethod{github.Squash, github.MergeCommit, github.Rebase}
}

// defaultMethod is the method a merge starts with: merge, else squash, else rebase.
func defaultMethod(allowed []string) github.MergeMethod {
	for _, m := range []github.MergeMethod{github.MergeCommit, github.Squash, github.Rebase} {
		if slices.Contains(allowed, string(m)) {
			return m
		}
	}

	return github.MergeCommit
}

// allowedMethods reads the repository's methods in the order the poll stores
// them. A repository whose methods the poll has not read yet allows all three,
// and GitHub refuses a method the repository forbids.
func allowedMethods(methods []string) []github.MergeMethod {
	var out []github.MergeMethod

	for _, m := range []github.MergeMethod{github.MergeCommit, github.Squash, github.Rebase} {
		if slices.Contains(methods, string(m)) {
			out = append(out, m)
		}
	}

	if len(out) == 0 {
		return []github.MergeMethod{github.MergeCommit, github.Squash, github.Rebase}
	}

	return out
}

// allows reports whether the box may merge by m.
func (v *mergeView) allows(m github.MergeMethod) bool { return slices.Contains(v.info.Methods, m) }

// deleteShown reports whether the box offers to delete the branch: not for a
// pull request from another repository, nor for the default branch, which
// github.Merge never deletes.
func (v *mergeView) deleteShown() bool {
	b := v.pr.HeadBranch

	return b != "" && !v.pr.CrossRepo && v.info.DefaultBranch != "" && b != v.info.DefaultBranch
}

// mergeReminder is the line under the warning: what enter does regardless.
const mergeReminder = "enter still merges if GitHub allows it"

// mergeWarning is what is wrong with a merge: failed or running checks and a
// missing approval, or "" when none applies. c are the checks of pr, when
// loaded. Failed checks are named while the line fits fit cells (fit <= 0 has no
// limit), else counted.
func mergeWarning(pr core.PRInfo, c *core.PRChecks, fit int) string {
	var names []string

	if c != nil {
		for _, ch := range c.Checks {
			if checkKindOf(ch.State) == checkFailed {
				names = append(names, printable(ch.Name))
			}
		}
	}

	failed := max(pr.ChecksFailed, len(names))

	for named := len(names) > 0; ; named = false {
		text := mergeWarningText(pr, names, failed, named)
		if !named || fit <= 0 || ansi.StringWidth(text) <= fit {
			return text
		}
	}
}

// mergeWarningText is mergeWarning with the failed checks named or counted.
func mergeWarningText(pr core.PRInfo, names []string, failed int, named bool) string {
	var parts []string

	switch {
	case named:
		text := strings.Join(names[:min(len(names), 2)], ", ")
		if more := len(names) - 2; more > 0 {
			text += fmt.Sprintf(" and %d more", more)
		}

		parts = append(parts, text+" failed")
	case failed == 1:
		parts = append(parts, "1 check failed")
	case failed > 1:
		parts = append(parts, fmt.Sprintf("%d checks failed", failed))
	}

	if running := pr.ChecksTotal - pr.ChecksDone; running == 1 {
		parts = append(parts, "1 check running")
	} else if running > 1 {
		parts = append(parts, fmt.Sprintf("%d checks running", running))
	}

	switch pr.ReviewDecision {
	case "APPROVED":
	case "CHANGES_REQUESTED":
		parts = append(parts, "changes were requested")
	default:
		parts = append(parts, "no approval yet")
	}

	return strings.Join(parts, ", ")
}

// mergeStateWord is GitHub's merge state of a pull request into base in words.
func mergeStateWord(state, base string, ui theme.UI) railRun {
	switch state {
	case "CLEAN", "HAS_HOOKS":
		return railRun{text: "mergeable", fg: ui.Success}
	case "UNSTABLE":
		return railRun{text: "mergeable, checks failing", fg: ui.Warning}
	case "BLOCKED":
		return railRun{text: "blocked by branch protection", fg: ui.Warning}
	case "BEHIND":
		return railRun{text: "behind " + printable(base), fg: ui.Warning}
	case "DIRTY":
		return railRun{text: "conflicts with " + printable(base), fg: ui.Error}
	case "DRAFT":
		return railRun{text: "draft", fg: ui.Muted}
	}

	return railRun{text: "mergeability unknown", fg: ui.Subtle}
}

// reviewMerge is `M`: it merges the drawer's pull request at the head its diff
// showed, after showing the pull request first when it is not shown.
func (a *App) reviewMerge() tea.Cmd { return a.prAct(thenMerge) }

// mergeCommand is `leader g m`: it merges what the active tab's drawer shows, or
// when none is open opens one on the focused pane first.
func (a *App) mergeCommand() tea.Cmd {
	if a.actions == nil {
		a.notice = noticePRsOff

		return nil
	}

	tab := a.store.State().Tab()
	if tab.Review != nil {
		return a.prAct(thenMerge)
	}

	if _, ok := a.store.State().Panes[tab.Focus]; !ok || !slices.Contains(tab.Panes, tab.Focus) {
		a.notice = noticeMergeNoPane

		return nil
	}

	return a.openWithPR(tab.Focus, nil, thenMerge)
}

// openMerge opens the merge box on the pull request the drawer has loaded, or
// says why it cannot.
func (a *App) openMerge(repoKey string, number int, sha string) {
	if a.modalOpen() {
		return
	}

	repo, ok := repoOfKey(repoKey)
	if !ok || a.actions == nil {
		a.notice = noticePRsOff

		return
	}

	if a.merging[mergeKey{repo, number}] {
		a.notice = fmt.Sprintf(noticeMergeBusy, number)

		return
	}

	st := a.store.State()
	pr := core.PRInfo{Number: number}

	if info, ok := prInfoByNumber(st, repoKey, number); ok {
		switch info.State {
		case "MERGED":
			a.notice = fmt.Sprintf(noticeMergeClosed, number, "merged")

			return
		case "CLOSED":
			a.notice = fmt.Sprintf(noticeMergeClosed, number, "closed")

			return
		}

		pr = info
	}

	rp := st.PRs[repoKey]
	info := github.RepoInfo{
		DefaultBranch: rp.DefaultBranch, Methods: allowedMethods(rp.Methods), DeleteBranchOnMerge: rp.DeleteBranchOnMerge,
	}

	v := &mergeView{repo: repo, pr: pr, info: info, sha: sha, method: defaultMethod(rp.Methods)}
	if !v.allows(v.method) {
		v.method = info.Methods[0]
	}

	v.del = v.deleteShown() && rp.DeleteBranchOnMerge

	a.endLeader()
	a.merge = v
	a.prs.dirty = true
}

// closeMerge closes the box and lets the poller go back to the drawer's pull request.
func (a *App) closeMerge() {
	a.merge = nil
	a.prs.dirty = true
}

// handleMergeKey answers the open merge box; any other key is swallowed.
func (a *App) handleMergeKey(msg tea.KeyPressMsg) tea.Cmd {
	v := a.merge

	switch key := msg.String(); key {
	case "esc":
		a.closeMerge()
	case "s", "m", "r":
		m := map[string]github.MergeMethod{"s": github.Squash, "m": github.MergeCommit, "r": github.Rebase}[key]
		if v.allows(m) {
			v.method = m
		}
	case "d":
		if v.deleteShown() {
			v.del = !v.del
		}
	case "enter":
		return a.mergeRun(v)
	}

	return nil
}

// mergeRun merges exactly what v froze off the update loop, under a timeout
// derived from the app's context.
func (a *App) mergeRun(v *mergeView) tea.Cmd {
	a.closeMerge()

	key := mergeKey{v.repo, v.pr.Number}

	if a.merging == nil {
		a.merging = map[mergeKey]bool{}
	}

	a.merging[key] = true
	a.notice = fmt.Sprintf(noticeMerging, key.number)

	var (
		actions = a.actions
		appCtx  = a.ctx
		pr      = github.PR{Number: v.pr.Number, HeadBranch: v.pr.HeadBranch, BaseBranch: v.pr.BaseBranch, CrossRepo: v.pr.CrossRepo}
		opts    = github.MergeOptions{Method: v.method, SHA: v.sha, DeleteBranch: v.del && v.deleteShown()}
		repo    = v.repo
		info    = v.info
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, mergeTimeout)
		defer cancel()

		res, err := actions.Merge(ctx, repo, pr, info, opts)

		return mergedMsg{key: key, method: opts.Method, branch: pr.HeadBranch, res: res, err: err}
	}
}

// onMerged says what a merge did and asks the poller for the repository. A
// merge cut short by the app closing says nothing.
func (a *App) onMerged(msg mergedMsg) {
	delete(a.merging, msg.key)

	n := msg.key.number
	if a.notice == fmt.Sprintf(noticeMerging, n) {
		a.notice = ""
	}

	switch {
	case errors.Is(msg.err, context.Canceled):
		return
	case errors.Is(msg.err, context.DeadlineExceeded):
		a.toast(levelError, fmt.Sprintf("merge #%d: timed out", n))

		return
	case errors.Is(msg.err, github.ErrHeadMoved):
		a.toast(levelError, fmt.Sprintf("merge #%d: the PR moved since you reviewed it; nothing merged", n))

		return
	case msg.err != nil:
		a.toast(levelError, fmt.Sprintf("merge #%d: %s", n, gitReason(msg.err)))

		return
	}

	text := fmt.Sprintf("merged #%d (%s)", n, msg.method)

	switch {
	case msg.res.DeleteErr != nil:
		text += fmt.Sprintf("; deleting %s failed: %s", printable(msg.branch), gitReason(msg.res.DeleteErr))
	case msg.res.BranchDeleted:
		text += ", branch deleted"
	}

	a.toast(levelSuccess, text)

	if a.prs.src != nil {
		a.prs.src.Refresh(msg.key.repo)
	}
}

// mergeChecks are the focused checks the box shows: those of its pull request
// at the head the poll lists; nil until they arrive.
func (a *App) mergeChecks(st core.State, v *mergeView) *core.PRChecks {
	return a.checksOf(st, &checksView{repo: v.repo.String(), number: v.pr.Number})
}
