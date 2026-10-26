// Package ui is the Bubble Tea front end.
package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/muxreveal/internal/core"
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
}

// App is the root Bubble Tea model.
type App struct {
	log           *slog.Logger
	store         *core.Store
	width, height int

	reg     *registry
	keymap  keymap
	pending bool   // leader pressed, awaiting a key
	popup   bool   // which-key popup visible; only while pending
	seq     int    // leader press counter; a whichKeyMsg with another seq is stale
	notice  string // status-bar message until the next key press

	// theme, styles and sample follow store.State().Theme; only sync updates them.
	theme  theme.Theme
	styles styles
	sample []string

	overlay *overlay
	preview *look // theme picker preview; nil unless previewing
	// reportedFg and reportedBg are the colours last given to the panes.
	reportedFg, reportedBg theme.Hex
	confirm                *confirm // yes/no question; owns every key while open

	panes        map[core.PaneID]*pane // runtime panes, keyed like State.Panes
	outerFocused bool                  // the outer terminal has focus; true until a BlurMsg

	getenv    func(string) string
	cwd       string
	recordDir string // empty unless recording
	started   bool   // the first size message was handled; it opens the first pane only once
	clock     clock  // the sync hold's time source
	start     func(ctx context.Context, c term.Command, cols, rows int) (proc, error)

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
	a := &App{
		log: opts.Logger, store: opts.Store, reg: reg, keymap: km,
		panes: map[core.PaneID]*pane{}, outerFocused: true,
		getenv: getenv, cwd: cwd, recordDir: opts.RecordDir, clock: realClock{}, start: termStart,
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
	before := a.store.State().Tab().Focus
	if err := a.store.Dispatch(act); err != nil {
		a.log.Error("dispatch action", "err", err)
		return err
	}
	a.reportFocusChange(before, a.store.State().Tab().Focus)
	if _, ok := act.(core.SetTheme); !ok {
		return nil
	}
	if err := a.sync(); err != nil {
		a.log.Error("sync theme", "err", err)
		return err
	}
	return nil
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := a.update(msg)
	a.syncPaneColors()

	return m, cmd
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
		a.width, a.height = msg.Width, msg.Height
		a.log.Debug("window size", "width", msg.Width, "height", msg.Height)
		var cmds []tea.Cmd
		if !a.started {
			a.started = true
			if a.store.State().Tab().Layout == nil {
				cmds = append(cmds, a.openPane(core.KindShell, core.SplitVertical))
			}
		}
		cmds = append(cmds, a.relayout())
		return a, tea.Batch(cmds...)
	case tea.KeyPressMsg:
		return a, a.handleKey(msg)
	case tea.PasteMsg:
		return a, a.handlePaste(msg)
	case tea.FocusMsg:
		a.outerFocused = true
		a.sendToFocused(func(p *pane) { p.sendFocus(true) })
	case tea.BlurMsg:
		a.outerFocused = false
		a.sendToFocused(func(p *pane) { p.sendFocus(false) })
	case paneDirtyMsg:
		return a, a.onPaneDirty(msg.id)
	case holdExpiredMsg:
		return a, a.onHoldExpired(msg.id)
	case paneExitedMsg:
		return a, a.onPaneExited(msg.id)
	case paneCwdMsg:
		a.onCwd(msg)
	case paneCwdDueMsg:
		return a, a.onCwdDue(msg.id)
	case palette.ChosenMsg:
		return a, a.chosen(msg.ID)
	case palette.ClosedMsg:
		a.closeOverlay()
	case palette.HighlightMsg:
		a.highlighted(msg.ID)
	case whichKeyMsg:
		a.popup = a.pending && msg.seq == a.seq
	default:
		// The input's own messages belong to the open palette.
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

// runLeader handles a key press in the leader state machine. It reports
// whether the key was consumed; a key outside a pending sequence that is not
// the leader is not.
func (a *App) runLeader(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	key := bindingKey(msg)
	if !a.pending {
		if key != a.keymap.leader {
			return nil, false
		}
		a.pending = true
		a.seq++
		seq := a.seq
		return tea.Tick(a.keymap.delay, func(time.Time) tea.Msg { return whichKeyMsg{seq: seq} }), true
	}
	a.pending, a.popup = false, false
	if key == "esc" {
		return nil, true
	}
	if key == a.keymap.leader {
		a.sendToFocused(func(p *pane) { p.sendKey(uv.KeyPressEvent(msg)) })
		return nil, true
	}
	id, ok := a.keymap.lookup(key)
	if !ok {
		a.notice = a.keymap.leader + " " + key + " is not bound"
		return nil, true
	}
	c, ok := a.reg.get(id)
	if !ok {
		a.log.Error("bound command missing", "key", key, "command", id)
		return nil, true
	}
	return c.run(a), true
}

// hints lists the bound commands with their command titles.
func (a *App) hints() []hint {
	hints := make([]hint, 0, len(a.keymap.bindings))
	for _, b := range a.keymap.bindings {
		if c, ok := a.reg.get(b.command); ok {
			hints = append(hints, hint{key: b.key, title: c.title})
		}
	}
	return hints
}

// View implements tea.Model. The shell is empty until the first
// WindowSizeMsg, so nothing is drawn at a guessed size.
func (a *App) View() tea.View {
	hints := a.hints()
	lk := a.current()
	g := a.geometry()
	content := a.frameAt(g)
	if a.popup {
		content = overlayWhichKey(content, a.width, a.height, a.keymap.leader, hints, lk.styles)
	}
	if a.overlay != nil {
		content = overlayOver(content, a.width, a.height, a.overlay.pal)
	}
	if a.confirm != nil {
		content = overlayConfirm(content, a.width, a.height, *a.confirm, lk.styles)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.ReportFocus = true
	// Modified keys arrive with their shifted character, which the legacy encoder needs.
	v.KeyboardEnhancements.ReportAlternateKeys = true
	v.Cursor = a.cursorAt(g)
	v.BackgroundColor = lk.theme.UI.Bg
	v.ForegroundColor = lk.theme.UI.Fg
	return v
}
