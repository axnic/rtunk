package download

import "testing"

func TestVersionConstraint_RealExamples(t *testing.T) {
	cases := []struct {
		constraint, version string
		want                bool
	}{
		// node's runtimes/node/plugin.yaml: "<16.0.0" gates the old macos-only
		// x64 variant; a modern version must NOT match it.
		{"<16.0.0", "22.16.0", false},
		{"<16.0.0", "14.21.3", true},
		// taplo's linters/taplo/plugin.yaml: ">=0.8.0" / ">=0.6.7" gate two
		// historical release conventions; a recent version matches the
		// newer (first-listed) one.
		{">=0.8.0", "0.10.0", true},
		{">=0.8.0", "0.7.0", false},
		{">=0.6.7", "0.7.0", true},
		// bare version (no operator) means exact match.
		{"1.2.3", "1.2.3", true},
		{"1.2.3", "1.2.4", false},
		// missing trailing components default to 0.
		{">=1.2", "1.2.0", true},
	}
	for _, c := range cases {
		vc, ok := parseVersionConstraint(c.constraint)
		if !ok {
			t.Fatalf("failed to parse constraint %q", c.constraint)
		}
		if got := vc.satisfies(c.version); got != c.want {
			t.Errorf("%q.satisfies(%q) = %v, want %v", c.constraint, c.version, got, c.want)
		}
	}
}

func TestParseVersionConstraint_Invalid(t *testing.T) {
	if _, ok := parseVersionConstraint(">=abc"); ok {
		t.Error("expected a non-numeric constraint to fail to parse")
	}
	if _, ok := parseVersionConstraint(""); ok {
		t.Error("expected an empty constraint to fail to parse")
	}
}

func TestVersionConstraint_UnparsableVersionNeverSatisfies(t *testing.T) {
	vc, ok := parseVersionConstraint(">=1.0.0")
	if !ok {
		t.Fatal("expected to parse")
	}
	if vc.satisfies("not-a-version") {
		t.Error("expected an unparsable target version to never satisfy a constraint")
	}
}
