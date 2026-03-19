package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/alansikora/clanopy/internal/config"
)

// RunOptions configures a review run.
type RunOptions struct {
	Repo       string
	PRNumber   int
	ConfigPath string
	Output     string // "markdown" or "json"
	Post       bool
	DryRun     bool
}

// resolveEnv builds the environment for Claude, resolving workspace config.
func resolveEnv() []string {
	env := os.Environ()
	hasAuth := os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != ""
	if os.Getenv("CLAUDE_CONFIG_DIR") == "" && !hasAuth {
		cfg, err := config.Load()
		if err == nil {
			cwd, _ := os.Getwd()
			if ws, _, err := cfg.FindWorkspaceForDir(cwd); err == nil {
				env = setEnv(env, "CLAUDE_CONFIG_DIR", config.SessionDir(ws.Name))
				if ws.APIKey != "" {
					env = setEnv(env, "ANTHROPIC_API_KEY", ws.APIKey)
				}
			}
		}
	}
	return env
}

// runClaude executes Claude with the given prompt and environment.
func runClaude(prompt string, env []string) ([]byte, error) {
	cmd := exec.Command("claude", "--print", "--no-session-persistence", prompt)
	cmd.Env = env
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("claude failed: %s\n%s", string(exitErr.Stderr), string(output))
		}
		return nil, fmt.Errorf("running claude: %w", err)
	}
	return output, nil
}

// parseFixedThreads extracts thread ID strings (e.g. "thread-0") from Claude's
// JSON response and returns the corresponding integer indices.
func parseFixedThreads(output string) []int {
	allMatches := jsonFenceRe.FindAllStringSubmatch(output, -1)
	for _, matches := range allMatches {
		var refs []string
		if err := json.Unmarshal([]byte(matches[1]), &refs); err != nil {
			continue
		}
		var indices []int
		for _, ref := range refs {
			var idx int
			if _, err := fmt.Sscanf(ref, "thread-%d", &idx); err == nil {
				indices = append(indices, idx)
			}
		}
		return indices
	}
	return nil
}

// allResolved checks if all clanopy threads have been resolved.
// fixedIndices are the sequential indices into the threads slice that were fixed.
func allResolved(threads []ReviewThread, fixedIndices []int) bool {
	fixedSet := make(map[int]bool, len(fixedIndices))
	for _, idx := range fixedIndices {
		fixedSet[idx] = true
	}
	for i := range threads {
		if !fixedSet[i] {
			return false
		}
	}
	return true
}

// Run orchestrates the full review flow.
func Run(opts RunOptions) error {
	// 1. Detect repo if not provided.
	repo := opts.Repo
	if repo == "" {
		detected, err := DetectRepo()
		if err != nil {
			return fmt.Errorf("detecting repo: %w", err)
		}
		repo = detected
	}

	// 2. Fetch PR data.
	pr, err := FetchPR(repo, opts.PRNumber)
	if err != nil {
		return fmt.Errorf("fetching PR: %w", err)
	}

	// 3. Load review config.
	var cfg *ReviewConfig
	configPath := opts.ConfigPath
	if configPath == "" {
		configPath = ".clanopy/review.yml"
	}
	loaded, err := LoadConfig(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || os.IsNotExist(err) {
			cfg = &ReviewConfig{}
		} else {
			var pathErr *os.PathError
			if errors.As(err, &pathErr) {
				cfg = &ReviewConfig{}
			} else {
				return fmt.Errorf("loading config: %w", err)
			}
		}
	} else {
		cfg = loaded
	}

	// Fetch full file contents for context.
	fileContents, skippedFiles := FetchFileContents(pr.Files, cfg.Ignore, cfg.EffectiveMaxFileSize(), cfg.EffectiveMaxTotalSize())
	pr.FileContents = fileContents
	if len(skippedFiles) > 0 {
		fmt.Fprintf(os.Stderr, "Skipped %d large/ignored files: %s\n", len(skippedFiles), strings.Join(skippedFiles, ", "))
	}

	// Resolve Claude environment once for reuse.
	env := resolveEnv()

	// Check for previous clanopy review threads.
	threads, _ := FetchReviewThreads(repo, opts.PRNumber)
	startIndex := len(threads) // continue fix_ref numbering after all existing threads
	var clanopyThreads []ReviewThread
	for _, t := range threads {
		if !t.Resolved {
			clanopyThreads = append(clanopyThreads, t)
		}
	}
	previousSHA := FetchPreviousReviewSHA(repo, opts.PRNumber)

	var prompt string
	var fixedIndices []int
	if len(clanopyThreads) > 0 && previousSHA != "" {
		// Incremental review.
		fmt.Fprintf(os.Stderr, "Re-reviewing PR #%d (%d unresolved threads, base %s)\n", opts.PRNumber, len(clanopyThreads), previousSHA[:8])
		incrementalDiff, diffErr := GetIncrementalDiff(previousSHA)
		if diffErr != nil {
			fmt.Fprintf(os.Stderr, "Could not compute incremental diff, will use full PR diff for reevaluation\n")
		}

		// Phase 1: Re-evaluate existing findings.
		// Use incremental diff when available, otherwise fall back to the full PR diff.
		reevalDiff := incrementalDiff
		if diffErr != nil {
			reevalDiff = pr.Diff
		}
		fmt.Fprintf(os.Stderr, "Evaluating %d previous findings against new changes...\n", len(clanopyThreads))
		reevalPrompt := BuildReevaluatePrompt(clanopyThreads, reevalDiff)

		if opts.DryRun {
			fmt.Print(reevalPrompt)
			fmt.Print("\n---\n\n")
		} else {
			reevalOutput, err := runClaude(reevalPrompt, env)
			if err == nil {
				fixedIndices = parseFixedThreads(string(reevalOutput))
				fmt.Fprintf(os.Stderr, "Result: %d fixed, %d still open\n", len(fixedIndices), len(clanopyThreads)-len(fixedIndices))
				for _, idx := range fixedIndices {
					if idx >= 0 && idx < len(clanopyThreads) {
						t := clanopyThreads[idx]
						label := t.Path
						if firstLine := t.Body; firstLine != "" {
							if nl := strings.Index(firstLine, "\n"); nl >= 0 {
								firstLine = firstLine[:nl]
							}
							label = firstLine
						}
						if err := ResolveThread(t.ID); err != nil {
							if strings.Contains(err.Error(), "Resource not accessible") {
								fmt.Fprintf(os.Stderr, "  ~ %s — fixed (auto-resolve unavailable: token lacks permission)\n", label)
							} else {
								fmt.Fprintf(os.Stderr, "  ! %s — fixed, but failed to resolve thread: %v\n", label, err)
							}
						} else {
							fmt.Fprintf(os.Stderr, "  ✓ %s — resolved\n", label)
						}
					}
				}
			} else {
				fmt.Fprintf(os.Stderr, "Warning: re-evaluation failed: %v\n", err)
			}
		}

		// Phase 2: Build review prompt.
		fixedSet := make(map[int]bool, len(fixedIndices))
		for _, idx := range fixedIndices {
			if idx >= 0 && idx < len(clanopyThreads) {
				fixedSet[idx] = true
			}
		}
		var unresolved []ReviewThread
		for i, t := range clanopyThreads {
			if !fixedSet[i] {
				unresolved = append(unresolved, t)
			}
		}
		if diffErr != nil {
			// No incremental diff available — fall back to full review.
			fmt.Fprintf(os.Stderr, "Falling back to full review (%d known issues excluded)...\n", len(unresolved))
			prompt = BuildPrompt(pr, cfg, startIndex)
		} else {
			fmt.Fprintf(os.Stderr, "Reviewing new changes (%d known issues excluded)...\n", len(unresolved))
			prompt = BuildIncrementalPrompt(incrementalDiff, cfg, unresolved, opts.PRNumber, startIndex, pr.FileContents, pr.Files)
		}
	} else {
		// First review — full PR diff.
		fmt.Fprintf(os.Stderr, "Reviewing PR #%d...\n", opts.PRNumber)
		prompt = BuildPrompt(pr, cfg, startIndex)
	}

	// 5. Dry run — print prompt and return.
	if opts.DryRun {
		fmt.Print(prompt)
		return nil
	}

	// 6. Run Claude.
	claudeOutput, err := runClaude(prompt, env)
	if err != nil {
		return err
	}

	// 7. Parse findings.
	findings, err := ParseFindings(string(claudeOutput))
	if err != nil {
		return fmt.Errorf("parsing findings: %w", err)
	}
	if len(findings) == 0 {
		fmt.Fprintf(os.Stderr, "No new findings\n")
	} else {
		fmt.Fprintf(os.Stderr, "Found %d new findings\n", len(findings))
	}

	// Get current HEAD SHA for tracking.
	headSHA, _ := exec.Command("git", "rev-parse", "HEAD").Output()

	// 8. Build result.
	result := &ReviewResult{
		PRNumber: opts.PRNumber,
		Repo:     repo,
		Findings: findings,
		SHA:      strings.TrimSpace(string(headSHA)),
	}

	// Handle zero-findings scenarios.
	if len(findings) == 0 && opts.Post {
		if len(clanopyThreads) > 0 && allResolved(clanopyThreads, fixedIndices) {
			// Re-review: all resolved — minimize old reviews and post all-clear.
			minimizeFailed := false
			if nodeIDs, err := FindClanopyReviewNodeIDs(repo, opts.PRNumber); err == nil {
				for _, nodeID := range nodeIDs {
					if err := MinimizeComment(nodeID); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: could not minimize review: %v\n", err)
						minimizeFailed = true
					}
				}
				if len(nodeIDs) > 0 && !minimizeFailed {
					fmt.Fprintf(os.Stderr, "Minimized %d previous review(s)\n", len(nodeIDs))
				}
			} else {
				fmt.Fprintf(os.Stderr, "Warning: could not fetch reviews for minimization: %v\n", err)
				minimizeFailed = true
			}
			if err := PostAllClearReview(repo, opts.PRNumber, minimizeFailed); err != nil {
				return fmt.Errorf("posting all-clear review: %w", err)
			}
			fmt.Fprintf(os.Stderr, "All clear! No issues remaining.\n")
			return nil
		}
		if len(clanopyThreads) > 0 {
			// Re-review: old unresolved threads still exist, nothing new — do nothing.
			fmt.Fprintf(os.Stderr, "No new findings. %d previous thread(s) still unresolved.\n", len(clanopyThreads))
			return nil
		}
	}

	// 8b. Cache result for later use by `clanopy fix`.
	if err := SaveResult(result); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to cache review result: %v\n", err)
	}

	// 9. Format output.
	outputFormat := opts.Output
	if outputFormat == "" {
		outputFormat = "markdown"
	}

	var formatted string
	switch outputFormat {
	case "json":
		jsonOut, err := FormatJSON(result)
		if err != nil {
			return fmt.Errorf("formatting JSON: %w", err)
		}
		formatted = jsonOut
	default:
		formatted = FormatMarkdown(result)
	}

	// 10. Print to stdout (skip when posting to avoid noisy CI logs).
	if !opts.Post {
		fmt.Print(formatted)
	}

	// 11. Post review if requested (uses PR Review API for inline comments).
	if opts.Post {
		if len(findings) == 0 {
			// First review, no issues — post a clean congratulations message.
			if err := PostCleanReview(repo, opts.PRNumber); err != nil {
				return fmt.Errorf("posting review: %w", err)
			}
		} else {
			if err := PostReview(repo, opts.PRNumber, result, pr.Diff); err != nil {
				return fmt.Errorf("posting review: %w", err)
			}
		}
		fmt.Fprintf(os.Stderr, "Review posted to PR #%d\n", opts.PRNumber)
	}

	return nil
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
