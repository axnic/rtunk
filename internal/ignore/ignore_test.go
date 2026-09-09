package ignore

import (
	"os"
	"testing"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/diagnostic"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMatchesConfigIgnore_DoublestarAndNegation(t *testing.T) {
	entries := []config.LintIgnore{
		{Linters: []string{"ALL"}, Paths: []config.GlobPattern{"**/generated/**", "!**/generated/**/*.keep"}},
		{Linters: []string{"eslint"}, Paths: []config.GlobPattern{"legacy/**"}},
	}

	cases := []struct {
		path, linter string
		want         bool
	}{
		{"a/generated/b/c.go", "ruff", true},
		{"a/generated/b/c.keep", "ruff", false}, // negated back
		{"legacy/x.js", "eslint", true},
		{"legacy/x.js", "ruff", false}, // legacy/** only applies to eslint
		{"src/main.go", "ruff", false},
	}
	for _, c := range cases {
		got, err := MatchesConfigIgnore(c.path, c.linter, entries)
		if err != nil {
			t.Fatalf("%s/%s: %v", c.path, c.linter, err)
		}
		if got != c.want {
			t.Errorf("%s/%s: got %v, want %v", c.path, c.linter, got, c.want)
		}
	}
}

func TestFilterPaths(t *testing.T) {
	entries := []config.LintIgnore{{Linters: []string{"ALL"}, Paths: []config.GlobPattern{"dist/**"}}}
	out, err := FilterPaths([]string{"dist/a.js", "src/a.js"}, "eslint", entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0] != "src/a.js" {
		t.Errorf("got %+v", out)
	}
}

func TestParseDirectives_BareSameLine(t *testing.T) {
	content := []byte("const x = 1 // rtunk-ignore: reason\n")
	ds := ParseDirectives(content)
	if len(ds) != 1 || ds[0].Kind != KindLine || ds[0].Line != 1 || len(ds[0].Targets) != 0 {
		t.Fatalf("got %+v", ds)
	}
}

func TestParseDirectives_StandaloneAppliesToNextLine(t *testing.T) {
	content := []byte("// rtunk-ignore(eslint/no-unused-vars)\nconst x = 1\n")
	ds := ParseDirectives(content)
	if len(ds) != 1 || ds[0].Line != 2 {
		t.Fatalf("expected directive to target line 2, got %+v", ds)
	}
	if len(ds[0].Targets) != 1 || ds[0].Targets[0].Linter != "eslint" || ds[0].Targets[0].Code != "no-unused-vars" {
		t.Errorf("unexpected targets: %+v", ds[0].Targets)
	}
}

func TestParseDirectives_MultiTargetAndTrunkAlias(t *testing.T) {
	content := []byte("x = 1  # trunk-ignore(ruff/E501,gitleaks)\n")
	ds := ParseDirectives(content)
	if len(ds) != 1 || len(ds[0].Targets) != 2 {
		t.Fatalf("got %+v", ds)
	}
	if ds[0].Targets[0].Linter != "ruff" || ds[0].Targets[0].Code != "E501" {
		t.Errorf("target 0: %+v", ds[0].Targets[0])
	}
	if ds[0].Targets[1].Linter != "gitleaks" || ds[0].Targets[1].Code != "" {
		t.Errorf("target 1: %+v", ds[0].Targets[1])
	}
}

func TestParseDirectives_AllAndBlock(t *testing.T) {
	content := []byte("// rtunk-ignore-all(gitleaks)\n")
	ds := ParseDirectives(content)
	if len(ds) != 1 || ds[0].Kind != KindAll {
		t.Fatalf("got %+v", ds)
	}

	content = []byte("// rtunk-ignore-begin(ruff)\nbad1\nbad2\n// rtunk-ignore-end(ruff)\n")
	ds = ParseDirectives(content)
	if len(ds) != 2 || ds[0].Kind != KindBlockStart || ds[1].Kind != KindBlockEnd {
		t.Fatalf("got %+v", ds)
	}
}

func TestApply_SuppressesMatchingLineAndCode(t *testing.T) {
	content := []byte("import unused\nconst x = 1 // rtunk-ignore(eslint/no-unused-vars)\n")
	directives := ParseDirectives(content)
	diags := []diagnostic.Diagnostic{
		{Path: "f.js", Line: 2, LinterName: "eslint", Code: "no-unused-vars", Message: "unused"},
		{Path: "f.js", Line: 1, LinterName: "eslint", Code: "no-unused-vars", Message: "unused import"},
	}
	kept := Apply("f.js", diags, directives)
	if len(kept) != 1 || kept[0].Line != 1 {
		t.Fatalf("expected only line 1's diagnostic to survive, got %+v", kept)
	}
}

func TestApply_BlockRangeSuppresses(t *testing.T) {
	content := []byte("// rtunk-ignore-begin(ruff)\nbad1\nbad2\n// rtunk-ignore-end(ruff)\nbad3\n")
	directives := ParseDirectives(content)
	diags := []diagnostic.Diagnostic{
		{Path: "f.py", Line: 2, LinterName: "ruff", Code: "E1"},
		{Path: "f.py", Line: 3, LinterName: "ruff", Code: "E2"},
		{Path: "f.py", Line: 5, LinterName: "ruff", Code: "E3"},
	}
	kept := Apply("f.py", diags, directives)
	if len(kept) != 1 || kept[0].Line != 5 {
		t.Fatalf("expected only line 5 to survive the block, got %+v", kept)
	}
}

func TestApply_UnusedDirectiveProducesNote(t *testing.T) {
	content := []byte("const x = 1 // rtunk-ignore(eslint/no-unused-vars)\n")
	directives := ParseDirectives(content)
	kept := Apply("f.js", nil, directives) // no diagnostics at all: the directive did nothing
	if len(kept) != 1 || kept[0].Code != "rtunk/ignore-does-nothing" || kept[0].Severity != diagnostic.Note {
		t.Fatalf("expected an ignore-does-nothing note, got %+v", kept)
	}
}

func TestApply_SelfSilencedUnusedDirective(t *testing.T) {
	content := []byte("const x = 1 // rtunk-ignore(eslint/no-unused-vars,rtunk)\n")
	directives := ParseDirectives(content)
	kept := Apply("f.js", nil, directives)
	if len(kept) != 0 {
		t.Fatalf("expected the rtunk-silenced directive to produce no note, got %+v", kept)
	}
}

func TestFilterAll_ChecksFilesWithNoDiagnosticsToo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/clean.go", "const x = 1 // rtunk-ignore(govet)\n")
	diags, err := FilterAll(dir, []string{"clean.go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 1 || diags[0].Code != "rtunk/ignore-does-nothing" {
		t.Fatalf("got %+v", diags)
	}
}
