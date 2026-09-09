package plugin

import (
	"os"
	"strings"
)

// envVarDef mirrors one runtime_environment/linter_environment entry (real
// example: github.com/trunk-io/plugins' runtimes/node/plugin.yaml) — sets
// or extends a single environment variable from a template, evaluated
// against ${env.NAME} (the current process environment, with an optional
// ":-default" fallback, bash-style) and a caller-supplied set of path
// variables (${runtime}, ${linter}). An Optional entry whose resolved value
// ends up empty is omitted entirely rather than exported as "NAME=".
type envVarDef struct {
	Name     string   `yaml:"name"`
	Value    string   `yaml:"value"`
	List     []string `yaml:"list"`
	Optional bool     `yaml:"optional"`
}

// resolveEnv evaluates defs into "NAME=value" entries (suitable for
// exec.Cmd.Env), substituting vars (e.g. {"runtime": runtimeDir, "linter":
// linterDir}) and ${env.NAME}/${env.NAME:-default} against the current
// process environment.
func resolveEnv(defs []envVarDef, vars map[string]string) []string {
	var out []string
	for _, d := range defs {
		var resolved string
		if len(d.List) > 0 {
			parts := make([]string, 0, len(d.List))
			for _, item := range d.List {
				if v := expandTemplate(item, vars); v != "" {
					parts = append(parts, v)
				}
			}
			resolved = strings.Join(parts, string(os.PathListSeparator))
		} else {
			resolved = expandTemplate(d.Value, vars)
		}
		if d.Optional && resolved == "" {
			continue
		}
		out = append(out, d.Name+"="+resolved)
	}
	return out
}

// expandTemplate substitutes every ${...} placeholder in tpl: ${env.NAME}
// or ${env.NAME:-default} against the process environment, else a plain
// ${name} against vars. An unresolvable placeholder expands to "".
func expandTemplate(tpl string, vars map[string]string) string {
	var b strings.Builder
	for i := 0; i < len(tpl); {
		if tpl[i] == '$' && i+1 < len(tpl) && tpl[i+1] == '{' {
			if end := strings.IndexByte(tpl[i+2:], '}'); end >= 0 {
				b.WriteString(expandExpr(tpl[i+2:i+2+end], vars))
				i += 2 + end + 1
				continue
			}
		}
		b.WriteByte(tpl[i])
		i++
	}
	return b.String()
}

func expandExpr(expr string, vars map[string]string) string {
	if name, ok := strings.CutPrefix(expr, "env."); ok {
		envName, def, hasDefault := strings.Cut(name, ":-")
		if v := os.Getenv(envName); v != "" {
			return v
		}
		if hasDefault {
			return def
		}
		return ""
	}
	return vars[expr]
}
