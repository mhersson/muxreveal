package git

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestLog(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "init")
	gitOut(t, dir, "switch", "-q", "-c", "feat")

	msgs := []string{
		"first 100% done\n\nbody one\nline two",
		"second\n\n%H literal",
		"third",
		"fourth\n\np1\n\np2",
	}
	for i, m := range msgs {
		writeFile(t, dir, fmt.Sprintf("f%d.txt", i), "x\n")
		commitAll(t, dir, m)
	}

	got, err := Log(context.Background(), openRepo(t, dir), "main")
	if err != nil {
		t.Fatal(err)
	}

	want := []Commit{
		{Subject: "first 100% done", Body: "body one\nline two"},
		{Subject: "second", Body: "%H literal"},
		{Subject: "third", Body: ""},
		{Subject: "fourth", Body: "p1\n\np2"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d commits, want %d: %+v", len(got), len(want), got)
	}

	for i, w := range want {
		if got[i].Subject != w.Subject || got[i].Body != w.Body {
			t.Errorf("commit %d = %q / %q, want %q / %q", i, got[i].Subject, got[i].Body, w.Subject, w.Body)
		}

		if len(got[i].OID) != 40 {
			t.Errorf("commit %d OID = %q", i, got[i].OID)
		}
	}

	if got[0].OID != gitOut(t, dir, "rev-parse", "HEAD~3") {
		t.Errorf("first OID = %s, not the oldest commit", got[0].OID)
	}
}

func TestLogCaps(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "init")
	gitOut(t, dir, "switch", "-q", "-c", "feat")

	for i := range 260 {
		gitOut(t, dir, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("c%03d", i))
	}

	got, err := Log(context.Background(), openRepo(t, dir), "main")
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 250 {
		t.Fatalf("got %d commits, want 250", len(got))
	}

	if got[0].Subject != "c010" || got[249].Subject != "c259" {
		t.Errorf("range = %q..%q, want c010..c259", got[0].Subject, got[249].Subject)
	}
}

func TestLogRefusesOptionBase(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "init")

	logPath := fakeGit(t, "")

	for _, base := range []string{"--all", "-n1"} {
		if _, err := Log(context.Background(), openRepo(t, dir), base); err == nil {
			t.Errorf("Log(%q) succeeded", base)
		}
	}

	calls, err := os.ReadFile(logPath)
	for _, opt := range []string{"--all", "-n1"} {
		if err == nil && strings.Contains(string(calls), opt) {
			t.Errorf("git ran with the option base %q: %s", opt, calls)
		}
	}
}

func TestLogUnknownBase(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "init")

	if _, err := Log(context.Background(), openRepo(t, dir), "nope"); err == nil {
		t.Error("Log with an unknown base succeeded")
	}
}
