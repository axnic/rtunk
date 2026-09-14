// Command rtunk is the CLI entry point. See ROADMAP.md for the staged command set; this file
// wires the process's argv/stdio/exit code to internal/cli, which implements the grammar itself.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

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
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	cli.Version = resolveVersion()
	if err := cli.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "rtunk:", err)
		os.Exit(1)
	}
}
