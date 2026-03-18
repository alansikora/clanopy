package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alansikora/clanopy/internal/review"
	"github.com/spf13/cobra"
)

var reviewGenerateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate a review config by analyzing the project with Claude",
	RunE: func(cmd *cobra.Command, args []string) error {
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		force, _ := cmd.Flags().GetBool("force")

		fmt.Fprintf(os.Stderr, "Analyzing project...\n")

		yamlStr, err := review.Generate()
		if err != nil {
			return fmt.Errorf("generating config: %w", err)
		}

		if dryRun {
			fmt.Print(yamlStr)
			return nil
		}

		configDir := ".clanopy"
		configPath := filepath.Join(configDir, "review.yml")

		// Back up existing file unless --force.
		if _, err := os.Stat(configPath); err == nil {
			if !force {
				bakPath := configPath + ".bak"
				data, err := os.ReadFile(configPath)
				if err != nil {
					return fmt.Errorf("reading existing config for backup: %w", err)
				}
				if err := os.WriteFile(bakPath, data, 0644); err != nil {
					return fmt.Errorf("writing backup: %w", err)
				}
				fmt.Fprintf(os.Stderr, "  Backed up existing config to %s\n", bakPath)
			}
		}

		if err := os.MkdirAll(configDir, 0755); err != nil {
			return fmt.Errorf("creating config directory: %w", err)
		}

		if err := os.WriteFile(configPath, []byte(yamlStr+"\n"), 0644); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}

		fmt.Fprintf(os.Stderr, "  Created %s\n", configPath)
		return nil
	},
}

func init() {
	reviewGenerateCmd.Flags().Bool("dry-run", false, "Print generated config without writing")
	reviewGenerateCmd.Flags().Bool("force", false, "Overwrite existing config without backup")
	reviewCmd.AddCommand(reviewGenerateCmd)
}
