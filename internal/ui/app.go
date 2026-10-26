// Package ui is the Bubble Tea front end.
package ui

import (
	"errors"
	"log/slog"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Options configures the root model.
type Options struct {
	Logger *slog.Logger
}

// App is the root Bubble Tea model.
type App struct {
	log           *slog.Logger
	width, height int
}

// New returns the root model.
func New(opts Options) (*App, error) {
	if opts.Logger == nil {
		return nil, errors.New("ui: Options.Logger is nil")
	}
	return &App{log: opts.Logger}, nil
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

// View implements tea.Model.
func (a *App) View() tea.View {
	v := tea.NewView(a.content())
	v.AltScreen = true
	return v
}

// content is empty until the first WindowSizeMsg, so nothing is drawn at a
// guessed size.
func (a *App) content() string {
	if a.width <= 0 || a.height <= 0 {
		return ""
	}
	title := lipgloss.NewStyle().MaxWidth(a.width).Render("MuxReveal")
	return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, title)
}
