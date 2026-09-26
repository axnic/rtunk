package download

import (
	"fmt"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/runtime"
)

// InstallPackage installs pkg@version through rt's own package manager into pkgInstallDir. What
// each runtime type does lives in pkg/trunk/runtime; a type it doesn't know (java, ...) fails
// explicitly rather than guessing, since trunk's own per-runtime install commands for anything
// else aren't part of the open plugin schema (see the spec's "Fetch mechanisms").
func InstallPackage(rt config.Runtime, runtimeInstallDir, pkgInstallDir, pkg, version string) error {
	r, ok := runtime.Lookup(rt.Type)
	if !ok || r.Install == nil {
		return fmt.Errorf("download: package-based fetch not yet supported for runtime %q", rt.Type)
	}
	return r.Install(runtimeInstallDir, pkgInstallDir, pkg, version)
}

// InstallPackagesFile installs every dependency named in the manifest at packagesFilePath through
// rt's own package manager into pkgInstallDir. Unlike InstallPackage (a single pkg@version), there
// is no version to key the install on -- the caller picks pkgInstallDir (pkg/trunk/actions keys it
// by the manifest's own content hash). Node-only in practice (see runtime.Runtime.InstallFile).
func InstallPackagesFile(rt config.Runtime, runtimeInstallDir, pkgInstallDir, packagesFilePath string) error {
	r, ok := runtime.Lookup(rt.Type)
	if !ok || r.InstallFile == nil {
		return fmt.Errorf("download: packages_file install not yet supported for runtime %q", rt.Type)
	}
	return r.InstallFile(runtimeInstallDir, pkgInstallDir, packagesFilePath)
}

// ExtraToolEnv returns environment a runtime+package tool's shim needs beyond what the plugin's
// own runtime_environment/linter_environment config provides (see runtime.Runtime.ShimEnv).
func ExtraToolEnv(rt config.Runtime, installDir string) ([]string, error) {
	r, ok := runtime.Lookup(rt.Type)
	if !ok || r.ShimEnv == nil {
		return nil, nil
	}
	return r.ShimEnv(installDir)
}
