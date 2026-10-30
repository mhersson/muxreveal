package ui

import (
	"os"
	"testing"
)

// TestMain isolates git from the machine's config once for the binary, so tests
// that build repositories need no t.Setenv and can call t.Parallel.
func TestMain(m *testing.M) {
	env := map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_AUTHOR_NAME":     "Test",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "Test",
		"GIT_COMMITTER_EMAIL": "test@example.com",
	}
	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}

	m.Run()
	removePushTemplate()
}
