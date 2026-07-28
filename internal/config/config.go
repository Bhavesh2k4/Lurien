// Package config loads and validates Lurien's runtime configuration. Adding a
// company means editing companies.yaml — no code change.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"lurien/internal/core"
)

// LoadDotEnv reads KEY=VALUE lines from path (if it exists) and sets them in the
// process environment, without overwriting variables already set. Missing file
// is not an error. Minimal by design — no external dependency.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`) // strip optional surrounding quotes
		if k == "" {
			continue
		}
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
	return sc.Err()
}

// File is the on-disk shape of companies.yaml.
type File struct {
	DefaultInterval time.Duration   `yaml:"default_interval"`
	Companies       []CompanyEntry  `yaml:"companies"`
}

// CompanyEntry is one monitored company.
type CompanyEntry struct {
	Name     string            `yaml:"name"`
	Slug     string            `yaml:"slug"`
	Provider string            `yaml:"provider"`
	Config   map[string]string `yaml:"config"`
	Enabled  *bool             `yaml:"enabled"`
	Interval time.Duration     `yaml:"interval"`
	Classify ClassifyOverride  `yaml:"classify"`
}

// ClassifyOverride holds per-company classification hints.
type ClassifyOverride struct {
	MaxEarlyLevel int `yaml:"max_early_level"`
}

// Load reads and validates companies.yaml, returning ready-to-run sources.
func Load(path string) ([]core.Source, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.DefaultInterval == 0 {
		f.DefaultInterval = 3 * time.Minute
	}

	seen := map[string]bool{}
	var sources []core.Source
	for i, c := range f.Companies {
		if c.Slug == "" || c.Provider == "" {
			return nil, fmt.Errorf("companies[%d] (%q): slug and provider are required", i, c.Name)
		}
		id := c.Provider + ":" + c.Slug
		if seen[id] {
			return nil, fmt.Errorf("duplicate source %q", id)
		}
		seen[id] = true

		enabled := true
		if c.Enabled != nil {
			enabled = *c.Enabled
		}
		interval := c.Interval
		if interval == 0 {
			interval = f.DefaultInterval
		}
		sources = append(sources, core.Source{
			ID:       id,
			Company:  core.Company{Slug: c.Slug, Name: c.Name},
			Provider: c.Provider,
			Params:   c.Config,
			Interval: interval,
			Enabled:  enabled,
			Hints:    core.ClassifyHints{MaxEarlyLevel: c.Classify.MaxEarlyLevel},
		})
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("%s: no companies configured", path)
	}
	return sources, nil
}
