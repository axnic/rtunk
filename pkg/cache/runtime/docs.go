// Package runtime holds everything rtunk knows about one runtime type (go, node, python, ...):
// how a package is installed through that runtime's own package manager, and what extra
// environment its shims need. pkg/cache/download consults it through Lookup and knows no runtime
// by name. Which Renovate datasource tracks a runtime's packages is pkg/renovate's own concern,
// not this package's -- see that package's runtimeDatasources.
package runtime
