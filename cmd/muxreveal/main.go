// Command muxreveal is the MuxReveal binary: CLI and wiring.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/ui"
	"github.com/mhersson/muxreveal/internal/ui/theme"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "muxreveal: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	env, envErr := core.DefaultEnv()
	var configPath string
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
			return run(cmd.Context(), env, configPath)
		},
	}
	defaultConfig := ""
	if envErr == nil {
		defaultConfig = core.ConfigPath(env)
	}
	cmd.Flags().StringVar(&configPath, "config", defaultConfig, "config file")
	return cmd
}

// run loads the config and opens the log before the TUI takes over the
// terminal, so their errors can still be printed.
func run(ctx context.Context, env core.Env, configPath string) (err error) {
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
	app, err := ui.New(ui.Options{Logger: logger, Store: core.NewStore(core.State(cfg))})
	if err != nil {
		return err
	}

	logger.Info("start", "config", configPath, "theme", cfg.Theme)
	// ctx is the only signal handler, so SIGINT and SIGTERM both end the run
	// cleanly instead of racing Bubble Tea's own handler.
	_, err = tea.NewProgram(app, tea.WithContext(ctx), tea.WithoutSignalHandler()).Run()
	if err != nil && ctx.Err() == nil {
		logger.Error("run", "err", err)
		return fmt.Errorf("run: %w", err)
	}
	logger.Info("exit")
	return nil
}
