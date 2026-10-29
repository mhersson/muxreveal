package git

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestPushRemoteOrder(t *testing.T) {
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
	root := newRepo(t)

	if _, err := PushRemote(context.Background(), openRepo(t, root), "feat"); !errors.Is(err, ErrNoRemote) {
		t.Fatalf("err = %v, want ErrNoRemote", err)
	}
}

func TestRemoteURLAppliesInsteadOf(t *testing.T) {
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
	root := newRepo(t)

	if _, err := RemoteURL(context.Background(), openRepo(t, root), "--all"); !errors.Is(err, ErrNoRemote) {
		t.Fatalf("err = %v, want ErrNoRemote", err)
	}
}

func TestRemoteURLNeverEchoesItsArgument(t *testing.T) {
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

func TestPushURLs(t *testing.T) {
	tests := []struct {
		name   string
		config [][]string
		want   []string
	}{
		{"the fetch url without a push url", nil, []string{"https://example.invalid/o/r.git"}},
		{"a push url", [][]string{{"remote.origin.pushurl", "https://push.invalid/o/r.git"}}, []string{"https://push.invalid/o/r.git"}},
		{
			"a rewrite of the push url",
			[][]string{{"url./srv/git/.pushInsteadOf", "https://example.invalid/"}},
			[]string{"/srv/git/o/r.git"},
		},
		{"a push url with a space", [][]string{{"remote.origin.pushurl", "/srv/my git/r.git"}}, []string{"/srv/my git/r.git"}},
		{
			"two push urls in order",
			[][]string{{"remote.origin.pushurl", "https://one.invalid/r.git"}, {"--add", "remote.origin.pushurl", "https://two.invalid/r.git"}},
			[]string{"https://one.invalid/r.git", "https://two.invalid/r.git"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRepo(t)
			gitOut(t, root, "remote", "add", "origin", "https://example.invalid/o/r.git")

			for _, kv := range tt.config {
				gitOut(t, root, append([]string{"config"}, kv...)...)
			}

			got, err := PushURLs(context.Background(), openRepo(t, root), "origin")
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("push urls = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPushURLsRefusesWhatIsNotARemote(t *testing.T) {
	root := newRepo(t)
	r := openRepo(t, root)

	for _, remote := range []string{"", "--all", "-x", "missing"} {
		if _, err := PushURLs(context.Background(), r, remote); !errors.Is(err, ErrNoRemote) {
			t.Errorf("PushURLs(%q) err = %v, want ErrNoRemote", remote, err)
		}
	}
}

func TestPushURLsIsARead(t *testing.T) {
	root := newRepo(t)
	gitOut(t, root, "remote", "add", "origin", "https://example.invalid/o/r.git")

	log := fakeGit(t, `echo "locks=$GIT_OPTIONAL_LOCKS" >> "$LOG.locks"`)

	if _, err := PushURLs(context.Background(), openRepo(t, root), "origin"); err != nil {
		t.Fatal(err)
	}

	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(calls), "remote get-url --push --all origin") {
		t.Fatalf("git calls:\n%s\nwant the push url read", calls)
	}

	locks, err := os.ReadFile(log + ".locks")
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range strings.Fields(string(locks)) {
		if line != "locks=0" {
			t.Errorf("a git call ran with %s, want GIT_OPTIONAL_LOCKS=0", line)
		}
	}
}

func TestRemoteHasBranch(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, f pushFixture)
		remote string
		branch string
		want   bool
	}{
		{"before any push", func(*testing.T, pushFixture) {}, "origin", "feat", false},
		{
			"after a push",
			func(t *testing.T, f pushFixture) { gitOut(t, f.root, "push", "-q", "origin", "feat") },
			"origin", "feat", true,
		},
		{
			"another branch is there",
			func(t *testing.T, f pushFixture) { gitOut(t, f.root, "push", "-q", "origin", "feat") },
			"origin", "other", false,
		},
		{
			"another remote has it",
			func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "push", "-q", "origin", "feat")
				gitOut(t, f.root, "remote", "add", "second", newBare(t))
			},
			"second", "feat", false,
		},
		{
			"the tracking ref is gone",
			func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "push", "-q", "origin", "feat")
				gitOut(t, f.root, "update-ref", "-d", "refs/remotes/origin/feat")
			},
			"origin", "feat", false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPushFixture(t)
			tt.setup(t, f)

			got, err := RemoteHasBranch(context.Background(), f.repo, tt.remote, tt.branch)
			if err != nil || got != tt.want {
				t.Fatalf("RemoteHasBranch = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestRemoteHasBranchRefusesBadArguments(t *testing.T) {
	f := newPushFixture(t)

	for _, remote := range []string{"", "--all", "-x"} {
		if _, err := RemoteHasBranch(context.Background(), f.repo, remote, "feat"); !errors.Is(err, ErrNoRemote) {
			t.Errorf("remote %q: err = %v, want ErrNoRemote", remote, err)
		}
	}

	for _, branch := range []string{"", "-x", "a..b", "feat?"} {
		if _, err := RemoteHasBranch(context.Background(), f.repo, "origin", branch); !errors.Is(err, ErrBadBranch) {
			t.Errorf("branch %q: err = %v, want ErrBadBranch", branch, err)
		}
	}
}

func TestHasUpstream(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, f pushFixture)
		want  bool
	}{
		{
			name:  "no upstream",
			setup: func(*testing.T, pushFixture) {},
		},
		{
			name: "upstream set",
			setup: func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "push", "-q", "-u", "origin", "feat")
			},
			want: true,
		},
		{
			name: "upstream configured and its tracking ref gone",
			setup: func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "push", "-q", "-u", "origin", "feat")
				gitOut(t, f.root, "update-ref", "-d", "refs/remotes/origin/feat")

				if _, ok, err := Ahead(context.Background(), f.repo); err != nil || ok {
					t.Fatalf("setup: Ahead = ok %v, err %v, want no comparison", ok, err)
				}
			},
			want: true,
		},
		{
			name: "upstream on a local branch",
			setup: func(t *testing.T, f pushFixture) {
				gitOut(t, f.root, "branch", "--set-upstream-to=main")
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPushFixture(t)
			tt.setup(t, f)

			got, err := HasUpstream(context.Background(), f.repo, "feat")
			if err != nil || got != tt.want {
				t.Fatalf("HasUpstream = %v, %v; want %v", got, err, tt.want)
			}

			// Push sets the upstream exactly when HasUpstream said there was none.
			res, err := Push(context.Background(), f.repo, "feat")
			if err != nil {
				t.Fatalf("Push: %v", err)
			}

			if res.SetUpstream == got {
				t.Errorf("Push SetUpstream = %v with HasUpstream = %v, want them opposite", res.SetUpstream, got)
			}
		})
	}
}

func TestHasUpstreamRefusesBadBranches(t *testing.T) {
	f := newPushFixture(t)

	for _, branch := range []string{"", "-x", "a..b", "feat?"} {
		if _, err := HasUpstream(context.Background(), f.repo, branch); !errors.Is(err, ErrBadBranch) {
			t.Errorf("HasUpstream(%q) err = %v, want ErrBadBranch", branch, err)
		}
	}
}

func TestHasUpstreamIsARead(t *testing.T) {
	f := newPushFixture(t)
	log := fakeGit(t, `echo "locks=$GIT_OPTIONAL_LOCKS" >> "$LOG.locks"`)

	if _, err := HasUpstream(context.Background(), f.repo, "feat"); err != nil {
		t.Fatal(err)
	}

	locks, err := os.ReadFile(log + ".locks")
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range strings.Fields(string(locks)) {
		if line != "locks=0" {
			t.Errorf("a git call ran with %s, want GIT_OPTIONAL_LOCKS=0", line)
		}
	}
}
