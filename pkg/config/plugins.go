package config

// Plugins declares remote definition sources.
// https://docs.trunk.io/code-quality/overview/getting-started/configuration/plugins
type Plugins struct {
	Sources []PluginSource `yaml:"sources"`
}

// PluginSource is a git repo of definitions, cloned at a pinned Ref.
type PluginSource struct {
	// ID names this source, referenced nowhere else but for readability.
	ID string `yaml:"id"`
	// URI is a git repo URL, cloned at Ref.
	URI string `yaml:"uri"`
	// Ref must be a tag or SHA — never a branch (reproducibility).
	Ref string `yaml:"ref"`
}
