package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Load finds .rtunk/rtunk.yaml (priority) or .trunk/trunk.yaml, walking up
// from dir to the filesystem root, and parses it. Returns a defaulted
// Config (Repo.TrunkBranch: "main") if neither file exists.
func Load(dir string) (*Config, error) {
	for _, candidate := range []string{".rtunk/rtunk.yaml", ".trunk/trunk.yaml"} {
		if path, ok := findUp(dir, candidate); ok {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read config %s: %w", path, err)
			}
			cfg, err := Parse(data)
			if err != nil {
				return nil, fmt.Errorf("parse config %s: %w", path, err)
			}
			applyDefaults(cfg)
			return cfg, nil
		}
	}
	return Default(), nil
}

// Default returns a Config with no file loaded, defaults applied.
func Default() *Config {
	c := &Config{}
	applyDefaults(c)
	return c
}

func applyDefaults(c *Config) {
	if c.Repo.TrunkBranch == "" {
		c.Repo.TrunkBranch = "main"
	}
}

func findUp(dir, rel string) (string, bool) {
	for {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
