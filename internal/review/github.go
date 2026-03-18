package review

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
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
func PostReview(repo string, prNumber int, result *ReviewResult, diffFiles []string) error {
	// Sort findings by severity before formatting.
	sortFindings(result.Findings)

	// Build a set of files in the PR diff for fast lookup.
	diffFileSet := make(map[string]bool, len(diffFiles))
	for _, f := range diffFiles {
		diffFileSet[f] = true
	}

	// Build inline comments for findings that have file+line and are in the diff.
	var comments []reviewComment
	for _, f := range result.Findings {
		if f.File != "" && f.Line > 0 && diffFileSet[f.File] {
			comments = append(comments, reviewComment{
				Path: f.File,
				Line: f.Line,
				Body: FormatFindingComment(&f),
			})
		}
	}

	body := FormatReviewBody(result, diffFileSet)

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

// ReviewThread represents a review thread from a PR.
type ReviewThread struct {
	ID       string
	Path     string
	Line     int
	Body     string
	FixRef   string // extracted from body
	Resolved bool
}

// fixRefRe matches `clanopy fix <ref>` in a comment body.
var fixRefRe = regexp.MustCompile(`clanopy fix (\S+)`)

// graphQLThreadsResponse is the JSON shape returned by the review threads query.
type graphQLThreadsResponse struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				ReviewThreads struct {
					Nodes []struct {
						ID         string `json:"id"`
						IsResolved bool   `json:"isResolved"`
						Comments   struct {
							Nodes []struct {
								Body string `json:"body"`
								Path string `json:"path"`
								Line int    `json:"line"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// FetchReviewThreads gets all clanopy review threads from a PR via GraphQL.
func FetchReviewThreads(repo string, prNumber int) ([]ReviewThread, error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format %q, expected owner/name", repo)
	}
	owner, name := parts[0], parts[1]

	query := fmt.Sprintf(`{
  repository(owner: "%s", name: "%s") {
    pullRequest(number: %d) {
      reviewThreads(first: 100) {
        nodes {
          id
          isResolved
          comments(first: 1) {
            nodes { body path line }
          }
        }
      }
    }
  }
}`, owner, name, prNumber)

	cmd := exec.Command("gh", "api", "graphql", "-f", "query="+query)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh api graphql: %w", err)
	}

	var resp graphQLThreadsResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parsing graphql response: %w", err)
	}

	var threads []ReviewThread
	for _, node := range resp.Data.Repository.PullRequest.ReviewThreads.Nodes {
		if len(node.Comments.Nodes) == 0 {
			continue
		}
		comment := node.Comments.Nodes[0]

		// Filter to clanopy threads only.
		if !strings.Contains(comment.Body, "clanopy fix") {
			continue
		}

		var fixRef string
		if m := fixRefRe.FindStringSubmatch(comment.Body); m != nil {
			fixRef = m[1]
		}

		threads = append(threads, ReviewThread{
			ID:       node.ID,
			Path:     comment.Path,
			Line:     comment.Line,
			Body:     comment.Body,
			FixRef:   fixRef,
			Resolved: node.IsResolved,
		})
	}

	return threads, nil
}

// ResolveThread resolves a review thread via GraphQL mutation.
func ResolveThread(threadID string) error {
	query := fmt.Sprintf(`mutation { resolveReviewThread(input: { threadId: "%s" }) { thread { isResolved } } }`, threadID)
	cmd := exec.Command("gh", "api", "graphql", "-f", "query="+query)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh api graphql resolve: %w\n%s", err, string(out))
	}
	return nil
}

// ghReview is the JSON shape for a PR review from the REST API.
type ghReview struct {
	Body string `json:"body"`
}

// FetchPreviousReviewSHA gets the SHA from the last clanopy review's hidden data.
func FetchPreviousReviewSHA(repo string, prNumber int) string {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return ""
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", parts[0], parts[1], prNumber)
	out, err := exec.Command("gh", "api", apiPath).Output()
	if err != nil {
		return ""
	}

	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return ""
	}

	const prefix = "<!-- clanopy:review "
	const suffix = " -->"

	// Search from most recent to oldest.
	for i := len(reviews) - 1; i >= 0; i-- {
		body := reviews[i].Body
		idx := strings.Index(body, prefix)
		if idx < 0 {
			continue
		}
		start := idx + len(prefix)
		endIdx := strings.Index(body[start:], suffix)
		if endIdx < 0 {
			continue
		}
		jsonData := body[start : start+endIdx]
		var result ReviewResult
		if err := json.Unmarshal([]byte(jsonData), &result); err != nil {
			continue
		}
		if result.SHA != "" {
			return result.SHA
		}
	}

	return ""
}

// GetIncrementalDiff gets the diff since a given SHA.
func GetIncrementalDiff(baseSHA string) (string, error) {
	out, err := exec.Command("git", "diff", baseSHA+"..HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return string(out), nil
}

// PostAllClearReview posts a review when all findings have been resolved.
func PostAllClearReview(repo string, prNumber int) error {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format %q, expected owner/name", repo)
	}

	payload := reviewPayload{
		Event: "COMMENT",
		Body:  "## \U0001F33F Clanopy Review\n\nAll previous findings have been addressed. No new issues found. \u2728",
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", parts[0], parts[1], prNumber)
	cmd := exec.Command("gh", "api", apiPath, "--method", "POST", "--input", "-")
	cmd.Stdin = strings.NewReader(string(payloadJSON))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh api create review: %w\n%s", err, string(out))
	}
	return nil
}
