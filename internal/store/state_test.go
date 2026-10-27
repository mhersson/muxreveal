package store

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhersson/muxreveal/internal/core"
)

var update = flag.Bool("update", false, "regenerate testdata/state.golden.json")

const (
	sessionA = "7c0b6f1e-3a52-4d8e-9b41-2f5e8a1d0c37"
	sessionB = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
)

func fixedState() core.State {
	return core.State{
		Active:    1,
		NextID:    4,
		NextTabID: 2,
		Tabs: []core.Tab{
			{
				ID: "t1", Name: "api", Focus: "p2", Zoomed: true,
				Layout: split(core.SplitVertical, 0.333333,
					leaf("p1"),
					split(core.SplitHorizontal, 0.5, leaf("p2"), leaf("p3"))),
			},
			{ID: "t2", Focus: "p4", Layout: leaf("p4")},
		},
		Panes: map[core.PaneID]core.Pane{
			"p1": {
				Kind: core.KindAgent, Cwd: "/home/me/code/api", Harness: "claude-code",
				Session: sessionA, Transcript: "/home/me/.claude/projects/api/" + sessionA + ".jsonl",
			},
			"p2": {Kind: core.KindShell, Cwd: "/home/me/code/api"},
			"p3": {Kind: core.KindShell},
			"p4": {Kind: core.KindAgent, Cwd: "/home/me", Harness: "claude-code"},
		},
	}
}

// roundTripped is fixedState as Decode returns it: the ratio on the 0.01 grid.
func roundTripped() core.State {
	st := fixedState()
	st.Tabs[0].Layout.Ratio = 0.33
	return st
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	data, err := Encode(fixedState())
	noErr(t, err)
	got, rep, err := Decode(data)
	noErr(t, err)
	eq(t, rep, Report{})
	eq(t, got, roundTripped())
}

func TestEncodeGolden(t *testing.T) {
	data, err := Encode(fixedState())
	noErr(t, err)
	path := filepath.Join("testdata", "state.golden.json")
	if *update {
		noErr(t, os.WriteFile(path, data, 0o644))
	}
	want, err := os.ReadFile(path)
	noErr(t, err)
	if string(data) != string(want) {
		t.Fatalf("encoding differs from %s:\n%s", path, data)
	}
	if !strings.Contains(string(data), `"ratio": 0.33,`) {
		t.Fatalf("ratio 0.333333 not written as 0.33:\n%s", data)
	}
}

func TestEncodeDeterministic(t *testing.T) {
	a, err := Encode(fixedState())
	noErr(t, err)
	for range 5 {
		b, err := Encode(fixedState())
		noErr(t, err)
		if string(a) != string(b) {
			t.Fatal("two encodings of one state differ")
		}
	}
}

func TestEncodeNoTabs(t *testing.T) {
	data, err := Encode(core.State{})
	noErr(t, err)
	got, _, err := Decode(data)
	noErr(t, err)
	eq(t, len(got.Tabs), 1)
	eq(t, got.Tabs[0].ID, core.TabID("t1"))
	if got.Tabs[0].Layout != nil || len(got.Panes) != 0 {
		t.Fatalf("empty state decoded to %+v", got)
	}
}

func TestEncodeRejects(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*core.State)
	}{
		{"active out of range", func(s *core.State) { s.Active = 5 }},
		{"leaf without pane metadata", func(s *core.State) { delete(s.Panes, "p3") }},
		{"unknown kind", func(s *core.State) { s.Panes["p3"] = core.Pane{Kind: "review"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := fixedState()
			tt.mod(&st)
			_, err := Encode(st)
			wantErr(t, err)
		})
	}
}

func TestEncodeAgentWithoutHarnessIsShell(t *testing.T) {
	st := fixedState()
	st.Panes["p4"] = core.Pane{Kind: core.KindAgent, Cwd: "/home/me", Session: sessionA, Transcript: "/t.jsonl"}
	data, err := Encode(st)
	noErr(t, err)
	got, _, err := Decode(data)
	noErr(t, err)
	eq(t, got.Panes["p4"], core.Pane{Kind: core.KindShell, Cwd: "/home/me"})
}

func TestEncodeSkipsOrphanPanes(t *testing.T) {
	st := fixedState()
	st.Panes["p9"] = core.Pane{Kind: core.KindShell}
	data, err := Encode(st)
	noErr(t, err)
	if _, _, err := Decode(data); err != nil {
		t.Fatalf("a pane in no tab broke the encoding: %v", err)
	}
}

// doc assembles a state file from raw JSON pieces.
func doc(version, active, tabs, panes string) string {
	return fmt.Sprintf(`{"version":%s,"active":%s,"tabs":[%s],"panes":{%s}}`, version, active, tabs, panes)
}

func tabDoc(id, focused, ids, layout string) string {
	return fmt.Sprintf(`{"id":%q,"focused":%q,"pane_ids":[%s],"layout":%s}`, id, focused, ids, layout)
}

const (
	twoPanes  = `"p1":{"kind":"shell","cwd":"/a"},"p2":{"kind":"shell"}`
	twoLayout = `{"split":"vertical","ratio":0.5,"children":[{"pane":"p1"},{"pane":"p2"}]}`
)

func TestDecodeVersions(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
		layout  bool // the file's own layout survives
	}{
		{"one", `1`, "", true},
		{"one as float", `1.0`, "", true},
		{"two", `2`, "2", false},
		{"string", `"x"`, `"x"`, false},
		{"null", `null`, "unknown", false},
		{"zero", `0`, "0", false},
		{"long", `"abcdefghijklmnopqrstuvwxyz"`, `"abcdefghijklmno`, false},
		{"object with whitespace", "{\"a\":\t1}", `{"a":1}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The layout ratio 0.3 is off the default, so a kept layout shows.
			layout := `{"split":"vertical","ratio":0.3,"children":[{"pane":"p1"},{"pane":"p2"}]}`
			data := doc(tt.version, "0", tabDoc("t1", "p2", `"p1","p2"`, layout), twoPanes)
			st, rep, err := Decode([]byte(data))
			noErr(t, err)
			if strings.ContainsFunc(rep.Version, func(r rune) bool { return r < ' ' || r == 0x7f }) {
				t.Fatalf("version %q has control characters", rep.Version)
			}
			if len([]rune(rep.Version)) > 16 {
				t.Fatalf("version %q longer than 16 runes", rep.Version)
			}
			eq(t, rep.Version, tt.want)
			eq(t, rep.Defaulted, 0)
			eq(t, len(st.Panes), 2)
			eq(t, st.Panes["p1"].Cwd, "/a")
			wantLayout := defaultLayout([]core.PaneID{"p1", "p2"})
			if tt.layout {
				wantLayout = split(core.SplitVertical, 0.3, leaf("p1"), leaf("p2"))
			}
			eq(t, st.Tabs[0].Layout, wantLayout)
			eq(t, st.Tabs[0].Focus, core.PaneID("p2"))
		})
	}
}

func TestDecodeMissingVersion(t *testing.T) {
	data := `{"active":0,"tabs":[` + tabDoc("t1", "p1", `"p1","p2"`, twoLayout) + `],"panes":{` + twoPanes + `}}`
	st, rep, err := Decode([]byte(data))
	noErr(t, err)
	eq(t, rep, Report{Version: "unknown"})
	eq(t, st.Tabs[0].Layout, defaultLayout([]core.PaneID{"p1", "p2"}))
}

func TestDecodeKeepsSessionsOnNewerVersion(t *testing.T) {
	panes := `"p1":{"kind":"agent","harness":"claude-code","cwd":"/a","session_id":"` + sessionA + `","transcript":"/t.jsonl"}`
	data := doc(`2`, "0", tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`), panes)
	st, _, err := Decode([]byte(data))
	noErr(t, err)
	p := st.Panes["p1"]
	eq(t, p.Session, core.SessionID(sessionA))
	eq(t, p.Transcript, "/t.jsonl")
	eq(t, p.Cwd, "/a")
}

func TestDecodeBrokenLayoutDefaultsThatTab(t *testing.T) {
	panes := `"p1":{"kind":"shell"},"p2":{"kind":"shell"},"p3":{"kind":"shell"},"p4":{"kind":"shell"}`
	good := `{"split":"horizontal","ratio":0.3,"children":[{"pane":"p3"},{"pane":"p4"}]}`
	tests := []struct {
		name   string
		layout string
	}{
		{"wrong leaves", `{"split":"vertical","ratio":0.5,"children":[{"pane":"p1"},{"pane":"p9"}]}`},
		{"ratio five on a non-split", `{"pane":"p1","ratio":5}`},
		{"garbage", `"nonsense"`},
		{"null", `null`},
		{"absent", ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout := tt.layout
			t1 := fmt.Sprintf(`{"id":"t1","focused":"p2","pane_ids":["p1","p2"]%s}`, optLayout(layout))
			t2 := tabDoc("t2", "p4", `"p3","p4"`, good)
			st, rep, err := Decode([]byte(doc("1", "1", t1+","+t2, panes)))
			noErr(t, err)
			eq(t, rep, Report{Defaulted: 1})
			eq(t, st.Tabs[0].Layout, defaultLayout([]core.PaneID{"p1", "p2"}))
			eq(t, st.Tabs[1].Layout, split(core.SplitHorizontal, 0.3, leaf("p3"), leaf("p4")))
			eq(t, st.Tabs[0].Focus, core.PaneID("p2"))
			eq(t, st.Active, 1)
		})
	}
}

func optLayout(l string) string {
	if l == "" {
		return ""
	}
	return `,"layout":` + l
}

func TestDecodeClampsRatio(t *testing.T) {
	layout := `{"split":"vertical","ratio":5,"children":[{"pane":"p1"},{"pane":"p2"}]}`
	st, rep, err := Decode([]byte(doc("1", "0", tabDoc("t1", "p1", `"p1","p2"`, layout), twoPanes)))
	noErr(t, err)
	eq(t, rep, Report{})
	eq(t, st.Tabs[0].Layout.Ratio, 0.9)
}

func TestDecodeValidation(t *testing.T) {
	one := tabDoc("t1", "p1", `"p1","p2"`, twoLayout)
	tests := []struct {
		name string
		data string
	}{
		{"truncated", doc("1", "0", one, twoPanes)[:40]},
		{"empty input", ``},
		{"null", `null`},
		{"array", `[]`},
		{"number", `0`},
		{"nan ratio token", `{"version":1,"active":0,"tabs":[` + tabDoc("t1", "p1", `"p1","p2"`, `{"split":"vertical","ratio":NaN,"children":[{"pane":"p1"},{"pane":"p2"}]}`) + `],"panes":{` + twoPanes + `}}`},
		{"trailing data", doc("1", "0", one, twoPanes) + `{}`},
		{"no tabs", `{"version":1,"active":0,"tabs":[],"panes":{}}`},
		{"tabs missing", `{"version":1,"active":0}`},
		{"active negative", doc("1", "-1", one, twoPanes)},
		{"active past the end", doc("1", "3", one+","+tabDoc("t2", "p3", `"p3"`, `{"pane":"p3"}`), twoPanes+`,"p3":{"kind":"shell"}`)},
		{"active not a number", doc("1", `"0"`, one, twoPanes)},
		{"duplicate tab id", doc("1", "0", one+","+tabDoc("t1", "p3", `"p3"`, `{"pane":"p3"}`), twoPanes+`,"p3":{"kind":"shell"}`)},
		{"pane in two tabs", doc("1", "0", one+","+tabDoc("t2", "p2", `"p2"`, `{"pane":"p2"}`), twoPanes)},
		{"pane twice in a tab", doc("1", "0", tabDoc("t1", "p1", `"p1","p1","p2"`, twoLayout), twoPanes)},
		{"pane_ids names a missing pane", doc("1", "0", tabDoc("t1", "p1", `"p1","p2","p7"`, twoLayout), twoPanes)},
		{"pane in no tab", doc("1", "0", tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`), twoPanes)},
		{"kind review", doc("1", "0", one, `"p1":{"kind":"review"},"p2":{"kind":"shell"}`)},
		{"kind missing", doc("1", "0", one, `"p1":{},"p2":{"kind":"shell"}`)},
		{"harness codex", doc("1", "0", one, `"p1":{"kind":"agent","harness":"codex"},"p2":{"kind":"shell"}`)},
		{"agent without harness", doc("1", "0", one, `"p1":{"kind":"agent"},"p2":{"kind":"shell"}`)},
		{"empty tab beside another", doc("1", "0", one+","+tabDoc("t2", "", ``, `null`), twoPanes)},
		{"tab id shape", doc("1", "0", tabDoc("x1", "p1", `"p1","p2"`, twoLayout), twoPanes)},
		{"tab id zero", doc("1", "0", tabDoc("t0", "p1", `"p1","p2"`, twoLayout), twoPanes)},
		{"tab id leading zero", doc("1", "0", tabDoc("t01", "p1", `"p1","p2"`, twoLayout), twoPanes)},
		{"tab id overflow", doc("1", "0", tabDoc("t99999999999999999999", "p1", `"p1","p2"`, twoLayout), twoPanes)},
		{"pane id shape", doc("1", "0", tabDoc("t1", "q1", `"q1"`, `{"pane":"q1"}`), `"q1":{"kind":"shell"}`)},
		{"pane id huge", doc("1", "0", tabDoc("t1", "p9223372036854775807", `"p9223372036854775807"`, `{"pane":"p9223372036854775807"}`), `"p9223372036854775807":{"kind":"shell"}`)},
		{"pane_ids wrong type", `{"version":1,"active":0,"tabs":[{"id":"t1","pane_ids":"p1"}],"panes":{"p1":{"kind":"shell"}}}`},
		{"pane wrong type", `{"version":1,"active":0,"tabs":[{"id":"t1","pane_ids":["p1"]}],"panes":{"p1":"shell"}}`},
		{"null tab", `{"version":1,"active":0,"tabs":[null],"panes":{}}`},
		{"newer version still validates tabs", doc("2", "0", one, `"p1":{"kind":"review"},"p2":{"kind":"shell"}`)},
		{"newer version still checks active", doc("2", "7", one, twoPanes)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Decode([]byte(tt.data))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

// manyPanes is a one-tab document with n shell panes in a row.
func manyPanes(n int) string {
	var ids, panes []string
	for i := 1; i <= n; i++ {
		ids = append(ids, fmt.Sprintf("%q", fmt.Sprintf("p%d", i)))
		panes = append(panes, fmt.Sprintf(`"p%d":{"kind":"shell"}`, i))
	}
	return doc("1", "0", tabDoc("t1", "p1", strings.Join(ids, ","), `null`), strings.Join(panes, ","))
}

func TestDecodePaneCap(t *testing.T) {
	st, _, err := Decode([]byte(manyPanes(64)))
	noErr(t, err)
	eq(t, len(st.Panes), 64)

	_, _, err = Decode([]byte(manyPanes(65)))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("65 panes: err = %v, want ErrInvalid", err)
	}
}

func TestDecodeFallbacks(t *testing.T) {
	other := tabDoc("t2", "p3", `"p3"`, `{"pane":"p3"}`)
	tests := []struct {
		name  string
		tabs  string
		panes string
		check func(*testing.T, core.State)
	}{
		{
			"focused pane from another tab",
			tabDoc("t1", "p3", `"p1","p2"`, twoLayout) + "," + other,
			twoPanes + `,"p3":{"kind":"shell"}`,
			func(t *testing.T, st core.State) { eq(t, st.Tabs[0].Focus, core.PaneID("p1")) },
		},
		{
			"focused pane missing",
			`{"id":"t1","pane_ids":["p1","p2"],"layout":` + twoLayout + `}`,
			twoPanes,
			func(t *testing.T, st core.State) { eq(t, st.Tabs[0].Focus, core.PaneID("p1")) },
		},
		{
			"focused pane missing from a reordered layout",
			tabDoc("t1", "p9", `"p1","p2"`, `{"split":"vertical","ratio":0.5,"children":[{"pane":"p2"},{"pane":"p1"}]}`),
			twoPanes,
			func(t *testing.T, st core.State) { eq(t, st.Tabs[0].Focus, core.PaneID("p2")) },
		},
		{
			"shell metacharacters in a session id",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"agent","harness":"claude-code","session_id":"$(rm -rf ~)","transcript":"/t.jsonl"}`,
			func(t *testing.T, st core.State) {
				p := st.Panes["p1"]
				eq(t, p.Session, core.SessionID(""))
				eq(t, p.Transcript, "")
				eq(t, p.Kind, core.KindAgent)
			},
		},
		{
			"session id with a flag",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"agent","harness":"claude-code","session_id":"--dangerously-skip-permissions","transcript":"/t.jsonl"}`,
			func(t *testing.T, st core.State) { eq(t, st.Panes["p1"].Session, core.SessionID("")) },
		},
		{
			"valid session kept",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"agent","harness":"claude-code","session_id":"` + sessionB + `","transcript":"/t.jsonl"}`,
			func(t *testing.T, st core.State) {
				eq(t, st.Panes["p1"].Session, core.SessionID(sessionB))
				eq(t, st.Panes["p1"].Transcript, "/t.jsonl")
			},
		},
		{
			"transcript without a session",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"agent","harness":"claude-code","transcript":"/t.jsonl"}`,
			func(t *testing.T, st core.State) { eq(t, st.Panes["p1"].Transcript, "") },
		},
		{
			"agent fields on a shell",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"shell","harness":"claude-code","session_id":"` + sessionA + `","transcript":"/t.jsonl"}`,
			func(t *testing.T, st core.State) { eq(t, st.Panes["p1"], core.Pane{Kind: core.KindShell}) },
		},
		{
			"relative cwd",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"shell","cwd":"work/api"}`,
			func(t *testing.T, st core.State) { eq(t, st.Panes["p1"].Cwd, "") },
		},
		{
			"cwd with spaces and quotes",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"shell","cwd":"/tmp/a b's \"c\"; $(x)"}`,
			func(t *testing.T, st core.State) { eq(t, st.Panes["p1"].Cwd, `/tmp/a b's "c"; $(x)`) },
		},
		{
			"long name with a tab",
			`{"id":"t1","name":"` + strings.Repeat("a", 5) + `\t` + strings.Repeat("b", 34) + `","focused":"p1","pane_ids":["p1"],"layout":{"pane":"p1"}}`,
			`"p1":{"kind":"shell"}`,
			func(t *testing.T, st core.State) {
				n := st.Tabs[0].Name
				eq(t, len([]rune(n)), 32)
				if strings.ContainsFunc(n, func(r rune) bool { return r < ' ' }) {
					t.Fatalf("name %q has control characters", n)
				}
				eq(t, n, strings.Repeat("a", 5)+strings.Repeat("b", 27))
			},
		},
		{
			"name with an escape sequence",
			`{"id":"t1","name":"x\u001b[2Jy\u0000z\u007f","focused":"p1","pane_ids":["p1"],"layout":{"pane":"p1"}}`,
			`"p1":{"kind":"shell"}`,
			func(t *testing.T, st core.State) { eq(t, st.Tabs[0].Name, "x[2Jyz") },
		},
		{
			"next ids from the highest numbers",
			tabDoc("t3", "p2", `"p2"`, `{"pane":"p2"}`) + "," + tabDoc("t9", "p7", `"p7"`, `{"pane":"p7"}`),
			`"p2":{"kind":"shell"},"p7":{"kind":"shell"}`,
			func(t *testing.T, st core.State) {
				eq(t, st.NextID, 7)
				eq(t, st.NextTabID, 9)
			},
		},
		{
			"empty only tab",
			tabDoc("t4", "", ``, `null`),
			``,
			func(t *testing.T, st core.State) {
				eq(t, len(st.Tabs), 1)
				if st.Tabs[0].Layout != nil || st.Tabs[0].Focus != "" {
					t.Fatalf("empty tab decoded to %+v", st.Tabs[0])
				}
				eq(t, st.NextTabID, 4)
				eq(t, st.NextID, 0)
			},
		},
		{
			"states are unknown and theme empty",
			tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`),
			`"p1":{"kind":"agent","harness":"claude-code"}`,
			func(t *testing.T, st core.State) {
				eq(t, st.Panes["p1"].State, core.AgentUnknown)
				eq(t, st.Theme, "")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, rep, err := Decode([]byte(doc("1", "0", tt.tabs, tt.panes)))
			noErr(t, err)
			eq(t, rep, Report{})
			tt.check(t, st)
		})
	}
}

func TestDecodeNeverPanics(t *testing.T) {
	full, err := Encode(fixedState())
	noErr(t, err)
	for i := range len(full) + 1 {
		_, _, _ = Decode(full[:i])
	}
	for _, in := range []string{
		`{"version":1,"active":0,"tabs":[{"id":"t1","pane_ids":["p1"],"layout":{"pane":"p1","children":[null]}}],"panes":{"p1":{"kind":"shell"}}}`,
		`{"version":[[[[[[]]]]]],"tabs":{},"panes":[]}`,
		`{"tabs":[{"id":"t1","pane_ids":[null]}],"panes":{"":{}}}`,
		`{"version":1,"tabs":[{"id":"t1","pane_ids":["p1"],"layout":{"split":"vertical","ratio":"x","children":[{},{}]}}],"panes":{"p1":{"kind":"shell"}}}`,
		strings.Repeat("[", 100000),
		strings.Repeat(`{"a":`, 100000),
	} {
		_, _, _ = Decode([]byte(in))
	}
}

func TestDecodeRejectsHugeInput(t *testing.T) {
	_, _, err := Decode([]byte(strings.Repeat(" ", 17<<20)))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	_, _, err := Load(filepath.Join(dir, "state.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}

	data, err := Encode(fixedState())
	noErr(t, err)
	path := filepath.Join(dir, "state.json")
	noErr(t, os.WriteFile(path, data, 0o600))
	st, _, err := Load(path)
	noErr(t, err)
	eq(t, st, roundTripped())

	noErr(t, os.WriteFile(path, []byte(`{"version":1`), 0o600))
	_, _, err = Load(path)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRoundTripAtPaneLimit(t *testing.T) {
	s := core.NewStore(core.InitialState(core.Config{}))
	for len(s.State().Panes) < core.MaxPanes {
		noErr(t, s.Dispatch(core.OpenPane{Kind: core.KindShell, Split: core.SplitVertical}))
	}

	data, err := Encode(s.State())
	noErr(t, err)

	got, _, err := Decode(data)
	noErr(t, err)

	if len(got.Panes) != core.MaxPanes {
		t.Errorf("panes = %d, want %d", len(got.Panes), core.MaxPanes)
	}
}
