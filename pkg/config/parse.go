package config

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// Parse decodes raw YAML bytes (a .trunk/trunk.yaml or .rtunk/rtunk.yaml)
// into a Config, validating it against schema/v0.1.json first (see
// validate.go — only version 0.1 is currently accepted).
func Parse(data []byte) (*Config, error) {
	var doc map[string]interface{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := validate(doc); err != nil {
		return nil, err
	}

	c := &Config{}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// ParseLinterDefinition decodes a single bundled/plugin definition.yaml.
func ParseLinterDefinition(data []byte) (*LinterDefinition, error) {
	d := &LinterDefinition{}
	if err := yaml.Unmarshal(data, d); err != nil {
		return nil, fmt.Errorf("decode linter definition: %w", err)
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return d, nil
}
