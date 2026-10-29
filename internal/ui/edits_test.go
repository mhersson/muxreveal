package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/muxreveal/internal/core"
	"github.com/mhersson/muxreveal/internal/git"
	"github.com/mhersson/muxreveal/internal/harness"
)

// fakeResolver answers the edit resolver from a table and counts its calls.
// A path in neither table is not counted.
type fakeResolver struct {
	mu     sync.Mutex
	places map[string]git.Place
	errs   map[string]error
	calls  []string
}

func (f *fakeResolver) resolve(_ context.Context, path string) (git.Place, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, path)

	if err := f.errs[path]; err != nil {
		return git.Place{}, err
	}

	if p, ok := f.places[path]; ok {
		return p, nil
	}

	return git.Place{}, fmt.Errorf("edit %s: %w", path, git.ErrNotCounted)
}

func (f *fakeResolver) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.calls)
}

func placeAt(root string) git.Place {
	return git.Place{Root: root, Common: root + "/.git", Repo: filepath.Base(root)}
}

// hookEditRig is the agent app with p2 an agent and a fake edit resolver.
type hookEditRig struct {
	*App
	id  core.PaneID
	res *fakeResolver
	log *bytes.Buffer // everything the app logs, debug included
}

func newHookEditRig(t *testing.T, places map[string]git.Place, errs map[string]error) *hookEditRig {
	t.Helper()

	a, _, _ := agentApp(t)

	// Opening the agent in a directory that is no repository left a notice.
	a.notice = ""

	r := &hookEditRig{App: a, id: "p2", res: &fakeResolver{places: places, errs: errs}, log: &bytes.Buffer{}}
	a.edits.resolve = r.res.resolve
	a.log = slog.New(slog.NewTextHandler(r.log, &slog.HandlerOptions{Level: slog.LevelDebug}))

	return r
}

func (r *hookEditRig) event(name, path string) harness.Event {
	return harness.Event{Pane: r.id, Name: name, Edit: path}
}

// hook delivers an event and returns the commands it raised.
func (r *hookEditRig) hook(ev harness.Event) tea.Cmd { return hook(r.App, ev) }

// deliver runs cmd and feeds every message it returns back to the app.
func (r *hookEditRig) deliver(cmd tea.Cmd) {
	for _, m := range flatten(cmd) {
		if m != nil {
			r.Update(m)
		}
	}
}

func (r *hookEditRig) pane() core.Pane { return r.store.State().Panes[r.id] }

func (r *hookEditRig) roots() []string {
	var out []string
	for _, e := range r.pane().Edits {
		out = append(out, e.Root)
	}

	slices.Sort(out)

	return out
}

func TestEditMovesFollowedPlace(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/b/x.go": placeAt("/w/b")}, nil)
	own := r.pane().FollowDir()

	cmd := r.hook(r.event("PreToolUse", "/w/b/x.go"))
	if got := r.pane().FollowDir(); got != own {
		t.Fatalf("FollowDir() = %q before the answer, want %q", got, own)
	}

	r.deliver(cmd)

	if got := r.pane().FollowDir(); got != "/w/b" {
		t.Errorf("FollowDir() = %q, want /w/b", got)
	}

	if e, ok := r.pane().Followed(); !ok || e.Repo != "b" || e.Common != "/w/b/.git" || e.At.IsZero() {
		t.Errorf("Followed() = %+v, %v, want the resolved place stamped with the hook's time", e, ok)
	}
}

func TestEditAnswersOutOfOrder(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/a/x.go": placeAt("/w/a"), "/w/b/x.go": placeAt("/w/b")}, nil)

	inA := r.hook(r.event("PreToolUse", "/w/a/x.go"))
	inB := r.hook(r.event("PreToolUse", "/w/b/x.go"))

	r.deliver(inB)
	r.deliver(inA)

	if got := r.pane().FollowDir(); got != "/w/b" {
		t.Errorf("FollowDir() = %q, want /w/b: the edit that arrived last", got)
	}

	if got := r.roots(); !slices.Equal(got, []string{"/w/a", "/w/b"}) {
		t.Errorf("edited places = %v, want both", got)
	}
}

func TestEditNotCounted(t *testing.T) {
	t.Parallel()

	// level is how the answer's error is logged; "" for not at all, since an
	// error that names the edited path must not reach the log for a normal case.
	tests := []struct {
		name  string
		err   error
		level string
	}{
		{"not counted", fmt.Errorf("edit /w/b/x.go: %w: outside any work tree", git.ErrNotCounted), ""},
		{"another error", errors.New("edit /w/b/x.go: git exploded"), "WARN"},
		{"the context ends", context.Canceled, "DEBUG"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newHookEditRig(t, nil, map[string]error{"/w/b/x.go": tt.err})
			own := r.pane().FollowDir()

			r.deliver(r.hook(r.event("PostToolUse", "/w/b/x.go")))

			if r.res.count() != 1 {
				t.Fatalf("resolver ran %d times, want 1", r.res.count())
			}

			if p := r.pane(); len(p.Edits) != 0 || p.FollowDir() != own {
				t.Errorf("pane = %+v, want no edits and FollowDir() %q", p, own)
			}

			if r.notice != "" {
				t.Errorf("notice = %q, want none", r.notice)
			}

			var levels []string

			for _, line := range strings.Split(r.log.String(), "\n") {
				if !strings.Contains(line, tt.err.Error()) {
					continue
				}

				for _, f := range strings.Fields(line) {
					if l, ok := strings.CutPrefix(f, "level="); ok {
						levels = append(levels, l)
					}
				}
			}

			var want []string
			if tt.level != "" {
				want = []string{tt.level}
			}

			if !slices.Equal(levels, want) {
				t.Errorf("the error was logged at %v, want %v", levels, want)
			}
		})
	}
}

func TestEditSamePathResolvesOnce(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/b/x.go": placeAt("/w/b")}, nil)

	// The tool runs between the two hooks, so the first answer is in by the second.
	r.deliver(r.hook(r.event("PreToolUse", "/w/b/x.go")))

	first, _ := r.pane().Followed()

	post := r.hook(r.event("PostToolUse", "/w/b/x.go"))
	if got := r.res.count(); got != 1 {
		t.Fatalf("resolver ran %d times by the PostToolUse, want 1", got)
	}

	r.deliver(post)

	if got := r.res.count(); got != 1 {
		t.Errorf("resolver ran %d times, want 1", got)
	}

	e, ok := r.pane().Followed()
	if !ok || len(r.pane().Edits) != 1 || e.Root != "/w/b" || e.Seq != first.Seq+1 {
		t.Errorf("Followed() = %+v, %v with %d entries, want /w/b with Seq %d", e, ok, len(r.pane().Edits), first.Seq+1)
	}
}

func TestEditOtherPathResolvesAgain(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/b/x.go": placeAt("/w/b"), "/w/b/y.go": placeAt("/w/b")}, nil)

	r.deliver(r.hook(r.event("PostToolUse", "/w/b/x.go")))
	r.deliver(r.hook(r.event("PostToolUse", "/w/b/y.go")))

	if got := r.res.count(); got != 2 {
		t.Errorf("resolver ran %d times, want one per path", got)
	}
}

func TestEditAfterSessionChange(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/b/x.go": placeAt("/w/b"), "/w/c/y.go": placeAt("/w/c")}, nil)
	start := func(session core.SessionID) harness.Event {
		return harness.Event{Pane: r.id, Name: "SessionStart", Start: true, Session: session}
	}

	r.hook(start("s1"))

	edit := r.hook(r.event("PostToolUse", "/w/b/x.go"))

	r.hook(start("s2"))
	r.deliver(edit)

	if p := r.pane(); len(p.Edits) != 0 || p.Session != "s2" {
		t.Errorf("pane = session %q with %d edits, want s2 with none: the answer is of the old session", p.Session, len(p.Edits))
	}

	r.deliver(r.hook(r.event("PostToolUse", "/w/c/y.go")))

	if got := r.roots(); !slices.Equal(got, []string{"/w/c"}) {
		t.Errorf("edited places = %v after an edit in the new session, want /w/c", got)
	}
}

func TestEditCacheFollowsThePane(t *testing.T) {
	t.Parallel()

	places := map[string]git.Place{"/w/b/x.go": placeAt("/w/b")}

	t.Run("becomes a shell", func(t *testing.T) {
		t.Parallel()

		r := newHookEditRig(t, places, nil)
		r.deliver(r.hook(r.event("PostToolUse", "/w/b/x.go")))

		if len(r.edits.last) != 1 {
			t.Fatalf("cache = %v, want the place just resolved", r.edits.last)
		}

		openIn(t, r.App, core.SetPaneKind{ID: r.id, Kind: core.KindShell})
		r.Update(struct{}{})

		if len(r.edits.last) != 0 {
			t.Errorf("cache = %v, want none for a shell", r.edits.last)
		}
	})

	t.Run("closes", func(t *testing.T) {
		t.Parallel()

		r := newHookEditRig(t, places, nil)
		r.deliver(r.hook(r.event("PostToolUse", "/w/b/x.go")))

		if len(r.edits.last) != 1 {
			t.Fatalf("cache = %v, want the place just resolved", r.edits.last)
		}

		r.closePane(r.id)
		r.Update(struct{}{})

		if len(r.edits.last) != 0 {
			t.Errorf("cache = %v, want none for a closed pane", r.edits.last)
		}
	})

	t.Run("an answer for a closed pane", func(t *testing.T) {
		t.Parallel()

		r := newHookEditRig(t, places, nil)
		cmd := r.hook(r.event("PostToolUse", "/w/b/x.go"))

		r.closePane(r.id)
		r.deliver(cmd)

		if len(r.edits.last) != 0 {
			t.Errorf("cache = %v, want none: the pane is gone", r.edits.last)
		}
	})
}

func TestSubagentCwdKeepsOwnDirectory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		subagent bool
		want     string
	}{
		{"a subagent's event", true, ""},
		{"the agent's own event", false, "/w/other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newHookEditRig(t, nil, nil)

			r.hook(harness.Event{Pane: r.id, Name: "PostToolUse", Cwd: "/w/other", Subagent: tt.subagent})

			if got := r.pane().AgentCwd; got != tt.want {
				t.Errorf("AgentCwd = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSubagentEditCounts(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/b/x.go": placeAt("/w/b")}, nil)

	ev := r.event("PostToolUse", "/w/b/x.go")
	ev.Subagent, ev.Cwd = true, "/w/b"

	r.deliver(r.hook(ev))

	if p := r.pane(); p.FollowDir() != "/w/b" || p.AgentCwd != "" {
		t.Errorf("pane = FollowDir %q AgentCwd %q, want the edit recorded and the agent's own directory kept", p.FollowDir(), p.AgentCwd)
	}
}

func TestEditInLinkedWorktree(t *testing.T) {
	r := newRailApp(t, false)
	tr := newAgentTrees(t, r.real)
	id := r.addAgent(t, false, tr.main)

	r.deliver(hook(r.App, harness.Event{Pane: id, Name: "PostToolUse", Edit: filepath.Join(tr.linked, "a.txt")}))

	p := r.store.State().Panes[id]

	e, ok := p.Followed()
	if !ok || p.FollowDir() != tr.linked || e.Root != tr.linked || e.Repo != filepath.Base(tr.main) || e.Common == "" {
		t.Errorf("Followed() = %+v, %v, FollowDir %q, want the linked worktree %q of repository %q", e, ok, p.FollowDir(), tr.linked, filepath.Base(tr.main))
	}
}
