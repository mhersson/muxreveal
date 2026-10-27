package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

const (
	gitEvery   = 5 * time.Second
	gitTimeout = 2 * time.Second
	loadEvery  = 2 * time.Second

	// loadHistory is the CPU samples the sparkline covers: two per cell.
	loadHistory  = 10
	sparkCells   = loadHistory / 2
	maxBranchLen = 18
)

// gitInfo is the focused pane's repository state.
type gitInfo struct {
	Branch string
	Dirty  bool
}

// loadSample is one reading of the machine: CPU percent and memory in use.
type loadSample struct {
	CPU int
	Mem uint64
}

// loadInfo is what the load module shows: the last CPU samples and the memory text.
type loadInfo struct {
	CPU []int
	Mem string
}

// loadSampler reads the machine load; nil means the platform has none.
type loadSampler interface {
	Sample(ctx context.Context) (loadSample, error)
}

// gitRunner returns the output of `git status` for cwd.
type gitRunner func(ctx context.Context, cwd string) ([]byte, error)

// trayState is the sampled data behind the tray. Only Update changes it; the
// samplers run in commands and report back by message.
type trayState struct {
	on    bool // sampling is armed; set by Init
	clock string
	git   *gitInfo
	load  *loadInfo

	gitRun  gitRunner
	gitCwd  string // the cwd of the latest probe
	gitFor  string // the cwd git was read for
	gitBusy bool   // a probe is in flight
	// gitSlow is the focus whose probe timed out: periodic probes stop until
	// the focused pane or its cwd changes.
	gitSlow gitFocus
}

// gitFocus is the pane and cwd a probe was made for.
type gitFocus struct {
	pane core.PaneID
	cwd  string
}

type (
	clockMsg   struct{ text string }
	gitTickMsg struct{}
	gitMsg     struct {
		cwd  string
		info *gitInfo
		err  error // the probe failed; info says nothing about the repository
	}
	loadMsg struct {
		sample loadSample
		err    error
	}
)

// sleepCtx waits d and reports false when ctx ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func clockText(t time.Time) string { return t.Format("15:04") }

// untilNextMinute is the wait to the next minute boundary; on the boundary it
// is a whole minute.
func untilNextMinute(now time.Time) time.Duration {
	return time.Minute - time.Duration(now.Second())*time.Second - time.Duration(now.Nanosecond())
}

// initTray arms every sampler.
func (a *App) initTray() tea.Cmd {
	a.tray.on = true

	cmds := []tea.Cmd{a.clockCmd(false), a.gitTickCmd(), a.syncGit()}
	if a.load != nil {
		cmds = append(cmds, a.loadCmd(false))
	}

	return tea.Batch(cmds...)
}

// clockWake decides what a clock wake-up does: the new text, or how long to
// wait again when the minute has not changed (a timer that fired early).
func clockWake(now time.Time, last string) (text string, again time.Duration) {
	if text = clockText(now); text == last {
		return "", untilNextMinute(now)
	}

	return text, 0
}

// clockCmd waits for the next minute boundary (at once when !wait) and reads the time.
func (a *App) clockCmd(wait bool) tea.Cmd {
	ctx, clk, last := a.ctx, a.clock, a.tray.clock

	return func() tea.Msg {
		if !wait {
			return clockMsg{text: clockText(clk.Now())}
		}

		d := untilNextMinute(clk.Now())

		for {
			if !sleepCtx(ctx, d) {
				return nil
			}

			text, again := clockWake(clk.Now(), last)
			if again == 0 {
				return clockMsg{text: text}
			}

			d = again
		}
	}
}

func (a *App) onClock(msg clockMsg) tea.Cmd {
	a.tray.clock = msg.text

	return a.clockCmd(true)
}

func (a *App) gitTickCmd() tea.Cmd {
	return tea.Tick(gitEvery, func(time.Time) tea.Msg { return gitTickMsg{} })
}

func (a *App) onGitTick() tea.Cmd {
	if a.tray.gitSlow == a.gitFocus() {
		return a.gitTickCmd()
	}

	return tea.Batch(a.gitTickCmd(), a.probeGit())
}

// focusCwd is the working directory of the focused pane.
func (a *App) focusCwd() string {
	return a.gitFocus().cwd
}

func (a *App) gitFocus() gitFocus {
	st := a.store.State()
	pane := st.Tab().Focus

	return gitFocus{pane: pane, cwd: st.Panes[pane].Cwd}
}

// syncGit probes at once when the focused pane's cwd differs from the last
// probe's, or when the focus moved off a probe that timed out.
func (a *App) syncGit() tea.Cmd {
	if !a.tray.on {
		return nil
	}

	if slow := a.tray.gitSlow; slow != (gitFocus{}) && slow != a.gitFocus() {
		a.tray.gitSlow = gitFocus{}

		return a.probeGit()
	}

	if a.focusCwd() == a.tray.gitCwd {
		return nil
	}

	return a.probeGit()
}

// probeGit starts a probe of the focused cwd unless one is already in flight.
func (a *App) probeGit() tea.Cmd {
	if !a.tray.on || a.tray.gitBusy {
		return nil
	}

	cwd := a.focusCwd()
	a.tray.gitCwd = cwd

	if cwd == "" {
		a.tray.git = nil

		return nil
	}

	a.tray.gitBusy = true
	ctx, run := a.ctx, a.tray.gitRun

	return func() tea.Msg {
		info, err := statusInfo(ctx, run, cwd)
		if err != nil && ctx.Err() == nil {
			a.log.Debug("git probe", "cwd", cwd, "err", err)
		}

		return gitMsg{cwd: cwd, info: info, err: err}
	}
}

func (a *App) onGit(msg gitMsg) tea.Cmd {
	a.tray.gitBusy = false

	if msg.cwd != a.focusCwd() {
		// The focus moved while the probe ran; its answer is for another directory.
		return a.probeGit()
	}

	if errors.Is(msg.err, context.DeadlineExceeded) {
		a.tray.gitSlow = a.gitFocus()
	}

	// A failed probe keeps what was read for this directory; elsewhere it
	// leaves nothing to show. Outside a repository the module goes away.
	if msg.err != nil && a.tray.gitFor == msg.cwd {
		return nil
	}

	a.tray.git, a.tray.gitFor = msg.info, msg.cwd

	return nil
}

// loadCmd waits loadEvery (not at all when !wait) and takes one sample.
func (a *App) loadCmd(wait bool) tea.Cmd {
	ctx, s := a.ctx, a.load

	return func() tea.Msg {
		if wait && !sleepCtx(ctx, loadEvery) {
			return nil
		}

		sample, err := s.Sample(ctx)

		return loadMsg{sample: sample, err: err}
	}
}

func (a *App) onLoad(msg loadMsg) tea.Cmd {
	if a.ctx.Err() != nil {
		return nil
	}

	if msg.err != nil {
		a.log.Debug("load sample", "err", msg.err)

		return a.loadCmd(true)
	}

	hist := []int{msg.sample.CPU}
	if a.tray.load != nil {
		hist = append(slices.Clone(a.tray.load.CPU), msg.sample.CPU)
	}

	if len(hist) > loadHistory {
		hist = hist[len(hist)-loadHistory:]
	}

	next := loadInfo{CPU: hist, Mem: formatMem(msg.sample.Mem)}
	a.tray.load = &next

	return a.loadCmd(true)
}

// formatMem writes bytes as 512M, 9.8G or 128G.
func formatMem(b uint64) string {
	const gib = 1 << 30

	switch {
	case b < gib:
		return fmt.Sprintf("%dM", b>>20)
	case b < 100*gib:
		return fmt.Sprintf("%.1fG", float64(b)/gib)
	}

	return fmt.Sprintf("%dG", b/gib)
}

// sparkColumns are the braille dots of the left and right column, bottom to top.
func sparkColumns() (left, right [4]rune) {
	return [4]rune{0x40, 0x04, 0x02, 0x01}, [4]rune{0x80, 0x20, 0x10, 0x08}
}

// sparkline draws the last ten CPU samples as five braille cells, two samples
// per cell, the newer on the right. A sample fills one dot per started quarter
// of CPU, at least one; samples not taken yet leave their column empty.
func sparkline(samples []int) string {
	if len(samples) > loadHistory {
		samples = samples[len(samples)-loadHistory:]
	}

	padded := make([]int, loadHistory-len(samples), loadHistory)
	for i := range padded {
		padded[i] = -1
	}

	padded = append(padded, samples...)

	column := func(pct int, dots [4]rune) rune {
		if pct < 0 {
			return 0
		}

		var bits rune

		for _, d := range dots[:min(max((pct+24)/25, 1), 4)] {
			bits |= d
		}

		return bits
	}

	var sb strings.Builder

	left, right := sparkColumns()

	for i := range sparkCells {
		sb.WriteRune(0x2800 + column(padded[2*i], left) + column(padded[2*i+1], right))
	}

	return sb.String()
}

// statusInfo runs git for cwd within gitTimeout. A directory outside a
// repository gives no info and no error.
func statusInfo(ctx context.Context, run gitRunner, cwd string) (*gitInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	out, err := run(ctx, cwd)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && ctx.Err() == nil {
			return nil, nil
		}

		return nil, fmt.Errorf("git status in %s: %w", cwd, err)
	}

	return parseGitStatus(out), nil
}

// runGit is the production gitRunner: explicit arguments, no shell, and an
// environment free of repository-local variables.
func runGit(ctx context.Context, cwd string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", gitStatusArgs(cwd)...)
	cmd.Env = gitProbeEnv(os.Environ())

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run git: %w", err)
	}

	return out, nil
}

// gitStatusArgs turns fsmonitor off so a repository cannot run a program from
// its config; the -c value also reaches child gits through GIT_CONFIG_PARAMETERS.
func gitStatusArgs(cwd string) []string {
	return []string{"-c", "core.fsmonitor=false", "-C", cwd, "status", "--porcelain=v2", "--branch", "-z"}
}

// gitProbeEnv drops git's repository-local variables from inherited and sets
// GIT_OPTIONAL_LOCKS=0. The list is from `git rev-parse --local-env-vars`
// (git 2.56); GIT_CONFIG_COUNT also removes its KEY_<n> and VALUE_<n> pairs.
func gitProbeEnv(inherited []string) []string {
	local := map[string]bool{}
	for _, name := range []string{
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS",
		"GIT_CONFIG_COUNT", "GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE",
		"GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE", "GIT_INDEX_FILE",
		"GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX",
		"GIT_SHALLOW_FILE", "GIT_COMMON_DIR", "GIT_OPTIONAL_LOCKS",
	} {
		local[name] = true
	}

	env := make([]string, 0, len(inherited)+1)

	for _, kv := range inherited {
		name, _, _ := strings.Cut(kv, "=")
		if local[name] || strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_") {
			continue
		}

		env = append(env, kv)
	}

	return append(env, "GIT_OPTIONAL_LOCKS=0")
}

// parseGitStatus reads `status --porcelain=v2 --branch -z`: the header records,
// then one record per changed path. A detached HEAD reads as the first seven
// characters of its commit. It returns nil without a branch header.
func parseGitStatus(out []byte) *gitInfo {
	var branch, oid string

	var info gitInfo

	for rec := range bytes.SplitSeq(out, []byte{0}) {
		s := string(rec)

		switch {
		case s == "":
		case !strings.HasPrefix(s, "#"):
			info.Dirty = true
		case strings.HasPrefix(s, "# branch.head "):
			branch = strings.TrimPrefix(s, "# branch.head ")
		case strings.HasPrefix(s, "# branch.oid "):
			oid = strings.TrimPrefix(s, "# branch.oid ")
		}

		if info.Dirty {
			break
		}
	}

	switch branch {
	case "":
		return nil
	case "(detached)":
		branch = oid[:min(len(oid), 7)]
	}

	info.Branch = branch

	return &info
}

// agentCount is the agent panes open across all tabs.
func (a *App) agentCount() int { return len(a.store.State().AgentPanes()) }

// trayRun is a piece of text in one colour on the Surface ground.
type trayRun struct {
	text string
	fg   theme.Hex
	bold bool
}

// surfaceModule joins runs into a module whose width is their text's.
func surfaceModule(id string, ui theme.UI, runs ...trayRun) trayModule {
	w := 0
	for _, r := range runs {
		w += ansi.StringWidth(r.text)
	}

	return trayModule{id: id, width: w, render: func() string {
		var sb strings.Builder
		for _, r := range runs {
			sb.WriteString(paint(r.fg, ui.Surface, r.bold).Render(r.text))
		}

		return sb.String()
	}}
}

// trayModules lists the modules that have data, left to right. The PR and CI
// slot stays empty until pull requests are tracked.
func (a *App) trayModules() []trayModule {
	ui := a.current().theme.UI
	icons := a.icons

	var mods []trayModule

	st := a.store.State()
	if ids := st.AgentPanes(); len(ids) > 0 {
		states := make([]core.AgentState, len(ids))
		for i, id := range ids {
			states[i] = st.Panes[id].State
		}

		mods = append(mods, agentsModule(states, icons, ui))
	}

	if g := a.tray.git; g != nil {
		mods = append(mods, gitModule(*g, icons, ui))
	}

	if l := a.tray.load; l != nil && len(l.CPU) > 0 {
		mods = append(mods, loadModule(*l, icons, ui))
	}

	if a.tray.clock != "" {
		mods = append(mods, clockModule(a.tray.clock, icons, ui))
	}

	return mods
}

// agentsModule shows one icon per agent in its state's colour (Agent for no
// state) and how many are blocked.
func agentsModule(states []core.AgentState, icons bool, ui theme.UI) trayModule {
	blocked := 0

	for _, s := range states {
		if s == core.AgentBlocked {
			blocked++
		}
	}

	if !icons {
		if blocked == 0 {
			return surfaceModule(modAgents, ui, trayRun{text: fmt.Sprintf(" agents %d ", len(states)), fg: ui.Fg})
		}

		return surfaceModule(modAgents, ui,
			trayRun{text: fmt.Sprintf(" agents %d", len(states)), fg: ui.Fg},
			trayRun{text: fmt.Sprintf(" blocked %d", blocked), fg: ui.Blocked, bold: true},
			trayRun{text: " ", fg: ui.Fg},
		)
	}

	runs := []trayRun{{text: " ", fg: ui.Fg}}
	for _, s := range states {
		runs = append(runs, trayRun{text: iconAgent, fg: stateColour(s, ui, ui.Agent)})
	}

	if blocked > 0 {
		runs = append(runs, trayRun{text: fmt.Sprintf(" %s%d", iconBlocked, blocked), fg: ui.Blocked, bold: true})
	}

	return surfaceModule(modAgents, ui, append(runs, trayRun{text: " ", fg: ui.Fg})...)
}

func gitModule(g gitInfo, icons bool, ui theme.UI) trayModule {
	runs := []trayRun{{text: " ", fg: ui.Fg}}
	if icons {
		runs = append(runs, trayRun{text: iconGit, fg: ui.Git}, trayRun{text: " ", fg: ui.Fg})
	}

	runs = append(runs, trayRun{text: ansi.Truncate(printable(g.Branch), maxBranchLen, "…"), fg: ui.Fg})

	if g.Dirty {
		marker := "*"
		if icons {
			marker = iconDirty
		}

		runs = append(runs, trayRun{text: " ", fg: ui.Fg}, trayRun{text: marker, fg: ui.Warning})
	}

	return surfaceModule(modGit, ui, append(runs, trayRun{text: " ", fg: ui.Fg})...)
}

func loadModule(l loadInfo, icons bool, ui theme.UI) trayModule {
	lead := trayRun{text: " cpu ", fg: ui.Muted}
	if icons {
		lead = trayRun{text: " " + iconLoad + " ", fg: ui.Load}
	}

	mem := "  " + iconMem + " " + l.Mem + " "
	if !icons {
		mem = "  mem " + l.Mem + " "
	}

	return surfaceModule(modLoad, ui,
		lead,
		trayRun{text: sparkline(l.CPU), fg: ui.Load},
		trayRun{text: fmt.Sprintf(" %2d%%", l.CPU[len(l.CPU)-1]), fg: ui.Fg},
		trayRun{text: mem, fg: ui.Muted},
	)
}

// clockModule is the clock's own segment; the tray draws the edge before it.
func clockModule(text string, icons bool, ui theme.UI) trayModule {
	label := " " + text + " "
	if icons {
		label = " " + iconClock + " " + text + " "
	}

	return trayModule{id: modClock, width: ansi.StringWidth(label), render: func() string {
		return paint(ui.OnAccent, ui.Clock, true).Render(label)
	}}
}
