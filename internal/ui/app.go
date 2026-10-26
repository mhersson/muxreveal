// Package ui is the Bubble Tea front end.
package ui

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui/palette"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// Options configures the root model.
type Options struct {
	Logger *slog.Logger
	Store  *core.Store
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
}

// New returns the root model. It fails when the store's theme is unknown.
func New(opts Options) (*App, error) {
	if opts.Logger == nil {
		return nil, errors.New("ui: Options.Logger is nil")
	}
	if opts.Store == nil {
		return nil, errors.New("ui: Options.Store is nil")
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
	a := &App{log: opts.Logger, store: opts.Store, reg: reg, keymap: km}
	if err := a.sync(); err != nil {
		return nil, err
	}
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

// dispatch applies act to the store and re-syncs the cached theme. A theme
// the theme package does not know is logged and rejected, leaving the store
// and the view as they were.
func (a *App) dispatch(act core.Action) error {
	if st, ok := act.(core.SetTheme); ok {
		if _, err := theme.ByName(st.Name); err != nil {
			a.log.Warn("unknown theme", "name", st.Name, "err", err)
			return err
		}
	}
	if err := a.store.Dispatch(act); err != nil {
		a.log.Error("dispatch action", "err", err)
		return err
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
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.log.Debug("window size", "width", msg.Width, "height", msg.Height)
	case tea.KeyPressMsg:
		if a.overlay != nil {
			return a, a.updateOverlay(msg)
		}
		return a, a.handleKey(msg.String())
	case palette.ChosenMsg:
		return a, a.chosen(msg.ID)
	case palette.ClosedMsg:
		a.closeOverlay()
	case palette.HighlightMsg:
		a.highlighted(msg.ID)
	case whichKeyMsg:
		a.popup = a.pending && msg.seq == a.seq
	default:
		// Paste and the input's own messages belong to the open palette.
		if a.overlay != nil {
			return a, a.updateOverlay(msg)
		}
	}
	return a, nil
}

// handleKey routes a key press through the leader state machine. Outside the
// leader sequence keys do nothing yet.
func (a *App) handleKey(key string) tea.Cmd {
	a.notice = ""
	if !a.pending {
		if key != a.keymap.leader {
			return nil
		}
		a.pending = true
		a.seq++
		seq := a.seq
		return tea.Tick(a.keymap.delay, func(time.Time) tea.Msg { return whichKeyMsg{seq: seq} })
	}
	a.pending, a.popup = false, false
	if key == "esc" || key == a.keymap.leader {
		return nil
	}
	id, ok := a.keymap.lookup(key)
	if !ok {
		a.notice = a.keymap.leader + " " + key + " is not bound"
		return nil
	}
	c, ok := a.reg.get(id)
	if !ok {
		a.log.Error("bound command missing", "key", key, "command", id)
		return nil
	}
	return c.run(a)
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
	bar := barState{leader: a.keymap.leader, pending: a.pending, notice: a.notice}
	hints := a.hints()
	full := make([]hint, len(hints))
	for i, h := range hints {
		full[i] = hint{key: a.keymap.leader + " " + h.key, title: h.title}
	}
	lk := a.current()
	content := shell(a.width, a.height, a.theme, lk.styles, lk.sample, full, bar)
	if a.popup {
		content = overlayWhichKey(content, a.width, a.height, a.keymap.leader, hints, lk.styles)
	}
	if a.overlay != nil {
		content = overlayOver(content, a.width, a.height, a.overlay.pal)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.BackgroundColor = lk.theme.UI.Bg
	v.ForegroundColor = lk.theme.UI.Fg
	return v
}
