package download

import (
	"strconv"
	"strings"
)

// versionConstraint parses a real trunk plugin.yaml download variant's
// `version:` field (real examples: node's "<16.0.0", taplo's ">=0.8.0") —
// an operator (<, <=, >, >=, ==, =) followed by a dotted numeric version,
// or a bare version (implicitly "=="). Only numeric major.minor.patch-style
// parts are compared; no pre-release/build-metadata handling, which no
// real example seen needs.
type versionConstraint struct {
	op  string
	ver []int
}

var constraintOps = []string{"<=", ">=", "==", "<", ">", "="}

func parseVersionConstraint(s string) (versionConstraint, bool) {
	for _, op := range constraintOps {
		if rest, ok := strings.CutPrefix(s, op); ok {
			v, ok := parseVersionParts(strings.TrimSpace(rest))
			if !ok {
				return versionConstraint{}, false
			}
			return versionConstraint{op: op, ver: v}, true
		}
	}
	v, ok := parseVersionParts(strings.TrimSpace(s))
	if !ok {
		return versionConstraint{}, false
	}
	return versionConstraint{op: "==", ver: v}, true
}

func parseVersionParts(s string) ([]int, bool) {
	if s == "" {
		return nil, false
	}
	fields := strings.Split(s, ".")
	out := make([]int, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// compareVersionParts compares a to b component-wise, treating a missing
// trailing component as 0 (so "1.2" == "1.2.0").
func compareVersionParts(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// satisfies reports whether version meets c — false if version doesn't
// parse as a dotted numeric version.
func (c versionConstraint) satisfies(version string) bool {
	v, ok := parseVersionParts(version)
	if !ok {
		return false
	}
	cmp := compareVersionParts(v, c.ver)
	switch c.op {
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	case ">":
		return cmp > 0
	case ">=":
		return cmp >= 0
	default: // "==", "="
		return cmp == 0
	}
}
