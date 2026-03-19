package review

import (
	"fmt"
	"strings"
)

// BuildPrompt constructs the review prompt from PR data and review config.
// startIndex is the number of existing findings across prior reviews so that
// fix_ref numbering continues from where the last review left off.
func BuildPrompt(pr *PRData, cfg *ReviewConfig, startIndex int) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. Review the following pull request and report findings.\n")
	b.WriteString("You will be given the full contents of changed files for context, along with the diff. Only report issues that are directly related to the changes in the diff — do not flag pre-existing issues in unchanged code. Do not report a finding if your analysis concludes that the code is correct and no action is needed — only report findings that require the author to make a change or consider a specific alternative.\n")
	b.WriteString("Also consider whether the changes could cause side effects in other files that depend on or interact with the modified code (e.g. callers, importers, shared state). If you identify a potential side effect, anchor your finding to the relevant line in the diff and describe the affected downstream code in the description.\n\n")

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

	// Changed file contents for full context.
	writeFileContents(&b, pr.FileContents, pr.Files)

	// Diff.
	b.WriteString("## Diff\n```diff\n")
	b.WriteString(pr.Diff)
	b.WriteString("\n```\n\n")

	// Output format instructions.
	b.WriteString("## Output Format\n")
	b.WriteString("Return your findings as a JSON array inside a ```json code fence. Each finding must have these fields:\n\n")
	b.WriteString("- `id` (string): The rule ID that was violated, or a short kebab-case identifier for general findings.\n")
	b.WriteString("- `file` (string): The file path where the issue was found. **Must be a file from the diff.** If your finding relates to a file not in the diff (e.g. a downstream consequence), set `file` and `line` to the diff location that triggers the issue and mention the affected file in `description`.\n")
	b.WriteString("- `line` (int): The line number in the file (must be a changed or adjacent line visible in the diff).\n")
	b.WriteString("- `severity` (string): One of \"critical\", \"bug\", \"warning\", \"suggestion\", or \"nitpick\".\n")
	b.WriteString("  - \"critical\": Security vulnerabilities, data loss, crashes.\n")
	b.WriteString("  - \"bug\": Logic errors, incorrect behavior.\n")
	b.WriteString("  - \"warning\": Potential issues, performance problems, code smells.\n")
	b.WriteString("  - \"suggestion\": Better patterns, readability improvements.\n")
	b.WriteString("  - \"nitpick\": Minor style, naming, formatting.\n")
	b.WriteString("- `title` (string): A short title for the finding.\n")
	b.WriteString("- `description` (string): A detailed explanation of the issue.\n")
	b.WriteString("- `suggestion` (string, optional): A suggested fix or improvement.\n")
	first := startIndex + 1
	fmt.Fprintf(&b, "- `fix_ref` (string): A reference ID in the format `%d-<index>` where index starts at %d (e.g. `%d-%d`, `%d-%d`).\n", pr.Number, first, pr.Number, first, pr.Number, first+1)
	b.WriteString("\n**IMPORTANT — JSON escaping:** When your description or suggestion references code containing backslash sequences (e.g. `\\n`, `\\t`, `\\\"`), you MUST double-escape the backslash in the JSON string value. For example, to mention `fmt.Print(\"\\n\")` in a JSON string, write `fmt.Print(\"\\\\n\")`. A single `\\n` in JSON is a newline character, not the literal text `\\n`.\n")
	b.WriteString("\n**Do not include findings where your conclusion is that the code is correct or no action is needed.** If you evaluate something and determine it is fine, omit it entirely rather than reporting it. Specifically: if you begin analyzing a potential issue but then realize the code handles it correctly, do NOT emit a finding that walks through the concern and then concludes \"this is actually fine\" or \"no bug here\" — simply drop it. Every finding you emit must represent a real, actionable problem.\n")
	b.WriteString("\nIf there are no findings, return an empty array: `[]`.\n")
	b.WriteString("\nExample:\n```json\n[\n  {\n    \"id\": \"rule-id\",\n    \"file\": \"src/main.go\",\n    \"line\": 42,\n    \"severity\": \"warning\",\n    \"title\": \"Short title\",\n    \"description\": \"Detailed description.\",\n    \"suggestion\": \"Consider doing X instead.\",\n")
	fmt.Fprintf(&b, "    \"fix_ref\": \"%d-%d\"\n  }\n]\n```\n", pr.Number, first)

	return b.String()
}

// BuildReevaluatePrompt asks Claude which previous findings are now fixed.
// Each thread is assigned a unique sequential ID (thread-0, thread-1, ...)
// since threads span multiple review rounds and have no stable external identifier.
func BuildReevaluatePrompt(threads []ReviewThread, incrementalDiff string) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. You previously left findings on a pull request. The author has pushed new changes.\n\n")
	b.WriteString("## Previous Findings\n")
	b.WriteString("Here are the unresolved findings from previous reviews:\n\n")

	for i, t := range threads {
		fmt.Fprintf(&b, "- **thread-%d** at `%s:%d`\n", i, t.Path, t.Line)
		// Extract the first line of the body as the severity+rule summary.
		firstLine := t.Body
		if idx := strings.Index(t.Body, "\n"); idx >= 0 {
			firstLine = t.Body[:idx]
		}
		fmt.Fprintf(&b, "  %s\n", firstLine)
	}

	b.WriteString("\n## Changes Since Last Review\n```diff\n")
	b.WriteString(incrementalDiff)
	b.WriteString("\n```\n\n")

	b.WriteString("## Task\n")
	b.WriteString("Determine which of the previous findings have been fixed by the new changes.\n\n")
	b.WriteString("Return a JSON array of thread ID strings for findings that are NOW FIXED inside a ```json code fence.\n")
	b.WriteString("If none are fixed, return an empty array: `[]`.\n\n")
	b.WriteString("Example:\n```json\n[\"thread-0\", \"thread-2\"]\n```\n")

	return b.String()
}

// BuildIncrementalPrompt reviews only new code, avoiding duplicate reports.
// startIndex is the number of existing findings so fix_ref numbering continues.
func BuildIncrementalPrompt(diff string, cfg *ReviewConfig, knownIssues []ReviewThread, prNumber int, startIndex int, fileContents map[string]string, files []string) string {
	var b strings.Builder

	b.WriteString("You are a code reviewer. Review ONLY the following incremental changes and report NEW findings.\n")
	b.WriteString("You will be given the full contents of changed files for context, along with the diff. Only report issues that are directly related to the changes in the diff — do not flag pre-existing issues in unchanged code. Do not report a finding if your analysis concludes that the code is correct and no action is needed — only report findings that require the author to make a change or consider a specific alternative.\n")
	b.WriteString("Also consider whether the changes could cause side effects in other files that depend on or interact with the modified code (e.g. callers, importers, shared state). If you identify a potential side effect, anchor your finding to the relevant line in the diff and describe the affected downstream code in the description.\n\n")

	// Explicit allowlist of files in this diff.
	if len(files) > 0 {
		b.WriteString("## Files in This Diff\n")
		b.WriteString("The following files — and ONLY these files — are part of this incremental diff. Every finding you report MUST reference one of these exact paths. Do NOT reference any file that is not in this list.\n\n")
		for _, f := range files {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}

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

	// Known issues to avoid duplicating.
	if len(knownIssues) > 0 {
		b.WriteString("## Known Issues (DO NOT DUPLICATE)\n")
		b.WriteString("These issues are already reported and unresolved. Do NOT report them again:\n\n")
		for _, t := range knownIssues {
			fmt.Fprintf(&b, "- `%s:%d`\n", t.Path, t.Line)
		}
		b.WriteString("\n")
	}

	// Changed file contents for full context.
	writeFileContents(&b, fileContents, files)

	// Incremental diff.
	b.WriteString("## Incremental Diff\n```diff\n")
	b.WriteString(diff)
	b.WriteString("\n```\n\n")

	// Output format instructions.
	b.WriteString("## Output Format\n")
	b.WriteString("Return your findings as a JSON array inside a ```json code fence. Each finding must have these fields:\n\n")
	b.WriteString("- `id` (string): The rule ID that was violated, or a short kebab-case identifier for general findings.\n")
	b.WriteString("- `file` (string): The file path where the issue was found. **Must be one of the exact paths listed in \"Files in This Diff\" above.** If a file path does not appear in that list, do NOT reference it. If your finding relates to a downstream file not in the diff, set `file` and `line` to the diff location that triggers the issue and mention the affected file in `description`.\n")
	b.WriteString("- `line` (int): The line number in the file (must be a changed or adjacent line visible in the diff).\n")
	b.WriteString("- `severity` (string): One of \"critical\", \"bug\", \"warning\", \"suggestion\", or \"nitpick\".\n")
	b.WriteString("  - \"critical\": Security vulnerabilities, data loss, crashes.\n")
	b.WriteString("  - \"bug\": Logic errors, incorrect behavior.\n")
	b.WriteString("  - \"warning\": Potential issues, performance problems, code smells.\n")
	b.WriteString("  - \"suggestion\": Better patterns, readability improvements.\n")
	b.WriteString("  - \"nitpick\": Minor style, naming, formatting.\n")
	b.WriteString("- `title` (string): A short title for the finding.\n")
	b.WriteString("- `description` (string): A detailed explanation of the issue.\n")
	b.WriteString("- `suggestion` (string, optional): A suggested fix or improvement.\n")
	first := startIndex + 1
	fmt.Fprintf(&b, "- `fix_ref` (string): A reference ID in the format `%d-<index>` where index starts at %d (e.g. `%d-%d`, `%d-%d`).\n", prNumber, first, prNumber, first, prNumber, first+1)
	b.WriteString("\n**IMPORTANT — JSON escaping:** When your description or suggestion references code containing backslash sequences (e.g. `\\n`, `\\t`, `\\\"`), you MUST double-escape the backslash in the JSON string value. For example, to mention `fmt.Print(\"\\n\")` in a JSON string, write `fmt.Print(\"\\\\n\")`. A single `\\n` in JSON is a newline character, not the literal text `\\n`.\n")
	b.WriteString("\n**Do not include findings where your conclusion is that the code is correct or no action is needed.** If you evaluate something and determine it is fine, omit it entirely rather than reporting it. Specifically: if you begin analyzing a potential issue but then realize the code handles it correctly, do NOT emit a finding that walks through the concern and then concludes \"this is actually fine\" or \"no bug here\" — simply drop it. Every finding you emit must represent a real, actionable problem.\n")
	b.WriteString("\n**CRITICAL: Do NOT invent or hallucinate file paths, function names, or code that does not appear in the diff or the provided file contents. If a file or function is not shown above, do not reference it.**\n")
	b.WriteString("\nOnly report NEW issues found in the incremental diff. If there are no new findings, return an empty array: `[]`.\n")
	b.WriteString("\nExample:\n```json\n[\n  {\n    \"id\": \"rule-id\",\n    \"file\": \"src/main.go\",\n    \"line\": 42,\n    \"severity\": \"warning\",\n    \"title\": \"Short title\",\n    \"description\": \"Detailed description.\",\n    \"suggestion\": \"Consider doing X instead.\",\n")
	fmt.Fprintf(&b, "    \"fix_ref\": \"%d-%d\"\n  }\n]\n```\n", prNumber, first)

	return b.String()
}

// writeFileContents adds a "Changed File Contents" section to the prompt builder.
func writeFileContents(b *strings.Builder, fileContents map[string]string, files []string) {
	if len(fileContents) == 0 {
		return
	}

	b.WriteString("## Changed File Contents\n")
	b.WriteString("Below are the full contents of changed files. Use these to understand surrounding code, types, imports, and control flow. Do NOT report findings on unchanged code — only flag issues directly related to changes in the diff.\n\n")

	for _, path := range files {
		content, ok := fileContents[path]
		if !ok {
			continue
		}
		fmt.Fprintf(b, "### `%s`\n", path)
		b.WriteString("```\n")
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			fmt.Fprintf(b, "%d: %s\n", i+1, line)
		}
		b.WriteString("```\n\n")
	}
}
