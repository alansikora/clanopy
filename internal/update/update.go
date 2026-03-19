package update

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/alansikora/clanopy/internal/config"
)

const (
	repo         = "alansikora/clanopy"
	cacheTTL     = 24 * time.Hour
	cacheFile    = "update-check.json"
	apiTimeout   = 5 * time.Second
	fetchTimeout = 60 * time.Second
)

// Release holds information about a GitHub release.
type Release struct {
	TagName string `json:"tag_name"`
}

// cachedCheck stores the last update check result.
type cachedCheck struct {
	LatestVersion string    `json:"latest_version"`
	CheckedAt     time.Time `json:"checked_at"`
}

func cachePath() string {
	return filepath.Join(config.ConfigDir(), cacheFile)
}

// readCache returns the cached check result, or nil if expired/missing.
func readCache() *cachedCheck {
	data, err := os.ReadFile(cachePath())
	if err != nil {
		return nil
	}
	var c cachedCheck
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	if time.Since(c.CheckedAt) > cacheTTL {
		return nil
	}
	return &c
}

func writeCache(version string) {
	c := cachedCheck{
		LatestVersion: version,
		CheckedAt:     time.Now(),
	}
	data, _ := json.Marshal(c)
	os.MkdirAll(filepath.Dir(cachePath()), 0755)
	os.WriteFile(cachePath(), data, 0644)
}

// CheckLatest returns the latest release version from GitHub.
// Uses a file cache to avoid hitting the API on every invocation.
func CheckLatest() (string, error) {
	if c := readCache(); c != nil {
		return c.LatestVersion, nil
	}

	client := &http.Client{Timeout: apiTimeout}
	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("github api: %s", resp.Status)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	writeCache(release.TagName)
	return release.TagName, nil
}

// IsNewer returns true if latest is a newer version than current.
// Both should be in "vX.Y.Z" format.
func IsNewer(current, latest string) bool {
	cur := parseVersion(current)
	lat := parseVersion(latest)
	if cur == nil || lat == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if lat[i] > cur[i] {
			return true
		}
		if lat[i] < cur[i] {
			return false
		}
	}
	return false
}

func parseVersion(v string) []int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return nil
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil
			}
			n = n*10 + int(c-'0')
		}
		nums[i] = n
	}
	return nums
}

// CheckUpdateNotice returns a notice string if an update is available,
// or empty string if current version is up to date or check fails.
// This is designed to be non-blocking and silent on errors.
func CheckUpdateNotice(currentVersion string) string {
	latest, err := CheckLatest()
	if err != nil {
		return ""
	}
	if IsNewer(currentVersion, latest) {
		return latest
	}
	return ""
}

// Upgrade downloads and installs the latest (or specified) version.
func Upgrade(targetVersion string) error {
	if targetVersion == "" {
		latest, err := fetchLatest()
		if err != nil {
			return fmt.Errorf("fetching latest version: %w", err)
		}
		targetVersion = latest
	}

	osName := runtime.GOOS
	arch := runtime.GOARCH

	// Find the download URL
	url, err := findAssetURL(targetVersion, osName, arch)
	if err != nil {
		return err
	}

	// Download the archive
	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	// Extract the binary to a temp file
	tmpDir, err := os.MkdirTemp("", "clanopy-upgrade-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	tmpBin := filepath.Join(tmpDir, "clanopy")
	if err := extractBinary(resp.Body, tmpBin); err != nil {
		return fmt.Errorf("extracting: %w", err)
	}

	// Find current binary path
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current binary: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}

	// Replace current binary
	if err := replaceBinary(tmpBin, exe); err != nil {
		return err
	}

	// Clear the update check cache so the notice disappears
	os.Remove(cachePath())

	return nil
}

// fetchLatest gets the latest release tag directly from GitHub (no cache).
func fetchLatest() (string, error) {
	client := &http.Client{Timeout: apiTimeout}
	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("github api: %s", resp.Status)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

// findAssetURL finds the download URL for a specific release asset.
func findAssetURL(tag, osName, arch string) (string, error) {
	client := &http.Client{Timeout: apiTimeout}
	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", repo, tag))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("release %s not found: %s", tag, resp.Status)
	}

	var release struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	suffix := fmt.Sprintf("_%s_%s.tar.gz", osName, arch)
	for _, a := range release.Assets {
		if strings.HasSuffix(a.Name, suffix) {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("no asset found for %s/%s in release %s", osName, arch, tag)
}

// extractBinary extracts the clanopy binary from a tar.gz stream.
func extractBinary(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) == "clanopy" && hdr.Typeflag == tar.TypeReg {
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(f, tr)
			return err
		}
	}
	return fmt.Errorf("clanopy binary not found in archive")
}

// replaceBinary atomically replaces the target binary with the new one.
func replaceBinary(src, dst string) error {
	// On macOS/Linux we can rename over the existing binary
	// First, copy to a temp file next to the destination (same filesystem)
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".clanopy-new-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	srcFile, err := os.Open(src)
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	defer srcFile.Close()

	if _, err := io.Copy(tmp, srcFile); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("replacing binary: %w", err)
	}

	return nil
}
