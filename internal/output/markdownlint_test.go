package output

import "testing"

// realMarkdownlintOutput is an actual `markdownlint --json` payload (from stderr).
const realMarkdownlintOutput = `[
  {
    "fileName": "bad.md",
    "lineNumber": 1,
    "ruleNames": ["MD022", "blanks-around-headings"],
    "ruleDescription": "Headings should be surrounded by blank lines",
    "ruleInformation": "https://github.com/DavidAnson/markdownlint/blob/v0.38.0/doc/md022.md",
    "errorDetail": "Expected: 1; Actual: 0; Below",
    "errorContext": "# Title",
    "errorRange": null
  },
  {
    "fileName": "bad.md",
    "lineNumber": 2,
    "ruleNames": ["MD018", "no-missing-space-atx"],
    "ruleDescription": "No space after hash on atx style heading",
    "ruleInformation": "https://github.com/DavidAnson/markdownlint/blob/v0.38.0/doc/md018.md",
    "errorDetail": null,
    "errorContext": "#Bad heading",
    "errorRange": [1, 2]
  }
]`

func TestParseMarkdownlint(t *testing.T) {
	diags, err := ParseMarkdownlint(realMarkdownlintOutput, "markdownlint", "lint")
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics, got %d: %+v", len(diags), diags)
	}
	if diags[0].Path != "bad.md" || diags[0].Line != 1 || diags[0].Code != "MD022" || diags[0].Col != 0 {
		t.Errorf("unexpected first diagnostic: %+v", diags[0])
	}
	if diags[0].Message != "Headings should be surrounded by blank lines: Expected: 1; Actual: 0; Below" {
		t.Errorf("expected errorDetail appended to message, got %q", diags[0].Message)
	}
	if diags[1].Code != "MD018" || diags[1].Col != 1 {
		t.Errorf("unexpected second diagnostic: %+v", diags[1])
	}
}

func TestParseMarkdownlint_Empty(t *testing.T) {
	diags, err := ParseMarkdownlint("", "markdownlint", "lint")
	if err != nil || len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %+v, err %v", diags, err)
	}
}
