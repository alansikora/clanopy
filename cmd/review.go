package cmd

import (
	"fmt"
	"strconv"

	"github.com/alansikora/clanopy/internal/review"
	"github.com/spf13/cobra"
)

var reviewCmd = &cobra.Command{
	Use:   "review <pr-number>",
	Short: "Review a pull request using Claude",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		prNumber, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid PR number %q: %w", args[0], err)
		}

		repo, _ := cmd.Flags().GetString("repo")
		output, _ := cmd.Flags().GetString("output")
		post, _ := cmd.Flags().GetBool("post")
		configPath, _ := cmd.Flags().GetString("config")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		return review.Run(review.RunOptions{
			Repo:       repo,
			PRNumber:   prNumber,
			ConfigPath: configPath,
			Output:     output,
			Post:       post,
			DryRun:     dryRun,
		})
	},
}

func init() {
	reviewCmd.Flags().StringP("repo", "r", "", "GitHub repo (owner/name)")
	reviewCmd.Flags().StringP("output", "o", "markdown", "Output format: markdown or json")
	reviewCmd.Flags().Bool("post", false, "Post findings as a PR comment")
	reviewCmd.Flags().StringP("config", "c", ".clanopy/review.yml", "Path to review config")
	reviewCmd.Flags().Bool("dry-run", false, "Show prompt without running Claude")
	rootCmd.AddCommand(reviewCmd)
}
