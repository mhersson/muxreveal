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
				Review: &core.Review{Pane: "p2", Mode: core.ReviewTurn},
				Mode:   core.ModeBSP, Panes: []core.PaneID{"p1", "p2", "p3"},
				Layout: split(core.SplitVertical, 0.333333,
					leaf("p1"),
					split(core.SplitHorizontal, 0.5, leaf("p2"), leaf("p3"))),
			},
			{ID: "t2", Focus: "p4", Mode: core.ModeBSP, Panes: []core.PaneID{"p4"}, Layout: leaf("p4")},
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
	got, rep, err := decodeDefault(data)
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
	got, _, err := decodeDefault(data)
	noErr(t, err)
	eq(t, len(got.Tabs), 1)
	eq(t, got.Tabs[0].ID, core.TabID("t1"))

	if !got.Tabs[0].Empty() || len(got.Panes) != 0 {
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
	got, _, err := decodeDefault(data)
	noErr(t, err)
	eq(t, got.Panes["p4"], core.Pane{Kind: core.KindShell, Cwd: "/home/me"})
}

func TestEncodeSkipsOrphanPanes(t *testing.T) {
	st := fixedState()
	st.Panes["p9"] = core.Pane{Kind: core.KindShell}
	data, err := Encode(st)
	noErr(t, err)

	if _, _, err := decodeDefault(data); err != nil {
		t.Fatalf("a pane in no tab broke the encoding: %v", err)
	}
}

// doc assembles a state file from raw JSON pieces.
func doc(version, active, tabs, panes string) string {
	return fmt.Sprintf(`{"version":%s,"active":%s,"tabs":[%s],"panes":{%s}}`, version, active, tabs, panes)
}

func tabDoc(id, focused, ids, layout string) string {
	return fmt.Sprintf(`{"id":%q,"mode":"bsp","focused":%q,"pane_ids":[%s],"layout":%s}`, id, focused, ids, layout)
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
		{"one", `1`, "1", false},
		{"two", `2`, "", true},
		{"two as float", `2.0`, "", true},
		{"three", `3`, "3", false},
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
			st, rep, err := decodeDefault([]byte(data))
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
	st, rep, err := decodeDefault([]byte(data))
	noErr(t, err)
	eq(t, rep, Report{Version: "unknown"})
	eq(t, st.Tabs[0].Layout, defaultLayout([]core.PaneID{"p1", "p2"}))
}

func TestDecodeKeepsSessionsOnNewerVersion(t *testing.T) {
	panes := `"p1":{"kind":"agent","harness":"claude-code","cwd":"/a","session_id":"` + sessionA + `","transcript":"/t.jsonl"}`
	data := doc(`2`, "0", tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`), panes)
	st, _, err := decodeDefault([]byte(data))
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
			t1 := fmt.Sprintf(`{"id":"t1","mode":"bsp","focused":"p2","pane_ids":["p1","p2"]%s}`, optLayout(layout))
			t2 := tabDoc("t2", "p4", `"p3","p4"`, good)
			st, rep, err := decodeDefault([]byte(doc("2", "1", t1+","+t2, panes)))
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
	st, rep, err := decodeDefault([]byte(doc("2", "0", tabDoc("t1", "p1", `"p1","p2"`, layout), twoPanes)))
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
		{"truncated", doc("2", "0", one, twoPanes)[:40]},
		{"empty input", ``},
		{"null", `null`},
		{"array", `[]`},
		{"number", `0`},
		{"nan ratio token", `{"version":2,"active":0,"tabs":[` + tabDoc("t1", "p1", `"p1","p2"`, `{"split":"vertical","ratio":NaN,"children":[{"pane":"p1"},{"pane":"p2"}]}`) + `],"panes":{` + twoPanes + `}}`},
		{"trailing data", doc("2", "0", one, twoPanes) + `{}`},
		{"no tabs", `{"version":2,"active":0,"tabs":[],"panes":{}}`},
		{"tabs missing", `{"version":2,"active":0}`},
		{"active negative", doc("2", "-1", one, twoPanes)},
		{"active past the end", doc("2", "3", one+","+tabDoc("t2", "p3", `"p3"`, `{"pane":"p3"}`), twoPanes+`,"p3":{"kind":"shell"}`)},
		{"active not a number", doc("2", `"0"`, one, twoPanes)},
		{"duplicate tab id", doc("2", "0", one+","+tabDoc("t1", "p3", `"p3"`, `{"pane":"p3"}`), twoPanes+`,"p3":{"kind":"shell"}`)},
		{"pane in two tabs", doc("2", "0", one+","+tabDoc("t2", "p2", `"p2"`, `{"pane":"p2"}`), twoPanes)},
		{"pane twice in a tab", doc("2", "0", tabDoc("t1", "p1", `"p1","p1","p2"`, twoLayout), twoPanes)},
		{"pane_ids names a missing pane", doc("2", "0", tabDoc("t1", "p1", `"p1","p2","p7"`, twoLayout), twoPanes)},
		{"pane in no tab", doc("2", "0", tabDoc("t1", "p1", `"p1"`, `{"pane":"p1"}`), twoPanes)},
		{"kind review", doc("2", "0", one, `"p1":{"kind":"review"},"p2":{"kind":"shell"}`)},
		{"kind missing", doc("2", "0", one, `"p1":{},"p2":{"kind":"shell"}`)},
		{"harness codex", doc("2", "0", one, `"p1":{"kind":"agent","harness":"codex"},"p2":{"kind":"shell"}`)},
		{"agent without harness", doc("2", "0", one, `"p1":{"kind":"agent"},"p2":{"kind":"shell"}`)},
		{"empty tab beside another", doc("2", "0", one+","+tabDoc("t2", "", ``, `null`), twoPanes)},
		{"tab id shape", doc("2", "0", tabDoc("x1", "p1", `"p1","p2"`, twoLayout), twoPanes)},
		{"tab id zero", doc("2", "0", tabDoc("t0", "p1", `"p1","p2"`, twoLayout), twoPanes)},
		{"tab id leading zero", doc("2", "0", tabDoc("t01", "p1", `"p1","p2"`, twoLayout), twoPanes)},
		{"tab id overflow", doc("2", "0", tabDoc("t99999999999999999999", "p1", `"p1","p2"`, twoLayout), twoPanes)},
		{"pane id shape", doc("2", "0", tabDoc("t1", "q1", `"q1"`, `{"pane":"q1"}`), `"q1":{"kind":"shell"}`)},
		{"pane id huge", doc("2", "0", tabDoc("t1", "p9223372036854775807", `"p9223372036854775807"`, `{"pane":"p9223372036854775807"}`), `"p9223372036854775807":{"kind":"shell"}`)},
		{"pane_ids wrong type", `{"version":2,"active":0,"tabs":[{"id":"t1","pane_ids":"p1"}],"panes":{"p1":{"kind":"shell"}}}`},
		{"pane wrong type", `{"version":2,"active":0,"tabs":[{"id":"t1","pane_ids":["p1"]}],"panes":{"p1":"shell"}}`},
		{"null tab", `{"version":2,"active":0,"tabs":[null],"panes":{}}`},
		{"newer version still validates tabs", doc("2", "0", one, `"p1":{"kind":"review"},"p2":{"kind":"shell"}`)},
		{"newer version still checks active", doc("2", "7", one, twoPanes)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := decodeDefault([]byte(tt.data))
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

	return doc("2", "0", tabDoc("t1", "p1", strings.Join(ids, ","), `null`), strings.Join(panes, ","))
}

func TestDecodePaneCap(t *testing.T) {
	st, _, err := decodeDefault([]byte(manyPanes(64)))
	noErr(t, err)
	eq(t, len(st.Panes), 64)

	_, _, err = decodeDefault([]byte(manyPanes(65)))
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
			`{"id":"t1","mode":"bsp","pane_ids":["p1","p2"],"layout":` + twoLayout + `}`,
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
			`{"id":"t1","mode":"bsp","name":"` + strings.Repeat("a", 5) + `\t` + strings.Repeat("b", 34) + `","focused":"p1","pane_ids":["p1"],"layout":{"pane":"p1"}}`,
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
			`{"id":"t1","mode":"bsp","name":"x\u001b[2Jy\u0000z\u007f","focused":"p1","pane_ids":["p1"],"layout":{"pane":"p1"}}`,
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

				if !st.Tabs[0].Empty() || st.Tabs[0].Focus != "" {
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
			st, rep, err := decodeDefault([]byte(doc("2", "0", tt.tabs, tt.panes)))
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
		_, _, _ = decodeDefault(full[:i])
	}

	for _, in := range []string{
		`{"version":2,"active":0,"tabs":[{"id":"t1","pane_ids":["p1"],"layout":{"pane":"p1","children":[null]}}],"panes":{"p1":{"kind":"shell"}}}`,
		`{"version":[[[[[[]]]]]],"tabs":{},"panes":[]}`,
		`{"tabs":[{"id":"t1","pane_ids":[null]}],"panes":{"":{}}}`,
		`{"version":2,"tabs":[{"id":"t1","pane_ids":["p1"],"layout":{"split":"vertical","ratio":"x","children":[{},{}]}}],"panes":{"p1":{"kind":"shell"}}}`,
		strings.Repeat("[", 100000),
		strings.Repeat(`{"a":`, 100000),
	} {
		_, _, _ = decodeDefault([]byte(in))
	}
}

func TestDecodeRejectsHugeInput(t *testing.T) {
	_, _, err := decodeDefault([]byte(strings.Repeat(" ", 17<<20)))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	_, _, err := Load(filepath.Join(dir, "state.json"), core.LayoutConfig{})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}

	data, err := Encode(fixedState())
	noErr(t, err)

	path := filepath.Join(dir, "state.json")
	noErr(t, os.WriteFile(path, data, 0o600))
	st, _, err := Load(path, core.LayoutConfig{})
	noErr(t, err)
	eq(t, st, roundTripped())

	noErr(t, os.WriteFile(path, []byte(`{"version":2`), 0o600))

	_, _, err = Load(path, core.LayoutConfig{})
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

	got, _, err := decodeDefault(data)
	noErr(t, err)

	if len(got.Panes) != core.MaxPanes {
		t.Errorf("panes = %d, want %d", len(got.Panes), core.MaxPanes)
	}
}

func decodeDefault(data []byte) (core.State, Report, error) {
	return Decode(data, core.LayoutConfig{})
}

func TestAgentCwdIsNotPersisted(t *testing.T) {
	st := fixedState()
	p := st.Panes["p1"]
	p.AgentCwd = "/home/me/code/api/sub"
	st.Panes["p1"] = p

	data, err := Encode(st)
	noErr(t, err)

	if strings.Contains(string(data), "/home/me/code/api/sub") {
		t.Errorf("the agent's cwd is written:\n%s", data)
	}

	got, rep, err := decodeDefault(data)
	noErr(t, err)
	eq(t, rep, Report{})
	eq(t, got.Panes["p1"].AgentCwd, "")
	eq(t, got.Panes["p1"].Cwd, "/home/me/code/api")
	eq(t, got, roundTripped())
}

func TestAgentStatusIsNotPersisted(t *testing.T) {
	st := fixedState()
	p := st.Panes["p1"]
	p.Model, p.Context, p.ContextKnown, p.StatusSession = "Opus 4.8", 73, true, "status-session"
	st.Panes["p1"] = p

	data, err := Encode(st)
	noErr(t, err)

	for _, leak := range []string{"Opus 4.8", "status-session", "context", "model"} {
		if strings.Contains(string(data), leak) {
			t.Errorf("the status is written (%q):\n%s", leak, data)
		}
	}

	got, rep, err := decodeDefault(data)
	noErr(t, err)
	eq(t, rep, Report{})
	eq(t, got.Panes["p1"].Model, "")
	eq(t, got.Panes["p1"].Context, 0)
	eq(t, got.Panes["p1"].ContextKnown, false)
	eq(t, got.Panes["p1"].StatusSession, core.SessionID(""))
	eq(t, got, roundTripped())
}

func TestEditsNotSaved(t *testing.T) {
	t.Parallel()

	st := fixedState()
	p := st.Panes["p1"]
	p.Edits = []core.Edit{{Root: "/home/me/code/other", Common: "/home/me/code/other/.git", Repo: "other", Seq: 3}}
	st.Panes["p1"] = p

	data, err := Encode(st)
	noErr(t, err)

	if strings.Contains(string(data), "/home/me/code/other") {
		t.Errorf("the places an agent edited are written:\n%s", data)
	}

	want, err := Encode(fixedState())
	noErr(t, err)
	eq(t, string(data), string(want))

	got, rep, err := decodeDefault(data)
	noErr(t, err)
	eq(t, rep, Report{})
	eq(t, len(got.Panes["p1"].Edits), 0)
	eq(t, got, roundTripped())
}

func TestRailPersistence(t *testing.T) {
	open := fixedState()
	open.Rail = true

	data, err := Encode(open)
	noErr(t, err)

	if !strings.Contains(string(data), "\"rail\": true") {
		t.Errorf("open rail is not written:\n%s", data)
	}

	got, rep, err := decodeDefault(data)
	noErr(t, err)
	eq(t, rep, Report{})
	eq(t, got.Rail, true)

	data, err = Encode(fixedState())
	noErr(t, err)

	if strings.Contains(string(data), "rail") {
		t.Errorf("closed rail is written:\n%s", data)
	}
}

func TestDecodeRailByVersion(t *testing.T) {
	tests := []struct {
		name, version, rail string
		want                bool
	}{
		{"v2 without the field", "2", "", false},
		{"v2 open", "2", `"rail":true,`, true},
		{"v2 explicitly closed", "2", `"rail":false,`, false},
		{"v1 without the field", "1", "", false},
		{"v1 ignores the field", "1", `"rail":true,`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := doc(tt.version, "0", tabDoc("t1", "p1", `"p1","p2"`, twoLayout), twoPanes)
			data := strings.Replace(body, `"active"`, tt.rail+`"active"`, 1)
			st, _, err := decodeDefault([]byte(data))
			noErr(t, err)
			eq(t, st.Rail, tt.want)
		})
	}
}

func TestReviewPersistence(t *testing.T) {
	open := fixedState()
	open.Tabs[0].Review = &core.Review{Pane: "p2", Mode: core.ReviewBranch}

	data, err := Encode(open)
	noErr(t, err)

	if !strings.Contains(string(data), `"review": {`) || !strings.Contains(string(data), `"version": 2`) {
		t.Errorf("open drawer or version not written as expected:\n%s", data)
	}

	got, rep, err := decodeDefault(data)
	noErr(t, err)
	eq(t, rep, Report{})
	eq(t, got.Tabs[0].Review, open.Tabs[0].Review)
	eq(t, got.Tabs[1].Review, (*core.Review)(nil))

	closed := fixedState()
	closed.Tabs[0].Review = nil

	data, err = Encode(closed)
	noErr(t, err)

	if strings.Contains(string(data), `"review":`) {
		t.Errorf("closed drawer is written:\n%s", data)
	}
}

// Each tab's drawer saves and loads its own tree state.
func TestReviewTreePersistence(t *testing.T) {
	tests := []struct {
		name         string
		tree1, tree2 bool
		saved        int
	}{
		{"first open, second closed", true, false, 1},
		{"first closed, second open", false, true, 1},
		{"both open", true, true, 2},
		{"both closed", false, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := fixedState()
			st.Tabs[0].Review = &core.Review{Pane: "p2", Mode: core.ReviewBranch, Tree: tt.tree1}
			st.Tabs[1].Review = &core.Review{Pane: "p4", Mode: core.ReviewTurn, Tree: tt.tree2}

			data, err := Encode(st)
			noErr(t, err)

			if n := strings.Count(string(data), `"tree": true`); n != tt.saved {
				t.Errorf("%d tree entries written, want %d:\n%s", n, tt.saved, data)
			}

			if strings.Contains(string(data), `"tree": false`) {
				t.Errorf("a closed tree is written:\n%s", data)
			}

			got, rep, err := decodeDefault(data)
			noErr(t, err)
			eq(t, rep, Report{})
			eq(t, got.Tabs[0].Review, st.Tabs[0].Review)
			eq(t, got.Tabs[1].Review, st.Tabs[1].Review)
		})
	}
}

func TestReviewDecodeDropsBadFields(t *testing.T) {
	tests := []struct {
		name, review string
		want         *core.Review
	}{
		{"valid", `{"pane":"p2","mode":"turn"}`, &core.Review{Pane: "p2", Mode: core.ReviewTurn}},
		{"tree open", `{"pane":"p2","mode":"branch","tree":true}`, &core.Review{Pane: "p2", Mode: core.ReviewBranch, Tree: true}},
		{"tree closed", `{"pane":"p2","mode":"turn","tree":false}`, &core.Review{Pane: "p2", Mode: core.ReviewTurn}},
		{"tree is a string", `{"pane":"p2","mode":"turn","tree":"yes"}`, nil},
		{"unknown field", `{"pane":"p2","mode":"turn","later":1}`, &core.Review{Pane: "p2", Mode: core.ReviewTurn}},
		{"unknown pane", `{"pane":"p9","mode":"turn"}`, nil},
		{"pane of another tab", `{"pane":"p3","mode":"turn"}`, nil},
		{"unknown mode", `{"pane":"p2","mode":"x"}`, nil},
		{"missing mode", `{"pane":"p2"}`, nil},
		{"null", `null`, nil},
		{"wrong type", `"p2"`, nil},
		{"pane is a number", `{"pane":2,"mode":"turn"}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t1 := strings.Replace(tabDoc("t1", "p1", `"p1","p2"`, twoLayout), `"mode":"bsp",`, `"mode":"bsp","review":`+tt.review+`,`, 1)
			t2 := tabDoc("t2", "p3", `"p3"`, `{"pane":"p3"}`)
			panes := twoPanes + `,"p3":{"kind":"shell"}`

			st, rep, err := decodeDefault([]byte(doc("2", "0", t1+","+t2, panes)))
			noErr(t, err)
			eq(t, rep, Report{})
			eq(t, st.Tabs[0].Review, tt.want)
			eq(t, len(st.Tabs[0].Panes), 2)
		})
	}
}

func TestPinNotSaved(t *testing.T) {
	t.Parallel()

	st := fixedState()
	st.Tabs[0].Pin = &core.Pin{Pane: "p2", Root: "/home/me/code/other"}

	data, err := Encode(st)
	noErr(t, err)

	if strings.Contains(string(data), "/home/me/code/other") {
		t.Errorf("the pinned worktree is written:\n%s", data)
	}

	want, err := Encode(fixedState())
	noErr(t, err)
	eq(t, string(data), string(want))

	got, rep, err := decodeDefault(data)
	noErr(t, err)
	eq(t, rep, Report{})

	if got.Tabs[0].Pin != nil {
		t.Errorf("round trip has a pin: %+v", got.Tabs[0].Pin)
	}

	eq(t, got, roundTripped())
}
