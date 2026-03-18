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

// parseFixedRefs extracts fix_ref strings from Claude's JSON response.
func parseFixedRefs(output string) []string {
	allMatches := jsonFenceRe.FindAllStringSubmatch(output, -1)
	for _, matches := range allMatches {
		var refs []string
		if err := json.Unmarshal([]byte(matches[1]), &refs); err != nil {
			continue
		}
		return refs
	}
	return nil
}

// allResolved checks if all clanopy threads have been resolved.
func allResolved(threads []ReviewThread, fixedRefs []string) bool {
	fixedSet := make(map[string]bool, len(fixedRefs))
	for _, ref := range fixedRefs {
		fixedSet[ref] = true
	}
	for _, t := range threads {
		if !fixedSet[t.FixRef] {
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
	var clanopyThreads []ReviewThread
	for _, t := range threads {
		if !t.Resolved && t.FixRef != "" {
			clanopyThreads = append(clanopyThreads, t)
		}
	}
	previousSHA := FetchPreviousReviewSHA(repo, opts.PRNumber)

	var prompt string
	var fixedRefs []string
	if len(clanopyThreads) > 0 && previousSHA != "" {
		// Incremental review.
		incrementalDiff, err := GetIncrementalDiff(previousSHA)
		if err != nil {
			// Fall back to full review.
			prompt = BuildPrompt(pr, cfg)
		} else {
			// Phase 1: Re-evaluate existing findings.
			reevalPrompt := BuildReevaluatePrompt(clanopyThreads, incrementalDiff)

			if opts.DryRun {
				fmt.Print(reevalPrompt)
				fmt.Print("\n---\n\n")
			} else {
				reevalOutput, err := runClaude(reevalPrompt, env)
				if err == nil {
					fixedRefs = parseFixedRefs(string(reevalOutput))
					for _, t := range clanopyThreads {
						for _, ref := range fixedRefs {
							if t.FixRef == ref {
								ResolveThread(t.ID)
								fmt.Fprintf(os.Stderr, "Resolved: %s\n", t.FixRef)
							}
						}
					}
				}
			}

			// Phase 2: Review incremental diff.
			var unresolved []ReviewThread
			for _, t := range clanopyThreads {
				resolved := false
				for _, ref := range fixedRefs {
					if t.FixRef == ref {
						resolved = true
						break
					}
				}
				if !resolved {
					unresolved = append(unresolved, t)
				}
			}
			prompt = BuildIncrementalPrompt(incrementalDiff, cfg, unresolved, opts.PRNumber, pr.FileContents, pr.Files)
		}
	} else {
		// First review — full PR diff.
		prompt = BuildPrompt(pr, cfg)
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

	// Get current HEAD SHA for tracking.
	headSHA, _ := exec.Command("git", "rev-parse", "HEAD").Output()

	// 8. Build result.
	result := &ReviewResult{
		PRNumber: opts.PRNumber,
		Repo:     repo,
		Findings: findings,
		SHA:      strings.TrimSpace(string(headSHA)),
	}

	// Check if no new findings and we resolved everything.
	if len(findings) == 0 && opts.Post {
		if len(clanopyThreads) > 0 && allResolved(clanopyThreads, fixedRefs) {
			PostAllClearReview(repo, opts.PRNumber)
			fmt.Fprintf(os.Stderr, "All clear! No issues remaining.\n")
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
		if err := PostReview(repo, opts.PRNumber, result, pr.Diff); err != nil {
			return fmt.Errorf("posting review: %w", err)
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
