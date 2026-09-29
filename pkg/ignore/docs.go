// Package ignore recognizes rtunk-ignore/trunk-ignore inline comment directives (AGENTS.md
// "rtunk-ignore with permanent trunk compatibility") and filters output.Finding accordingly.
// Filter is the only entry point: pkg/run/engine calls it once per job's findings, the same place
// it already calls output.ApplyIssueURL/ApplyIsSecurity.
package ignore
