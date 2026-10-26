// Command airmixer runs the Air-Mixer server.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/yeixio/air-mixer/internal/app"
	"github.com/yeixio/air-mixer/internal/config"
	"github.com/yeixio/air-mixer/internal/logging"
)

// Set at build time with -ldflags "-X main.Version=...".
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configFile string
	root := &cobra.Command{
		Use:           "airmixer",
		Short:         "Route and mix audio between speakers from different ecosystems",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.PersistentFlags().StringVarP(&configFile, "config", "c", "", "config file (default: ./config.yaml, ~/.airmixer/config.yaml or /etc/airmixer/config.yaml)")

	server := &cobra.Command{
		Use:     "server",
		Aliases: []string{"start", "serve"},
		Short:   "Run the Air-Mixer server",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configFile)
			if err != nil {
				return err
			}
			log := logging.New(os.Stderr, cfg.Logging)
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return app.Run(ctx, app.Options{Config: cfg, Logger: log, Version: Version})
		},
	}

	version := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "airmixer %s (commit %s, built %s, %s %s/%s)\n",
				Version, GitCommit, BuildTime, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		},
	}

	root.AddCommand(server, version)
	root.SetContext(context.Background())
	return root
}
