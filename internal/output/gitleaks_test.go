package output

import "testing"

// realGitleaksOutput is an actual `gitleaks detect --report-format json` payload.
const realGitleaksOutput = `[
 {
  "RuleID": "slack-bot-token",
  "Description": "Identified a Slack Bot token, which may compromise bot integrations and communication channel security.",
  "StartLine": 3,
  "EndLine": 3,
  "StartColumn": 22,
  "EndColumn": 76,
  "Match": "xoxb-123456789012-123456789012-abcdefghijklmnopqrstuvwx",
  "Secret": "xoxb-123456789012-123456789012-abcdefghijklmnopqrstuvwx",
  "File": "secret.go",
  "Fingerprint": "secret.go:slack-bot-token:3"
 }
]`

func TestParseGitleaksJSON(t *testing.T) {
	diags, err := ParseGitleaksJSON(realGitleaksOutput, "gitleaks", "lint")
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	d := diags[0]
	if d.Path != "secret.go" || d.Line != 3 || d.Col != 22 || d.Code != "slack-bot-token" {
		t.Errorf("unexpected diagnostic: %+v", d)
	}
}

func TestParseGitleaksJSON_Empty(t *testing.T) {
	diags, err := ParseGitleaksJSON("[]", "gitleaks", "lint")
	if err != nil || len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %+v, err %v", diags, err)
	}
	diags, err = ParseGitleaksJSON("", "gitleaks", "lint")
	if err != nil || len(diags) != 0 {
		t.Errorf("expected no diagnostics for empty raw, got %+v, err %v", diags, err)
	}
}
