package ui

import (
	"bytes"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	xkitty "github.com/charmbracelet/x/ansi/kitty"

	"github.com/mhersson/muxreveal/internal/md"
	"github.com/mhersson/muxreveal/internal/ui/kitty"
)

type recJobs struct{ jobs []pictureJob }

func (r *recJobs) add(j pictureJob) { r.jobs = append(r.jobs, j) }

func (r *recJobs) take() []pictureJob {
	out := r.jobs
	r.jobs = nil

	return out
}

type logBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.Write(p)
}

func (l *logBuf) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return strings.Count(l.buf.String(), "picture not loaded")
}

func newTestPictures(env map[string]string) (*pictures, *recJobs, *logBuf) {
	jobs := &recJobs{}
	lb := &logBuf{}
	p := newPictures(jobs, func(k string) string { return env[k] }, slog.New(slog.NewTextHandler(lb, nil)))

	return p, jobs, lb
}

var (
	gfxOK    = uv.KittyGraphicsEvent{Options: xkitty.Options{ID: kitty.QueryID}, Payload: []byte("OK")}
	gfxNo    = uv.KittyGraphicsEvent{Options: xkitty.Options{ID: kitty.QueryID}, Payload: []byte("ENOTSUPPORTED:no")}
	cell10x2 = uv.CellSizeEvent{Width: 10, Height: 20}
	da1      = uv.PrimaryDeviceAttributesEvent{62, 4}
	truecol  = tea.ColorProfileMsg{Profile: colorprofile.TrueColor}
	color256 = tea.ColorProfileMsg{Profile: colorprofile.ANSI256}
)

// feed sends msgs through reply and returns each command.
func feed(p *pictures, msgs ...tea.Msg) []tea.Cmd {
	out := make([]tea.Cmd, len(msgs))
	for i, m := range msgs {
		out[i], _ = p.reply(m)
	}

	return out
}

// batched is the number of commands a batch command holds.
func batched(t *testing.T, cmd tea.Cmd) int {
	t.Helper()

	if cmd == nil {
		return 0
	}

	v := reflect.ValueOf(cmd())
	if v.Kind() != reflect.Slice {
		t.Fatalf("command gave %v, want a batch", v)
	}

	return v.Len()
}

func TestPictureDetect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		env   map[string]string
		start bool
		msgs  []tea.Msg
		on    bool
	}{
		{"all three", nil, true, []tea.Msg{truecol, gfxOK, cell10x2, da1}, true},
		{"truecolor after the reply", nil, true, []tea.Msg{gfxOK, cell10x2, da1, truecol}, true},
		{"da1 first", nil, true, []tea.Msg{truecol, da1, gfxOK, cell10x2}, false},
		{"no cell size", nil, true, []tea.Msg{truecol, gfxOK, da1}, false},
		{"not supported", nil, true, []tea.Msg{truecol, gfxNo, cell10x2, da1}, false},
		{"256 colours", nil, true, []tea.Msg{color256, gfxOK, cell10x2, da1}, false},
		{"256 colours then truecolor", nil, true, []tea.Msg{color256, gfxOK, cell10x2, da1, truecol}, true},
		{"tmux", map[string]string{"TMUX": "/tmp/x"}, false, []tea.Msg{truecol, gfxOK, cell10x2, da1}, false},
		{"nested pane", map[string]string{"MUXREVEAL_PANE": "1"}, false, []tea.Msg{truecol, gfxOK, cell10x2, da1}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, _, _ := newTestPictures(tt.env)

			if got := p.start() != nil; got != tt.start {
				t.Fatalf("start() sent a query = %v, want %v", got, tt.start)
			}

			feed(p, tt.msgs...)

			if got := p.on(); got != tt.on {
				t.Errorf("on() = %v, want %v", got, tt.on)
			}
		})
	}
}

func TestPictureCapabilityRequest(t *testing.T) {
	t.Parallel()

	p, _, _ := newTestPictures(nil)
	cmds := feed(p, color256, gfxOK)

	if got := batched(t, cmds[1]); got != 2 {
		t.Fatalf("OK below truecolor gave %d requests, want RGB and Tc", got)
	}

	if again, _ := p.reply(gfxOK); again != nil {
		t.Error("the capability requests were sent twice")
	}

	q, _, _ := newTestPictures(nil)
	if cmd := feed(q, truecol, gfxOK)[1]; cmd != nil {
		t.Error("OK at truecolor asked for capabilities")
	}
}

func TestPictureNilJobs(t *testing.T) {
	t.Parallel()

	p := newPictures(nil, func(string) string { return "" }, nil)
	if p.start() != nil {
		t.Error("start with no jobs sent a query")
	}

	feed(p, truecol, gfxOK, cell10x2, da1)

	if p.on() {
		t.Error("pictures on with no loader")
	}
}

func onPictures(t *testing.T) (*pictures, *recJobs, *logBuf) {
	t.Helper()

	p, jobs, lb := newTestPictures(nil)
	p.start()
	feed(p, truecol, gfxOK, cell10x2, da1)

	if !p.on() {
		t.Fatal("pictures are off")
	}

	return p, jobs, lb
}

const ghURL = "https://github.com/user-attachments/assets/x"

func TestPictureSize(t *testing.T) {
	t.Parallel()

	d := func(p *pictures) docPictures { return docPictures{reg: p, root: "/repo", host: "github.com"} }

	t.Run("off", func(t *testing.T) {
		t.Parallel()

		p, jobs, _ := newTestPictures(nil)
		if got := p.size(d(p), ghURL, 96); got != (md.PictureSize{}) || len(jobs.jobs) != 0 {
			t.Errorf("off gave %+v with %d jobs", got, len(jobs.jobs))
		}
	})

	for _, src := range []string{
		"https://img.shields.io/b.png",
		"http://github.com/a.png",
		"https://github.com/a.svg",
		"/elsewhere/a.png",
		"/repo/../x/a.png",
		"/repo/a.SVG",
		"rel/a.png",
	} {
		t.Run("refused "+src, func(t *testing.T) {
			t.Parallel()

			p, jobs, _ := onPictures(t)
			if got := p.size(d(p), src, 96); got != (md.PictureSize{}) || len(jobs.jobs) != 0 {
				t.Errorf("gave %+v with %d jobs", got, len(jobs.jobs))
			}
		})
	}

	t.Run("local", func(t *testing.T) {
		t.Parallel()

		p, jobs, _ := onPictures(t)
		if got := p.size(d(p), "/repo/docs/a.png", 96); !got.Loading || len(jobs.jobs) != 1 || jobs.jobs[0].key.scope != "/repo" {
			t.Errorf("gave %+v with %+v", got, jobs.jobs)
		}
	})

	t.Run("lifecycle", func(t *testing.T) {
		t.Parallel()

		p, jobs, _ := onPictures(t)
		dp := d(p)

		if got := p.size(dp, ghURL, 96); got != (md.PictureSize{Loading: true}) {
			t.Fatalf("first = %+v", got)
		}

		if got := jobs.take(); len(got) != 1 || got[0].render || got[0].key.src != ghURL || got[0].key.scope != "github.com" || got[0].host != "github.com" {
			t.Fatalf("info job = %+v", got)
		}

		if p.size(dp, ghURL, 96); len(jobs.jobs) != 0 {
			t.Fatal("a second call added a job")
		}

		gen := p.gen()
		key := pictureKey{ghURL, "github.com"}

		if cmd := p.done(pictureDoneMsg{job: pictureJob{key: key}, w: 1847, h: 948}); cmd != nil {
			t.Error("an info answer returned a command")
		}

		if p.gen() == gen {
			t.Error("gen did not move")
		}

		if got := p.size(dp, ghURL, 96); got != (md.PictureSize{Loading: true}) {
			t.Fatalf("after info = %+v", got)
		}

		jb := jobs.take()
		if len(jb) != 1 || !jb[0].render || jb[0].cols != 78 || jb[0].rows != 20 || jb[0].id == 0 || jb[0].cell != (kitty.Cell{W: 10, H: 20}) {
			t.Fatalf("render job = %+v", jb)
		}

		cmd := p.done(pictureDoneMsg{job: jb[0], seq: "SEQ"})
		if raw, ok := cmd().(tea.RawMsg); !ok || raw.Msg != "SEQ" {
			t.Fatalf("render command gave %+v", cmd())
		}

		want := md.PictureSize{Cols: 78, Rows: 20, ID: jb[0].id}
		if got := p.size(dp, ghURL, 96); got != want {
			t.Errorf("after render = %+v, want %+v", got, want)
		}

		if got := p.size(dp, ghURL, 40); !got.Loading || len(jobs.take()) != 1 {
			t.Errorf("a narrower line gave %+v", got)
		}
	})

	t.Run("failure", func(t *testing.T) {
		t.Parallel()

		p, _, lb := onPictures(t)
		dp := d(p)
		key := pictureKey{ghURL, "github.com"}
		p.size(dp, ghURL, 96)
		p.done(pictureDoneMsg{job: pictureJob{key: key}, err: errors.New("boom")})

		if got := p.size(dp, ghURL, 96); got != (md.PictureSize{}) {
			t.Errorf("failed source gave %+v", got)
		}

		p.done(pictureDoneMsg{job: pictureJob{key: key}, err: errors.New("boom")})

		if n := lb.count(); n != 1 {
			t.Errorf("%d log records, want 1", n)
		}
	})
}

func TestPictureRetry(t *testing.T) {
	t.Parallel()

	p, jobs, lb := onPictures(t)
	dp := docPictures{reg: p, root: "/repo", host: "github.com"}
	key := pictureKey{ghURL, "github.com"}
	lay := md.Layout{Images: []md.Image{{Src: ghURL}}}

	p.size(dp, ghURL, 96)
	jobs.take()
	p.done(pictureDoneMsg{job: pictureJob{key: key}, err: errors.New("boom")})

	if got := p.size(dp, ghURL, 96); got != (md.PictureSize{}) {
		t.Fatalf("failed = %+v", got)
	}

	gen := p.gen()
	p.retry(dp, md.Layout{Images: []md.Image{{Src: "https://github.com/other.png"}}})

	if p.gen() != gen {
		t.Error("retry of a layout with no failed source moved gen")
	}

	p.retry(dp, lay)

	if p.gen() == gen {
		t.Error("retry did not move gen")
	}

	if got := p.size(dp, ghURL, 96); !got.Loading || len(jobs.take()) != 1 {
		t.Errorf("after retry = %+v", got)
	}

	p.done(pictureDoneMsg{job: pictureJob{key: key}, err: errors.New("boom again")})

	if n := lb.count(); n != 2 {
		t.Errorf("%d log records, want 2", n)
	}
}

// rendered sizes src, answers its info and render, and returns the id.
func rendered(t *testing.T, p *pictures, jobs *recJobs, dp docPictures, src string) (uint32, tea.Cmd) {
	t.Helper()

	p.size(dp, src, 96)

	info := jobs.take()
	if len(info) != 1 {
		t.Fatalf("info jobs = %+v", info)
	}

	p.done(pictureDoneMsg{job: info[0], w: 100, h: 100})
	p.size(dp, src, 96)

	r := jobs.take()
	if len(r) != 1 || !r[0].render {
		t.Fatalf("render jobs = %+v", r)
	}

	return r[0].id, p.done(pictureDoneMsg{job: r[0], seq: "S" + src})
}

func TestPictureLate(t *testing.T) {
	t.Parallel()

	dp := func(p *pictures) docPictures { return docPictures{reg: p, root: "/repo"} }

	t.Run("after a cell change", func(t *testing.T) {
		t.Parallel()

		p, jobs, _ := onPictures(t)
		p.size(dp(p), "/repo/a.png", 96)
		p.done(pictureDoneMsg{job: jobs.take()[0], w: 100, h: 100})
		p.size(dp(p), "/repo/a.png", 96)

		job := jobs.take()[0]

		p.reply(uv.CellSizeEvent{Width: 8, Height: 16})

		gen := p.gen()
		if cmd := p.done(pictureDoneMsg{job: job, seq: "S"}); cmd != nil || p.gen() != gen {
			t.Error("a stale render changed state")
		}
	})

	t.Run("evicted id", func(t *testing.T) {
		t.Parallel()

		p, jobs, _ := onPictures(t)
		p.size(dp(p), "/repo/a.png", 96)
		p.done(pictureDoneMsg{job: jobs.take()[0], w: 100, h: 100})
		p.size(dp(p), "/repo/a.png", 96)

		job := jobs.take()[0]
		job.id += 1000

		gen := p.gen()
		if cmd := p.done(pictureDoneMsg{job: job, seq: "S"}); cmd != nil || p.gen() != gen {
			t.Error("an unknown id changed state")
		}
	})

	t.Run("failed source", func(t *testing.T) {
		t.Parallel()

		p, jobs, lb := onPictures(t)
		p.size(dp(p), "/repo/a.png", 96)

		job := jobs.take()[0]
		p.done(pictureDoneMsg{job: job, err: errors.New("x")})

		gen := p.gen()
		if cmd := p.done(pictureDoneMsg{job: job, w: 5, h: 5}); cmd != nil || p.gen() != gen {
			t.Error("an info answer for a failed source changed state")
		}

		if got := p.size(dp(p), "/repo/a.png", 96); got != (md.PictureSize{}) || lb.count() != 1 {
			t.Errorf("got %+v, %d logs", got, lb.count())
		}
	})
}

func TestPictureEvict(t *testing.T) {
	t.Parallel()

	p, jobs, _ := onPictures(t)
	dp := docPictures{reg: p, root: "/repo"}

	ids := make([]uint32, 0, pictureMaxSent+1)
	srcs := make([]string, 0, pictureMaxSent+1)

	var last tea.Cmd

	for i := range pictureMaxSent + 1 {
		src := "/repo/" + strings.Repeat("a", i+1) + ".png"
		srcs = append(srcs, src)

		var id uint32

		id, last = rendered(t, p, jobs, dp, src)
		ids = append(ids, id)

		if i < pictureMaxSent && strings.Contains(rawOf(t, last), "a=d") {
			t.Fatalf("picture %d evicted early", i)
		}
	}

	raw := rawOf(t, last)
	if !strings.Contains(raw, kitty.Delete(ids[0])) {
		t.Errorf("command %q lacks the delete of the oldest", raw)
	}

	if strings.Count(raw, "a=d") != 1 {
		t.Errorf("command %q deletes more than one image", raw)
	}

	if got := p.size(dp, srcs[0], 96); !got.Loading || len(jobs.take()) != 1 {
		t.Errorf("evicted picture gave %+v", got)
	}

	if got := p.size(dp, srcs[1], 96); got.ID != ids[1] {
		t.Errorf("a kept picture gave %+v", got)
	}
}

func rawOf(t *testing.T, cmd tea.Cmd) string {
	t.Helper()

	if cmd == nil {
		return ""
	}

	raw, ok := cmd().(tea.RawMsg)
	if !ok {
		t.Fatalf("command did not give a RawMsg")
	}

	s, _ := raw.Msg.(string)

	return s
}

func TestPictureCellChange(t *testing.T) {
	t.Parallel()

	p, jobs, _ := onPictures(t)
	dp := docPictures{reg: p, root: "/repo"}
	rendered(t, p, jobs, dp, "/repo/a.png")

	gen := p.gen()

	cmd, _ := p.reply(uv.CellSizeEvent{Width: 8, Height: 16})
	if got := rawOf(t, cmd); got != kitty.DeleteAll() {
		t.Errorf("command = %q, want delete all", got)
	}

	if p.gen() == gen {
		t.Error("gen did not move")
	}

	if got := p.size(dp, "/repo/a.png", 96); !got.Loading {
		t.Fatalf("size = %+v", got)
	}

	jb := jobs.take()
	if len(jb) != 1 || !jb[0].render || jb[0].cell != (kitty.Cell{W: 8, H: 16}) {
		t.Errorf("jobs = %+v", jb)
	}

	if cmd, _ := p.reply(uv.CellSizeEvent{Width: 8, Height: 16}); cmd != nil {
		t.Error("an unchanged cell size returned a command")
	}
}

func TestPictureResized(t *testing.T) {
	t.Parallel()

	p, _, _ := onPictures(t)
	if rawOf(t, p.resized()) != kitty.CellSizeQuery {
		t.Error("resized did not ask for the cell size")
	}

	q, _, _ := newTestPictures(nil)
	if q.resized() != nil {
		t.Error("resized asked while off")
	}
}

func TestPictureQuit(t *testing.T) {
	t.Parallel()

	next := func() tea.Msg { return "next" }

	p, jobs, _ := onPictures(t)
	if got := p.quit(next)(); got != "next" {
		t.Errorf("with nothing sent, quit gave %v", got)
	}

	rendered(t, p, jobs, docPictures{reg: p, root: "/repo"}, "/repo/a.png")

	v := reflect.ValueOf(p.quit(next)())
	if v.Kind() != reflect.Slice || v.Len() != 2 {
		t.Fatalf("quit gave %v", v)
	}

	first, _ := v.Index(0).Interface().(tea.Cmd)
	if got := rawOf(t, first); got != kitty.DeleteAll() {
		t.Errorf("first = %q", got)
	}

	second, _ := v.Index(1).Interface().(tea.Cmd)
	if second() != "next" {
		t.Error("second is not next")
	}
}

func TestDocPictureMove(t *testing.T) {
	t.Parallel()

	src := []byte("hello\n\n![a](/repo/a.png)\n")

	t.Run("gen bump", func(t *testing.T) {
		t.Parallel()

		p, _, _ := onPictures(t)
		d := parseMarkdown(src, "/repo", nil).withPictures(docPictures{reg: p, root: "/repo"})
		first := d.layout(80, false)

		if _, ok := d.pictureMove(); ok {
			t.Fatal("move before any change")
		}

		p.g++

		second := d.layout(80, false)
		if d.layouts != 2 {
			t.Fatalf("layouts = %d, want 2", d.layouts)
		}

		old, ok := d.pictureMove()
		if !ok || !reflect.DeepEqual(old, first) {
			t.Errorf("move = %v, old layout match = %v", ok, reflect.DeepEqual(old, first))
		}

		if _, ok := d.pictureMove(); ok {
			t.Error("move offered twice")
		}

		_ = second
	})

	t.Run("width change", func(t *testing.T) {
		t.Parallel()

		p, _, _ := onPictures(t)
		d := parseMarkdown(src, "/repo", nil).withPictures(docPictures{reg: p, root: "/repo"})
		d.layout(80, false)

		p.g++

		d.layout(70, false)

		if _, ok := d.pictureMove(); ok {
			t.Error("a width change offered a move")
		}
	})

	t.Run("no images", func(t *testing.T) {
		t.Parallel()

		p, _, _ := onPictures(t)
		d := parseMarkdown([]byte("just text\n"), "/repo", nil).withPictures(docPictures{reg: p, root: "/repo"})
		d.layout(80, false)

		p.g++

		d.layout(80, false)

		if d.layouts != 1 {
			t.Errorf("layouts = %d, want 1", d.layouts)
		}
	})
}
