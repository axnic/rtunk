package config

import "fmt"

// Tools are additional managed CLI tools, independent of linting.
// https://docs.trunk.io/code-quality/overview/getting-started/tools
type Tools struct {
	Enabled     []PackageVersion `yaml:"enabled"`
	Definitions []ToolDefinition `yaml:"definitions"`
}

// Validate checks each Enabled entry's "name" or "name@version" format.
func (t Tools) Validate() error {
	for _, pv := range t.Enabled {
		if err := pv.Validate(); err != nil {
			return fmt.Errorf("tools.enabled: %w", err)
		}
	}
	return nil
}

// ToolDefinition declares how to fetch and expose a managed CLI tool.
type ToolDefinition struct {
	Name             string   `yaml:"name"`
	Download         string   `yaml:"download"`
	KnownGoodVersion string   `yaml:"known_good_version"`
	Shims            []string `yaml:"shims"`
}
