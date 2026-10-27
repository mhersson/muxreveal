package github

import (
	"os"
	"testing"
)

// isolatedEnv are the variables go-gh and gh read for their login and host.
var isolatedEnv = []string{
	"GH_CONFIG_DIR", "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN",
	"GITHUB_ENTERPRISE_TOKEN", "GH_HOST", "GH_DEBUG",
}

type savedVar struct {
	value string
	set   bool
}

// originalEnv holds the isolatedEnv values from before TestMain cleared them,
// for the live test to restore.
var originalEnv = map[string]savedVar{}

// go-gh reads its config once per process, so the environment is isolated
// before any test runs.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "github-test-")
	if err != nil {
		panic(err)
	}

	for _, k := range isolatedEnv {
		v, ok := os.LookupEnv(k)
		originalEnv[k] = savedVar{value: v, set: ok}
	}

	env := map[string]string{"GH_CONFIG_DIR": dir}
	for _, k := range isolatedEnv[1:] {
		env[k] = ""
	}

	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}

	code := m.Run()

	if err := os.RemoveAll(dir); err != nil {
		panic(err)
	}

	os.Exit(code)
}
