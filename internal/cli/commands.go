package cli

import (
	"fmt"
	"strings"

	"github.com/ducconit/gvm/internal/mcp"
	"github.com/ducconit/gvm/internal/platform"
	"github.com/ducconit/gvm/internal/skills"
	"github.com/spf13/cobra"
)

func newInstallCmd(deps *Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:     "install <version>",
		Aliases: []string{"i"},
		Short:   "Install a specified Go version or alias (e.g. g install 1.27, g i like)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			iv, err := deps.Manager.Install(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			activeNote := ""
			if iv.Active {
				activeNote = " (active default)"
			}
			fmt.Fprintf(deps.Out, "Successfully installed Go %s at %s%s\n", iv.Version, iv.Path, activeNote)
			return nil
		},
	}
}

func newAliasCmd(deps *Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "alias <name> <version>",
		Short: "Set a favorite version alias (e.g. g alias like 1.27)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, target := args[0], args[1]
			if err := deps.Manager.Alias(name, target); err != nil {
				return err
			}
			fmt.Fprintf(deps.Out, "Alias %q -> %q saved in %s\n", name, target, deps.Config.AliasesFile())
			return nil
		},
	}
}

func newUpdateCmd(deps *Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "update [version]",
		Short: "Self-update the g CLI to the latest or specified version",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			updatedVer, err := deps.Updater.SelfUpdate(cmd.Context(), target)
			if err != nil {
				return err
			}
			fmt.Fprintf(deps.Out, "Successfully updated g CLI to v%s (%s)\n", updatedVer, deps.Config.ExecutablePath)
			return nil
		},
	}
}

func newVersionCmd(deps *Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show g CLI version and active Go version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return printVersionInfo(deps)
		},
	}
}

func newLsCmd(deps *Dependencies) *cobra.Command {
	var remoteFlag bool
	var allUnstable bool

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List installed Go versions (or official remote releases with -r / --remote)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if remoteFlag {
				remotes, err := deps.Manager.ListRemote(cmd.Context(), allUnstable)
				if err != nil {
					return err
				}
				fmt.Fprintf(deps.Out, "Official Go releases for %s/%s:\n", deps.Config.GOOS, deps.Config.GOARCH)
				for _, r := range remotes {
					marker := "  "
					if r.Active {
						marker = "* "
					} else if r.Installed {
						marker = "+ "
					}
					var tags []string
					if r.Active {
						tags = append(tags, "active")
					} else if r.Installed {
						tags = append(tags, "installed")
					}
					if !r.Stable {
						tags = append(tags, "prerelease")
					}
					if len(r.Aliases) > 0 {
						tags = append(tags, "alias: "+strings.Join(r.Aliases, ", "))
					}
					suffix := ""
					if len(tags) > 0 {
						suffix = " (" + strings.Join(tags, " | ") + ")"
					}
					fmt.Fprintf(deps.Out, "%s%-12s%s\n", marker, r.Version, suffix)
				}
				return nil
			}

			installed, err := deps.Manager.ListLocal()
			if err != nil {
				return err
			}
			aliases, _ := deps.Store.GetAliases()
			if len(installed) == 0 {
				fmt.Fprintf(deps.Out, "No Go versions installed in %s\n", deps.Config.VersionsDir())
				if len(aliases) > 0 {
					fmt.Fprintln(deps.Out, "\nConfigured aliases:")
					for k, v := range aliases {
						fmt.Fprintf(deps.Out, "  %s -> %s\n", k, v)
					}
				}
				return nil
			}

			fmt.Fprintf(deps.Out, "Installed Go versions (in %s):\n", deps.Config.VersionsDir())
			for _, iv := range installed {
				marker := "  "
				if iv.Active {
					marker = "* "
				}
				aliasStr := "-"
				if len(iv.Aliases) > 0 {
					aliasStr = strings.Join(iv.Aliases, ", ")
				}
				fmt.Fprintf(deps.Out, "%s%-10s  [alias: %s]  %s\n", marker, iv.Version, aliasStr, iv.Path)
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&remoteFlag, "remote", "r", false, "List official Go versions from go.dev/dl")
	cmd.Flags().BoolVar(&allUnstable, "all", false, "Include unstable RC/beta versions when listing remote releases")
	return cmd
}

func newUninstallCmd(deps *Dependencies) *cobra.Command {
	var allFlag bool

	cmd := &cobra.Command{
		Use:   "uninstall [version]",
		Short: "Uninstall a Go version/alias, all unused Go versions (--all), or the g CLI itself (no args)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Case 1: `g uninstall` with no version argument and no --all flag -> uninstall the g CLI itself
			if len(args) == 0 && !allFlag {
				if err := deps.Manager.UninstallSelf(); err != nil {
					return err
				}
				fmt.Fprintf(deps.Out, "Uninstalled g CLI (%s) and removed %s\n", deps.Config.ExecutablePath, deps.Config.GVMHome)
				return nil
			}

			// Case 2: `g uninstall <version>` or `g uninstall --all` or `g uninstall <version> --all`
			versionSpec := ""
			if len(args) == 1 {
				versionSpec = args[0]
			}
			removed, err := deps.Manager.UninstallVersion(versionSpec, allFlag)
			if err != nil {
				return err
			}
			if len(removed) == 0 {
				active, _ := deps.Store.ActiveVersion()
				fmt.Fprintf(deps.Out, "No versions removed (active version %q kept)\n", active)
				return nil
			}
			for _, v := range removed {
				fmt.Fprintf(deps.Out, "Uninstalled Go %s (%s)\n", v, deps.Config.VersionDir(v))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&allFlag, "all", false, "Remove all installed Go versions except the currently active version")
	return cmd
}

func newUseCmd(deps *Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "use <version>",
		Short: "Set a specific Go version or alias as the active default",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			iv, err := deps.Manager.Use(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(deps.Out, "Now using Go %s (%s)\n", iv.Version, iv.Path)
			return nil
		},
	}
}

func newEnvCmd(deps *Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "env",
		Short: "Display the active Go and g environment configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			active, _ := deps.Store.ActiveVersion()
			fmt.Fprint(deps.Out, platform.FormatEnvExport(deps.Config, active))
			return nil
		},
	}
}

func newMcpCmd(deps *Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start the Model Context Protocol (MCP) server over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			srv := mcp.New(deps.Manager)
			return srv.Serve(cmd.Context(), deps.In, deps.Out)
		},
	}
}

func newSkillsCmd(deps *Dependencies) *cobra.Command {
	skillsCmd := &cobra.Command{
		Use:   "skills",
		Short: "Manage AI agent skills for the g CLI",
	}

	var customPath string
	var ideName string

	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install the g agent skill (defaults to ~/.agents/skills, or use --path / --ide)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			targetDir, files, err := skills.Install(deps.Config.HomeDir, customPath, ideName)
			if err != nil {
				return err
			}
			fmt.Fprintf(deps.Out, "Installed g agent skill to %s (%d file(s)):\n", targetDir, len(files))
			for _, f := range files {
				fmt.Fprintf(deps.Out, "  - %s\n", f)
			}
			return nil
		},
	}

	installCmd.Flags().StringVar(&customPath, "path", "", "Custom directory path to install skills")
	installCmd.Flags().StringVar(&ideName, "ide", "", "Quick IDE target (e.g. antigravity -> ~/.gemini/config/skills, claude, cursor, codex, opencode, windsurf)")

	skillsCmd.AddCommand(installCmd)
	return skillsCmd
}
