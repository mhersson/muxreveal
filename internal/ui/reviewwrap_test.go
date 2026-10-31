package ui

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

func TestWrapLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		text  string
		units []diffUnit
		width int
		want  [][2]int
	}{
		{"exact fit", "abcdefghij", nil, 10, [][2]int{{0, 10}}},
		{"one over", "abcdefghijk", nil, 10, [][2]int{{0, 10}, {10, 11}}},
		{"space in reach", strings.Repeat("a", 21) + " " + strings.Repeat("b", 8), nil, 25, [][2]int{{0, 22}, {22, 30}}},
		{"space out of reach", strings.Repeat("a", 7) + " " + strings.Repeat("b", 22), nil, 25, [][2]int{{0, 25}, {25, 30}}},
		{"tab moves whole", "abcdefghi\tx", nil, 10, [][2]int{{0, 9}, {9, 13}}},
		{"tag moves whole", "abcdefgh\u200bxy", nil, 10, [][2]int{{0, 8}, {8, 18}}},
		{"tag wider than the column", "\u200b", nil, 5, [][2]int{{0, 8}}},
		{"empty", "", nil, 10, [][2]int{{0, 0}}},
		{"ending tag wraps alone", "", lineUnits("abcdefghij", true), 10, [][2]int{{0, 10}, {10, 18}}},
		{"ending tag skips the reach space", "", lineUnits("foo barxyz", true), 10, [][2]int{{0, 10}, {10, 18}}},
		{"budget ellipsis wraps alone", "", lineUnits(strings.Repeat("a", 1500), false), 1024, [][2]int{{0, 1024}, {1024, 1025}}},
		{"budget ellipsis skips the reach space", "", lineUnits(strings.Repeat("a", 1010)+" "+strings.Repeat("b", 100), false), 1024, [][2]int{{0, 1024}, {1024, 1025}}},
		{"no width", "abc def", nil, 0, [][2]int{{0, 7}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			units := tt.units
			if units == nil {
				units = diffUnits(tt.text)
			}

			parts := wrapLine(units, tt.width)

			var got [][2]int
			for _, p := range parts {
				got = append(got, [2]int{p.c0, p.c1})
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parts = %v, want %v", got, tt.want)
			}

			if tt.width <= 0 && (len(parts) != 1 || parts[0].u0 != 0 || parts[0].u1 != len(units)) {
				t.Fatalf("width %d: part %+v does not hold all %d units", tt.width, parts, len(units))
			}
		})
	}
}

func TestLineUnits(t *testing.T) {
	t.Parallel()

	long := lineUnits(strings.Repeat("x", 1500), false)
	if len(long) != lineBudget+1 {
		t.Fatalf("units = %d, want %d", len(long), lineBudget+1)
	}

	if last := long[len(long)-1]; !last.whole || last.cells != 1 {
		t.Fatalf("last unit = %+v, want a whole 1-cell ellipsis", last)
	}

	tagged := lineUnits("a", true)
	if len(tagged) != 2 || tagged[1].text != endingTag {
		t.Fatalf("tagged units = %+v, want the ending tag last", tagged)
	}

	if got := lineUnits("a\r", false); len(got) != 1 {
		t.Fatalf("a CR without the tag gave %d units, want 1", len(got))
	}
}

func TestLinePartsFastPath(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []rune("ab c\t\u200b\r")

	for range 200 {
		var b strings.Builder
		for range rng.IntN(60) {
			b.WriteRune(alphabet[rng.IntN(len(alphabet))])
		}

		raw := b.String()
		width := 1 + rng.IntN(40)

		for _, tagCR := range []bool{false, true} {
			got := lineParts(raw, tagCR, width)
			want := wrapLine(lineUnits(raw, tagCR), width)

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("lineParts(%q, %v, %d) = %v, want %v", raw, tagCR, width, got, want)
			}
		}
	}

	long := strings.Repeat("a", 1500)
	if got, want := lineParts(long, false, 2000), wrapLine(lineUnits(long, false), 2000); !reflect.DeepEqual(got, want) {
		t.Fatalf("long line: lineParts = %v, want %v", got, want)
	}
}
