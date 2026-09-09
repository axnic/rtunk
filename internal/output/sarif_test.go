package output

import (
	"testing"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/diagnostic"
)

const realSarifOutput = `{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {"driver": {"name": "example-linter", "rules": []}},
      "results": [
        {
          "ruleId": "no-foo",
          "level": "error",
          "message": {"text": "foo is not allowed"},
          "locations": [
            {"physicalLocation": {
              "artifactLocation": {"uri": "src/main.go"},
              "region": {"startLine": 10, "startColumn": 5}
            }}
          ]
        },
        {
          "ruleId": "style-hint",
          "level": "note",
          "message": {"text": "consider renaming"},
          "locations": [
            {"physicalLocation": {
              "artifactLocation": {"uri": "src/other.go"},
              "region": {"startLine": 2, "startColumn": 1}
            }}
          ]
        }
      ]
    }
  ]
}`

func TestParseSarif(t *testing.T) {
	diags, err := ParseSarif(realSarifOutput, "example-linter", "lint")
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics, got %d: %+v", len(diags), diags)
	}
	if diags[0].Path != "src/main.go" || diags[0].Line != 10 || diags[0].Col != 5 || diags[0].Code != "no-foo" || diags[0].Severity != diagnostic.Error {
		t.Errorf("unexpected first diagnostic: %+v", diags[0])
	}
	if diags[1].Severity != diagnostic.Note {
		t.Errorf("expected note severity for level=note, got %v", diags[1].Severity)
	}
}

func TestParseSarif_Empty(t *testing.T) {
	diags, err := ParseSarif("", "x", "lint")
	if err != nil || len(diags) != 0 {
		t.Errorf("expected no diagnostics for empty raw, got %+v, err %v", diags, err)
	}
}

func TestParseSarif_NoResults(t *testing.T) {
	diags, err := ParseSarif(`{"version":"2.1.0","runs":[{"results":[]}]}`, "x", "lint")
	if err != nil || len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %+v, err %v", diags, err)
	}
}
