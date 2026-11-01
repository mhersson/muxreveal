package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenLog(t *testing.T) {
	writeFile := func(t *testing.T, path, content string) {
		t.Helper()

		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name     string
		setup    func(t *testing.T, dir string)
		wantPrev string // content of muxreveal.log.1; "" means it must not exist
		wantErr  bool
	}{
		{name: "missing dir"},
		{
			name:     "rotates the previous log",
			setup:    func(t *testing.T, dir string) { writeFile(t, filepath.Join(dir, "muxreveal.log"), "old") },
			wantPrev: "old",
		},
		{
			name: "replaces an older rotation",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "muxreveal.log"), "old")
				writeFile(t, filepath.Join(dir, "muxreveal.log.1"), "older")
			},
			wantPrev: "old",
		},
		{
			name:    "dir is a regular file",
			setup:   func(t *testing.T, dir string) { writeFile(t, dir, "") },
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "state", "muxreveal")
			if tt.setup != nil {
				tt.setup(t, dir)
			}

			f, err := openLog(dir)

			if tt.wantErr {
				if err == nil {
					_ = f.Close()

					t.Fatal("openLog() succeeded, want an error")
				}

				return
			}

			if err != nil {
				t.Fatalf("openLog() error = %v", err)
			}

			t.Cleanup(func() {
				if err := f.Close(); err != nil {
					t.Error(err)
				}
			})

			logPath := filepath.Join(dir, "muxreveal.log")
			if f.Name() != logPath {
				t.Errorf("log file = %q, want %q", f.Name(), logPath)
			}

			assertMode(t, dir, 0o700)
			assertMode(t, logPath, 0o600)

			if got, err := os.ReadFile(logPath); err != nil || len(got) != 0 {
				t.Errorf("new log = %q, %v; want empty", got, err)
			}

			prev, err := os.ReadFile(logPath + ".1")
			switch {
			case tt.wantPrev == "" && !os.IsNotExist(err):
				t.Errorf("muxreveal.log.1 exists (%q, %v), want none", prev, err)
			case tt.wantPrev != "" && string(prev) != tt.wantPrev:
				t.Errorf("muxreveal.log.1 = %q, %v; want %q", prev, err, tt.wantPrev)
			}
		})
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := info.Mode().Perm(); got != want {
		t.Errorf("mode of %s = %o, want %o", path, got, want)
	}
}
