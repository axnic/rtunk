package shim

import (
	"os"
	"path/filepath"
	"runtime"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/download"
)

// InstallBinary ensures d's binary is present under
// base/<name>/<version>/<osArch>/ (idempotent — a second call is a stat +
// return), returning a Shim that execs it directly. It also covers a
// language runtime's own distribution (pkg/runtime) — extracted, it's
// structurally the same thing: a directory with binaries in it — and the
// "rust"/"java" entries of trunk's own catalog, which are plain
// GitHub-release downloads rather than package-manager installs.
//
// rt, if non-nil, is merged into the result's Path/Env — the java/jar
// case, where a tool downloaded as a plain jar still needs its runtime's
// own `java` on PATH to exec it (`java -jar ${linter}/x.jar`).
//
// No marker file, no lazy reinstall-on-invocation: this is trunk's own
// wrapper-script mechanism, deliberately not replicated here — rtunk
// already resolves everything synchronously (this call) before running a
// command.
func InstallBinary(name string, d *config.Download, base string, rt *Shim) (Shim, error) {
	osArch := runtime.GOOS + "_" + download.ArchName(d)
	dir := filepath.Join(base, name, d.Version, osArch)
	binPath := filepath.Join(dir, d.Bin)
	if _, err := os.Stat(binPath); err != nil {
		if err := download.Install(d, dir); err != nil {
			return Shim{}, err
		}
		if err := os.Chmod(binPath, 0o755); err != nil {
			return Shim{}, err
		}
	}
	s := Shim{Name: name, Exec: d.Bin, Dir: dir, Path: []string{dir}}
	if rt != nil {
		s.Path = append(s.Path, rt.Path...)
		s.Env = rt.Env
	}
	return s, nil
}
