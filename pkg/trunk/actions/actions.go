// Package actions runs trunk-style Actions (ARCHITECTURE.md `actions:`): single Run invocations
// with git-hook/manual-CLI context, not file-matched batch jobs -- deliberately a separate package
// from pkg/trunk/engine (the lint/fmt job-queue engine), since the two execution models share
// little beyond "resolve a runtime shim, exec a shell command".
package actions

import (
	"sort"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// Resolve returns cfg's enabled actions, sorted by ID for deterministic execution order.
// hookName == "" returns every enabled action (for `rtunk actions list`/`run --hook`'s own
// hook-name enumeration in pkg/trunk/githooks); a non-empty hookName keeps only actions with a
// Trigger.GitHooks entry naming it.
func Resolve(cfg config.Config, hookName string) []config.Action {
	ids := make([]string, 0, len(cfg.Actions.Definitions))
	for id := range cfg.Actions.Definitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []config.Action
	for _, id := range ids {
		a := cfg.Actions.Definitions[id]
		if hookName == "" {
			out = append(out, a)
			continue
		}
		for _, trig := range a.Triggers {
			if containsString(trig.GitHooks, hookName) {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
