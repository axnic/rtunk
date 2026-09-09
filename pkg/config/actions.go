package config

// Actions are automations (git hooks, triggers).
// https://docs.trunk.io/code-quality/overview/getting-started/actions
type Actions struct {
	Enabled  []string `yaml:"enabled"`
	Disabled []string `yaml:"disabled"`
}
