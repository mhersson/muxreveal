package github

import (
	"os"
	"testing"
)

// go-gh reads its config once per process, so the environment is isolated
// before any test runs.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "github-test-")
	if err != nil {
		panic(err)
	}

	env := map[string]string{"GH_CONFIG_DIR": dir}
	for _, k := range []string{
		"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN",
		"GITHUB_ENTERPRISE_TOKEN", "GH_HOST", "GH_DEBUG",
	} {
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
