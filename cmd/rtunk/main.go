// Command rtunk is the CLI entry point. See ROADMAP.md for the staged command set; this file
// wires the process's argv/stdio/exit code to internal/cli, which implements the grammar itself.
package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/xunleii/rtunk/internal/cli"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z" (goreleaser or an
// equivalent release pipeline's job -- see docs/superpowers/specs/2026-09-14-v0.6-upgrade-
// design.md's own "prerequisite gap" section for why no such pipeline exists yet). Falls back to
// the Go module version recorded by `go install <module>@<version>` (a real, common install path
// this project doesn't control the build flags for), then to the literal "dev" when neither is
// available (a local `go build`/`go run`).
var version = "dev"

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && !isUnresolvedVersion(info.Main.Version) {
		return info.Main.Version
	}
	return "dev"
}

// isUnresolvedVersion reports whether v is a build-info version that doesn't identify a real
// release: the empty string, Go's literal "(devel)" (a `go test`/`go run` build), or a Go VCS
// pseudo-version (a plain `go build` in a module with no reachable semver tag, e.g.
// "v0.0.0-20260914192836-83c4ff4b160c") -- all three should fall back to "dev".
func isUnresolvedVersion(v string) bool {
	return v == "" || v == "(devel)" || strings.HasPrefix(v, "v0.0.0-")
}

func main() {
	cli.Version = resolveVersion()
	if err := cli.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "rtunk:", err)
		os.Exit(1)
	}
}
