package review

import (
	"encoding/json"
	"fmt"
	"strings"
)

// severityIcon returns the emoji icon for a severity level.
func severityIcon(severity string) string {
	switch strings.ToLower(severity) {
	case "error":
		return "\U0001F534" // 🔴
	case "warning":
		return "\U0001F7E1" // 🟡
	case "info":
		return "\U0001F535" // 🔵
	default:
		return "\U0001F535" // 🔵
	}
}

// FormatMarkdown formats review findings as a markdown string suitable for a
// PR comment.
func FormatMarkdown(result *ReviewResult) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## \U0001F33F Clanopy Review: PR #%d\n\n", result.PRNumber)

	// Summary section.
	if result.Summary != "" {
		fmt.Fprintf(&b, "### Summary\n%s\n", result.Summary)
	} else {
		// Build a default summary from severity counts.
		counts := map[string]int{}
		for _, f := range result.Findings {
			counts[strings.ToLower(f.Severity)]++
		}
		parts := []string{}
		total := len(result.Findings)
		if n := counts["error"]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d error", n))
			if n > 1 {
				parts[len(parts)-1] += "s"
			}
		}
		if n := counts["warning"]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d warning", n))
			if n > 1 {
				parts[len(parts)-1] += "s"
			}
		}
		if n := counts["info"]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d info", n))
		}
		fmt.Fprintf(&b, "### Summary\nFound %d issues (%s)\n", total, strings.Join(parts, ", "))
	}

	// Individual findings.
	for _, f := range result.Findings {
		b.WriteString("\n---\n\n")
		icon := severityIcon(f.Severity)
		fmt.Fprintf(&b, "### %s `%s` in `%s:%d`\n", icon, f.ID, f.File, f.Line)
		fmt.Fprintf(&b, "**%s**\n\n", f.Title)
		fmt.Fprintf(&b, "%s\n", f.Description)

		if f.Suggestion != "" {
			fmt.Fprintf(&b, "\n> **Suggestion**: %s\n", f.Suggestion)
		}

		if f.FixRef != "" {
			b.WriteString("\n<details><summary>Fix this issue</summary>\n\n")
			fmt.Fprintf(&b, "```\nclanopy fix %s\n```\n\n", f.FixRef)
			b.WriteString("</details>\n")
		}
	}

	return b.String()
}

// FormatJSON formats review findings as a JSON string.
func FormatJSON(result *ReviewResult) (string, error) {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling review result: %w", err)
	}
	return string(data), nil
}
