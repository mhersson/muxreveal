package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeGH(t *testing.T, body string) (script, args string) {
	t.Helper()
	dir := t.TempDir()
	script = filepath.Join(dir, "gh")
	args = filepath.Join(dir, "args")

	src := "#!/bin/sh\necho \"$@\" > '" + args + "'\n" + body
	if err := os.WriteFile(script, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}

	return script, args
}

func TestTokenFromEnvOrConfig(t *testing.T) {
	gh, args := fakeGH(t, "echo from-gh\n")

	var asked string

	o := TokenOptions{
		GH: gh,
		FromEnvOrConfig: func(host string) string {
			asked = host

			return "env-tok"
		},
	}

	got, err := Token(t.Context(), "github.com", o)
	if err != nil || got != "env-tok" {
		t.Fatalf("token = %q, %v", got, err)
	}

	if asked != "github.com" {
		t.Fatalf("asked for %q", asked)
	}

	if _, err := os.Stat(args); err == nil {
		t.Fatal("gh ran although a token was found")
	}
}

func TestTokenFallsBackToGh(t *testing.T) {
	gh, args := fakeGH(t, "echo tok\n")
	o := TokenOptions{GH: gh, FromEnvOrConfig: func(string) string { return "" }}

	got, err := Token(t.Context(), "h", o)
	if err != nil || got != "tok" {
		t.Fatalf("token = %q, %v", got, err)
	}

	b, _ := os.ReadFile(args)
	if want := "auth token --secure-storage --hostname h"; strings.TrimSpace(string(b)) != want {
		t.Fatalf("gh args = %q, want %q", b, want)
	}
}

func TestTokenNone(t *testing.T) {
	o := TokenOptions{
		GH:              filepath.Join(t.TempDir(), "no-gh"),
		FromEnvOrConfig: func(string) string { return "" },
	}

	_, err := Token(t.Context(), "h", o)
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}

	if !strings.Contains(err.Error(), "gh auth login --hostname h") {
		t.Fatalf("message %q does not say how to log in", err)
	}
}

func TestTokenGhFailureHidesStderr(t *testing.T) {
	gh, _ := fakeGH(t, "echo secret >&2\nexit 1\n")
	o := TokenOptions{GH: gh, FromEnvOrConfig: func(string) string { return "" }}

	_, err := Token(t.Context(), "h", o)
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}

	if strings.Contains(err.Error(), "secret") {
		t.Fatal("error carries gh's stderr")
	}
}

func TestTokenGhEmptyOutput(t *testing.T) {
	gh, _ := fakeGH(t, "echo\n")

	o := TokenOptions{GH: gh, FromEnvOrConfig: func(string) string { return "" }}
	if _, err := Token(t.Context(), "h", o); !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestTokenGhHangs(t *testing.T) {
	gh, _ := fakeGH(t, "exec sleep 30\n")
	o := TokenOptions{GH: gh, FromEnvOrConfig: func(string) string { return "" }}

	ctx, cancel := contextWithTimeout(t, 300*time.Millisecond)
	defer cancel()

	start := time.Now()

	_, err := Token(ctx, "h", o)
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}

	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Token took %v after the deadline", d)
	}
}

func contextWithTimeout(t *testing.T, d time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()

	return context.WithTimeout(t.Context(), d)
}

func TestTokenEmptyHostDoesNotRunGh(t *testing.T) {
	gh, args := fakeGH(t, "echo tok\n")

	o := TokenOptions{GH: gh, FromEnvOrConfig: func(string) string { return "" }}
	if _, err := Token(t.Context(), "", o); !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}

	if _, err := os.Stat(args); err == nil {
		t.Fatal("gh ran for an empty host")
	}
}
