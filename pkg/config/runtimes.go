package config

import "fmt"

// Runtimes are managed language runtimes available to linters.
// https://docs.trunk.io/code-quality/overview/getting-started/runtimes
type Runtimes struct {
	// Enabled is a list of "name@version" entries, e.g. "node@20.11.0".
	Enabled []PackageVersion `yaml:"enabled"`
}

// Validate checks each Enabled entry's "name" or "name@version" format.
func (r Runtimes) Validate() error {
	for _, pv := range r.Enabled {
		if err := pv.Validate(); err != nil {
			return fmt.Errorf("runtimes.enabled: %w", err)
		}
	}
	return nil
}
