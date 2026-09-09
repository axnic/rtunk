package download

import (
	"runtime"
	"strings"
	"testing"
)

// TestResolve_RealNodeShape is a regression test using the real
// runtimes/node/plugin.yaml shape (github.com/trunk-io/plugins, v1.10.2):
// an old macos-only variant constrained to "<16.0.0", followed by an
// unconstrained map-shaped variant covering linux/macos and multiple
// CPUs — a modern version must skip the first (constraint doesn't match)
// and resolve via the second, with the OS/CPU substitution correctly
// resolved for the current machine (not the raw trunk-keyed map, which
// pkg/shim's GOOS/GOARCH-keyed lookup would silently miss).
func TestResolve_RealNodeShape(t *testing.T) {
	group := Group{
		Name: "node",
		Variants: []Variant{
			{OSFixed: "macos", Constraint: "<16.0.0", URL: "https://nodejs.org/dist/v${version}/node-v${version}-darwin-x64.tar.gz"},
			{
				OSMap:  map[string]string{"linux": "linux", "macos": "darwin"},
				CPUMap: map[string]string{"x86_64": "x64", "arm_64": "arm64"},
				URL:    "https://nodejs.org/dist/v${version}/node-v${version}-${os}-${cpu}.tar.gz",
			},
		},
	}
	dl, reason := Resolve(group, "22.16.0", "node")
	if dl == nil {
		t.Fatalf("expected a match for a modern version, got reason: %s", reason)
	}
	if !strings.Contains(dl.URL, "${os}-${cpu}") {
		t.Errorf("expected the unconstrained (second) variant to win, got %+v", dl)
	}
	wantCPU := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	if dl.OSMap[runtime.GOOS] == "" || dl.ArchMap[runtime.GOARCH] != wantCPU {
		t.Errorf("expected os_map/arch_map resolved for %s/%s (cpu -> %q), got %+v", runtime.GOOS, runtime.GOARCH, wantCPU, dl)
	}

	// The old, pre-16 variant must still resolve correctly for an old
	// version, proving the constraint actually gates selection both ways.
	dl, reason = Resolve(group, "14.21.3", "node")
	if dl == nil {
		t.Fatalf("expected the constrained macos variant to match an old version, got reason: %s", reason)
	}
	if !strings.Contains(dl.URL, "darwin-x64.tar.gz") || dl.OSMap[runtime.GOOS] != "macos" {
		t.Errorf("expected the constrained (first) variant to win for an old version, got %+v", dl)
	}
}

// TestComputeArgs_StripsHistoricalPrefix mirrors taplo's real args block:
// a pinned "release-taplo-cli-0.6.7"-shaped version must yield a bare
// "0.6.7" semver, while a bare "0.10.0" pin (no prefix to strip) passes
// through unchanged.
func TestComputeArgs_StripsHistoricalPrefix(t *testing.T) {
	args := map[string]string{"semver": "${version}=>(?:release-cli-|release-taplo-cli-)?(?P<semver>.*)"}

	got, reason := computeArgs(args, "release-taplo-cli-0.6.7")
	if reason != "" {
		t.Fatalf("unexpected reason: %s", reason)
	}
	if got["semver"] != "0.6.7" {
		t.Errorf("expected the release- prefix stripped, got %+v", got)
	}

	got, reason = computeArgs(args, "0.10.0")
	if reason != "" {
		t.Fatalf("unexpected reason: %s", reason)
	}
	if got["semver"] != "0.10.0" {
		t.Errorf("expected a bare version to pass through unchanged, got %+v", got)
	}
}

func TestComputeArgs_NoCaptureGroupNamedAfterArg(t *testing.T) {
	_, reason := computeArgs(map[string]string{"semver": "${version}=>(?P<other>.*)"}, "1.0.0")
	if reason == "" {
		t.Error("expected a reason naming the missing capture group")
	}
}

func TestComputeArgs_Empty(t *testing.T) {
	got, reason := computeArgs(nil, "1.0.0")
	if got != nil || reason != "" {
		t.Errorf("expected a no-op for no args, got %+v, %q", got, reason)
	}
}

// TestResolve_RealTaploShape is a regression test using (part of) the real
// linters/taplo/plugin.yaml shape: several fixed-OS/CPU variants, each
// gated by a version constraint, covering the same platform across
// historical release-naming conventions (>=0.8.0 vs >=0.6.7) — the newer,
// first-listed range must win when both technically match.
func TestResolve_RealTaploShape(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("this fixture only covers the linux/macos variants")
	}
	group := Group{
		Name:             "taplo",
		RenameSingleFile: true,
		Args:             map[string]string{"semver": "${version}=>(?:release-cli-|release-taplo-cli-)?(?P<semver>.*)"},
		Variants: []Variant{
			{OSFixed: "linux", CPUFixed: "x86_64", Constraint: ">=0.8.0", URL: "https://github.com/tamasfe/taplo/releases/download/${semver}/taplo-linux-${cpu}.gz"},
			{OSFixed: "macos", CPUFixed: "x86_64", Constraint: ">=0.8.0", URL: "https://github.com/tamasfe/taplo/releases/download/${semver}/taplo-darwin-x86_64.gz"},
			{OSFixed: "macos", CPUFixed: "arm_64", Constraint: ">=0.8.0", URL: "https://github.com/tamasfe/taplo/releases/download/${semver}/taplo-darwin-aarch64.gz"},
			{OSFixed: "linux", CPUFixed: "x86_64", Constraint: ">=0.6.7", URL: "https://github.com/tamasfe/taplo/releases/download/release-taplo-cli-${semver}/taplo-x86_64-unknown-linux-gnu.tar.gz"},
			{OSFixed: "macos", CPUFixed: "x86_64", Constraint: ">=0.6.7", URL: "https://github.com/tamasfe/taplo/releases/download/release-taplo-cli-${semver}/taplo-x86_64-apple-darwin-gnu.tar.gz"},
		},
	}
	newVariant, oldVariant := "taplo-linux-", "release-taplo-cli-"
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		newVariant, oldVariant = "taplo-darwin-aarch64", "" // no >=0.6.7 arm64 macos variant in this fixture
	} else if runtime.GOOS == "darwin" {
		newVariant, oldVariant = "taplo-darwin-x86_64", "release-taplo-cli-"
	}

	dl, reason := Resolve(group, "0.10.0", "taplo")
	if dl == nil {
		t.Fatalf("expected a match, got reason: %s", reason)
	}
	if !strings.Contains(dl.URL, newVariant) {
		t.Errorf("expected the newer (>=0.8.0, first-listed) variant to win, got %+v", dl)
	}
	if dl.ExtraArgs["semver"] != "0.10.0" || !dl.RenameSingleFile {
		t.Errorf("expected semver derived from version (no prefix to strip) and RenameSingleFile carried over, got %+v", dl)
	}
	if resolved := substituteDownload(dl.URL, dl); strings.Contains(resolved, "${semver}") {
		t.Errorf("expected ${semver} resolved in the URL, got %q", resolved)
	}

	if oldVariant == "" {
		return // this platform has no older-range fixture variant to check against
	}
	dl, reason = Resolve(group, "0.7.0", "taplo")
	if dl == nil {
		t.Fatalf("expected the older range to match 0.7.0, got reason: %s", reason)
	}
	if !strings.Contains(dl.URL, oldVariant) {
		t.Errorf("expected the >=0.6.7 variant to win for 0.7.0, got %+v", dl)
	}
}
