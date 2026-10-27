package ui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/muxreveal/internal/core"
)

func TestUntilNextMinute(t *testing.T) {
	base := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		now  time.Time
		want time.Duration
	}{
		{"on the boundary", base, time.Minute},
		{"mid minute", base.Add(30*time.Second + 250*time.Millisecond), 29*time.Second + 750*time.Millisecond},
		{"just before", base.Add(59*time.Second + 999*time.Millisecond), time.Millisecond},
		{"just after", base.Add(time.Nanosecond), time.Minute - time.Nanosecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := untilNextMinute(tt.now); got != tt.want {
				t.Errorf("untilNextMinute(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}

// The clock text changes at minute boundaries only, and a message that repeats
// it changes nothing.
func TestClockTextChangesAtMinuteBoundaries(t *testing.T) {
	a := newTestApp(t)
	clk := &fakeClock{t: time.Date(2026, 10, 26, 14, 32, 5, 0, time.UTC)}
	a.clock = clk

	step := func() string {
		msg := a.clockCmd(false)()

		_, cmd := a.Update(msg)
		if cmd == nil {
			t.Fatal("clockMsg did not re-arm the clock")
		}

		return a.tray.clock
	}

	if got := step(); got != "14:32" {
		t.Fatalf("clock = %q, want 14:32", got)
	}

	clk.Advance(54 * time.Second)

	if got := step(); got != "14:32" {
		t.Errorf("clock at 14:32:59 = %q, want 14:32", got)
	}

	clk.Advance(time.Second)

	if got := step(); got != "14:33" {
		t.Errorf("clock at 14:33:00 = %q, want 14:33", got)
	}
}

func modIDs(mods []trayModule) []string {
	var ids []string
	for _, m := range mods {
		ids = append(ids, m.id)
	}

	return ids
}

func TestAgentsModuleFollowsAgentPanes(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)

	has := func() (int, bool) {
		for _, m := range a.trayModules() {
			if m.id == modAgents {
				return m.width, true
			}
		}

		return 0, false
	}

	if _, ok := has(); ok {
		t.Fatal("agents module present with no panes")
	}

	openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/a", Split: core.SplitVertical})

	if _, ok := has(); ok {
		t.Error("agents module present with only a shell")
	}

	openIn(t, a, core.OpenPane{Kind: core.KindAgent, Cwd: "/w/b", Split: core.SplitVertical})

	if w, ok := has(); !ok || w != 3 {
		t.Errorf("one agent: present=%v width=%d, want true 3", ok, w)
	}

	// An agent in another tab counts too.
	openIn(t, a, core.NewTab{Kind: core.KindAgent, Cwd: "/w/c"}, core.SelectTab{Index: 0})

	if w, ok := has(); !ok || w != 4 {
		t.Errorf("two agents across tabs: present=%v width=%d, want true 4", ok, w)
	}

	st := a.store.State()
	for id, m := range st.Panes {
		if m.Kind == core.KindAgent {
			openIn(t, a, core.ClosePane{ID: id})
		}
	}

	if _, ok := has(); ok {
		t.Error("agents module present after the agent panes closed")
	}
}

func TestParseGitStatus(t *testing.T) {
	const oid = "0123456789abcdef0123456789abcdef01234567"

	tests := []struct {
		name string
		out  string
		want *gitInfo
	}{
		{"clean", "# branch.oid " + oid + "\x00# branch.head main\x00# branch.upstream origin/main\x00# branch.ab +0 -0\x00", &gitInfo{Branch: "main"}},
		{"modified", "# branch.oid " + oid + "\x00# branch.head dev\x001 .M N... 100644 100644 100644 aaa bbb file.go\x00", &gitInfo{Branch: "dev", Dirty: true}},
		{"untracked", "# branch.oid " + oid + "\x00# branch.head dev\x00? new.txt\x00", &gitInfo{Branch: "dev", Dirty: true}},
		{"rename", "# branch.oid " + oid + "\x00# branch.head dev\x002 R. N... 100644 100644 100644 a b R100 new\x00old\x00", &gitInfo{Branch: "dev", Dirty: true}},
		{"detached", "# branch.oid " + oid + "\x00# branch.head (detached)\x00", &gitInfo{Branch: "0123456"}},
		{"detached and dirty", "# branch.oid " + oid + "\x00# branch.head (detached)\x00? x\x00", &gitInfo{Branch: "0123456", Dirty: true}},
		{"initial commit", "# branch.oid (initial)\x00# branch.head main\x00", &gitInfo{Branch: "main"}},
		{"branch with spaces is not possible but slashes are", "# branch.oid " + oid + "\x00# branch.head feat/x\x00", &gitInfo{Branch: "feat/x"}},
		{"empty", "", nil},
		{"no head line", "# branch.oid " + oid + "\x00", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGitStatus([]byte(tt.out))
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Errorf("parseGitStatus = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestStatusInfoRunner(t *testing.T) {
	var gotCwd string

	ok := func(_ context.Context, cwd string) ([]byte, error) {
		gotCwd = cwd

		return []byte("# branch.oid abc\x00# branch.head main\x00"), nil
	}

	info, err := statusInfo(context.Background(), ok, "/w/x")
	if err != nil || info == nil || info.Branch != "main" || gotCwd != "/w/x" {
		t.Errorf("statusInfo = %+v, %v (cwd %q)", info, err, gotCwd)
	}

	notRepo := func(context.Context, string) ([]byte, error) {
		cmd := exec.Command("false")
		_ = cmd.Run()

		return nil, &exec.ExitError{ProcessState: cmd.ProcessState}
	}

	info, err = statusInfo(context.Background(), notRepo, "/w/x")
	if info != nil || err != nil {
		t.Errorf("not a repo: statusInfo = %+v, %v, want nil, nil", info, err)
	}

	timeout := func(ctx context.Context, _ string) ([]byte, error) {
		<-ctx.Done()

		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	info, err = statusInfo(ctx, timeout, "/w/x")
	if info != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: statusInfo = %+v, %v, want nil and Canceled", info, err)
	}

	deadline := func(context.Context, string) ([]byte, error) { return nil, context.DeadlineExceeded }

	info, err = statusInfo(context.Background(), deadline, "/w/x")
	if info != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("timeout: statusInfo = %+v, %v, want nil and DeadlineExceeded", info, err)
	}
}

// git runs in a temp repo; the global and system git config are isolated.
func TestGitStatusRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)

	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git("init", "-b", "main")
	git("commit", "--allow-empty", "-m", "first")

	info, err := statusInfo(context.Background(), runGit, dir)
	if err != nil || info == nil || *info != (gitInfo{Branch: "main"}) {
		t.Errorf("clean repo: %+v, %v", info, err)
	}

	if err := os.WriteFile(dir+"/f.txt", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err = statusInfo(context.Background(), runGit, dir)
	if err != nil || info == nil || *info != (gitInfo{Branch: "main", Dirty: true}) {
		t.Errorf("dirty repo: %+v, %v", info, err)
	}

	git("add", "f.txt")
	git("commit", "-m", "second")
	git("checkout", "--detach")

	info, err = statusInfo(context.Background(), runGit, dir)
	if err != nil || info == nil || len(info.Branch) != 7 || info.Dirty {
		t.Errorf("detached repo: %+v, %v", info, err)
	}

	info, err = statusInfo(context.Background(), runGit, t.TempDir())
	if info != nil || err != nil {
		t.Errorf("not a repo: %+v, %v", info, err)
	}
}

func TestSparkline(t *testing.T) {
	const blank = "\u2800"

	tests := []struct {
		name string
		in   []int
		want string
	}{
		{"nothing sampled yet", nil, strings.Repeat(blank, 5)},
		{"idle fills one dot each", make([]int, 10), strings.Repeat("\u28C0", 5)},
		{"quarters", []int{1, 25, 26, 50, 51, 75, 76, 100, 100, 100}, "\u28C0\u28E4\u28F6\u28FF\u28FF"},
		{"right column", []int{0, 1, 0, 26, 0, 51, 0, 100, 0, 0}, "\u28C0\u28E0\u28F0\u28F8\u28C0"},
		{"newest sample is last", []int{100, 0}, strings.Repeat(blank, 4) + "\u28C7"},
		{"only the last ten", []int{100, 100, 100, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, strings.Repeat("\u28C0", 5)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sparkline(tt.in); got != tt.want {
				t.Errorf("sparkline(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatMem(t *testing.T) {
	const gib = 1 << 30

	tests := []struct {
		in   uint64
		want string
	}{
		{0, "0M"},
		{512 << 20, "512M"},
		{gib, "1.0G"},
		{gib * 98 / 10, "9.8G"},
		{15*gib + gib/2, "15.5G"},
		{128 * gib, "128G"},
	}

	for _, tt := range tests {
		if got := formatMem(tt.in); got != tt.want {
			t.Errorf("formatMem(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// fixtureTray fills every sampled module with fixed values.
func fixtureTray(a *App) {
	a.tray.clock = "14:32"
	a.tray.git = &gitInfo{Branch: "feat/tabs", Dirty: true}
	a.tray.load = &loadInfo{CPU: []int{5, 12, 30, 55, 80, 12, 3, 12, 40, 12}, Mem: "9.8G"}
}

func trayApp(t *testing.T, icons bool) *App {
	t.Helper()

	a := newTestApp(t)
	a.icons = icons
	sized(t, a, 100, 30)
	openIn(t, a,
		core.OpenPane{Kind: core.KindAgent, Cwd: "/w/api", Split: core.SplitVertical},
		core.NewTab{Kind: core.KindShell, Cwd: "/w/web"},
	)
	fixtureTray(a)

	return a
}

func TestTrayModuleWidths(t *testing.T) {
	for _, icons := range []bool{true, false} {
		a := trayApp(t, icons)
		mods := a.trayModules()

		if want := []string{modLayout, modAgents, modGit, modLoad, modClock}; !slices.Equal(modIDs(mods), want) {
			t.Fatalf("icons=%v modules = %v, want %v", icons, modIDs(mods), want)
		}

		for _, m := range mods {
			if got := lipgloss.Width(m.render()); got != m.width {
				t.Errorf("icons=%v %s renders %d cells, width says %d", icons, m.id, got, m.width)
			}
		}
	}
}

func TestTrayOmitsAbsentModules(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)

	if got := a.trayModules(); len(got) != 0 {
		t.Errorf("modules before any sample = %v, want none", modIDs(got))
	}

	a.tray.clock = "09:05"

	if got := modIDs(a.trayModules()); !slices.Equal(got, []string{modClock}) {
		t.Errorf("modules = %v, want only the clock", got)
	}
}

func TestBarLinesStayExactWidth(t *testing.T) {
	for _, icons := range []bool{true, false} {
		a := trayApp(t, icons)

		for w := 1; w <= 130; w++ {
			if got := lipgloss.Width(a.workspaceBar(w)); got != w {
				t.Fatalf("icons=%v width %d: bar is %d cells", icons, w, got)
			}
		}
	}
}

// The tray gives way in DESIGN's order as the bar narrows.
func TestTrayDropOrder(t *testing.T) {
	rank := map[string]int{modLoad: 0, modGit: 1, modPR: 2, modLayout: 3, modClock: 4, modAgents: 5}

	for _, icons := range []bool{true, false} {
		a := trayApp(t, icons)
		tabs := a.tabInfos()
		full := modIDs(a.trayModules())

		for w := 130; w >= 1; w-- {
			p := planBar(tabs, a.store.State().Active, a.trayModules(), w, icons)
			kept := modIDs(p.tray)

			for _, id := range full {
				if slices.Contains(kept, id) {
					continue
				}

				for _, other := range kept {
					if rank[other] < rank[id] {
						t.Fatalf("icons=%v width %d: %s stayed after %s left (kept %v)", icons, w, other, id, kept)
					}
				}
			}
		}
	}
}

func TestBarGoldens(t *testing.T) {
	tests := []struct {
		name  string
		icons bool
		width int
	}{
		{"icons wide", true, 100},
		{"icons without load", true, 70},
		{"icons agents and clock", true, 50},
		{"plain wide", false, 100},
		{"plain without load", false, 66},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := trayApp(t, tt.icons)
			golden.RequireEqual(t, []byte(a.workspaceBar(tt.width)))
		})
	}
}

func TestTrayOnlyClockEdge(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 40, 10)
	a.tray.clock = "14:32"

	vt := lineVT(t, a.workspaceBar(40), 40)
	ui := mochaUI(t)

	assertColours(t, vt, 30, 0, trayOpen, ui.Clock, ui.Bar, false)
	assertColours(t, vt, 32, 0, iconClock, ui.OnAccent, ui.Clock, true)
}

func TestTrayColours(t *testing.T) {
	a := trayApp(t, true)
	ui := mochaUI(t)
	w := 100
	vt := lineVT(t, a.workspaceBar(w), w)

	// Walk the tray from the right: clock segment, load, git, agents.
	clockW := lipgloss.Width(trayModuleByID(a, modClock).render())
	x := w - clockW
	assertColours(t, vt, x-1, 0, trayOpen, ui.Clock, ui.Surface, false)
	assertColours(t, vt, x+1, 0, iconClock, ui.OnAccent, ui.Clock, true)
	assertColours(t, vt, x+3, 0, "1", ui.OnAccent, ui.Clock, true)

	loadW := trayModuleByID(a, modLoad).width
	lx := x - 1 - loadW
	assertColours(t, vt, lx+1, 0, iconLoad, ui.Load, ui.Surface, false)

	gitW := trayModuleByID(a, modGit).width
	gx := lx - 1 - gitW
	assertColours(t, vt, lx-1, 0, edgeThin, ui.Bar, ui.Surface, false)
	assertColours(t, vt, gx+1, 0, iconGit, ui.Git, ui.Surface, false)
	assertColours(t, vt, gx+3, 0, "f", ui.Fg, ui.Surface, false)
	assertColours(t, vt, gx+gitW-2, 0, iconDirty, ui.Warning, ui.Surface, false)

	agW := trayModuleByID(a, modAgents).width
	ax := gx - 1 - agW
	assertColours(t, vt, ax-1, 0, edgeThin, ui.Bar, ui.Surface, false)
	assertColours(t, vt, ax+1, 0, iconAgent, ui.Agent, ui.Surface, false)

	lyW := trayModuleByID(a, modLayout).width
	yx := ax - 1 - lyW
	assertColours(t, vt, yx-1, 0, trayOpen, ui.Surface, ui.Bar, false)
}

func trayModuleByID(a *App, id string) trayModule {
	for _, m := range a.trayModules() {
		if m.id == id {
			return m
		}
	}

	return trayModule{}
}

func TestBranchCut(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)
	a.tray.git = &gitInfo{Branch: strings.Repeat("b", 30)}

	m := trayModuleByID(a, modGit)
	if !strings.Contains(m.render(), strings.Repeat("b", 17)+"…") {
		t.Errorf("long branch not cut to 18 cells: %q", m.render())
	}

	if m.width != 1+1+1+18+1 {
		t.Errorf("git width = %d, want %d", m.width, 1+1+1+18+1)
	}
}

// Messages that change nothing neither re-render state nor start work.
// Samples that repeat what the bar shows leave its row byte-identical.
func TestUnchangedSamplesLeaveBarIdentical(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)

	for range loadHistory {
		a.Update(loadMsg{sample: loadSample{CPU: 12, Mem: 9 << 30}})
	}

	a.Update(clockMsg{text: "14:32"})

	before := a.workspaceBar(100)

	a.Update(clockMsg{text: "14:32"})
	a.Update(loadMsg{sample: loadSample{CPU: 12, Mem: 9<<30 + 1}})

	if after := a.workspaceBar(100); after != before {
		t.Errorf("bar changed:\n%q\n%q", before, after)
	}
}

func TestGitFailureKeepsLastInfo(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)
	openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/a", Split: core.SplitVertical})
	a.tray.on = true

	main := &gitInfo{Branch: "main"}

	tests := []struct {
		name string
		msg  gitMsg
		want *gitInfo
	}{
		{"read", gitMsg{cwd: "/w/a", info: main}, main},
		{"timeout keeps the last info", gitMsg{cwd: "/w/a", err: context.DeadlineExceeded}, main},
		{"not a repository clears it", gitMsg{cwd: "/w/a"}, nil},
		{"failure with nothing read stays empty", gitMsg{cwd: "/w/a", err: context.DeadlineExceeded}, nil},
	}

	for _, tt := range tests {
		a.tray.gitBusy = true
		a.Update(tt.msg)

		if a.tray.git != tt.want {
			t.Errorf("%s: git = %+v, want %+v", tt.name, a.tray.git, tt.want)
		}
	}

	// A failure for another directory must not keep the old one's info.
	a.tray.gitBusy = true
	a.Update(gitMsg{cwd: "/w/a", info: main})
	openIn(t, a, core.SetPaneCwd{ID: a.store.State().Tab().Focus, Cwd: "/w/b"})
	a.later = nil
	a.tray.gitBusy = true
	a.Update(gitMsg{cwd: "/w/b", err: context.DeadlineExceeded})

	if a.tray.git != nil {
		t.Errorf("info of /w/a kept for /w/b: %+v", a.tray.git)
	}
}

func TestClockWake(t *testing.T) {
	base := time.Date(2026, 10, 26, 14, 32, 0, 0, time.UTC)
	tests := []struct {
		name      string
		now       time.Time
		last      string
		wantText  string
		wantAgain time.Duration
	}{
		{"new minute", base.Add(time.Minute), "14:32", "14:33", 0},
		{"woke early", base.Add(59*time.Second + 900*time.Millisecond), "14:32", "", 100 * time.Millisecond},
		{"clock stepped back", base.Add(-30 * time.Second), "14:32", "14:31", 0},
		{"first reading", base, "", "14:32", 0},
	}

	for _, tt := range tests {
		text, again := clockWake(tt.now, tt.last)
		if text != tt.wantText || again != tt.wantAgain {
			t.Errorf("%s: clockWake = %q, %v, want %q, %v", tt.name, text, again, tt.wantText, tt.wantAgain)
		}
	}
}

func TestLoadHistoryKeepsLastTen(t *testing.T) {
	a := newTestApp(t)

	for i := range 15 {
		a.Update(loadMsg{sample: loadSample{CPU: i, Mem: 1 << 30}})
	}

	want := []int{5, 6, 7, 8, 9, 10, 11, 12, 13, 14}
	if got := a.tray.load.CPU; !slices.Equal(got, want) {
		t.Errorf("history = %v, want %v", got, want)
	}
}

func TestLoadErrorKeepsPreviousAndRearms(t *testing.T) {
	a := newTestApp(t)
	a.load = &fakeLoad{}

	a.Update(loadMsg{sample: loadSample{CPU: 7, Mem: 1 << 30}})

	_, cmd := a.Update(loadMsg{err: errors.New("boom")})
	if cmd == nil {
		t.Error("a failed sample did not re-arm")
	}

	if a.tray.load == nil || a.tray.load.CPU[len(a.tray.load.CPU)-1] != 7 {
		t.Errorf("load after an error = %+v, want the previous value", a.tray.load)
	}
}

type fakeLoad struct{ calls atomic.Int32 }

func (f *fakeLoad) Sample(context.Context) (loadSample, error) {
	f.calls.Add(1)

	return loadSample{CPU: 3, Mem: 2 << 30}, nil
}

// gitCounter is a git runner that counts calls and answers from a script.
type gitCounter struct {
	calls   atomic.Int32
	started chan string
	release chan struct{}
}

func newGitCounter() *gitCounter {
	return &gitCounter{started: make(chan string, 16), release: make(chan struct{}, 16)}
}

func (g *gitCounter) run(_ context.Context, cwd string) ([]byte, error) {
	g.calls.Add(1)

	g.started <- cwd

	<-g.release

	return []byte("# branch.oid abc\x00# branch.head " + strings.ReplaceAll(strings.Trim(cwd, "/"), "/", "-") + "\x00"), nil
}

// No tray work runs inside Update: it only returns commands.
func TestNoIOInUpdate(t *testing.T) {
	a := newTestApp(t)
	g := newGitCounter()
	a.tray.gitRun = g.run
	fl := &fakeLoad{}
	a.load = fl

	a.Init()
	sized(t, a, 100, 30)
	openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/a", Split: core.SplitVertical})
	_, cmd := a.Update(gitTickMsg{})
	a.Update(clockMsg{text: "10:00"})
	a.Update(loadMsg{sample: loadSample{CPU: 1, Mem: 1 << 30}})

	if g.calls.Load() != 0 || fl.calls.Load() != 0 {
		t.Errorf("Update ran sampling: git %d load %d", g.calls.Load(), fl.calls.Load())
	}

	if cmd == nil {
		t.Error("the git tick did not return a command")
	}
}

func TestGitProbeOnFocusAndCwdChange(t *testing.T) {
	a := newTestApp(t)
	g := newGitCounter()
	a.tray.gitRun = g.run
	a.tray.on = true
	sized(t, a, 100, 30)

	openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/a", Split: core.SplitVertical})

	// A probe is queued for the new focus; run it.
	cmd := a.takeLater()
	if cmd == nil {
		t.Fatal("no probe queued after the first pane opened")
	}

	done := make(chan tea.Msg, 1)

	go func() { done <- cmd() }()

	if cwd := <-g.started; cwd != "/w/a" {
		t.Errorf("probe cwd = %q, want /w/a", cwd)
	}

	// While it is in flight, a focus change must not start a second probe.
	openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/b", Split: core.SplitVertical})

	if later := a.takeLater(); later != nil {
		t.Error("a second probe was queued while one was in flight")
	}

	g.release <- struct{}{}

	msg := <-done

	// The result is for a stale cwd: it is dropped and the focused cwd is probed.
	_, next := a.Update(msg)
	if a.tray.git != nil {
		t.Errorf("git shows %+v for a cwd that lost focus", a.tray.git)
	}

	if next == nil {
		t.Fatal("no follow-up probe for the new focus")
	}

	go func() { done <- next() }()

	if cwd := <-g.started; cwd != "/w/b" {
		t.Errorf("follow-up probe cwd = %q, want /w/b", cwd)
	}

	g.release <- struct{}{}

	a.Update(<-done)

	if a.tray.git == nil || a.tray.git.Branch != "w-b" {
		t.Errorf("git = %+v, want branch w-b", a.tray.git)
	}

	// A pane cwd change probes again.
	openIn(t, a, core.SetPaneCwd{ID: a.store.State().Tab().Focus, Cwd: "/w/c"})

	if a.takeLater() == nil {
		t.Error("a cwd change did not queue a probe")
	}
}

// takeLater returns the one command the app queued outside an update's return path.
func (a *App) takeLater() tea.Cmd {
	if len(a.later) == 0 {
		return nil
	}

	cmd := a.later[0]
	if len(a.later) != 1 {
		panic("takeLater: more than one queued command")
	}

	a.later = nil

	return cmd
}

// A tick probes the focused cwd when no probe is in flight.
func TestGitTickProbes(t *testing.T) {
	a := newTestApp(t)
	g := newGitCounter()
	a.tray.gitRun = g.run
	a.tray.on = true
	sized(t, a, 100, 30)
	openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/a", Split: core.SplitVertical})

	probe := a.takeLater()

	g.release <- struct{}{}

	a.Update(probe())

	if a.tray.gitBusy {
		t.Fatal("gitBusy after the result arrived")
	}

	a.Update(gitTickMsg{})

	if !a.tray.gitBusy {
		t.Error("the tick started no probe")
	}
}

// A timed-out probe stops the periodic probes until the focused pane or its
// cwd changes.
func TestGitTimeoutBacksOff(t *testing.T) {
	a := newTestApp(t)
	a.tray.on = true
	sized(t, a, 100, 30)
	openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/a", Split: core.SplitVertical})
	a.takeLater()
	a.tray.gitBusy = true
	a.Update(gitMsg{cwd: "/w/a", err: context.DeadlineExceeded})

	a.Update(gitTickMsg{})

	if a.tray.gitBusy {
		t.Fatal("the tick probed a cwd whose probe timed out")
	}

	// A new focus in the same cwd probes again, and a success resumes polling.
	openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/a", Split: core.SplitVertical})

	if a.takeLater() == nil || !a.tray.gitBusy {
		t.Fatal("a focus change did not probe again")
	}

	a.Update(gitMsg{cwd: "/w/a", info: &gitInfo{Branch: "main"}})
	a.Update(gitTickMsg{})

	if !a.tray.gitBusy {
		t.Error("polling did not resume after the focus change")
	}

	// A cwd change probes again too.
	a.Update(gitMsg{cwd: "/w/a", err: context.DeadlineExceeded})
	openIn(t, a, core.SetPaneCwd{ID: a.store.State().Tab().Focus, Cwd: "/w/b"})

	if a.takeLater() == nil {
		t.Error("a cwd change did not probe again")
	}
}

func TestBranchIsPrintable(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)
	a.tray.git = &gitInfo{Branch: "ma\u0085in"}

	if got := trayModuleByID(a, modGit).render(); strings.Contains(got, "\u0085") {
		t.Errorf("C1 control in the git module: %q", got)
	}
}

func TestGitStatusArgs(t *testing.T) {
	want := []string{"-c", "core.fsmonitor=false", "-C", "/work/repo", "status", "--porcelain=v2", "--branch", "-z"}
	if got := gitStatusArgs("/work/repo"); !slices.Equal(got, want) {
		t.Fatalf("gitStatusArgs = %q, want %q", got, want)
	}
}

func TestGitProbeEnv(t *testing.T) {
	inherited := []string{
		"GIT_DIR=/x",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.fsmonitor",
		"GIT_CONFIG_VALUE_0=/evil",
		"GIT_OPTIONAL_LOCKS=1",
		"GIT_INDEX_FILE=/i",
		"LANG=C.UTF-8",
	}

	want := []string{"LANG=C.UTF-8", "GIT_OPTIONAL_LOCKS=0"}
	if got := gitProbeEnv(inherited); !slices.Equal(got, want) {
		t.Fatalf("gitProbeEnv = %q, want %q", got, want)
	}
}
