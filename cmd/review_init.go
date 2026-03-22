package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alansikora/clanopy/internal/auth"
	"github.com/alansikora/clanopy/internal/review"
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

		// 2. Install the Clanopy Review App for bot identity.
		if err := auth.InstallClanopyReviewApp(repo); err != nil {
			return fmt.Errorf("installing Clanopy Review app: %w", err)
		}

		// 3. Check for Claude auth secret.
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

		// 4. Create workflow file.
		workflowDir := filepath.Join(".github", "workflows")
		workflowPath := filepath.Join(workflowDir, "clanopy-review.yml")

		var authEnv string
		if useAPIKey {
			authEnv = "          anthropic_api_key: ${{ secrets.ANTHROPIC_API_KEY }}"
		} else {
			authEnv = "          claude_code_oauth_token: ${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}"
		}

		actionRef := "v1"

		workflow := fmt.Sprintf(`name: Clanopy Review
on:
  pull_request:
    types: [opened, synchronize]
  pull_request_review_comment:
    types: [created]

permissions:
  contents: read
  id-token: write
  pull-requests: write

jobs:
  filter:
    if: >-
      github.event_name == 'pull_request' || (
        github.event.comment.user.login != 'clanopy-review[bot]' &&
        github.event.comment.in_reply_to_id
      )
    runs-on: ubuntu-latest
    outputs:
      should_review: ${{ github.event_name == 'pull_request' || steps.check.outputs.is_clanopy_thread == 'true' }}
    steps:
      - name: Check if clanopy thread
        id: check
        if: github.event_name == 'pull_request_review_comment'
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          BODY=$(gh api repos/${{ github.repository }}/pulls/comments/${{ github.event.comment.in_reply_to_id }} --jq '.body')
          if echo "$BODY" | grep -q "clanopy fix"; then
            echo "is_clanopy_thread=true" >> "$GITHUB_OUTPUT"
          else
            echo "is_clanopy_thread=false" >> "$GITHUB_OUTPUT"
          fi

  review:
    needs: filter
    if: needs.filter.outputs.should_review == 'true'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          ref: ${{ github.event.pull_request.head.sha || github.sha }}

      - uses: alansikora/clanopy-review@%s
        with:
%s
          config_path: .clanopy/review.yml
          reply_only: ${{ github.event_name == 'pull_request_review_comment' }}
`, actionRef, authEnv)

		if err := os.MkdirAll(workflowDir, 0755); err != nil {
			return fmt.Errorf("creating workflow directory: %w", err)
		}

		_, existsErr := os.Stat(workflowPath)
		workflowExisted := existsErr == nil
		if err := os.WriteFile(workflowPath, []byte(workflow), 0644); err != nil {
			return fmt.Errorf("writing workflow file: %w", err)
		}
		workflowCreated := true
		if workflowExisted {
			fmt.Fprintf(os.Stderr, "  Updated %s\n", workflowPath)
		} else {
			fmt.Fprintf(os.Stderr, "  Created %s\n", workflowPath)
		}

		// 4. Generate review config using Claude (fall back to static template).
		configDir := ".clanopy"
		configPath := filepath.Join(configDir, "review.yml")

		if err := os.MkdirAll(configDir, 0755); err != nil {
			return fmt.Errorf("creating config directory: %w", err)
		}

		configCreated := false
		if _, err := os.Stat(configPath); err == nil {
			fmt.Fprintf(os.Stderr, "  %s already exists, skipping\n", configPath)
		} else {
			configContent := review.StarterConfig
			fmt.Fprintf(os.Stderr, "Generating review config...\n")
			if generated, err := review.Generate(); err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: could not generate config with Claude: %v\n", err)
				fmt.Fprintf(os.Stderr, "  Using starter template instead\n")
			} else {
				configContent = generated + "\n"
			}
			if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
				return fmt.Errorf("writing review config: %w", err)
			}
			fmt.Fprintf(os.Stderr, "  Created %s\n", configPath)
			configCreated = true
		}

		// 5. Update .gitignore with clanopy entries.
		gitignoreUpdated, err := updateGitignore()
		if err != nil {
			return fmt.Errorf("updating .gitignore: %w", err)
		}

		// 6. Create a PR with the review setup.
		if !workflowCreated && !configCreated && !gitignoreUpdated {
			fmt.Fprintf(os.Stderr, "\nReview setup is already complete — nothing to do.\n")
			return nil
		}

		var filesToAdd []string
		var bullets []string
		if workflowCreated {
			filesToAdd = append(filesToAdd, ".github/workflows/clanopy-review.yml")
			bullets = append(bullets, "- Add Clanopy automated PR review workflow")
		}
		if configCreated {
			filesToAdd = append(filesToAdd, ".clanopy/review.yml")
			bullets = append(bullets, "- Add starter `.clanopy/review.yml` config")
		}
		if gitignoreUpdated {
			filesToAdd = append(filesToAdd, ".gitignore")
			bullets = append(bullets, "- Update `.gitignore` with clanopy entries")
		}

		if len(filesToAdd) == 0 {
			return fmt.Errorf("internal error: no files to stage")
		}

		branch := "clanopy/review-setup"
		fmt.Fprintf(os.Stderr, "\nCreating PR...\n")

		// Check if branch already exists from a prior run.
		if err := exec.Command("git", "show-ref", "--verify", "refs/heads/"+branch).Run(); err == nil {
			return fmt.Errorf("branch %s already exists — delete it with `git branch -D %s` to retry", branch, branch)
		}

		if out, err := exec.Command("git", "checkout", "-b", branch).CombinedOutput(); err != nil {
			return fmt.Errorf("creating branch: %s\n%s", err, string(out))
		}
		if out, err := exec.Command("git", append([]string{"add"}, filesToAdd...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("staging files: %s\n%s", err, string(out))
		}

		if out, err := exec.Command("git", "commit", "-m", "Add Clanopy automated PR review").CombinedOutput(); err != nil {
			return fmt.Errorf("committing: %s\n%s", err, string(out))
		}

		if out, err := exec.Command("git", "push", "-u", "origin", branch).CombinedOutput(); err != nil {
			return fmt.Errorf("pushing: %s\n%s", err, string(out))
		}

		prBody := "## Summary\n" + strings.Join(bullets, "\n") + "\n\nPRs will be automatically reviewed by Claude on open and update."
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

func updateGitignore() (bool, error) {
	entries := []string{
		".clanopy/reviews/",
		".clanopy/**/*.bak",
	}

	gitignorePath := ".gitignore"
	existing := ""
	if data, err := os.ReadFile(gitignorePath); err == nil {
		existing = string(data)
	}

	var toAdd []string
	for _, entry := range entries {
		if !strings.Contains(existing, entry) {
			toAdd = append(toAdd, entry)
		}
	}

	if len(toAdd) == 0 {
		fmt.Fprintf(os.Stderr, "  .gitignore already up to date\n")
		return false, nil
	}

	// Ensure existing content ends with a newline before appending.
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}

	prefix := "\n"
	if existing == "" {
		prefix = ""
	}
	section := prefix + "# clanopy\n" + strings.Join(toAdd, "\n") + "\n"
	if err := os.WriteFile(gitignorePath, []byte(existing+section), 0644); err != nil {
		return false, err
	}

	fmt.Fprintf(os.Stderr, "  Updated .gitignore\n")
	return true, nil
}

func init() {
	reviewInitCmd.Flags().Bool("api-key", false, "Use ANTHROPIC_API_KEY instead of OAuth token")
	reviewCmd.AddCommand(reviewInitCmd)
}
