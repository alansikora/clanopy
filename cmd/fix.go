package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/alansikora/clanopy/internal/commands"
	"github.com/alansikora/clanopy/internal/config"
	"github.com/alansikora/clanopy/internal/review"
	"github.com/spf13/cobra"
)

var fixCmd = &cobra.Command{
	Use:   "fix <pr-number>-<finding-index>",
	Short: "Fix a review finding in an isolated worktree",
	Long:  "Creates a git worktree, writes finding context as instructions, and launches Claude to help fix the issue.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fixRef := args[0]

		// 1. Parse fix-ref to extract PR number and finding index.
		prNumber, _, err := parseFixRef(fixRef)
		if err != nil {
			return err
		}

		// 2. Load review result: try local cache first, then fetch from PR comment.
		result, err := review.LoadLatestResult(prNumber)
		if err != nil {
			result, err = fetchAndCacheReview(prNumber)
			if err != nil {
				return err
			}
		}

		// 3. Find the finding by matching fix_ref.
		// Try latest review first, then search all reviews for older fix_refs.
		finding, err := lookupFinding(result, fixRef)
		if err != nil && result != nil {
			// Cache might be stale — try fetching fresh data from PR.
			freshResult, fetchErr := fetchAndCacheReview(prNumber)
			if fetchErr == nil {
				result = freshResult
				finding, err = lookupFinding(result, fixRef)
			}
		}
		if err != nil {
			// fix_ref not in the latest review — search all reviews.
			repo, detectErr := review.DetectRepo()
			if detectErr == nil {
				fmt.Fprintf(os.Stderr, "Searching older reviews for %s...\n", fixRef)
				f, fetchErr := review.FetchFindingFromPR(repo, prNumber, fixRef)
				if fetchErr == nil {
					finding = f
					err = nil
				}
			}
		}
		if err != nil {
			return err
		}

		// 4. Create git worktree.
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}

		worktreePath := fmt.Sprintf(".claude/worktrees/fix-%s", fixRef)
		branchName := fmt.Sprintf("clanopy/fix-%s", fixRef)

		absWorktreePath := worktreePath
		if !strings.HasPrefix(worktreePath, "/") {
			absWorktreePath = cwd + "/" + worktreePath
		}

		// 4. Create worktree or reuse existing one.
		resuming := false
		if _, err := os.Stat(absWorktreePath); err == nil {
			resuming = true
		} else {
			gitCmd := exec.Command("git", "worktree", "add", worktreePath, "-b", branchName)
			gitCmd.Dir = cwd
			if output, err := gitCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("creating worktree: %s\n%s", err, string(output))
			}

			// Set upstream tracking to the PR's head branch so pushes target
			// the correct remote branch instead of requiring manual routing.
			if headBranch := getPRHeadBranch(result.Repo, prNumber); headBranch != "" {
				trackCmd := exec.Command("git", "branch", "--set-upstream-to", "origin/"+headBranch, branchName)
				trackCmd.Dir = cwd
				_ = trackCmd.Run() // best-effort; don't fail the fix if tracking can't be set
			}
		}

		// 5. Write finding context as instructions.
		instructionsContent := buildFixInstructions(finding, result)
		instructionsPath := absWorktreePath + "/.claude/instructions.md"
		if err := os.MkdirAll(absWorktreePath+"/.claude", 0755); err != nil {
			return fmt.Errorf("creating .claude directory: %w", err)
		}
		if err := os.WriteFile(instructionsPath, []byte(instructionsContent), 0644); err != nil {
			return fmt.Errorf("writing instructions: %w", err)
		}

		// 6. Set up workspace environment.
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		ws, _, err := resolveWorkspace(cfg, nil)
		if err != nil {
			return fmt.Errorf("resolving workspace: %w", err)
		}

		claudePath, err := exec.LookPath("claude")
		if err != nil {
			return fmt.Errorf("claude not found in PATH: %w", err)
		}

		sessionDir := config.SessionDir(ws.Name)
		commands.Ensure()

		env := os.Environ()
		env = setEnv(env, "CLAUDE_CONFIG_DIR", sessionDir)
		if ws.APIKey != "" {
			env = setEnv(env, "ANTHROPIC_API_KEY", ws.APIKey)
		}

		if err := syscall.Chdir(absWorktreePath); err != nil {
			return fmt.Errorf("changing to worktree directory: %w", err)
		}

		if resuming {
			fmt.Fprintf(os.Stderr, "\033[90m↳ resuming fix for %s in %s (PR #%d)\033[0m\n", finding.Title, finding.File, prNumber)
			return syscall.Exec(claudePath, []string{"claude", "--continue"}, env)
		}

		fmt.Fprintf(os.Stderr, "\033[90m↳ fixing %s in %s (PR #%d)\033[0m\n", finding.Title, finding.File, prNumber)

		fixPrompt := fmt.Sprintf("Fix this issue in %s:%d — %s\n\n%s",
			finding.File, finding.Line, finding.Title, finding.Description)
		if finding.Suggestion != "" {
			fixPrompt += "\n\nSuggested fix: " + finding.Suggestion
		}

		return syscall.Exec(claudePath, []string{"claude", "--permission-mode", "plan", fixPrompt}, env)
	},
}

func fetchAndCacheReview(prNumber int) (*review.ReviewResult, error) {
	repo, err := review.DetectRepo()
	if err != nil {
		return nil, fmt.Errorf("no local cache and could not detect repo: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Fetching review from PR #%d...\n", prNumber)
	result, err := review.FetchReviewFromPR(repo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("loading review result: %w", err)
	}
	review.SaveResult(result)
	return result, nil
}

func lookupFinding(result *review.ReviewResult, fixRef string) (*review.Finding, error) {
	for i := range result.Findings {
		if result.Findings[i].FixRef == fixRef {
			return &result.Findings[i], nil
		}
	}
	return nil, fmt.Errorf("fix_ref %q not found in review result", fixRef)
}

func parseFixRef(ref string) (prNumber, findingIdx int, err error) {
	parts := strings.SplitN(ref, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid fix-ref %q: expected format <pr-number>-<finding-index>", ref)
	}

	prNumber, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid PR number in fix-ref %q: %w", ref, err)
	}

	findingIdx, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid finding index in fix-ref %q: %w", ref, err)
	}

	return prNumber, findingIdx, nil
}

func buildFixInstructions(finding *review.Finding, result *review.ReviewResult) string {
	var b strings.Builder

	b.WriteString("# Fix Request\n\n")
	b.WriteString(fmt.Sprintf("This worktree was created to fix a review finding from PR #%d.\n\n", result.PRNumber))

	b.WriteString("## Finding\n\n")
	b.WriteString(fmt.Sprintf("**Title:** %s\n", finding.Title))
	b.WriteString(fmt.Sprintf("**Severity:** %s\n", finding.Severity))
	b.WriteString(fmt.Sprintf("**File:** %s\n", finding.File))
	if finding.Line > 0 {
		b.WriteString(fmt.Sprintf("**Line:** %d\n", finding.Line))
	}
	b.WriteString(fmt.Sprintf("\n**Description:**\n%s\n", finding.Description))

	if finding.Suggestion != "" {
		b.WriteString(fmt.Sprintf("\n**Suggestion:**\n%s\n", finding.Suggestion))
	}

	b.WriteString("\n## Instructions\n\n")
	b.WriteString("Please fix the issue described above. Focus on the specific file and location mentioned.\n")
	b.WriteString("After making the fix, verify that the code compiles and any related tests pass.\n")

	return b.String()
}

// getPRHeadBranch returns the head branch name for a PR, or "" on any error.
func getPRHeadBranch(repo string, prNumber int) string {
	out, err := exec.Command("gh", "pr", "view", fmt.Sprintf("%d", prNumber),
		"--repo", repo,
		"--json", "headRefName",
		"--jq", ".headRefName",
	).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func init() {
	rootCmd.AddCommand(fixCmd)
}
