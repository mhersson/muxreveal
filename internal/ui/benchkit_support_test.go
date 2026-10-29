package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"runtime"
	"slices"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/term"
	"github.com/mhersson/muxreveal/internal/term/rec"
)

// This file is the replay bench: the real App with panes fed from recordings
// on a virtual clock. It builds in every test run; the gate run itself is in
// bench_test.go behind the bench tag.

// maxBenchPanes is the largest grid runBench lays out (3x2).
const maxBenchPanes = 6

// fakeClock is a settable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.t = t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.t = c.t.Add(d)
}

var errNoCwd = errors.New("replay pane has no cwd")

// replayProc is a pane process whose output the bench feeds itself: Read blocks
// until Close, so the pane's read loop ends with the pane.
type replayProc struct {
	done chan struct{}
	once sync.Once
}

func newReplayProc() *replayProc { return &replayProc{done: make(chan struct{})} }

func (r *replayProc) Read([]byte) (int, error) {
	<-r.done

	return 0, io.EOF
}

func (r *replayProc) Write(b []byte) (int, error) { return len(b), nil }
func (r *replayProc) Resize(int, int) error       { return nil }
func (r *replayProc) Done() <-chan struct{}       { return r.done }
func (r *replayProc) Cwd() (string, error)        { return "", errNoCwd }
func (r *replayProc) Foreground() (string, error) { return "", errNoCwd }

func (r *replayProc) Close() error {
	r.once.Do(func() { close(r.done) })

	return nil
}

// summary is the nearest-rank percentiles of a set of durations.
type summary struct {
	Count                     int
	P50, P95, P99, Max, Total time.Duration
}

type frameTimes struct{ ds []time.Duration }

func (f *frameTimes) add(d time.Duration) { f.ds = append(f.ds, d) }

func (f *frameTimes) summary() summary {
	n := len(f.ds)
	if n == 0 {
		return summary{}
	}

	sorted := slices.Clone(f.ds)
	slices.Sort(sorted)

	var total time.Duration
	for _, d := range sorted {
		total += d
	}

	rank := func(p float64) time.Duration {
		return sorted[max(int(math.Ceil(p*float64(n)))-1, 0)]
	}

	return summary{Count: n, P50: rank(0.50), P95: rank(0.95), P99: rank(0.99), Max: sorted[n-1], Total: total}
}

// benchConfig describes one run. Pane i replays Recordings[i%len] from
// Start + i*Stagger for Length.
type benchConfig struct {
	Panes                  int
	Recordings             [][]rec.Record
	Width, Height          int
	Start, Length, Stagger time.Duration
	FPS                    int
	// Selection highlights every content row of the first pane for the whole run.
	Selection bool
	// Agents makes every pane a working agent whose badge spinner advances every spinEvery.
	Agents bool
	// Mode is the bench tab's layout mode; "" is bsp.
	Mode core.LayoutMode
	// Rail opens the agents rail before the run.
	Rail bool
	// Review opens the review drawer on the first pane over a 40-file result; a new result arrives every benchReviewEvery.
	Review bool
	Trace  func(at time.Duration, drew bool) // tests only; nil in production
}

// benchScrollingColumnWidth is the column width (percent) that keeps all six
// bench panes visible in scrolling mode.
const benchScrollingColumnWidth = 16

// parseBenchMode maps MUXREVEAL_BENCH_MODE to a layout mode; "" is bsp.
func parseBenchMode(s string) (core.LayoutMode, error) {
	switch s {
	case "", string(core.ModeBSP):
		return core.ModeBSP, nil
	case string(core.ModeMaster):
		return core.ModeMaster, nil
	case string(core.ModeScrolling):
		return core.ModeScrolling, nil
	}

	return "", fmt.Errorf("MUXREVEAL_BENCH_MODE %q is not one of bsp, master-stack, scrolling", s)
}

// parseBenchRail maps MUXREVEAL_BENCH_RAIL to whether the rail is open; "" is closed.
func parseBenchRail(s string) (bool, error) { return parseBenchSwitch("MUXREVEAL_BENCH_RAIL", s) }

// parseBenchReview maps MUXREVEAL_BENCH_REVIEW to whether the drawer is open; "" is closed.
func parseBenchReview(s string) (bool, error) { return parseBenchSwitch("MUXREVEAL_BENCH_REVIEW", s) }

// parseBenchSwitch reads an on/off knob: "" and "0" are off, "1" is on.
func parseBenchSwitch(name, s string) (bool, error) {
	switch s {
	case "", "0":
		return false, nil
	case "1":
		return true, nil
	}

	return false, fmt.Errorf("%s %q is not 0 or 1", name, s)
}

type benchResult struct {
	Panes, Width, Height  int
	PaneSizes             [][2]int // guest grid per pane, in open order
	Frames                summary  // feed + update + view + present, per drawn frame
	Feed, Render, Present summary  // components per drawn frame; Render = update + view
	CPUPercent            float64  // all step work / Length * 100
	BytesFed              int64
	Steps                 int
	Updates               int // pane Update calls in timed steps
	Reviews               int // results the fake engine served in timed steps
	Fallbacks             int // frames whose drawer the compositor drew because the splice declined
	GCs                   uint32
	GCPause               time.Duration
}

// replayFeed walks one pane's records; they are sorted by At.
type replayFeed struct {
	pane  *pane
	recs  []rec.Record
	start time.Duration
	next  int
}

// feedBefore feeds every data record with At < limit and returns the bytes fed.
// Recorded resizes are skipped: the layout sets the pane size.
func (f *replayFeed) feedBefore(limit time.Duration) int64 {
	var n int64

	for f.next < len(f.recs) && f.recs[f.next].At < limit {
		if d := f.recs[f.next].Data; d != nil {
			f.pane.feed(d)

			n += int64(len(d))
		}

		f.next++
	}

	return n
}

// presenter mirrors Bubble Tea v2.1.0's cursedRenderer.flush in alt screen
// mode, writing to io.Discard.
type presenter struct {
	scr *uv.TerminalRenderer
	buf uv.ScreenBuffer
}

func newPresenter(w, h int) *presenter {
	scr := uv.NewTerminalRenderer(io.Discard, []string{"TERM=xterm-ghostty"})
	scr.SetColorProfile(colorprofile.TrueColor)
	scr.SetTabStops(w)
	scr.SetBackspace(true)
	scr.SetScrollOptim(runtime.GOOS != "windows")
	scr.Resize(w, h)
	scr.SetFullscreen(true)
	scr.SetRelativeCursor(false)
	scr.Erase()

	return &presenter{scr: scr, buf: uv.NewScreenBuffer(w, h)}
}

func (p *presenter) present(frame string) error {
	content := uv.NewStyledString(frame)

	p.buf.Clear()
	content.Draw(p.buf, p.buf.Bounds())
	p.scr.Render(p.buf.RenderBuffer)

	if err := p.scr.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}

	return nil
}

func (c benchConfig) validate() error {
	if c.Panes <= 0 || c.Panes > maxBenchPanes {
		return fmt.Errorf("bench needs 1 to %d panes, got %d", maxBenchPanes, c.Panes)
	}

	if len(c.Recordings) == 0 || c.FPS <= 0 || c.Length <= 0 || c.Width <= 0 || c.Height <= 1 {
		return fmt.Errorf("bench needs a recording, fps, length and size (got %d recordings, %d fps, %v, %dx%d)",
			len(c.Recordings), c.FPS, c.Length, c.Width, c.Height)
	}

	for i := range c.Panes {
		need := c.Start + time.Duration(i)*c.Stagger + c.Length
		if have := rec.Duration(c.Recordings[i%len(c.Recordings)]); have < need {
			return fmt.Errorf("pane %d needs a recording of %v, have %v", i, need, have)
		}
	}

	return nil
}

// benchGrid is the columns and rows runBench lays n panes out in.
func benchGrid(n int) (cols, rows int) {
	if n <= 2 {
		return n, 1
	}

	return (n + 1) / 2, 2
}

// newBenchApp builds an App of replay panes in a grid, sized width x height,
// and returns the pane ids in open order. Columns come first (side by side),
// then each top pane is split to open a row below it.
func newBenchApp(cfg benchConfig, clk clock) (*App, []core.PaneID, error) {
	conf := core.DefaultConfig()
	if cfg.Mode != "" {
		conf.Layout.DefaultMode = string(cfg.Mode)
	}

	if cfg.Mode == core.ModeScrolling {
		conf.Layout.ColumnWidth = benchScrollingColumnWidth
	}

	app, err := New(Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:  core.NewStore(core.InitialState(conf)),
		Getenv: func(string) string { return "" },
		Cwd:    "/",
		Icons:  true,
	})
	if err != nil {
		return nil, nil, err
	}

	// A copy must never reach the operator's real clipboard tools.
	app.clip.run = (&fakeRun{}).run

	app.clock = clk
	app.start = func(context.Context, term.Command, int, int) (proc, error) { return newReplayProc(), nil }

	// The pane commands run on the update loop; their tea.Cmds are never run, so no wall time passes.
	app.Update(tea.WindowSizeMsg{Width: cfg.Width, Height: cfg.Height})

	ids := []core.PaneID{app.store.State().Tab().Focus}
	open := func(split core.Split) {
		app.openPane(core.KindShell, split)
		ids = append(ids, app.store.State().Tab().Focus)
	}

	cols, _ := benchGrid(cfg.Panes)
	for range cols - 1 {
		open(core.SplitVertical)
	}

	// Three columns: the first takes about a third instead of half. The
	// other modes keep their own default sizes.
	if cols == 3 && (cfg.Mode == "" || cfg.Mode == core.ModeBSP) {
		_ = app.dispatch(core.FocusPane{ID: ids[0]})

		for range 3 {
			_ = app.act(core.ResizeDir{Dir: core.Left})
		}
	}

	for i := range cfg.Panes - cols {
		_ = app.dispatch(core.FocusPane{ID: ids[i]})

		open(core.SplitHorizontal)
	}

	if len(ids) != cfg.Panes || len(app.panes) != cfg.Panes {
		_ = app.Close(context.Background())

		return nil, nil, fmt.Errorf("opened %d of %d panes", len(app.panes), cfg.Panes)
	}

	if cfg.Agents {
		for _, id := range ids {
			for _, act := range []core.Action{
				core.SetPaneKind{ID: id, Kind: core.KindAgent, Harness: "claude-code"},
				core.SetAgentState{ID: id, State: core.AgentWorking},
			} {
				if err := app.dispatch(act); err != nil {
					_ = app.Close(context.Background())

					return nil, nil, fmt.Errorf("make pane %s a working agent: %w", id, err)
				}
			}
		}
	}

	if cfg.Rail {
		// The probe and relayout cmds are not run: the bench has no wall time.
		_ = app.railToggle()
		app.rail.focused = false
	}

	if cfg.Review {
		if err := openBenchDrawer(app, ids[0]); err != nil {
			_ = app.Close(context.Background())

			return nil, nil, err
		}
	}

	return app, ids, nil
}

const (
	// benchReviewFiles is the size of the result the fake engine serves.
	benchReviewFiles = 40
	// benchReviewHunks is how many hunks each of its files has.
	benchReviewHunks = 2
	// benchReviewEvery is the virtual time between two results.
	benchReviewEvery = 500 * time.Millisecond
	// benchReviewRoot is the worktree the reviewed pane resolves to.
	benchReviewRoot = "/bench/repo"
)

// benchReviewHunk is one hunk of a file of the fake engine's result; seq is the
// number the hunk's last stamp carries, 0 before it has one.
func benchReviewHunk(file, hunk, seq int) git.Hunk {
	start := 10 + hunk*40

	return hunkSpec(start, start, fmt.Sprintf("func (s *Server) handle%d_%d(w http.ResponseWriter, r *http.Request)", file, hunk),
		" \tctx := r.Context()",
		" \tlog := s.log.With(\"handler\", \"handle\")",
		"-\tid := r.URL.Query().Get(\"id\")",
		"+\tid := mux.Vars(r)[\"id\"]",
		" \tif id == \"\" {",
		" \t\thttp.Error(w, \"missing id\", http.StatusBadRequest)",
		" \t\treturn",
		" \t}",
		"-\titem, err := s.store.Get(id)",
		"+\titem, err := s.store.Get(ctx, id)",
		fmt.Sprintf("+\tstamp := %d", seq),
		" \tif err != nil {",
		" \t\tlog.Error(\"get\", \"id\", id, \"err\", err)",
		" \t\treturn",
		" \t}",
	)
}

// benchReviewResult is the fake engine's result number rev: 40 files of two
// hunks. Each result after the first changes one hunk of the first file, the
// one at the top of the drawer, so every result shows.
func benchReviewResult(rev int) git.Result {
	files := make([]git.FileDiff, benchReviewFiles)

	for i := range files {
		hunks := make([]git.Hunk, benchReviewHunks)

		for j := range hunks {
			seq := 0
			if i == 0 && rev-1 >= j {
				seq = rev - (rev-1-j)%benchReviewHunks
			}

			hunks[j] = benchReviewHunk(i, j, seq)
		}

		files[i] = fileSpec(fmt.Sprintf("internal/pkg%02d/file%02d.go", i/4, i), git.Modified, hunks...)
	}

	return git.Result{Old: "base", New: fmt.Sprintf("tree%d", rev), Live: true, Files: files}
}

// benchReviewMsg is the answer the engine's goroutine would send for the active
// tab's load of res; live is an answer to a watcher batch, not to a new source.
func benchReviewMsg(app *App, res git.Result, live bool) reviewLoadedMsg {
	tab := app.store.State().Tab().ID

	return reviewLoadedMsg{
		tab: tab, gen: app.drawer.views[tab].epoch, res: res, live: live,
		info: reviewInfo{project: "bench", branch: "feat/bench", base: "main", hidden: hiddenFlags(res.Files)},
	}
}

// openBenchDrawer opens the drawer on pane in branch mode, with its tree open
// and the keyboard, and answers its first load with the fake engine's first result. The app has
// no engine: an answer is the one message a load ends in, so the sync that
// starts the load and the apply of its answer run as in the app. The load's
// command is not run, so its context is ended here.
func openBenchDrawer(app *App, pane core.PaneID) error {
	if err := app.dispatch(core.OpenReview{Pane: pane, Mode: core.ReviewBranch}); err != nil {
		return fmt.Errorf("open the drawer: %w", err)
	}

	if err := app.dispatch(core.SetReviewTree{Open: true}); err != nil {
		return fmt.Errorf("open the drawer's tree: %w", err)
	}

	app.watches.panes[pane] = &paneWatch{cwd: app.store.State().Panes[pane].Cwd, want: benchReviewRoot}
	_ = app.reviewSync()
	app.drawerTake()

	tab := app.store.State().Tab().ID
	if v := app.drawer.views[tab]; v == nil || !v.loading {
		return errors.New("the drawer did not start a load")
	}

	app.drawer.views[tab].cancel()
	app.Update(benchReviewMsg(app, benchReviewResult(0), false))

	switch v := app.drawer.views[tab]; {
	case v.err != nil:
		return fmt.Errorf("apply the first result: %w", v.err)
	case !v.loaded:
		return errors.New("the drawer did not apply the first result")
	}

	return nil
}

// selectWholePane selects the pane's whole history and more, so every content
// row stays highlighted in every frame while the replay scrolls.
func selectWholePane(app *App, id core.PaneID) {
	cols, _ := app.panes[id].size()

	app.sel = &paneSelection{
		pane:   id,
		mode:   selStream,
		anchor: stablePoint{},
		head:   stablePoint{row: 1 << 40, col: cols - 1},
	}
}

// holdEnd is when the pane's current sync hold ends.
func (p *pane) holdEnd() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.openedAt.Add(syncMaxHold)
}

// runBench replays the recordings through the real App on a virtual clock and
// times feed + update + view + present for every drawn frame.
//
// Fairness: every pane replays the same recording window (offset by Stagger),
// history before the window is fed untimed, the GC runs before timing, steps
// where nothing was fed or released are not counted, and the caller takes the
// median of three runs.
func runBench(cfg benchConfig) (benchResult, error) {
	if err := cfg.validate(); err != nil {
		return benchResult{}, err
	}

	t0 := time.Unix(1_000_000, 0)
	clk := &fakeClock{t: t0}

	app, ids, err := newBenchApp(cfg, clk)
	if err != nil {
		return benchResult{}, err
	}

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = app.Close(ctx)
	}()

	feeds := make([]*replayFeed, cfg.Panes)
	res := benchResult{Panes: cfg.Panes, Width: cfg.Width, Height: cfg.Height}
	tab := app.store.State().Tab().ID
	rev := 0

	for i, id := range ids {
		pn := app.panes[id]
		cols, rows := pn.size()
		res.PaneSizes = append(res.PaneSizes, [2]int{cols, rows})
		feeds[i] = &replayFeed{pane: pn, recs: cfg.Recordings[i%len(cfg.Recordings)], start: cfg.Start + time.Duration(i)*cfg.Stagger}
	}

	// A hold's tick would fire on a timer in the app; the bench delivers it when the virtual clock passes it.
	armed := map[core.PaneID]time.Time{}
	send := func(id core.PaneID, msg tea.Msg) {
		app.Update(msg)

		if p := app.panes[id]; p != nil && p.holdArmed {
			if _, ok := armed[id]; !ok {
				armed[id] = p.holdEnd()
			}
		}
	}

	// Untimed warm-up: history before each pane's window, then one full frame.
	for i, f := range feeds {
		f.feedBefore(f.start)
		send(ids[i], paneDirtyMsg{id: ids[i], p: app.panes[ids[i]]})
	}

	if cfg.Selection {
		selectWholePane(app, ids[0])
	}

	pr := newPresenter(cfg.Width, cfg.Height)
	last := app.View().Content

	if err := pr.present(last); err != nil {
		return benchResult{}, fmt.Errorf("present: %w", err)
	}

	runtime.GC()

	var mem0, mem1 runtime.MemStats

	runtime.ReadMemStats(&mem0)

	fi := time.Second / time.Duration(cfg.FPS)
	res.Steps = int((cfg.Length*time.Duration(cfg.FPS) + time.Second - 1) / time.Second)

	var frames, feedS, renderS, presentS frameTimes

	var total time.Duration

	fed := make([]bool, cfg.Panes)

	for k := 1; k <= res.Steps; k++ {
		at := time.Duration(k) * fi
		now := t0.Add(at)
		clk.Set(now)

		// The engine builds its answer off the update loop, so it is built before timing.
		var answer *reviewLoadedMsg

		if cfg.Review && at/benchReviewEvery > (at-fi)/benchReviewEvery {
			rev++
			msg := benchReviewMsg(app, benchReviewResult(rev), true)
			answer = &msg
		}

		t := time.Now()

		for i, f := range feeds {
			n := f.feedBefore(f.start + at)
			fed[i] = n > 0
			res.BytesFed += n
		}

		feedD := time.Since(t)
		total += feedD

		t = time.Now()
		updates := 0

		for i, id := range ids {
			if fed[i] {
				send(id, paneDirtyMsg{id: id, p: app.panes[id]})

				updates++
			}

			if end, ok := armed[id]; ok && !now.Before(end) {
				delete(armed, id)
				send(id, holdExpiredMsg{id: id, p: app.panes[id]})

				updates++
			}
		}

		// The agent tick advances the badge spinner every spinEvery while an agent works.
		if cfg.Agents && at/spinEvery > (at-fi)/spinEvery {
			app.spin++

			updates++
		}

		if answer != nil {
			app.Update(*answer)

			res.Reviews++
			updates++
		}

		// The flash of rows an answer added ends on a tick in the app; a later answer moves its end.
		if cfg.Review {
			if v := app.drawer.views[tab]; v != nil && !v.flashUntil.IsZero() && !now.Before(v.flashUntil) {
				app.Update(reviewFlashMsg{tab: tab})

				updates++
			}
		}

		res.Updates += updates
		drew := false

		if updates > 0 {
			frame := app.View().Content
			renderD := time.Since(t)
			total += renderD

			if frame != last {
				last = frame
				drew = true

				t = time.Now()
				err := pr.present(frame)
				presentD := time.Since(t)
				total += presentD

				if err != nil {
					return benchResult{}, fmt.Errorf("present: %w", err)
				}

				frames.add(feedD + renderD + presentD)
				feedS.add(feedD)
				renderS.add(renderD)
				presentS.add(presentD)
			}
		}

		if cfg.Trace != nil {
			cfg.Trace(at, drew)
		}
	}

	runtime.ReadMemStats(&mem1)

	res.GCs, res.GCPause = mem1.NumGC-mem0.NumGC, time.Duration(mem1.PauseTotalNs-mem0.PauseTotalNs)
	res.Fallbacks = app.drawer.fallbacks
	res.Frames, res.Feed, res.Render, res.Present = frames.summary(), feedS.summary(), renderS.summary(), presentS.summary()
	res.CPUPercent = float64(total) / float64(cfg.Length) * 100

	return res, nil
}
