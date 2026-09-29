package download

import (
	"os"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// BuildEnv resolves a Runtime's RuntimeEnvironment/LinterEnvironment entries
// (ARCHITECTURE.md "runtimes:") into NAME=value pairs for exec.Cmd.Env / WriteEnvShim.
//
// ponytail: only the placeholders trunk-io/plugins' real node/eslint definitions actually use are
// supported -- ${runtime}, ${linter} (looked up in subst), and ${env.NAME}/${env.NAME:-}
// (os.Getenv) -- not a general templating engine. Extend the replacer if a real plugin needs
// another one.
func BuildEnv(entries []config.EnvironmentEntry, subst map[string]string) []string {
	var out []string
	for _, e := range entries {
		var resolved string
		if len(e.List) > 0 {
			parts := make([]string, len(e.List))
			for i, v := range e.List {
				parts[i] = resolvePlaceholders(v, subst)
			}
			resolved = strings.Join(parts, string(os.PathListSeparator))
		} else {
			resolved = resolvePlaceholders(e.Value, subst)
		}
		if resolved == "" && e.Optional {
			continue
		}
		out = append(out, e.Name+"="+resolved)
	}
	return out
}

// resolvePlaceholders substitutes ${runtime}/${linter} from subst and ${env.NAME}/${env.NAME:-}
// from the process environment (both forms are treated the same -- v0.2 has no distinct behavior
// for the ":-" default-empty variant beyond what os.Getenv already gives, an empty string).
func resolvePlaceholders(s string, subst map[string]string) string {
	for k, v := range subst {
		s = strings.ReplaceAll(s, "${"+k+"}", v)
	}
	for {
		start := strings.Index(s, "${env.")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			break
		}
		name := strings.TrimSuffix(s[start+len("${env."):start+end], ":-")
		s = s[:start] + os.Getenv(name) + s[start+end+1:]
	}
	return s
}
