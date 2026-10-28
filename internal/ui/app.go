// Package ui is the Bubble Tea front end.
package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/harness"
	"github.com/mhersson/muxreveal/internal/term"
	"github.com/mhersson/muxreveal/internal/ui/palette"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// Options configures the root model.
type Options struct {
	Logger *slog.Logger
	Store  *core.Store
	// Getenv reads the environment for the pane shell ($SHELL); nil means os.Getenv.
	Getenv func(string) string
	// Cwd is the launch directory, where the first pane starts; empty means os.Getwd.
	Cwd string
	// RecordDir, when set, receives one recording per pane (diagnostics); empty means off.
	RecordDir string
	// Icons draws Nerd Font glyphs; false drops every one.
	Icons bool
	// FocusFollowsMouse focuses the pane under the pointer; CopyOnSelect copies
	// a selection when the button is released.
	FocusFollowsMouse, CopyOnSelect bool
	// NoOSC52 and NoNativeClipboard turn off a copy path; the zero value keeps both on.
	NoOSC52, NoNativeClipboard bool
	// Harness launches and recognises agents; nil means harness.Claude{}.
	Harness harness.Harness
	// HookSettings is the --settings file for agents; empty launches them without hooks.
	HookSettings string
	// HookSock is MUXREVEAL_SOCK for every pane; empty sets none.
	HookSock string
	// Events delivers hook events; nil means none arrive.
	Events <-chan harness.Event
	// Notice is shown as a toast at start; empty for none.
	Notice string
	// GitTempDir is where the diff engine puts its temporary index for the
	// rail's diff stats; empty turns the stats off.
	GitTempDir string
	// IndexCache is the directory where git keeps a refreshed index copy per
	// worktree; empty disables the cache.
	IndexCache string
	// Watches shares one git watcher per worktree among the agent panes in it;
	// nil means no watches.
	Watches *git.WatchSet
	// Diff is the [diff] config: the review drawer's starting layout and mode and
	// the diff engine's limits. The zero value means the defaults.
	Diff core.DiffConfig
	// Worktrees is the [worktrees] config: where a new worktree goes. The zero
	// value means inside the main checkout.
	Worktrees core.WorktreesConfig
	// Save is called with the state after every successful dispatch; nil saves nothing.
	Save func(core.State)
}

// App is the root Bubble Tea model.
type App struct {
	log           *slog.Logger
	store         *core.Store
	width, height int

	reg     *registry
	keymap  keymap
	pending bool   // leader pressed, awaiting a key
	prefix  string // first key of a two-key sequence while pending; "" before it
	popup   bool   // which-key popup visible; only while pending with no prefix
	seq     int    // leader press counter; a whichKeyMsg with another seq is stale
	notice  string // toast message until the next key press

	// theme, styles and sample follow store.State().Theme; only sync updates them.
	theme  theme.Theme
	styles styles
	sample []string

	overlay *overlay
	preview *look // theme picker preview; nil unless previewing
	// reportedFg and reportedBg are the colours last given to the panes.
	reportedFg, reportedBg theme.Hex
	confirm                *confirm // yes/no question; owns every key while open
	prompt                 *prompt  // text input; owns every key and paste while open

	panes        map[core.PaneID]*pane // runtime panes, keyed like State.Panes
	later        []tea.Cmd             // commands raised outside the update's return path; Update batches them
	outerFocused bool                  // the outer terminal has focus; true until a BlurMsg

	getenv    func(string) string
	cwd       string
	recordDir string // empty unless recording
	icons     bool   // draw Nerd Font glyphs
	started   bool   // the first size message was handled; it opens the first pane only once
	restoring bool   // startRestored is running; its dispatches are not saved
	save      func(core.State)
	clock     clock // the sync hold's and the tray's time source
	tray      trayState
	rail      railState
	drawer    drawerState
	picker    pickerState
	watches   watchState
	storeVer  uint64        // counts the dispatches the store accepted
	engine    *git.Engine   // nil when the rail's diff stats and the review drawer's diffs are off
	probeSem  chan struct{} // its capacity bounds the rail's probes running at once
	clip      *clipboard
	sel       *paneSelection // the one selection; survives tab switches
	copy      *copyState     // copy mode; owns every key and ignores the mouse while set
	mouse     mouseState

	focusFollows, copyOnSelect bool
	harness                    harness.Harness
	tick                       func(time.Duration, func(time.Time) tea.Msg) tea.Cmd // tea.Tick; tests replace it
	hookSettings, hookSock     string
	events                     <-chan harness.Event
	spin                       int         // badge spinner frame
	agentTicking               bool        // an agentTickMsg is outstanding
	slideTicking               bool        // a drawerSlideMsg is outstanding
	drawersOpen                bool        // some tab's state holds a review drawer; only dispatch changes it
	settled                    settleKey   // what settleDrawer last found nothing to change in
	load                       loadSampler // nil on platforms without a load reading
	start                      func(ctx context.Context, c term.Command, cols, rows int) (proc, error)

	// ctx is the lifetime of the pane processes; Close cancels it.
	ctx     context.Context
	cancel  context.CancelFunc
	closing sync.WaitGroup // pane closes running off the update loop
}

// New returns the root model. It fails when the store's theme is unknown.
func New(opts Options) (*App, error) {
	if opts.Logger == nil {
		return nil, errors.New("ui: Options.Logger is nil")
	}

	if opts.Store == nil {
		return nil, errors.New("ui: Options.Store is nil")
	}

	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	cwd := opts.Cwd
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("ui: launch directory: %w", err)
		}

		cwd = wd
	}

	if opts.RecordDir != "" {
		if err := os.MkdirAll(opts.RecordDir, recordDirMode); err != nil {
			return nil, fmt.Errorf("ui: recording directory: %w", err)
		}
	}

	reg, err := newRegistry(defaultCommands())
	if err != nil {
		return nil, fmt.Errorf("build command registry: %w", err)
	}

	km := defaultKeymap()
	for _, b := range km.bindings {
		if _, ok := reg.get(b.command); !ok {
			return nil, fmt.Errorf("ui: binding %q names unknown command %q", b.key, b.command)
		}
	}

	h := opts.Harness
	if h == nil {
		h = harness.Claude{}
	}

	a := &App{
		harness: h, tick: tea.Tick, hookSettings: opts.HookSettings, hookSock: opts.HookSock,
		events: opts.Events, notice: opts.Notice, save: opts.Save,
		log: opts.Logger, store: opts.Store, reg: reg, keymap: km,
		panes: map[core.PaneID]*pane{}, outerFocused: true,
		watches: watchState{set: opts.Watches, open: git.Open, panes: map[core.PaneID]*paneWatch{}, failed: map[string]bool{}},
		getenv:  getenv, cwd: cwd, recordDir: opts.RecordDir, icons: opts.Icons, clock: realClock{}, start: termStart,
		load:         newSysLoad(),
		probeSem:     make(chan struct{}, railProbeMax),
		focusFollows: opts.FocusFollowsMouse, copyOnSelect: opts.CopyOnSelect,
		clip: newClipboard(clipboardConfig{OSC52: !opts.NoOSC52, Native: !opts.NoNativeClipboard}, getenv, runtime.GOOS, opts.Logger),
	}

	a.drawer.cfg = opts.Diff
	a.picker.cfg = opts.Worktrees
	a.drawersOpen = anyReview(opts.Store.State())

	if opts.GitTempDir != "" {
		a.engine = git.NewEngine(git.EngineOptions{
			TempDir: opts.GitTempDir, IndexCache: opts.IndexCache,
			Limits: git.Limits{Files: opts.Diff.MaxFiles, Bytes: opts.Diff.MaxBytes, Lines: opts.Diff.MaxLines},
		})
	}

	if err := a.sync(); err != nil {
		return nil, err
	}

	a.ctx, a.cancel = context.WithCancel(context.Background())

	return a, nil
}

// sync loads the store's theme into the cached theme, styles and sample.
func (a *App) sync() error {
	th, err := theme.ByName(a.store.State().Theme)
	if err != nil {
		return err
	}

	lk, err := newLook(th)
	if err != nil {
		return err
	}

	a.theme, a.styles, a.sample = lk.theme, lk.styles, lk.sample

	return nil
}

// dispatch applies act to the store and re-syncs the cached theme after a
// theme change. A theme the theme package does not know is logged and
// rejected, leaving the store and the view as they were.
func (a *App) dispatch(act core.Action) error {
	if st, ok := act.(core.SetTheme); ok {
		if _, err := theme.ByName(st.Name); err != nil {
			a.log.Warn("unknown theme", "name", st.Name, "err", err)

			return err
		}
	}

	// A drawer sliding out is closed before an action that could change the active
	// tab or the drawer, so it is never left open in a tab that is not shown.
	if a.drawer.closing && mayChangeTab(act) {
		a.endSlide()
	}

	prev := a.store.State().Tab()
	if err := a.store.Dispatch(act); err != nil {
		if expectedError(err) {
			a.log.Debug("dispatch refused", "err", err)
		} else {
			a.log.Error("dispatch action", "err", err)
		}

		return err
	}

	a.storeVer++

	st := a.store.State()
	a.drawersOpen = anyReview(st)

	if a.save != nil && !a.restoring {
		a.save(st)
	}

	a.dropGoneTab(st)
	a.reportFocusChange(prev.Focus, st.Tab().Focus)

	if st.Tab().ID != prev.ID {
		a.endSlide()
		a.renderStale(st)
	}

	if _, ok := act.(core.SetTheme); !ok {
		return nil
	}

	if err := a.sync(); err != nil {
		a.log.Error("sync theme", "err", err)

		return err
	}

	return nil
}

// anyReview reports whether some tab of st has a review drawer open.
func anyReview(st core.State) bool {
	return slices.ContainsFunc(st.Tabs, func(t core.Tab) bool { return t.Review != nil })
}

// mayChangeTab reports whether act can change the active tab or its drawer.
func mayChangeTab(act core.Action) bool {
	switch act.(type) {
	case core.SelectTab, core.CycleTab, core.NewTab, core.CloseTab, core.ClosePane, core.MovePane, core.BreakPane,
		core.OpenReview, core.CloseReview:
		return true
	}

	return false
}

// dropGoneTab closes a confirm or prompt about a tab that no longer exists, and
// the worktree picker once another tab shows.
func (a *App) dropGoneTab(st core.State) {
	has := func(id core.TabID) bool {
		return slices.ContainsFunc(st.Tabs, func(t core.Tab) bool { return t.ID == id })
	}

	if a.confirm != nil && a.confirm.tab != "" && !has(a.confirm.tab) {
		a.confirm = nil
	}

	if a.prompt != nil && a.prompt.kind == promptRename && !has(a.prompt.tab) {
		a.prompt = nil
	}

	if a.picker.open != nil && st.Tab().ID != a.picker.open.tab {
		a.picker.open = nil
	}
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd {
	var rail tea.Cmd
	if a.store.State().Rail {
		rail = a.railOpened()
	}

	return tea.Batch(a.initTray(), a.waitEvent(), a.waitWatch(), rail)
}

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	check := a.keyboardCheck(msg)
	m, cmd := a.update(msg)

	check()
	a.syncPaneColors()
	a.railAfterUpdate()

	if c := a.watchSync(); c != nil {
		a.later = append(a.later, c)
	}

	if c := a.reviewSync(); c != nil {
		a.later = append(a.later, c)
	}

	if c := a.drawerAfterUpdate(); c != nil {
		a.later = append(a.later, c)
	}

	if len(a.later) > 0 {
		cmd = tea.Batch(append(a.later, cmd)...)
		a.later = nil
	}

	return m, cmd
}

// keyboardCheck starts watching a message and returns the check to run once it
// is handled. If a user-input message moved the active tab or its focused pane,
// the keyboard returns to the panes from the rail or the drawer, whichever had
// it; changes with other causes (a process exit, a hook, a restore) leave it
// alone. The drawer is then settled (see settleDrawer).
func (a *App) keyboardCheck(msg tea.Msg) func() {
	user := isUserInput(msg)
	railWas, drawerWas, ver := a.rail.focused, a.drawer.focused, a.storeVer

	// The state before the update is copied only when something can depend on it:
	// the rail or a drawer has the keyboard, or a drawer is open in some tab.
	var prev core.State
	if user && (railWas || drawerWas || a.drawersOpen) {
		prev = a.store.State()
	}

	return func() {
		acted := user && a.storeVer != ver

		if acted && (railWas || drawerWas) {
			if now, was := a.store.State().Tab(), prev.Tab(); now.ID != was.ID || now.Focus != was.Focus {
				if railWas {
					a.railRelease()
				}

				if drawerWas {
					a.drawerRelease()
				}
			}
		}

		a.settleDrawer(prev, acted)
	}
}

// isUserInput reports whether msg comes from the operator: keys, pastes, mouse
// events, a palette choice and the answers that carry out a new agent request.
func isUserInput(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg, tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg, palette.ChosenMsg,
		pickerOpenMsg, pickerCreatedMsg:
		return true
	}

	return false
}

// syncPaneColors tells every pane the current look's colours, including a
// theme picker preview, so guests that query the background pick a matching palette.
func (a *App) syncPaneColors() {
	ui := a.current().theme.UI
	if ui.Fg == a.reportedFg && ui.Bg == a.reportedBg {
		return
	}

	a.reportedFg, a.reportedBg = ui.Fg, ui.Bg

	for _, p := range a.panes {
		p.setColors(ui.Fg, ui.Bg)
	}
}

func (a *App) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.endSlide()

		a.width, a.height = msg.Width, msg.Height
		a.log.Debug("window size", "width", msg.Width, "height", msg.Height)

		var cmds []tea.Cmd

		if !a.started {
			a.started = true
			if len(a.store.State().Panes) > 0 {
				cmds = append(cmds, a.startRestored())
			} else if a.store.State().Tab().Empty() {
				cmds = append(cmds, a.openPane(core.KindShell, core.SplitVertical))
			}
		}

		cmds = append(cmds, a.relayout())

		return a, tea.Batch(cmds...)
	case tea.KeyPressMsg:
		return a, a.handleKey(msg)
	case tea.PasteMsg:
		return a, a.handlePaste(msg)
	case tea.MouseWheelMsg:
		a.handleWheel(msg)
	case tea.MouseClickMsg:
		return a, a.handleClick(msg)
	case tea.MouseMotionMsg:
		return a, a.handleMotion(msg)
	case tea.MouseReleaseMsg:
		return a, a.handleRelease(msg)
	case tea.FocusMsg:
		a.outerFocused = true
		a.sendToFocused(func(p *pane) { p.sendFocus(true) })
	case tea.BlurMsg:
		a.outerFocused = false
		a.sendToFocused(func(p *pane) { p.sendFocus(false) })
	case paneDirtyMsg:
		return a, a.onPaneDirty(msg)
	case holdExpiredMsg:
		return a, a.onHoldExpired(msg)
	case paneExitedMsg:
		return a, a.onPaneExited(msg)
	case paneCwdMsg:
		return a, a.onCwd(msg)
	case paneCwdDueMsg:
		return a, a.onCwdDue(msg)
	case paneKindDueMsg:
		return a, a.onKindDue(msg)
	case hookMsg:
		return a, tea.Batch(a.onHook(msg.ev), a.waitEvent())
	case watchMsg:
		return a, tea.Batch(a.onWatch(msg), a.waitWatch())
	case paneRepoMsg:
		return a, a.onPaneRepo(msg)
	case watchAcquiredMsg:
		a.onWatchAcquired(msg)
	case agentTickMsg:
		return a, a.onAgentTick()
	case clockMsg:
		return a, a.onClock(msg)
	case loadMsg:
		return a, a.onLoad(msg)
	case railHeadMsg:
		return a, a.onRailHead(msg)
	case railStatsMsg:
		return a, a.onRailStats(msg)
	case railNameMsg:
		return a, a.onRailName(msg)
	case railDueMsg:
		return a, a.onRailDue(msg)
	case railPulseMsg:
		a.onRailPulse()
	case railFlashMsg:
		a.onRailFlash(msg)
	case drawerPaintMsg:
	case reviewOpenMsg:
		return a, a.onReviewOpen(msg)
	case slideOpenMsg:
		return a, a.onSlideOpen(msg)
	case drawerSlideMsg:
		return a, a.onDrawerSlide()
	case reviewLoadedMsg:
		return a, a.onReviewLoaded(msg)
	case reviewFullMsg:
		return a, a.onReviewFull(msg)
	case reviewFlashMsg:
		a.onReviewFlash(msg)
	case pickerOpenMsg:
		return a, a.onPickerOpen(msg)
	case pickerPageMsg:
		a.onPickerPage(msg)
	case pickerCreatedMsg:
		return a, a.onPickerCreated(msg)
	case palette.ChosenMsg:
		return a, a.chosen(msg.ID)
	case palette.ClosedMsg:
		a.closeOverlay()
	case palette.HighlightMsg:
		a.highlighted(msg.ID)
	case whichKeyMsg:
		a.popup = a.pending && a.prefix == "" && msg.seq == a.seq
	default:
		// The input's own messages belong to the open prompt or palette.
		if a.prompt != nil {
			return a, a.updatePrompt(msg)
		}

		if a.overlay != nil {
			return a, a.updateOverlay(msg)
		}
	}

	return a, nil
}

// bindingKey names a key for keymap lookup. A shift-only key without text
// resolves to its shifted character, so "H" and ":" match however the
// terminal reports them.
func bindingKey(msg tea.KeyPressMsg) string {
	if msg.Text == "" && msg.Mod == tea.ModShift && msg.ShiftedCode != 0 {
		return string(msg.ShiftedCode)
	}

	return msg.String()
}

// sequenceOpen reports whether a sequence prefix waits for its second key,
// which shows the sequence popup at once.
func (a *App) sequenceOpen() bool { return a.pending && a.prefix != "" }

// endLeader drops a pending leader sequence and its which-key popup.
func (a *App) endLeader() {
	a.pending, a.popup, a.prefix = false, false, ""
}

// runLeader handles a key press in the leader state machine. It reports
// whether the key was consumed; a key outside a pending sequence that is not
// the leader is not.
func (a *App) runLeader(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	key := bindingKey(msg)
	if !a.pending {
		if key != a.keymap.leader {
			return nil, false
		}

		a.pending, a.prefix = true, ""
		a.seq++
		seq := a.seq

		return tea.Tick(a.keymap.delay, func(time.Time) tea.Msg { return whichKeyMsg{seq: seq} }), true
	}

	prefix := a.prefix
	if prefix == "" && a.keymap.isPrefix(key) {
		if key == ";" && !a.tabIsBSP() {
			a.notice = noticePreselectBSP
			a.endLeader()

			return nil, true
		}

		a.prefix, a.popup = key, false

		return nil, true
	}

	a.endLeader()

	if prefix != "" {
		key = prefix + " " + key
	}

	if id, ok := a.keymap.lookup(key); ok {
		c, ok := a.reg.get(id)
		if !ok {
			a.log.Error("bound command missing", "key", key, "command", id)

			return nil, true
		}

		return c.run(a), true
	}

	switch bindingKey(msg) {
	case "esc":
		return nil, true
	case a.keymap.leader:
		if !a.rail.focused && !a.drawerHasKeys() {
			a.keyToFocused(msg)
		}

		return nil, true
	}

	a.notice = a.keymap.leader + " " + key + " is not bound"

	return nil, true
}

// View implements tea.Model. The shell is empty until the first
// WindowSizeMsg, so nothing is drawn at a guessed size.
func (a *App) View() tea.View {
	lk := a.current()
	g := a.geometry()

	content := a.frameAt(g)
	if r, ok := a.drawerRect(a.store.State()); ok {
		content = overlayAt(content, a.width, a.height, r.X, r.Y, a.drawerLines(a.store.State(), r))
	}

	content = overlayToast(content, a.width, a.height, a.notice, a.icons, lk.styles)
	if a.sequenceOpen() {
		content = overlayCorner(content, a.width, a.height, sequencePopup(a.keymap.leader, a.prefix, a.icons, lk.styles))
	}

	if a.popup {
		content = overlayWhichKey(content, a.width, a.height, a.keymap.leader, a.hints(), a.icons, lk.styles)
	}

	if a.overlay != nil {
		content = overlayOver(content, a.width, a.height, a.overlay.pal)
	}

	if a.picker.open != nil {
		content = a.overlayPicker(content, lk)
	}

	if a.confirm != nil {
		content = overlayConfirm(content, a.width, a.height, *a.confirm, a.icons, lk.styles)
	}

	v := tea.NewView(content)
	v.AltScreen = true

	v.MouseMode = tea.MouseModeCellMotion
	if a.focusFollows {
		v.MouseMode = tea.MouseModeAllMotion
	}

	v.ReportFocus = true
	// Modified keys arrive with their shifted character, which the legacy encoder needs.
	v.KeyboardEnhancements.ReportAlternateKeys = true

	v.Cursor = a.cursorAt(g)
	if a.picker.open != nil {
		v.Cursor = a.pickerCursor()
	}

	v.BackgroundColor = lk.theme.UI.Bg
	v.ForegroundColor = lk.theme.UI.Fg

	return v
}
