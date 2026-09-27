package download

import (
	goruntime "runtime"
	"strconv"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// goosNames maps Go's GOOS to trunk's own os vocabulary (ARCHITECTURE.md "downloads[].os"),
// the key a DownloadEntry.OS map is keyed by.
var goosNames = map[string]string{"darwin": "macos", "linux": "linux", "windows": "windows"}

// goarchNames is goosNames' CPU equivalent.
var goarchNames = map[string]string{"amd64": "x86_64", "arm64": "arm_64"}

// HostOSName returns the current host's name in trunk's own os vocabulary (linux, macos,
// windows) -- the same vocabulary Command.Platforms and DownloadEntry.OS are keyed by. ok is
// false when runtime.GOOS has no mapping (mirrors MatchEntry's own goosNames lookup).
func HostOSName() (string, bool) {
	name, ok := goosNames[goruntime.GOOS]
	return name, ok
}

// MatchEntry returns the first entries[] whose OS/CPU both cover goos/goarch and whose Version
// range (if any) admits version, along with the upstream-naming values (DownloadEntry.OS/CPU's
// map values) to template into ${os}/${cpu}. ok is false if nothing matches -- no entry for this
// platform, no entry whose range admits version, or Go's GOOS/GOARCH has no mapping in trunk's
// vocabulary.
//
// The Version check matters because a real plugin recipe can list several entries for the same
// OS/CPU, each gated to a different upstream release: python-build-standalone re-tags its whole
// release under a new date periodically, so "<=3.10.17" and "<=3.14.4" point at two different
// dated release URLs. Without this, the first OS/CPU match won regardless of range and could
// point an unrelated version at a release tag that never shipped it (a real 404 in production).
func MatchEntry(entries []config.DownloadEntry, goos, goarch, version string) (config.DownloadEntry, string, string, bool) {
	osName, ok := goosNames[goos]
	if !ok {
		return config.DownloadEntry{}, "", "", false
	}
	cpuName, ok := goarchNames[goarch]
	if !ok {
		return config.DownloadEntry{}, "", "", false
	}
	for _, e := range entries {
		osVal, ok := e.OS[osName]
		if !ok {
			continue
		}
		cpuVal, ok := e.CPU[cpuName]
		if !ok {
			continue
		}
		if !VersionSatisfies(e.Version, version) {
			continue
		}
		return e, osVal, cpuVal, true
	}
	return config.DownloadEntry{}, "", "", false
}

// VersionSatisfies reports whether version meets constraint, a DownloadEntry.Version or
// Command.Version string like "<=3.14.4", "<2.13.1", or ">=2.13.1". An empty constraint always
// matches (most entries have none). Malformed constraints or versions fall back to matching, so a
// data quirk this doesn't understand degrades to the pre-fix behavior (first match wins, or "keep
// the command") rather than rejecting every entry and leaving nothing to run.
func VersionSatisfies(constraint, version string) bool {
	if constraint == "" {
		return true
	}
	for _, op := range []string{"<=", ">=", "<", ">", "="} {
		if rest, ok := strings.CutPrefix(constraint, op); ok {
			cmp, ok := compareVersions(version, rest)
			if !ok {
				return true
			}
			switch op {
			case "<=":
				return cmp <= 0
			case ">=":
				return cmp >= 0
			case "<":
				return cmp < 0
			case ">":
				return cmp > 0
			case "=":
				return cmp == 0
			}
		}
	}
	return true
}

// compareVersions compares two dotted-numeric versions (e.g. "3.14.4"), component by component,
// treating a missing trailing component as 0. ok is false if either side has a non-numeric
// component (e.g. a pre-release suffix like "3.14.0a6") -- callers treat that as "can't tell,
// don't filter it out" rather than guessing.
func compareVersions(a, b string) (cmp int, ok bool) {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv int
		if i < len(as) {
			n, err := strconv.Atoi(as[i])
			if err != nil {
				return 0, false
			}
			av = n
		}
		if i < len(bs) {
			n, err := strconv.Atoi(bs[i])
			if err != nil {
				return 0, false
			}
			bv = n
		}
		if av != bv {
			if av < bv {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}
