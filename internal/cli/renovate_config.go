package cli

import "io"

// renovateConfigSnippet is the static customManagers block to add to the user's own
// renovate.json5 -- static because it matches the generic "# renovate: ..." comment shape
// renovate enable's annotate step produces, not any specific tool, so it never needs regenerating
// as enabled linters/tools/runtimes change. Verified directly against real annotated output (both
// the flat "- id@version" sequence-entry shape and the "ref: <value>" mapping-entry shape) in
// Node.js (the engine Renovate actually runs), not just eyeballed.
//
// customType/managerFilePatterns is Renovate's current schema (its own older regexManagers/
// fileMatch spelling still works today, but only via an in-memory auto-migration that logs
// "Config migration necessary" and, in a real repo, opens an unwanted config-migration PR --
// confirmed directly by running the older spelling through a real `renovate` CLI and reading its
// own migration output). managerFilePatterns wraps each pattern in its own leading/trailing "/"
// to mark it as a regex rather than a glob -- confirmed against the same real run.
const renovateConfigSnippet = `{
  "customManagers": [
    {
      "customType": "regex",
      "managerFilePatterns": ["/(^|/)\\.trunk/trunk\\.yaml$/", "/(^|/)\\.rtunk/rtunk\\.yaml$/"],
      "matchStrings": [
        "# renovate: datasource=(?<datasource>\\S+) depName=(?<depName>\\S+)(?:\\s+extractVersion=(?<extractVersion>\\S+))?\\s*\\n\\s*(?:-\\s*\\S+@|ref:\\s*)(?<currentValue>\\S+)"
      ]
    }
  ]
}
`

type renovateConfigCmd struct{}

func (c *renovateConfigCmd) Run(stdout io.Writer) error {
	_, err := io.WriteString(stdout, renovateConfigSnippet)
	return err
}
