package review

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// Finding represents a single review issue found in the code.
type Finding struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Severity    string `json:"severity"` // One of: critical, bug, warning, suggestion, nitpick
	Title       string `json:"title"`
	Description string `json:"description"`
	Suggestion  string `json:"suggestion,omitempty"`
	FixRef      string `json:"fix_ref"`
}

// ReviewResult holds the complete output of a review run.
type ReviewResult struct {
	PRNumber int       `json:"pr_number"`
	Repo     string    `json:"repo"`
	Findings []Finding `json:"findings"`
	Summary  string    `json:"summary"`
	SHA      string    `json:"sha,omitempty"`
}

// jsonFenceRe matches a ```json ... ``` code fence.
var jsonFenceRe = regexp.MustCompile("(?s)```json\\s*\n(.*?)```")

// ParseFindings extracts findings from Claude's output by looking for a JSON
// array inside a ```json code fence.
func ParseFindings(output string) ([]Finding, error) {
	matches := jsonFenceRe.FindStringSubmatch(output)
	if matches == nil {
		return nil, fmt.Errorf("no ```json code fence found in output")
	}

	raw := matches[1]

	var findings []Finding
	if err := json.Unmarshal([]byte(raw), &findings); err != nil {
		return nil, fmt.Errorf("parsing findings JSON: %w", err)
	}

	return findings, nil
}
