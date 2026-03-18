package review

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CacheDir returns the path to the cache directory, anchored to the git repo root.
func CacheDir() string {
	// Find the git repo root so the cache is always in the same place
	// regardless of which subdirectory the command is run from.
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		root := strings.TrimSpace(string(out))
		return filepath.Join(root, ".clanopy", "reviews")
	}
	return filepath.Join(".clanopy", "reviews")
}

// SaveResult saves a review result to .clanopy/reviews/<pr>-<timestamp>.json
func SaveResult(result *ReviewResult) error {
	dir := CacheDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating cache directory: %w", err)
	}

	filename := fmt.Sprintf("%d-%d.json", result.PRNumber, time.Now().Unix())
	path := filepath.Join(dir, filename)

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling result: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing cache file: %w", err)
	}

	return nil
}

// LoadLatestResult loads the most recent review result for a PR.
func LoadLatestResult(prNumber int) (*ReviewResult, error) {
	dir := CacheDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading cache directory: %w", err)
	}

	prefix := fmt.Sprintf("%d-", prNumber)
	var matching []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".json") {
			matching = append(matching, e)
		}
	}

	if len(matching) == 0 {
		return nil, fmt.Errorf("no cached review found for PR #%d", prNumber)
	}

	// Sort by timestamp (embedded in filename) descending to get the latest.
	sort.Slice(matching, func(i, j int) bool {
		ti := extractTimestamp(matching[i].Name(), prefix)
		tj := extractTimestamp(matching[j].Name(), prefix)
		return ti > tj
	})

	path := filepath.Join(dir, matching[0].Name())
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading cache file: %w", err)
	}

	var result ReviewResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing cache file: %w", err)
	}

	return &result, nil
}

func extractTimestamp(filename, prefix string) int64 {
	// filename is like "42-1700000000.json"
	name := strings.TrimPrefix(filename, prefix)
	name = strings.TrimSuffix(name, ".json")
	ts, _ := strconv.ParseInt(name, 10, 64)
	return ts
}
