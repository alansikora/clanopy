package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alansikora/clanopy/internal/auth"
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

		needsAuth := false
		secretsOut, err := exec.Command("gh", "secret", "list", "--repo", repo).Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not check secrets: %v\n", err)
		} else if !strings.Contains(string(secretsOut), secretName) {
			needsAuth = true
		}

		if needsAuth {
			if useAPIKey {
				fmt.Fprintf(os.Stderr, "No %s secret found on %s.\n\n", secretName, repo)
				fmt.Fprintf(os.Stderr, "Set it with:\n")
				fmt.Fprintf(os.Stderr, "  gh secret set ANTHROPIC_API_KEY\n\n")
				return nil
			}

			// Step 1: Install Claude GitHub App.
			if err := auth.InstallGitHubApp(repo); err != nil {
				return fmt.Errorf("installing GitHub App: %w", err)
			}

			// Step 2: OAuth flow to get long-lived token.
			token, err := auth.OAuthToken()
			if err != nil {
				return fmt.Errorf("authentication failed: %w", err)
			}

			// Step 3: Set token as GitHub secret.
			fmt.Fprintf(os.Stderr, "Setting %s secret on %s...\n", secretName, repo)
			if err := auth.SetGitHubSecret(repo, secretName, token); err != nil {
				return fmt.Errorf("setting secret: %w", err)
			}
			fmt.Fprintf(os.Stderr, "  Done!\n\n")
		} else {
			fmt.Fprintf(os.Stderr, "  %s secret found on %s\n\n", secretName, repo)
		}

		// 3. Create workflow file.
		workflowDir := filepath.Join(".github", "workflows")
		workflowPath := filepath.Join(workflowDir, "clanopy-review.yml")

		var authEnv string
		if useAPIKey {
			authEnv = "          anthropic_api_key: ${{ secrets.ANTHROPIC_API_KEY }}"
		} else {
			authEnv = "          claude_code_oauth_token: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}"
		}

		actionRef := Version
		if !strings.HasPrefix(actionRef, "v") {
			actionRef = "v" + actionRef
		}

		workflow := fmt.Sprintf(`name: Clanopy Review
on:
  pull_request:
    types: [opened, synchronize]

permissions:
  pull-requests: write
  contents: read
  issues: write

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: alansikora/clanopy@%s
        with:
%s
          config_path: .clanopy/review.yml
`, actionRef, authEnv)

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

		// 5. Create a PR with the review setup.
		branch := "clanopy/review-setup"
		fmt.Fprintf(os.Stderr, "\nCreating PR...\n")

		// Create branch, add files, commit, push, create PR.
		if out, err := exec.Command("git", "checkout", "-b", branch).CombinedOutput(); err != nil {
			return fmt.Errorf("creating branch: %s\n%s", err, string(out))
		}

		if out, err := exec.Command("git", "add",
			".github/workflows/clanopy-review.yml",
			".clanopy/review.yml",
		).CombinedOutput(); err != nil {
			return fmt.Errorf("staging files: %s\n%s", err, string(out))
		}

		if out, err := exec.Command("git", "commit", "-m", "Add Clanopy automated PR review").CombinedOutput(); err != nil {
			return fmt.Errorf("committing: %s\n%s", err, string(out))
		}

		if out, err := exec.Command("git", "push", "-u", "origin", branch).CombinedOutput(); err != nil {
			return fmt.Errorf("pushing: %s\n%s", err, string(out))
		}

		prBody := "## Summary\n- Add Clanopy automated PR review workflow\n- Add starter `.clanopy/review.yml` config\n\nPRs will be automatically reviewed by Claude on open and update."
		prOut, err := exec.Command("gh", "pr", "create",
			"--title", "Add Clanopy PR review",
			"--body", prBody,
			"--repo", repo,
		).CombinedOutput()
		if err != nil {
			return fmt.Errorf("creating PR: %s\n%s", err, string(prOut))
		}

		fmt.Fprintf(os.Stderr, "  %s\n", strings.TrimSpace(string(prOut)))
		fmt.Fprintf(os.Stderr, "\nDone! Merge the PR to enable automated reviews.\n")
		return nil
	},
}

func init() {
	reviewInitCmd.Flags().Bool("api-key", false, "Use ANTHROPIC_API_KEY instead of OAuth token")
	reviewCmd.AddCommand(reviewInitCmd)
}
