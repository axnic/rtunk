// Package security resolves where a linter command runs: ResolveRunFrom turns a Command.RunFrom
// expression into a concrete directory, and StageSandbox builds the isolated directory tree a
// sandboxed command is pointed at instead of the repository itself.
package security
