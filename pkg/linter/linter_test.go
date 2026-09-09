package linter

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
)

func TestGroupTargets(t *testing.T) {
	groups, err := groupTargets("${file}", []string{"a/b.go", "a/c.go"})
	if err != nil || len(groups) != 2 {
		t.Fatalf("expected 2 file groups, got %+v, err %v", groups, err)
	}

	groups, err = groupTargets("${parent}", []string{"a/b.go", "a/c.go", "d.go"})
	if err != nil || len(groups) != 2 {
		t.Fatalf("expected 2 deduped parent groups, got %+v, err %v", groups, err)
	}
	resolved := map[string]bool{}
	for _, g := range groups {
		resolved[g.resolved] = true
	}
	if !resolved["./a"] || !resolved["."] {
		t.Errorf("unexpected group resolutions: %+v", resolved)
	}

	if _, err := groupTargets("${bogus}", []string{"a.go"}); err == nil {
		t.Error("expected error for unsupported target template")
	}
}

func TestSubstitute(t *testing.T) {
	got := substitute("tool --source ${target} --out ${tmpfile}", "./dir", "/tmp/x")
	if got != "tool --source ./dir --out /tmp/x" {
		t.Errorf("got %q", got)
	}
}

func TestSeverityOf(t *testing.T) {
	if s, err := severityOf(""); err != nil || s.String() != "warning" {
		t.Errorf("empty should default to warning, got %v, err %v", s, err)
	}
	if s, err := severityOf("error"); err != nil || s.String() != "error" {
		t.Errorf("got %v, err %v", s, err)
	}
	if _, err := severityOf("critical"); err == nil {
		t.Error("expected error for invalid severity")
	}
}

func TestContainsInt(t *testing.T) {
	if !containsInt([]int{0, 1}, 1) || containsInt([]int{0, 1}, 2) {
		t.Error("containsInt logic broken")
	}
}

func TestResolveConfigArgs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".yamllint.yaml"), []byte("rules: {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	def := config.LinterDefinition{ConfigFlag: "-c", DirectConfigs: []string{".yamllint", ".yamllint.yaml", ".yamllint.yml"}}
	got := resolveConfigArgs(dir, def)
	if len(got) != 2 || got[0] != "-c" || got[1] != ".yamllint.yaml" {
		t.Errorf("expected [-c .yamllint.yaml], got %+v", got)
	}

	if got := resolveConfigArgs(dir, config.LinterDefinition{DirectConfigs: []string{".yamllint.yaml"}}); got != nil {
		t.Errorf("no ConfigFlag declared: expected nil, got %+v", got)
	}
	if got := resolveConfigArgs(t.TempDir(), def); got != nil {
		t.Errorf("no matching file present: expected nil, got %+v", got)
	}
}

func TestResolveConfigArgs_TrunkConfigsFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".trunk", "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".trunk", "configs", ".yamllint.yaml"), []byte("rules: {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	def := config.LinterDefinition{ConfigFlag: "-c", DirectConfigs: []string{".yamllint", ".yamllint.yaml", ".yamllint.yml"}}
	got := resolveConfigArgs(dir, def)
	want := filepath.Join(".trunk", "configs", ".yamllint.yaml")
	if len(got) != 2 || got[0] != "-c" || got[1] != want {
		t.Errorf("expected [-c %s], got %+v", want, got)
	}
}

func TestRunParser_PipesStdinResolvesPluginVar(t *testing.T) {
	pluginDir := t.TempDir()
	script := filepath.Join(pluginDir, "upper.py")
	if err := os.WriteFile(script, []byte(
		"import sys\nsys.stdout.write(sys.stdin.read().upper())\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runParser(t.TempDir(), pluginDir, config.Parser{Run: "python3 ${plugin}/upper.py"}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if out != "HELLO" {
		t.Errorf("expected the parser's stdout back, got %q", out)
	}
}

func TestRunParser_NonZeroExitIsError(t *testing.T) {
	_, err := runParser(t.TempDir(), "", config.Parser{Run: "false"}, "raw")
	if err == nil {
		t.Error("expected a non-zero parser exit to error")
	}
}
