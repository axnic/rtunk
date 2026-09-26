package render

// sarifRenderer writes the run as one SARIF 2.1.0 document (spec 2026-09-26-v0.9.2, section 5).
type sarifRenderer struct{ base }

type sarifMessage struct {
	Text string `json:"text"`
}
type sarifRule struct {
	ID      string `json:"id"`
	HelpURI string `json:"helpUri,omitempty"`
}
type sarifDriver struct {
	Name    string      `json:"name"`
	Version string      `json:"version"`
	Rules   []sarifRule `json:"rules"`
}
type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}
type sarifNotification struct {
	Level   string       `json:"level"`
	Message sarifMessage `json:"message"`
}
type sarifInvocation struct {
	ExecutionSuccessful bool                `json:"executionSuccessful"`
	Notifications       []sarifNotification `json:"toolExecutionNotifications"`
}
type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn,omitempty"`
}
type sarifArtifact struct {
	URI string `json:"uri"`
}
type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           *sarifRegion  `json:"region,omitempty"`
}
type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}
type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifMessage    `json:"message"`
	Locations []sarifLocation `json:"locations"`
}
type sarifRun struct {
	Tool        sarifTool         `json:"tool"`
	Invocations []sarifInvocation `json:"invocations"`
	Results     []sarifResult     `json:"results"`
}
type sarifDoc struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

// sarifLevel is the inverse of pkg/trunk/output's sarifSeverity: an unknown severity is a note.
func sarifLevel(s string) string {
	switch s {
	case "error":
		return "error"
	case "warning":
		return "warning"
	}
	return "note"
}

func (r *sarifRenderer) Close(Summary) error {
	rules := []sarifRule{}
	ruleIdx := map[string]int{}
	results := []sarifResult{}
	for _, f := range r.sortedFindings() {
		id := linterRule(f)
		if i, seen := ruleIdx[id]; !seen {
			ruleIdx[id] = len(rules)
			rules = append(rules, sarifRule{ID: id, HelpURI: f.URL})
		} else if rules[i].HelpURI == "" {
			rules[i].HelpURI = f.URL
		}
		phys := sarifPhysical{ArtifactLocation: sarifArtifact{URI: f.File}}
		if f.Line > 0 {
			phys.Region = &sarifRegion{StartLine: f.Line}
			if f.Column > 0 {
				phys.Region.StartColumn = f.Column
			}
		}
		results = append(results, sarifResult{
			RuleID: id, Level: sarifLevel(f.Severity), Message: sarifMessage{Text: f.Message},
			Locations: []sarifLocation{{PhysicalLocation: phys}},
		})
	}

	notes := []sarifNotification{}
	for _, f := range r.sortedFailures() {
		notes = append(notes, sarifNotification{Level: "error", Message: sarifMessage{Text: f.Linter + ": " + f.Err}})
	}

	return writeJSON(r.stdout, sarifDoc{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool:        sarifTool{Driver: sarifDriver{Name: "rtunk", Version: r.opts.Version, Rules: rules}},
			Invocations: []sarifInvocation{{ExecutionSuccessful: len(notes) == 0, Notifications: notes}},
			Results:     results,
		}},
	})
}
