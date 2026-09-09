package config

import "testing"

func TestPackageVersion_Validate(t *testing.T) {
	valid := []PackageVersion{"gofmt", "gofmt@1.22.0", "golangci-lint2@2.12.2"}
	for _, v := range valid {
		if err := v.Validate(); err != nil {
			t.Errorf("%q: expected valid, got %v", v, err)
		}
	}

	invalid := []PackageVersion{"", "gofmt@", "@1.22.0", "gofmt@1@2"}
	for _, v := range invalid {
		if err := v.Validate(); err == nil {
			t.Errorf("%q: expected an error, got nil", v)
		}
	}
}

func TestPackageVersion_NameAndVersion(t *testing.T) {
	if p := PackageVersion("gofmt@1.22.0"); p.Name() != "gofmt" || p.Version() != "1.22.0" {
		t.Errorf("got name=%q version=%q", p.Name(), p.Version())
	}
	if p := PackageVersion("gofmt"); p.Name() != "gofmt" || p.Version() != "" {
		t.Errorf("unpinned: got name=%q version=%q", p.Name(), p.Version())
	}
}

func TestGlobPattern_Validate(t *testing.T) {
	valid := []GlobPattern{"**/generated/**", "!**/generated/**/*.keep", "ALL"}
	for _, g := range valid {
		if err := g.Validate(); err != nil {
			t.Errorf("%q: expected valid, got %v", g, err)
		}
	}
	if err := GlobPattern("").Validate(); err == nil {
		t.Error(`"": expected an error, got nil`)
	}
	if err := GlobPattern("!").Validate(); err == nil {
		t.Error(`"!": expected an error (empty pattern after negation), got nil`)
	}
	if err := GlobPattern("[unterminated").Validate(); err == nil {
		t.Error(`"[unterminated": expected an error (bad glob syntax), got nil`)
	}
	if err := GlobPattern("!src/[").Validate(); err == nil {
		t.Error(`"!src/[": expected an error (bad glob syntax after negation), got nil`)
	}
}

func TestGlobPattern_NegatedAndPattern(t *testing.T) {
	g := GlobPattern("!legacy/**")
	if !g.Negated() || g.Pattern() != "legacy/**" {
		t.Errorf("got Negated=%v Pattern=%q", g.Negated(), g.Pattern())
	}
	g = GlobPattern("legacy/**")
	if g.Negated() || g.Pattern() != "legacy/**" {
		t.Errorf("got Negated=%v Pattern=%q", g.Negated(), g.Pattern())
	}
}

func TestRegexPattern_Validate(t *testing.T) {
	if err := RegexPattern(`(?P<path>.*):(?P<line>\d+): (?P<message>.*)`).Validate(); err != nil {
		t.Errorf("expected valid regex, got %v", err)
	}
	if err := RegexPattern("").Validate(); err == nil {
		t.Error("empty: expected an error, got nil")
	}
	if err := RegexPattern("(unclosed").Validate(); err == nil {
		t.Error("malformed regex: expected an error, got nil")
	}
}

func TestConfig_Validate_RejectsMalformedPackageVersion(t *testing.T) {
	_, err := Parse([]byte("version: 0.1\nlint:\n  enabled:\n    - \"gofmt@1@2\"\n"))
	if err == nil {
		t.Fatal("expected an error for a malformed lint.enabled entry, got nil")
	}
}

func TestConfig_Validate_RejectsMalformedIgnorePattern(t *testing.T) {
	_, err := Parse([]byte("version: 0.1\nlint:\n  ignore:\n    - linters: [ALL]\n      paths: [\"\"]\n"))
	if err == nil {
		t.Fatal("expected an error for an empty lint.ignore path, got nil")
	}
}

func TestConfig_Validate_RejectsMalformedRegex(t *testing.T) {
	data := []byte(`
version: 0.1
lint:
  definitions:
    - name: broken
      files: [ALL]
      commands:
        - name: lint
          run: "broken ${target}"
          output: regex
          parse_regex: "(unclosed"
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected an error for a malformed parse_regex, got nil")
	}
}

func TestParseLinterDefinition_RejectsMalformedFilesGlob(t *testing.T) {
	_, err := ParseLinterDefinition([]byte("name: broken\nfiles: [\"\"]\n"))
	if err == nil {
		t.Fatal("expected an error for an empty files glob, got nil")
	}
}
