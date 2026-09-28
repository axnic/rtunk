package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyIssueURL(t *testing.T) {
	findings := []Finding{{RuleID: "no-eval"}, {RuleID: ""}}
	ApplyIssueURL(findings, "https://example.com/rules/{}")
	assert.Equal(t, "https://example.com/rules/no-eval", findings[0].URL)
	assert.Equal(t, "", findings[1].URL, "no RuleID means no URL even with a format set")
}

func TestApplyIssueURL_EmptyFormat(t *testing.T) {
	findings := []Finding{{RuleID: "no-eval"}}
	ApplyIssueURL(findings, "")
	assert.Equal(t, "", findings[0].URL)
}

func TestApplyIsSecurity_TagsEveryFinding(t *testing.T) {
	findings := []Finding{{Message: "a"}, {Message: "b"}}
	ApplyIsSecurity(findings, true)
	assert.True(t, findings[0].IsSecurity)
	assert.True(t, findings[1].IsSecurity)
}

func TestApplyIsSecurity_FalseLeavesFindingsUntouched(t *testing.T) {
	findings := []Finding{{Message: "a"}, {Message: "b"}}
	ApplyIsSecurity(findings, false)
	assert.False(t, findings[0].IsSecurity)
	assert.False(t, findings[1].IsSecurity)
}
