package review

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// PRData holds PR metadata and diff.
type PRData struct {
	Number     int
	Title      string
	Body       string
	Author     string
	BaseBranch string
	HeadBranch string
	Diff       string
	Files      []string
}

// ghPRView is the JSON shape returned by gh pr view.
type ghPRView struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
	Files       []struct {
		Path string `json:"path"`
	} `json:"files"`
}

// FetchPR gets PR metadata and diff using the gh CLI.
func FetchPR(repo string, number int) (*PRData, error) {
	numStr := fmt.Sprintf("%d", number)

	// Fetch PR metadata as JSON.
	viewOut, err := exec.Command("gh", "pr", "view", numStr,
		"--repo", repo,
		"--json", "title,body,author,baseRefName,headRefName,files",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr view: %w", err)
	}

	var view ghPRView
	if err := json.Unmarshal(viewOut, &view); err != nil {
		return nil, fmt.Errorf("parsing gh pr view output: %w", err)
	}

	// Fetch the diff.
	diffOut, err := exec.Command("gh", "pr", "diff", numStr,
		"--repo", repo,
	).Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr diff: %w", err)
	}

	files := make([]string, len(view.Files))
	for i, f := range view.Files {
		files[i] = f.Path
	}

	return &PRData{
		Number:     number,
		Title:      view.Title,
		Body:       view.Body,
		Author:     view.Author.Login,
		BaseBranch: view.BaseRefName,
		HeadBranch: view.HeadRefName,
		Diff:       string(diffOut),
		Files:      files,
	}, nil
}

// PostComment posts a review comment on a PR.
func PostComment(repo string, number int, body string) error {
	numStr := fmt.Sprintf("%d", number)
	cmd := exec.Command("gh", "pr", "comment", numStr,
		"--repo", repo,
		"--body", body,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh pr comment: %w\n%s", err, string(out))
	}
	return nil
}

// reviewPayload is the JSON structure for the GitHub PR review API.
type reviewPayload struct {
	Event    string          `json:"event"`
	Body     string          `json:"body"`
	Comments []reviewComment `json:"comments"`
}

// reviewComment is a single inline comment in a PR review.
type reviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

// PostReview posts a PR review with inline comments using the GitHub API.
// Findings with file and line information become inline comments; others are
// included in the review body.
func PostReview(repo string, prNumber int, result *ReviewResult) error {
	// Sort findings by severity before formatting.
	sortFindings(result.Findings)

	// Build inline comments for findings that have file+line.
	var comments []reviewComment
	for _, f := range result.Findings {
		if f.File != "" && f.Line > 0 {
			comments = append(comments, reviewComment{
				Path: f.File,
				Line: f.Line,
				Body: FormatFindingComment(&f),
			})
		}
	}

	body := FormatReviewBody(result)

	payload := reviewPayload{
		Event:    "COMMENT",
		Body:     body,
		Comments: comments,
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling review payload: %w", err)
	}

	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format %q, expected owner/name", repo)
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", parts[0], parts[1], prNumber)
	cmd := exec.Command("gh", "api", apiPath, "--method", "POST", "--input", "-")
	cmd.Stdin = strings.NewReader(string(payloadJSON))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh api create review: %w\n%s", err, string(out))
	}
	return nil
}

// FetchReviewFromPR extracts cached review data from a PR comment's hidden HTML tag.
func FetchReviewFromPR(repo string, prNumber int) (*ReviewResult, error) {
	numStr := fmt.Sprintf("%d", prNumber)
	out, err := exec.Command("gh", "pr", "view", numStr,
		"--repo", repo,
		"--json", "comments",
		"--jq", ".comments[].body",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("fetching PR comments: %w", err)
	}

	// Look for <!-- clanopy:review {...} --> in comments.
	const prefix = "<!-- clanopy:review "
	const suffix = " -->"
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, suffix) {
			jsonData := line[len(prefix) : len(line)-len(suffix)]
			var result ReviewResult
			if err := json.Unmarshal([]byte(jsonData), &result); err != nil {
				continue
			}
			return &result, nil
		}
	}

	return nil, fmt.Errorf("no clanopy review data found in PR #%d comments", prNumber)
}

// DetectRepo gets owner/name from the current git remote.
func DetectRepo() (string, error) {
	out, err := exec.Command("gh", "repo", "view",
		"--json", "nameWithOwner",
		"--jq", ".nameWithOwner",
	).Output()
	if err != nil {
		return "", fmt.Errorf("gh repo view: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
