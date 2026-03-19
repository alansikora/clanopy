package cmd

import (
	"fmt"
	"os"

	"github.com/alansikora/clanopy/internal/config"
	"github.com/alansikora/clanopy/internal/tui"
	"github.com/alansikora/clanopy/internal/update"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var Version = "dev"

// DisplayVersion returns the version formatted for display.
// Release versions keep their "v" prefix (e.g. "v1.2.3").
// Non-release versions like "canary" or "dev" have any "v" prefix stripped.
func DisplayVersion() string {
	v := Version
	if len(v) > 1 && v[0] == 'v' && (v[1] < '0' || v[1] > '9') {
		v = v[1:]
	}
	return v
}

var rootCmd = &cobra.Command{
	Use:   "clanopy",
	Short: "The canopy over your Claude Code",
	Long:  "Workspaces, reviews, and workflows for Claude Code.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		latestVersion := update.CheckUpdateNotice(DisplayVersion())
		m := tui.NewModel(cfg, DisplayVersion(), latestVersion)
		p := tea.NewProgram(m, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return nil
	},
}

func Execute() error {
	rootCmd.Version = DisplayVersion()
	return rootCmd.Execute()
}
