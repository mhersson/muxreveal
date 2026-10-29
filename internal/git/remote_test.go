package git

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPushRemoteOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  [][]string
		want    string
		wantErr error
	}{
		{"default origin", nil, "origin", nil},
		{"branch remote", [][]string{{"branch.feat.remote", "second"}}, "second", nil},
		{"dot ignored", [][]string{{"branch.feat.remote", "."}}, "origin", nil},
		{"push default over branch remote", [][]string{{"branch.feat.remote", "origin"}, {"remote.pushDefault", "second"}}, "second", nil},
		{"push remote over push default", [][]string{{"remote.pushDefault", "origin"}, {"branch.feat.pushRemote", "second"}}, "second", nil},
		{"not a remote", [][]string{{"branch.feat.pushRemote", "nope"}}, "", ErrNoRemote},
		{"option name", [][]string{{"branch.feat.pushRemote", "-x"}}, "", ErrNoRemote},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := newRepo(t)
			gitOut(t, root, "remote", "add", "origin", "/nonexistent/origin.git")
			gitOut(t, root, "remote", "add", "second", "/nonexistent/second.git")

			for _, kv := range tt.config {
				gitOut(t, root, "config", kv[0], kv[1])
			}

			got, err := PushRemote(context.Background(), openRepo(t, root), "feat")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("remote = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPushRemoteNoRemotes(t *testing.T) {
	t.Parallel()
	root := newRepo(t)

	if _, err := PushRemote(context.Background(), openRepo(t, root), "feat"); !errors.Is(err, ErrNoRemote) {
		t.Fatalf("err = %v, want ErrNoRemote", err)
	}
}

func TestRemoteURLAppliesInsteadOf(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	gitOut(t, root, "remote", "add", "origin", "gh:o/r.git")
	gitOut(t, root, "config", "url.https://example.invalid/.insteadOf", "gh:")

	got, err := RemoteURL(context.Background(), openRepo(t, root), "origin")
	if err != nil {
		t.Fatal(err)
	}

	if want := "https://example.invalid/o/r.git"; got != want {
		t.Errorf("url = %q, want %q", got, want)
	}

	if _, err := RemoteURL(context.Background(), openRepo(t, root), "missing"); !errors.Is(err, ErrNoRemote) {
		t.Errorf("missing remote: err = %v, want ErrNoRemote", err)
	}
}

func TestRemoteURLRefusesOptionNames(t *testing.T) {
	t.Parallel()
	root := newRepo(t)

	if _, err := RemoteURL(context.Background(), openRepo(t, root), "--all"); !errors.Is(err, ErrNoRemote) {
		t.Fatalf("err = %v, want ErrNoRemote", err)
	}
}

func TestRemoteURLNeverEchoesItsArgument(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	arg := "https://user:secret@example.invalid/o/r.git" //nolint:gosec // G101: fixture URL, no real credential

	_, err := RemoteURL(context.Background(), openRepo(t, root), arg)
	if !errors.Is(err, ErrNoRemote) {
		t.Fatalf("err = %v, want ErrNoRemote", err)
	}

	for _, leak := range []string{"secret", "example.invalid"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q contains %q", err, leak)
		}
	}
}
