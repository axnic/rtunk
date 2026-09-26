# Runtime Package: Design

**Status:** approved by the user through live discussion (layout: one package, one file per
runtime; the `Spec` type renamed `Runtime`; `finalizeInstall` moved to a leaf package).

## Goal

Everything specific to one runtime type (`go`, `node`, `python`, `ruby`, `rust`, `php`) lives in
one place: how rtunk installs a package through that runtime's package manager, what extra
environment its shims need, and which Renovate datasource tracks its packages. Today that
knowledge is split across two packages and glued together by a comment:

- `pkg/trunk/download/runtime.go` dispatches on `rt.Type` in three separate `switch`es
  (`InstallPackage`, `InstallPackagesFile`, `ExtraToolEnv`) to `runtime_<x>.go`.
- `pkg/trunk/renovate/annotate.go` has its own `runtimeDatasources` table and `goVersionPrefix`.
- `runtime_go.go` prepends `v` to the version before `go install`, with a comment pointing at
  `renovate.goVersionPrefix`: two halves of one fact about Go, in two packages.

After this change, adding a runtime means adding one file. `download` and `renovate` know no
runtime by name.

## Non-goals

- Downloading a runtime's own toolchain (the generic `Download` recipe path) stays in
  `download`.
- The GitHub-based annotation resolution (`github-releases`, `github-tags`) is not runtime
  specific and stays in `renovate`.
- No external behaviour change: same installs, same annotations, same CLI output.

## Design

### `pkg/trunk/runtime` (new)

Imports neither `config`, `download` nor `renovate`, so no import cycle is possible. It is keyed
by the runtime type string.

```go
package runtime

// Runtime is everything rtunk knows about one runtime type. A nil func / empty string means
// "not supported" (the behaviour of the former switch `default:` branches).
type Runtime struct {
	// Install installs pkg@version through the runtime's package manager into pkgDir.
	Install func(runtimeDir, pkgDir, pkg, version string) error
	// InstallFile installs every dependency of a manifest (node's package.json only).
	InstallFile func(runtimeDir, pkgDir, file string) error
	// ShimEnv returns environment an installed package tool's shim needs beyond the plugin's own
	// runtime_environment/linter_environment: a value only known once the package is on disk,
	// which those templates can't express (python's PYTHONPATH only).
	ShimEnv func(installDir string) ([]string, error)
	// Datasource is the Renovate datasource tracking this runtime's packages; "" = none.
	Datasource string
	// ExtractVersion is the Renovate extractVersion regex, when the datasource's versions
	// carry something a trunk.yaml pin doesn't (go's "v" prefix only).
	ExtractVersion string
}

// Lookup returns the Runtime for a runtime type; ok is false for an unknown type (java, ...).
func Lookup(typ string) (Runtime, bool)
```

The registry is one unexported `map[string]Runtime` in `runtime.go`, no `init()`
registration, no blank imports. Each runtime file defines its own `<x>Runtime` var; adding a
runtime is that file plus one line in the map.

One file per runtime, each with its test moved alongside:

| File        | Install            | InstallFile | ShimEnv      | Datasource  | ExtractVersion      |
| ----------- | ------------------ | ----------- | ------------ | ----------- | ------------------- |
| `go.go`     | `go install`       |             |              | `go`        | `^v(?<version>.+)$` |
| `node.go`   | `npm install`      | yes         |              | `npm`       |                     |
| `python.go` | `pip --prefix`     |             | `PYTHONPATH` | `pypi`      |                     |
| `php.go`    | `composer require` |             |              | `packagist` |                     |
| `rust.go`   | `cargo install`    |             |              | `crate`     |                     |
| `ruby.go`   | `gem install`      |             |              |             |                     |

`go.go` holds both the `v` prefix added before `go install` and the `ExtractVersion` regex that
strips it back for Renovate: the coupling is now in one file. Java and any future type are absent
from the registry, so `Lookup` reports `ok=false`, and callers keep today's explicit "not yet
supported" error.

Installs keep their current scaffolding (`MkdirAll` parent, `MkdirTemp` scratch, run the package
manager, finalize). Error messages change prefix from `download:` to `runtime:`.

### `pkg/trunk/install` (new, leaf)

`finalizeInstall` (`pkg/trunk/download/extract.go`) is used by every package install and by
`InstallDownload`. The "destDir already exists as a directory" check it needs is inlined (3
lines) rather than dragging `download.dirNonEmpty` along. `runtime` cannot import `download` (`download` imports `runtime`), so it moves
to a leaf package as `install.Finalize` and both import it. Exporting it from `runtime` for
`download` to call was rejected: the dependency would read backwards.

### Consumers

- `download/runtime.go` keeps `InstallPackage`, `InstallPackagesFile` and `ExtraToolEnv` as thin
  wrappers (`fetchToolRef` and `pkg/trunk/actions/run.go` call them, plus their tests): each
  becomes `Lookup(rt.Type)`, then the matching field, or the existing "not yet supported for
  runtime %q" error when the type or field is absent. The `switch`es and all
  `download/runtime_<x>.go` files (with their tests) are deleted; callers and the public API of
  `download` are unchanged.
- `renovate.resolveToolAnnotation`: `Lookup(tool.Runtime)`; `ok` is false, as today, when the
  runtime is unknown or `Datasource == ""`. `runtimeDatasources` and `goVersionPrefix` are
  deleted; `Annotation.ExtractVersion` comes from `Runtime.ExtractVersion`.

## Testing

- The `runtime_*_test.go` files in `download` stay where they are: they are external tests that
  drive `download.InstallPackage` / `ExtraToolEnv` through the wrappers, which makes them the
  behaviour guard for the move. No assertion matched the old `download:` error prefix, so none
  changed.
- New `runtime/runtime_test.go`: for every runtime type, `Install` is set and `Datasource`,
  `ExtractVersion`, `InstallFile`, `ShimEnv` match what the former `runtimeDatasources` table,
  `goVersionPrefix` and the three `switch`es said; `java` is absent.
- `renovate` tests are unchanged except the one that referenced the deleted `goVersionPrefix`
  constant, which now asserts the literal regex.
- `go test ./...` green before and after. `golangci-lint` was not available in the session that
  implemented this.

## Naming note

The package is named `runtime`, which shadows the standard library's `runtime` for importers.
`download/cache.go` already aliases the stdlib one as `goruntime`; any file importing both
aliases the stdlib one the same way. `runtime.Runtime` is distinct from `config.Runtime` (the
trunk.yaml definition): the former is behaviour, the latter is data.
