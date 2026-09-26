package engine

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBaseEnv_AllowGlobsDenyWins(t *testing.T) {
	for k, v := range map[string]string{
		"HOME": "/home/x", "GOCACHE": "/c", "XDG_CACHE_HOME": "/x", "npm_config_cache": "/n", "LC_ALL": "C",
		"GOAUTH": "netrc", "MY_DB_PWD": "p", "SERVICE_CREDS": "c", "GITHUB_TOKEN": "hunter2", "SOME_VAR": "v", "AWS_SECRET_ACCESS_KEY": "z",
	} {
		t.Setenv(k, v)
	}

	env := "\n" + strings.Join(baseEnv(), "\n") + "\n"
	for _, want := range []string{"HOME=/home/x", "GOCACHE=/c", "XDG_CACHE_HOME=/x", "npm_config_cache=/n", "LC_ALL=C"} {
		assert.Contains(t, env, "\n"+want+"\n")
	}
	for _, not := range []string{"GOAUTH", "GITHUB_TOKEN", "SOME_VAR", "AWS_SECRET_ACCESS_KEY", "MY_DB_PWD", "SERVICE_CREDS"} {
		assert.NotContains(t, env, "\n"+not+"=")
	}
}
