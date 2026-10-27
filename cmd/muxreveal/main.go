// Command muxreveal is the MuxReveal binary: CLI and wiring.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

// closeTimeout bounds how long quitting waits for pane processes to end.
const closeTimeout = 3 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	err := newRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "muxreveal: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	env, envErr := core.DefaultEnv()
	var configPath, recordDir string
	cmd := &cobra.Command{
		Use:           "muxreveal",
		Short:         "Coding agents in terminal panes, with their diffs and pull requests beside them",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if envErr != nil {
				return envErr
			}
			// Only the default path may be missing; a path the user named must exist.
			if cmd.Flags().Changed("config") {
				if _, err := os.Stat(configPath); err != nil {
					return fmt.Errorf("config: %w", err)
				}
			}
			return run(cmd.Context(), env, configPath, recordDir)
		},
	}
	defaultConfig := ""
	if envErr == nil {
		defaultConfig = core.ConfigPath(env)
	}
	cmd.Flags().StringVar(&configPath, "config", defaultConfig, "config file")
	cmd.Flags().StringVar(&recordDir, "record", "", "write each pane's terminal output to a recording in this directory")
	// Diagnostics for bench inputs and VT fixtures, not a user feature.
	_ = cmd.Flags().MarkHidden("record")
	cmd.AddCommand(newHookCmd(os.Getenv))

	return cmd
}

// run loads the config and opens the log before the TUI takes over the
// terminal, so their errors can still be printed.
func run(ctx context.Context, env core.Env, configPath, recordDir string, progOpts ...tea.ProgramOption) (err error) {
	cfg, err := core.LoadConfig(configPath)
	if err != nil {
		return err
	}
	if _, err := theme.ByName(cfg.Theme); err != nil {
		return fmt.Errorf("config %s: %w", configPath, err)
	}
	f, err := openLog(core.StateDir(env))
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close log: %w", cerr)
		}
	}()

	logger := slog.New(slog.NewTextHandler(f, nil))
	hooks := startHooks(logger, core.StateDir(env))
	defer hooks.Stop()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("launch directory: %w", err)
	}
	app, err := ui.New(ui.Options{
		Logger: logger,
		Store:  core.NewStore(core.InitialState(cfg)),
		Getenv: os.Getenv,
		Cwd:    cwd,
		Icons:  cfg.IconsOn(),

		FocusFollowsMouse: cfg.Mouse.FocusFollowsMouse,
		CopyOnSelect:      cfg.Mouse.CopyOnSelect,
		NoOSC52:           !cfg.Clipboard.OSC52On(),
		NoNativeClipboard: !cfg.Clipboard.NativeOn(),

		RecordDir: recordDir,

		HookSettings: hooks.Settings,
		HookSock:     hooks.Sock,
		Events:       hooks.Events,
		Notice:       hooks.Notice,
	})
	if err != nil {
		return err
	}

	logger.Info("start", "config", configPath, "theme", cfg.Theme)
	// ctx is the only signal handler, so SIGINT, SIGTERM and SIGHUP (a closed
	// terminal) end the run cleanly instead of racing Bubble Tea's own handler.
	opts := append([]tea.ProgramOption{tea.WithContext(ctx), tea.WithoutSignalHandler()}, progOpts...)
	_, err = tea.NewProgram(app, opts...).Run()
	// The context may be cancelled already, so the close gets its own deadline.
	closeCtx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	if cerr := app.Close(closeCtx); cerr != nil {
		logger.Error("close panes", "err", cerr)
	}
	if err != nil && ctx.Err() == nil {
		logger.Error("run", "err", err)
		return fmt.Errorf("run: %w", err)
	}
	logger.Info("exit")
	return nil
}
