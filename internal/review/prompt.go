package review

import (
	"fmt"
	"strings"
)

// BuildPrompt constructs the review prompt from PR data and review config.
func BuildPrompt(pr *PRData, cfg *ReviewConfig) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. Review the following pull request and report findings.\n\n")

	// PR metadata.
	fmt.Fprintf(&b, "## Pull Request #%d\n", pr.Number)
	fmt.Fprintf(&b, "**Title:** %s\n", pr.Title)
	fmt.Fprintf(&b, "**Author:** %s\n", pr.Author)
	if pr.Body != "" {
		fmt.Fprintf(&b, "**Description:**\n%s\n", pr.Body)
	}
	b.WriteString("\n")

	// Context from config.
	if cfg != nil && cfg.Context != "" {
		fmt.Fprintf(&b, "## Additional Context\n%s\n\n", cfg.Context)
	}

	// Review rules.
	if cfg != nil && len(cfg.Rules) > 0 {
		b.WriteString("## Review Rules\n")
		b.WriteString("Apply the following rules when reviewing:\n\n")
		for _, rule := range cfg.Rules {
			fmt.Fprintf(&b, "- **%s** (severity: %s): %s\n", rule.ID, rule.Severity, rule.Description)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("## Review Rules\nNo specific rules are defined. Perform a general code review covering correctness, security, performance, and maintainability.\n\n")
	}

	// Ignore patterns.
	if cfg != nil && len(cfg.Ignore) > 0 {
		b.WriteString("## Ignore Patterns\nDo NOT report findings for files matching these patterns:\n")
		for _, pat := range cfg.Ignore {
			fmt.Fprintf(&b, "- `%s`\n", pat)
		}
		b.WriteString("\n")
	}

	// Max findings.
	if cfg != nil && cfg.MaxFindings > 0 {
		fmt.Fprintf(&b, "Limit your output to at most %d findings.\n\n", cfg.MaxFindings)
	}

	// Diff.
	b.WriteString("## Diff\n```diff\n")
	b.WriteString(pr.Diff)
	b.WriteString("\n```\n\n")

	// Output format instructions.
	b.WriteString("## Output Format\n")
	b.WriteString("Return your findings as a JSON array inside a ```json code fence. Each finding must have these fields:\n\n")
	b.WriteString("- `id` (string): The rule ID that was violated, or a short kebab-case identifier for general findings.\n")
	b.WriteString("- `file` (string): The file path where the issue was found.\n")
	b.WriteString("- `line` (int): The line number in the file.\n")
	b.WriteString("- `severity` (string): One of \"error\", \"warning\", or \"info\".\n")
	b.WriteString("- `title` (string): A short title for the finding.\n")
	b.WriteString("- `description` (string): A detailed explanation of the issue.\n")
	b.WriteString("- `suggestion` (string, optional): A suggested fix or improvement.\n")
	fmt.Fprintf(&b, "- `fix_ref` (string): A reference ID in the format `%d-<index>` where index is 1-based (e.g. `%d-1`, `%d-2`).\n", pr.Number, pr.Number, pr.Number)
	b.WriteString("\nIf there are no findings, return an empty array: `[]`.\n")
	b.WriteString("\nExample:\n```json\n[\n  {\n    \"id\": \"rule-id\",\n    \"file\": \"src/main.go\",\n    \"line\": 42,\n    \"severity\": \"warning\",\n    \"title\": \"Short title\",\n    \"description\": \"Detailed description.\",\n    \"suggestion\": \"Consider doing X instead.\",\n")
	fmt.Fprintf(&b, "    \"fix_ref\": \"%d-1\"\n  }\n]\n```\n", pr.Number)

	return b.String()
}
