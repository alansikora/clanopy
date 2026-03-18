package review

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

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
			// No config file — use default empty config for general review.
			cfg = &ReviewConfig{}
		} else {
			// Try unwrapping in case the error wraps a path error.
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

	// 4. Build prompt.
	prompt := BuildPrompt(pr, cfg)

	// 5. Dry run — print prompt and return.
	if opts.DryRun {
		fmt.Print(prompt)
		return nil
	}

	// 6. Run Claude.
	claudeCmd := exec.Command("claude", "--print", "--no-session-persistence", prompt)
	env := os.Environ()
	// Resolve workspace config dir if not already set, so Claude uses the
	// correct auth credentials (the shell wrapper normally does this).
	if os.Getenv("CLAUDE_CONFIG_DIR") == "" {
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
	claudeCmd.Env = env
	claudeOutput, err := claudeCmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("claude failed: %s\n%s", string(exitErr.Stderr), string(claudeOutput))
		}
		return fmt.Errorf("running claude: %w", err)
	}

	// 7. Parse findings.
	findings, err := ParseFindings(string(claudeOutput))
	if err != nil {
		return fmt.Errorf("parsing findings: %w", err)
	}

	// 8. Build result.
	result := &ReviewResult{
		PRNumber: opts.PRNumber,
		Repo:     repo,
		Findings: findings,
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

	// 10. Print to stdout.
	fmt.Print(formatted)

	// 11. Post comment if requested.
	if opts.Post {
		mdOutput := formatted
		if outputFormat != "markdown" {
			mdOutput = FormatMarkdown(result)
		}
		if err := PostComment(repo, opts.PRNumber, mdOutput); err != nil {
			return fmt.Errorf("posting comment: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Comment posted to PR #%d\n", opts.PRNumber)
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
