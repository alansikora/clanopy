package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var reviewInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up automated PR reviews for this repo",
	RunE: func(cmd *cobra.Command, args []string) error {
		useAPIKey, _ := cmd.Flags().GetBool("api-key")

		// 1. Detect repo.
		repoOut, err := exec.Command("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner").Output()
		if err != nil {
			return fmt.Errorf("detecting repo (is gh installed and are you in a git repo?): %w", err)
		}
		repo := strings.TrimSpace(string(repoOut))
		fmt.Fprintf(os.Stderr, "Setting up clanopy review for %s\n\n", repo)

		// 2. Check for auth secret.
		secretName := "CLAUDE_CODE_OAUTH_TOKEN"
		if useAPIKey {
			secretName = "ANTHROPIC_API_KEY"
		}

		secretsOut, err := exec.Command("gh", "secret", "list", "--repo", repo).Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not check secrets: %v\n", err)
		} else if !strings.Contains(string(secretsOut), secretName) {
			if useAPIKey {
				fmt.Fprintf(os.Stderr, "No %s secret found on %s.\n\n", secretName, repo)
				fmt.Fprintf(os.Stderr, "Set it with:\n")
				fmt.Fprintf(os.Stderr, "  gh secret set ANTHROPIC_API_KEY\n\n")
			} else {
				fmt.Fprintf(os.Stderr, "No %s secret found on %s.\n\n", secretName, repo)
				fmt.Fprintf(os.Stderr, "To set it up, run one of:\n")
				fmt.Fprintf(os.Stderr, "  1. /install-github-app inside a Claude Code session\n")
				fmt.Fprintf(os.Stderr, "  2. gh secret set CLAUDE_CODE_OAUTH_TOKEN\n")
				fmt.Fprintf(os.Stderr, "  3. clanopy review init --api-key (to use an API key instead)\n\n")
			}
			fmt.Fprintf(os.Stderr, "Run 'clanopy review init' again after setting up auth.\n")
			return nil
		}

		// 3. Create workflow file.
		workflowDir := filepath.Join(".github", "workflows")
		workflowPath := filepath.Join(workflowDir, "clanopy-review.yml")

		var authLine string
		if useAPIKey {
			authLine = "          anthropic_api_key: ${{ secrets.ANTHROPIC_API_KEY }}"
		} else {
			authLine = "          claude_code_oauth_token: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}"
		}

		workflow := fmt.Sprintf(`name: Clanopy Review
on:
  pull_request:
    types: [opened, synchronize]

permissions:
  pull-requests: write
  contents: read

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: alansikora/clanopy@v1
        with:
%s
`, authLine)

		if err := os.MkdirAll(workflowDir, 0755); err != nil {
			return fmt.Errorf("creating workflow directory: %w", err)
		}

		if _, err := os.Stat(workflowPath); err == nil {
			fmt.Fprintf(os.Stderr, "  %s already exists, skipping\n", workflowPath)
		} else {
			if err := os.WriteFile(workflowPath, []byte(workflow), 0644); err != nil {
				return fmt.Errorf("writing workflow file: %w", err)
			}
			fmt.Fprintf(os.Stderr, "  Created %s\n", workflowPath)
		}

		// 4. Create starter review config.
		configDir := ".clanopy"
		configPath := filepath.Join(configDir, "review.yml")

		starterConfig := `version: 1

# Add review rules for your project.
# See https://github.com/alansikora/clanopy for documentation.
#
# rules:
#   - id: example-rule
#     description: "Describe what to check for"
#     severity: warning
#
# context: |
#   Describe your project stack and conventions here.
#
# ignore:
#   - "dist/**"
#   - "*.lock"
`

		if err := os.MkdirAll(configDir, 0755); err != nil {
			return fmt.Errorf("creating config directory: %w", err)
		}

		if _, err := os.Stat(configPath); err == nil {
			fmt.Fprintf(os.Stderr, "  %s already exists, skipping\n", configPath)
		} else {
			if err := os.WriteFile(configPath, []byte(starterConfig), 0644); err != nil {
				return fmt.Errorf("writing review config: %w", err)
			}
			fmt.Fprintf(os.Stderr, "  Created %s\n", configPath)
		}

		fmt.Fprintf(os.Stderr, "\nDone! Commit and push these files to enable automated reviews.\n")
		return nil
	},
}

func init() {
	reviewInitCmd.Flags().Bool("api-key", false, "Use ANTHROPIC_API_KEY instead of OAuth token")
	reviewCmd.AddCommand(reviewInitCmd)
}
