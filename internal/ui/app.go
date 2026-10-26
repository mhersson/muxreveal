// Package ui is the Bubble Tea front end.
package ui

import (
	"errors"
	"fmt"
	"log/slog"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
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

	// theme, styles and sample follow store.State().Theme; only sync updates them.
	theme  theme.Theme
	styles styles
	sample []string
}

// New returns the root model. It fails when the store's theme is unknown.
func New(opts Options) (*App, error) {
	if opts.Logger == nil {
		return nil, errors.New("ui: Options.Logger is nil")
	}
	if opts.Store == nil {
		return nil, errors.New("ui: Options.Store is nil")
	}
	a := &App{log: opts.Logger, store: opts.Store}
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
	sample, err := highlight(welcomeSample, "go", th)
	if err != nil {
		return fmt.Errorf("highlight welcome sample: %w", err)
	}
	a.theme, a.styles, a.sample = th, newStyles(th), sample
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
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
	}
	return a, nil
}

// View implements tea.Model. The shell is empty until the first
// WindowSizeMsg, so nothing is drawn at a guessed size.
func (a *App) View() tea.View {
	v := tea.NewView(shell(a.width, a.height, a.theme, a.styles, a.sample))
	v.AltScreen = true
	v.BackgroundColor = a.theme.UI.Bg
	v.ForegroundColor = a.theme.UI.Fg
	return v
}
