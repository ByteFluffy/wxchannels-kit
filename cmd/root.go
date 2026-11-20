package cmd

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"wx_channel/internal/application"
	"wx_channel/internal/config"
)

var (
	Version                                    string
	Cfg                                        *config.Config
	device, config_filepath, workdir, hostname string
	port                                       int
	debug                                      bool
	start_transferred                          bool
	capture_consent                            bool
	capture_api_only                           bool
)

var error_prefix = color.RedString("[ERROR]")

var root_cmd = &cobra.Command{
	Use: "wxc", Short: "Personal-use, source-owned local WeChat Channels downloader",
	Long: "A local Go application with shared CLI/MCP downloads. Capture is never started implicitly; use start --accept-system-changes only after reviewing the setup instructions.",
	PersistentPreRunE: func(command *cobra.Command, args []string) error {
		isStart := command.Name() == "start"
		if isStart && !capture_api_only && !capture_consent {
			return errors.New("capture requires --accept-system-changes: it may install a local CA and change the system proxy; no changes were made")
		}
		if err := application.PrepareConfig(Cfg, config_filepath); err != nil {
			return err
		}
		Cfg.CaptureAuthorized = isStart && !capture_api_only && capture_consent
		Cfg.Update("update.sources", []config.UpdateSource{})
		Cfg.Update("bridge.enabled", false)
		Cfg.Update("download.remoteServer.enabled", false)
		// A config file alone is not authorization to alter networking.
		if !Cfg.CaptureAuthorized {
			Cfg.Update("proxy.enabled", false)
			Cfg.Update("proxy.system", false)
			Cfg.Update("proxy.skipInstallRootCert", true)
		}
		shouldExit, err := application.PrepareStartPrivileges(isStart && Cfg.CaptureAuthorized)
		start_transferred = shouldExit
		return err
	},
	RunE:          func(command *cobra.Command, args []string) error { return command.Help() },
	SilenceErrors: true, SilenceUsage: true,
}

func init() {
	root_cmd.PersistentFlags().StringVar(&device, "dev", "", "Network service to use for explicitly enabled capture")
	root_cmd.PersistentFlags().StringVarP(&config_filepath, "config", "c", "", "Configuration file")
	root_cmd.PersistentFlags().StringVar(&workdir, "workdir", "", "Private runtime directory")
	root_cmd.PersistentFlags().StringVar(&hostname, "hostname", "127.0.0.1", "Capture listener hostname")
	root_cmd.PersistentFlags().IntVar(&port, "port", 2023, "Capture listener port")
	root_cmd.PersistentFlags().BoolVar(&debug, "debug", false, "Enable diagnostics")
	_ = viper.BindPFlag("workdir", root_cmd.PersistentFlags().Lookup("workdir"))
	_ = viper.BindPFlag("debug.error", root_cmd.PersistentFlags().Lookup("debug"))
	_ = viper.BindPFlag("proxy.networkService", root_cmd.PersistentFlags().Lookup("dev"))
	_ = viper.BindPFlag("proxy.hostname", root_cmd.PersistentFlags().Lookup("hostname"))
	_ = viper.BindPFlag("proxy.port", root_cmd.PersistentFlags().Lookup("port"))
}

func Execute(cfg *config.Config) error {
	cobra.MousetrapHelpText = ""
	Version, Cfg = cfg.Version, cfg
	start_transferred = false
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// Errors remain errors: no Enter-to-exit pause and no successful exit code
	// after a failed startup. main prints errors to stderr, never MCP stdout.
	return root_cmd.ExecuteContext(ctx)
}

func Register(command *cobra.Command) { root_cmd.AddCommand(command) }
