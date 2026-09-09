package output

import (
	"regexp"
	"testing"
)

func TestParseRegex(t *testing.T) {
	re := regexp.MustCompile(`^(?P<path>[^:]+):(?P<line>\d+):(?P<col>\d+): (?P<message>.*)$`)
	raw := "main.go:10:2: unreachable code\nnot a match\nother.go:1:1: unused import"

	diags := ParseRegex(re, raw, "govet", "lint")

	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics, got %d: %+v", len(diags), diags)
	}
	if diags[0].Path != "main.go" || diags[0].Line != 10 || diags[0].Col != 2 || diags[0].Message != "unreachable code" {
		t.Errorf("unexpected first diagnostic: %+v", diags[0])
	}
	if diags[1].Path != "other.go" {
		t.Errorf("unexpected second diagnostic: %+v", diags[1])
	}
}
