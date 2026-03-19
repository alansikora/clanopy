package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PRData holds PR metadata and diff.
type PRData struct {
	Number       int
	Title        string
	Body         string
	Author       string
	BaseBranch   string
	HeadBranch   string
	Diff         string
	Files        []string
	FileContents map[string]string // path -> full file content
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

// diffLineMap maps each file to its sorted list of valid line numbers from the diff.
type diffLineMap map[string][]int

// parseDiffLines extracts valid line numbers per file from a unified diff.
func parseDiffLines(diff string) diffLineMap {
	valid := make(diffLineMap)
	var currentFile string
	var lineNum int

	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++ b/") {
			currentFile = line[6:]
			continue
		}
		if strings.HasPrefix(line, "@@ ") {
			// Parse hunk header: @@ -old,count +new,count @@
			if idx := strings.Index(line, "+"); idx >= 0 {
				rest := line[idx+1:]
				if comma := strings.IndexAny(rest, ", "); comma >= 0 {
					rest = rest[:comma]
				}
				fmt.Sscanf(rest, "%d", &lineNum)
			}
			continue
		}
		if currentFile == "" || lineNum == 0 {
			continue
		}
		if strings.HasPrefix(line, "-") {
			continue
		}
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, " ") {
			valid[currentFile] = append(valid[currentFile], lineNum)
			lineNum++
			continue
		}
	}
	return valid
}

// nearestLine returns the closest valid diff line for a file:line pair.
// Returns the line itself if valid, the nearest valid line in that file,
// or 0 if the file is not in the diff at all.
func (d diffLineMap) nearestLine(file string, line int) int {
	lines, ok := d[file]
	if !ok || len(lines) == 0 {
		return 0
	}
	best := lines[0]
	bestDist := abs(line - best)
	for _, l := range lines[1:] {
		dist := abs(line - l)
		if dist < bestDist {
			best = l
			bestDist = dist
		}
	}
	return best
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// PostReview posts a PR review with inline comments using the GitHub API.
// Findings with file and line information become inline comments; others are
// included in the review body.
func PostReview(repo string, prNumber int, result *ReviewResult, diff string) error {
	// Sort findings by severity before formatting.
	sortFindings(result.Findings)

	// Parse the diff to find valid line positions for inline comments.
	validLines := parseDiffLines(diff)

	// A finding can be inlined if its file is in the diff.
	canInline := func(f Finding) bool {
		return f.File != "" && f.Line > 0 && validLines.nearestLine(f.File, f.Line) > 0
	}

	comments := make([]reviewComment, 0)
	for _, f := range result.Findings {
		if canInline(f) {
			comments = append(comments, reviewComment{
				Path: f.File,
				Line: validLines.nearestLine(f.File, f.Line),
				Body: FormatFindingComment(&f),
			})
		}
	}

	body := FormatReviewBody(result, canInline)

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

// FetchReviewFromPR extracts cached review data from a PR review's hidden HTML tag.
func FetchReviewFromPR(repo string, prNumber int) (*ReviewResult, error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format %q, expected owner/name", repo)
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", parts[0], parts[1], prNumber)
	out, err := exec.Command("gh", "api", apiPath).Output()
	if err != nil {
		return nil, fmt.Errorf("fetching PR reviews: %w", err)
	}

	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, fmt.Errorf("parsing PR reviews: %w", err)
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
		return &result, nil
	}

	return nil, fmt.Errorf("no clanopy review data found in PR #%d reviews", prNumber)
}

// FetchFindingFromPR searches all clanopy reviews on a PR for a specific fix_ref.
// Unlike FetchReviewFromPR (which returns the latest review), this searches every
// review so that fix_ref links from older review rounds still resolve correctly.
func FetchFindingFromPR(repo string, prNumber int, fixRef string) (*Finding, error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format %q, expected owner/name", repo)
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", parts[0], parts[1], prNumber)
	out, err := exec.Command("gh", "api", apiPath).Output()
	if err != nil {
		return nil, fmt.Errorf("fetching PR reviews: %w", err)
	}

	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, fmt.Errorf("parsing PR reviews: %w", err)
	}

	const prefix = "<!-- clanopy:review "
	const suffix = " -->"

	for _, rev := range reviews {
		idx := strings.Index(rev.Body, prefix)
		if idx < 0 {
			continue
		}
		start := idx + len(prefix)
		endIdx := strings.Index(rev.Body[start:], suffix)
		if endIdx < 0 {
			continue
		}
		var result ReviewResult
		if err := json.Unmarshal([]byte(rev.Body[start:start+endIdx]), &result); err != nil {
			continue
		}
		for i := range result.Findings {
			if result.Findings[i].FixRef == fixRef {
				return &result.Findings[i], nil
			}
		}
	}

	return nil, fmt.Errorf("fix_ref %q not found in any review on PR #%d", fixRef, prNumber)
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
	Resolved bool
}

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

		threads = append(threads, ReviewThread{
			ID:       node.ID,
			Path:     comment.Path,
			Line:     comment.Line,
			Body:     comment.Body,
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
	NodeID string `json:"node_id"`
	Body   string `json:"body"`
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

// FilesFromDiff extracts the list of file paths touched in a unified diff.
func FilesFromDiff(diff string) []string {
	var files []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++ b/") {
			path := line[6:]
			if !seen[path] {
				seen[path] = true
				files = append(files, path)
			}
		}
	}
	return files
}

// GetIncrementalDiff gets the diff since a given SHA.
func GetIncrementalDiff(baseSHA string) (string, error) {
	// Ensure the base SHA is available locally (shallow clones may not have it).
	exec.Command("git", "fetch", "--depth=1", "origin", baseSHA).Run()
	out, err := exec.Command("git", "diff", baseSHA+"..HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return string(out), nil
}

// PostCleanReview posts a review when the first review finds no issues.
func PostCleanReview(repo string, prNumber int) error {
	return postSimpleReview(repo, prNumber, "\U0001F33F Clanopy reviewed this PR \u2014 no issues found.")
}

// PostAllClearReview posts a review when all previous findings have been resolved.
// If minimizeFailed is true, a note is appended warning about visible old reviews.
func PostAllClearReview(repo string, prNumber int, minimizeFailed bool) error {
	body := "## \U0001F33F Clanopy Review\n\nAll previous findings have been addressed. No new issues found. \u2728"
	if minimizeFailed {
		body += "\n\n> \u26A0\uFE0F Some previous review comments could not be minimized and may still be visible."
	}
	return postSimpleReview(repo, prNumber, body)
}

func postSimpleReview(repo string, prNumber int, body string) error {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format %q, expected owner/name", repo)
	}

	payload := reviewPayload{
		Event:    "COMMENT",
		Body:     body,
		Comments: make([]reviewComment, 0),
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

// FindClanopyReviewNodeIDs returns the node_ids of all clanopy reviews on a PR.
func FindClanopyReviewNodeIDs(repo string, prNumber int) ([]string, error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format %q, expected owner/name", repo)
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", parts[0], parts[1], prNumber)
	out, err := exec.Command("gh", "api", apiPath).Output()
	if err != nil {
		return nil, fmt.Errorf("fetching PR reviews: %w", err)
	}

	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, fmt.Errorf("parsing PR reviews: %w", err)
	}

	const prefix = "<!-- clanopy:review "

	var nodeIDs []string
	for _, rev := range reviews {
		if strings.Contains(rev.Body, prefix) {
			nodeIDs = append(nodeIDs, rev.NodeID)
		}
	}

	return nodeIDs, nil
}

// MinimizeComment hides a comment on GitHub using the minimizeComment GraphQL mutation.
func MinimizeComment(nodeID string) error {
	cmd := exec.Command("gh", "api", "graphql",
		"-f", "query=mutation($id:ID!){minimizeComment(input:{subjectId:$id,classifier:RESOLVED}){minimizedComment{isMinimized}}}",
		"-F", "id="+nodeID,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh api graphql minimize: %w\n%s", err, string(out))
	}
	return nil
}

// FetchFileContents reads the full contents of changed files from disk.
// It skips files that are too large, binary, deleted, or match ignore patterns.
// Returns a map of path->content and a list of skipped file paths.
func FetchFileContents(files []string, ignorePatterns []string, maxPerFile, maxTotal int) (map[string]string, []string) {
	contents := make(map[string]string)
	var skipped []string
	totalSize := 0

	for _, path := range files {
		// Check ignore patterns.
		if matchesIgnore(path, ignorePatterns) {
			skipped = append(skipped, path)
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			// File may have been deleted in this PR — skip gracefully.
			continue
		}

		// Skip binary files (null bytes in first 512 bytes).
		peek := data
		if len(peek) > 512 {
			peek = peek[:512]
		}
		if bytes.ContainsRune(peek, 0) {
			skipped = append(skipped, path)
			continue
		}

		size := len(data)

		// Skip files exceeding per-file limit.
		if size > maxPerFile {
			skipped = append(skipped, path)
			continue
		}

		// Stop if total budget would be exceeded.
		if totalSize+size > maxTotal {
			skipped = append(skipped, path)
			continue
		}

		contents[path] = string(data)
		totalSize += size
	}

	return contents, skipped
}

// isSetupPR detects whether this is the initial clanopy setup PR.
// Returns true only when a new workflow file referencing clanopy is added AND
// the PR contains no other files beyond expected setup artifacts (workflow +
// config), so that PRs bundling real code changes are never silently skipped.
func isSetupPR(diff string, files []string) bool {
	// All files must be known setup paths.
	for _, f := range files {
		if !isSetupFile(f) {
			return false
		}
	}

	// At least one newly added workflow file must reference clanopy.
	lines := strings.Split(diff, "\n")
	for i := 0; i < len(lines)-1; i++ {
		if lines[i] != "--- /dev/null" {
			continue
		}
		plusLine := lines[i+1]
		if !strings.HasPrefix(plusLine, "+++ b/.github/workflows/") {
			continue
		}
		for j := i + 2; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "--- ") || strings.HasPrefix(lines[j], "diff --git") {
				break
			}
			if strings.HasPrefix(lines[j], "+") && strings.Contains(lines[j], "clanopy") {
				return true
			}
		}
	}
	return false
}

// isSetupFile returns true if the file path is a known clanopy setup artifact.
func isSetupFile(path string) bool {
	return strings.HasPrefix(path, ".github/workflows/") ||
		strings.HasPrefix(path, ".clanopy/")
}

// matchesIgnore checks if a path matches any of the ignore glob patterns.
func matchesIgnore(path string, patterns []string) bool {
	for _, pat := range patterns {
		if matched, _ := filepath.Match(pat, path); matched {
			return true
		}
		// Also try matching against just the filename.
		if matched, _ := filepath.Match(pat, filepath.Base(path)); matched {
			return true
		}
	}
	return false
}
