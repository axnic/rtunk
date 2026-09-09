// Package download resolves and fetches a plugin's declared download
// recipes: picking the right variant for the current platform/version out
// of a multi-variant catalog entry (Resolve), and turning a single,
// already-resolved recipe into bytes on disk — fetched, checksum-verified,
// and extracted (Install).
package download

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
)

// Group is a named, possibly multi-variant download recipe — one entry of
// a plugin.yaml's top-level `downloads:` list, normalized to a gob-safe
// shape (pkg/plugin's trunkDownloadGroup/trunkDownloadVariant, whose
// OS/CPU fields decode as `any`, are only used transiently during YAML
// decode and normalized into this shape right after).
type Group struct {
	Name string
	// Args derives extra named template variables from a regex over an
	// already-known one — real shape (taplo):
	// {"semver": "${version}=>(?:release-cli-|release-taplo-cli-)?(?P<semver>.*)"},
	// stripping a historical release-tag prefix off a pinned version to get
	// the bare semver a newer release URL convention needs. See computeArgs.
	Args map[string]string
	// RenameSingleFile means each variant's download is the tool's single
	// binary itself, compressed (real examples are all gzip, named ${target}.gz)
	// rather than an archive with a directory structure.
	RenameSingleFile bool
	Variants         []Variant
}

// Variant is one platform/version-gated entry of a Group's own `downloads:`
// list.
type Variant struct {
	// OSMap/CPUMap are set when os/cpu was a remapping table (trufflehog's
	// shape); OSFixed/CPUFixed when it's a single fixed string per variant
	// (taplo's shape) — matchPlatform matches either.
	OSMap    map[string]string
	CPUMap   map[string]string
	OSFixed  string
	CPUFixed string
	// Constraint is the variant's own `version:` field (real examples:
	// node's "<16.0.0", taplo's ">=0.8.0") gating which variant applies for
	// a given target version; empty means unconditional.
	Constraint      string
	URL             string
	StripComponents int
}

// PlatformGOOS maps trunk's platform names to Go's runtime.GOOS.
var PlatformGOOS = map[string]string{
	"windows": "windows",
	"linux":   "linux",
	"macos":   "darwin",
}

// PlatformGOARCH maps trunk's CPU family names to Go's runtime.GOARCH.
var PlatformGOARCH = map[string]string{
	"x86_64": "amd64",
	"arm_64": "arm64",
}

// Resolve picks the first variant (in file order) of group matching both
// the current OS/CPU and, if constrained, version — real plugin.yaml files
// split a download into several variants either to cover every platform in
// one go via a remapping table (trufflehog: one variant, os/cpu given as
// maps) or one fixed-OS/CPU variant per platform, optionally further split
// across historical release conventions by a version constraint (node's
// "<16.0.0", taplo's ">=0.8.0"/">=0.6.7").
//
// The OS/CPU substitution for THIS machine is resolved now, not deferred:
// config.Download.OSMap/ArchMap always end up as the single-entry,
// runtime.GOOS/GOARCH-keyed shape Install's own osName/archName expect —
// passing a variant's raw multi-key trunk-family map straight through
// (keyed by names like "macos", never "darwin") would silently miss that
// lookup and fall back to a bare, unmapped runtime.GOOS/GOARCH — which
// happens to match trufflehog's own release names by coincidence, but not
// node's (its cpu map's value for "x86_64" is "x64", not "amd64").
func Resolve(group Group, version, bin string) (dl *config.Download, reason string) {
	if len(group.Variants) == 0 {
		return nil, fmt.Sprintf("download %q has no variants", group.Name)
	}
	extraArgs, reason := computeArgs(group.Args, version)
	if reason != "" {
		return nil, fmt.Sprintf("download %q: %s", group.Name, reason)
	}
	for _, v := range group.Variants {
		if v.Constraint != "" {
			c, ok := parseVersionConstraint(v.Constraint)
			if !ok {
				reason = fmt.Sprintf("download %q: unparsable version constraint %q", group.Name, v.Constraint)
				continue
			}
			if !c.satisfies(version) {
				continue // not an error: just not this variant's version range
			}
		}
		osSub, ok := matchPlatform(v.OSMap, v.OSFixed, PlatformGOOS, runtime.GOOS)
		if !ok {
			continue
		}
		cpuSub, ok := matchPlatform(v.CPUMap, v.CPUFixed, PlatformGOARCH, runtime.GOARCH)
		if !ok {
			continue
		}
		return &config.Download{
			Version:          version,
			URL:              v.URL,
			OSMap:            map[string]string{runtime.GOOS: osSub},
			ArchMap:          map[string]string{runtime.GOARCH: cpuSub},
			Bin:              bin,
			StripComponents:  v.StripComponents,
			ExtraArgs:        extraArgs,
			RenameSingleFile: group.RenameSingleFile,
		}, ""
	}
	if reason != "" {
		return nil, reason
	}
	return nil, fmt.Sprintf("no variant matches the current platform (%s/%s) and version %s", runtime.GOOS, runtime.GOARCH, version)
}

// computeArgs evaluates group.Args (each a "<expr>=>regex" pair — expr's own
// "${version}" is substituted first, then regex's named capture group
// matching the arg's own name supplies its value) into a plain
// name->value map for substituteDownload to fold into ${name} substitution
// alongside the built-in ${version}/${os}/${cpu}/${ext}.
func computeArgs(args map[string]string, version string) (map[string]string, string) {
	if len(args) == 0 {
		return nil, ""
	}
	out := make(map[string]string, len(args))
	for name, expr := range args {
		src, pattern, ok := strings.Cut(expr, "=>")
		if !ok {
			return nil, fmt.Sprintf("arg %q: expected \"<expr>=>regex\", got %q", name, expr)
		}
		src = strings.ReplaceAll(src, "${version}", version)
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Sprintf("arg %q: %v", name, err)
		}
		m := re.FindStringSubmatch(src)
		if m == nil {
			return nil, fmt.Sprintf("arg %q: %q does not match %q", name, src, pattern)
		}
		found := false
		for i, groupName := range re.SubexpNames() {
			if groupName == name {
				out[name] = m[i]
				found = true
			}
		}
		if !found {
			return nil, fmt.Sprintf("arg %q: regex %q has no capture group named %q", name, pattern, name)
		}
	}
	return out, ""
}

// matchPlatform resolves what a variant's os/cpu field substitutes to for
// the current platform: for a remapping table (m, keyed by a trunk-side
// family name), one of its keys must translate — via family (e.g.
// PlatformGOOS) — to want, and that key's own value is the substitution;
// for a single fixed name (fixed, e.g. taplo's `cpu: x86_64`), fixed
// itself must translate to want, and fixed itself (not its translation)
// is the substitution — real URLs reference the trunk-side spelling
// ("x86_64"), not Go's ("amd64").
func matchPlatform(m map[string]string, fixed string, family map[string]string, want string) (substitution string, ok bool) {
	if m != nil {
		for trunkName, sub := range m {
			if family[trunkName] == want {
				return sub, true
			}
		}
		return "", false
	}
	if fixed != "" {
		if family[fixed] == want {
			return fixed, true
		}
		return "", false
	}
	// Neither shape given: this field isn't platform-restricted at all —
	// no real example seen needs this, but match unconditionally rather
	// than silently excluding every platform.
	return want, true
}
