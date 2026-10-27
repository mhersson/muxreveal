package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/muxreveal/internal/core"
)

func TestRailWidthRule(t *testing.T) {
	tests := []struct {
		w, h, want int
	}{
		{200, 28, 44},
		{84, 28, 44},
		{83, 28, 43},
		{70, 28, 30},
		{64, 28, 24},
		{63, 28, 0},
		{10, 28, 0},
		{100, 3, 44},
		{100, 2, 0},
		{100, 0, 0},
	}

	for _, tt := range tests {
		if got := railWidthFor(core.Rect{Y: 2, W: tt.w, H: tt.h}); got != tt.want {
			t.Errorf("railWidthFor(%dx%d) = %d, want %d", tt.w, tt.h, got, tt.want)
		}
	}
}

func TestRailLayout(t *testing.T) {
	groups := []railGroup{
		{state: core.AgentBlocked, cards: []railCard{{pane: "p1"}}},
		{state: core.AgentIdle, cards: []railCard{{pane: "p2"}, {pane: "p3"}}},
	}

	var sb strings.Builder

	for _, r := range railLayout(groups) {
		switch r.kind {
		case railRowHeader:
			fmt.Fprintf(&sb, "H%d ", r.group)
		case railRowBlank:
			sb.WriteString("_ ")
		case railRowCard:
			fmt.Fprintf(&sb, "c%d.%d ", r.card, r.line)
		}
	}

	want := "H0 _ c0.0 c0.1 c0.2 c0.3 _ H1 _ c1.0 c1.1 c1.2 c1.3 _ c2.0 c2.1 c2.2 c2.3 _ "
	if got := sb.String(); got != want {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestRailClampTop(t *testing.T) {
	groups := []railGroup{
		{state: core.AgentWorking, cards: []railCard{{pane: "p1"}, {pane: "p2"}, {pane: "p3"}, {pane: "p4"}}},
		{state: core.AgentIdle, cards: []railCard{{pane: "p5"}, {pane: "p6"}}},
	}
	rows := railLayout(groups)
	// Rows: header 0; card k has its blank at 1+5k and lines 2+5k to 5+5k; the group's
	// closing blank is row 21 and the second header row 22.
	tests := []struct {
		name         string
		h, top, card int
		want         int
	}{
		{"fits already", 20, 0, 0, 0},
		{"first card shows its header", 20, 5, 0, 0},
		{"scrolls down to show the blank below", 10, 0, 3, 12},
		{"scrolls up to show the blank above", 10, 20, 1, 6},
		{"first card of a group brings its header", 10, 30, 4, 22},
		{"last card ends at the list end", 10, 0, 5, len(rows) - 10},
		{"too short: the first line wins", 3, 0, 2, 12},
		{"no selection leaves the top, clamped", 10, 99, -1, len(rows) - 10},
		{"negative top", 10, -4, -1, 0},
	}

	for _, tt := range tests {
		if got := railClampTop(rows, tt.h, tt.top, tt.card); got != tt.want {
			t.Errorf("%s: railClampTop(h=%d, top=%d, card=%d) = %d, want %d", tt.name, tt.h, tt.top, tt.card, got, tt.want)
		}
	}
}

func TestRailAge(t *testing.T) {
	now := t0

	tests := []struct {
		ago  time.Duration
		want string
	}{
		{0, "0s"},
		{8 * time.Second, "8s"},
		{59*time.Second + 900*time.Millisecond, "59s"},
		{60 * time.Second, "1m"},
		{4 * time.Minute, "4m"},
		{59 * time.Minute, "59m"},
		{60 * time.Minute, "1h"},
		{23 * time.Hour, "23h"},
		{24 * time.Hour, "1d"},
		{80 * time.Hour, "3d"},
		{-time.Second, "0s"},
	}

	for _, tt := range tests {
		if got := railAge(now.Add(-tt.ago), now); got != tt.want {
			t.Errorf("railAge(%v ago) = %q, want %q", tt.ago, got, tt.want)
		}
	}

	if got := railAge(time.Time{}, now); got != "" {
		t.Errorf("railAge(zero) = %q, want empty", got)
	}
}

func TestRailCardAt(t *testing.T) {
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	a.rail.sel = "p5"

	v, ok := a.railViewOf(a.store.State())
	if !ok {
		t.Fatal("rail hidden")
	}

	l := v.geom.list

	for i := range l.H {
		row := v.rows[v.top+i]
		got, found := v.cardAt(l.X+3, l.Y+i)

		switch row.kind {
		case railRowCard:
			if !found || got != row.card {
				t.Errorf("row %d (card %d line %d): cardAt = %d, %v", i, row.card, row.line, got, found)
			}
		case railRowHeader:
			if found {
				t.Errorf("row %d (header): cardAt = %d, want none", i, got)
			}
		case railRowBlank:
			if found {
				t.Errorf("row %d (blank): cardAt = %d, want none", i, got)
			}
		}
	}

	if _, found := v.cardAt(l.X-1, l.Y+3); found {
		t.Error("cardAt found a card on the rail's border")
	}

	if _, found := v.cardAt(l.X+3, l.Y+l.H); found {
		t.Error("cardAt found a card under the list")
	}
}
