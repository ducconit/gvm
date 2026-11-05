package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/ducconit/gvm/internal/goversion"
	"github.com/ducconit/gvm/internal/manager"
	"github.com/ducconit/gvm/internal/platform"
	"github.com/ducconit/gvm/internal/store"
	"github.com/ducconit/gvm/internal/updater"
	"github.com/ducconit/gvm/internal/version"
	"github.com/spf13/cobra"
)

// Dependencies holds injectable dependencies for the CLI commands.
type Dependencies struct {
	Config  platform.EnvConfig
	Store   *store.Store
	Remote  goversion.RemoteClient
	Manager *manager.Manager
	Updater *updater.Updater
	In      io.Reader
	Out     io.Writer
	ErrOut  io.Writer
}

// NewDefaultDependencies builds production dependencies using OS environment detection.
func NewDefaultDependencies(in io.Reader, out, errOut io.Writer) (*Dependencies, error) {
	cfg, err := platform.Detect()
	if err != nil {
		return nil, err
	}
	st, err := store.New(cfg)
	if err != nil {
		return nil, err
	}
	remote := goversion.NewHTTPClient()
	mgr := manager.New(st, remote, out)
	upd := updater.New(cfg.ExecutablePath, cfg.GOOS, cfg.GOARCH, version.Version)

	return &Dependencies{
		Config:  cfg,
		Store:   st,
		Remote:  remote,
		Manager: mgr,
		Updater: upd,
		In:      in,
		Out:     out,
		ErrOut:  errOut,
	}, nil
}

// NewRootCmd constructs the root `g` Cobra command and all subcommands.
func NewRootCmd(deps *Dependencies) *cobra.Command {
	var showVersion bool
	var mirrorURL string

	rootCmd := &cobra.Command{
		Use:   "g",
		Short: "Cross-platform Golang version manager (macOS, Linux, Windows)",
		Long: `g is a fast, cross-platform Golang version manager for darwin (macOS), linux, and windows.

Common commands:
  g install [version] | g i [version]   Install a Go version, alias, or auto-detect from go.mod/.go-version
  g use [version]                       Set default Go version (or auto-detect from go.mod/.go-version)
  g alias [name] [version]              List aliases or set a favorite alias (e.g. g alias like 1.27)
  g unalias <name>                      Remove a configured version alias
  g exec <version> -- <cmd> [args...]   Run a command with a specific Go version without switching default
  g ls [-r|--remote]                    List installed or official remote Go versions
  g uninstall [version] [--all]         Uninstall a Go version, all unused versions, or g itself
  g clean [--cache] [--modcache]        Clean temporary downloads and Go build/module caches
  g doctor                              Diagnose GVM_HOME, GOROOT symlink, PATH, and network health
  g update [version]                    Self-update g CLI to latest or specified version
  g -v | g version                      Show g CLI version and active Go version
  g env                                 Show active Go environment variables
  g mcp                                 Run Model Context Protocol (MCP) server over stdio
  g skills install [--path] [--ide]     Install AI agent skills`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if mirrorURL != "" {
				if hc, ok := deps.Remote.(*goversion.HTTPClient); ok {
					hc.SetMirror(mirrorURL)
				}
			}
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				return printVersionInfo(deps)
			}
			return cmd.Help()
		},
	}

	rootCmd.SetIn(deps.In)
	rootCmd.SetOut(deps.Out)
	rootCmd.SetErr(deps.ErrOut)

	rootCmd.Flags().BoolVarP(&showVersion, "version", "v", false, "Show g CLI version and active Go version")
	rootCmd.PersistentFlags().StringVar(&mirrorURL, "mirror", "", "Custom Go download mirror base URL (or set GVM_MIRROR)")

	rootCmd.AddCommand(
		newInstallCmd(deps),
		newAliasCmd(deps),
		newUnaliasCmd(deps),
		newUpdateCmd(deps),
		newVersionCmd(deps),
		newLsCmd(deps),
		newUninstallCmd(deps),
		newUseCmd(deps),
		newExecCmd(deps),
		newCleanCmd(deps),
		newDoctorCmd(deps),
		newEnvCmd(deps),
		newMcpCmd(deps),
		newSkillsCmd(deps),
	)

	return rootCmd
}

// Execute runs the CLI with the provided arguments and I/O streams.
func Execute(ctx context.Context, args []string, deps *Dependencies) error {
	rootCmd := NewRootCmd(deps)
	rootCmd.SetArgs(args)
	return rootCmd.ExecuteContext(ctx)
}

func printVersionInfo(deps *Dependencies) error {
	cfg := deps.Config
	fmt.Fprintf(deps.Out, "g version %s (%s/%s)\n", version.Version, cfg.GOOS, cfg.GOARCH)

	active, _ := deps.Store.ActiveVersion()
	if active != "" {
		activeDir := cfg.VersionDir(active)
		goBin := cfg.GoBinaryPath(activeDir)
		if out, err := exec.Command(goBin, "version").Output(); err == nil {
			fmt.Fprintf(deps.Out, "Active Go: %s (%s)\n", strings.TrimSpace(string(out)), activeDir)
			return nil
		}
		fmt.Fprintf(deps.Out, "Active Go: go%s (%s/%s) at %s\n", active, cfg.GOOS, cfg.GOARCH, activeDir)
		return nil
	}

	if out, err := exec.Command("go", "version").Output(); err == nil {
		fmt.Fprintf(deps.Out, "Active Go: (none managed by g; system: %s)\n", strings.TrimSpace(string(out)))
		return nil
	}
	fmt.Fprintln(deps.Out, "Active Go: (none installed or selected; run 'g install latest')")
	return nil
}
