// Package config is the public, complete model of the .trunk/trunk.yaml /
// .rtunk/rtunk.yaml schema, plus file discovery, parsing, and validation.
//
// The package is split by schema domain: config.go (root Config + CLI/Cache/
// Repo), runtimes.go, plugins.go, lint.go (linters — the biggest block:
// definitions, commands, ignore/triggers), tools.go, actions.go, types.go
// (format-constrained string types shared across those: PackageVersion,
// GlobPattern, RegexPattern). parse.go/load.go/validate.go hold the
// file-discovery, decoding, and schema-validation entry points.
//
// rtunk.yaml is trunk.yaml plus the Cache section and the Command.Severity /
// LinterDefinition.Download fields — every other field matches a real
// trunk.yaml as produced by `trunk init`. Every field links to the trunk.io
// doc page it is functionally inspired by — read for behavior, never copied
// verbatim.
//
// Structurally, trunk publishes its own canonical JSON Schema at
// https://static.trunk.io/pub/trunk-yaml-schema.json — that is the reference
// for "what shape does a real trunk.yaml have"; validate.go's schema/v0.1.json
// is rtunk's own (smaller, hand-maintained) schema, which additionally pins
// `version` to exactly "0.1".
package config
