package review

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type ReviewConfig struct {
	Version      int               `yaml:"version"`
	Rules        []Rule            `yaml:"rules"`
	Context      string            `yaml:"context"`
	Ignore       []string          `yaml:"ignore"`
	MaxFindings  int               `yaml:"max_findings"`
	MaxFileSize  int               `yaml:"max_file_size"`  // per-file content limit in bytes (default 100KB)
	MaxTotalSize int               `yaml:"max_total_size"` // total file content limit in bytes (default 500KB)
	Evaluation   *EvaluationConfig `yaml:"evaluation"`
}

// EvaluationConfig holds per-evaluation-type settings for re-evaluation prompts.
type EvaluationConfig struct {
	CodeChange EvalTypeConfig `yaml:"code_change"`
	Reply      EvalTypeConfig `yaml:"reply"`
}

// EvalTypeConfig holds settings for a specific evaluation type.
type EvalTypeConfig struct {
	Context string `yaml:"context"`
}

// EffectiveMaxFileSize returns the per-file size limit, defaulting to 100KB.
func (c *ReviewConfig) EffectiveMaxFileSize() int {
	if c != nil && c.MaxFileSize > 0 {
		return c.MaxFileSize
	}
	return 100 * 1024
}

// EffectiveMaxTotalSize returns the total file content limit, defaulting to 500KB.
func (c *ReviewConfig) EffectiveMaxTotalSize() int {
	if c != nil && c.MaxTotalSize > 0 {
		return c.MaxTotalSize
	}
	return 500 * 1024
}

type Rule struct {
	ID           string   `yaml:"id"`
	Description  string   `yaml:"description"`
	Severity     string   `yaml:"severity"` // One of: critical, bug, warning, suggestion, nitpick
	Paths        []string `yaml:"paths"`
	ExcludePaths []string `yaml:"exclude_paths"`
}

// LoadConfig reads and parses a review config YAML file from the given path.
func LoadConfig(path string) (*ReviewConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg ReviewConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	return &cfg, nil
}

// FindConfig looks for .clanopy/review.yml starting from the current directory
// and walking up the directory tree until it finds one or reaches the root.
func FindConfig() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}

	for {
		candidate := filepath.Join(dir, ".clanopy", "review.yml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("no .clanopy/review.yml found")
}
