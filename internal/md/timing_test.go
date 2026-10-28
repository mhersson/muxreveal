package md

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// skipTiming skips the wall-clock tests under -race and -short. make check
// runs only the race tests, so these run under make test and in the gate run.
func skipTiming(t *testing.T) {
	t.Helper()

	if raceEnabled {
		t.Skip("timings are meaningless under the race detector")
	}

	if testing.Short() {
		t.Skip("timing tests skip under -short")
	}
}

// fastestLayout parses src outside the timed region and returns the fastest of
// three Layout runs and the last layout.
func fastestLayout(t *testing.T, name string, src []byte) (time.Duration, Layout) {
	t.Helper()

	start := time.Now()
	d := Parse(src, ParseOptions{})

	t.Logf("%s: parse %v", name, time.Since(start))

	if len(src) <= MaxBytes && d.Cut() {
		t.Errorf("%s: a document within MaxBytes was cut", name)
	}

	best := time.Duration(1 << 62)

	var l Layout

	for range 3 {
		start := time.Now()
		l = d.Layout(LayoutOptions{Width: 100, Icons: true})
		took := time.Since(start)

		t.Logf("%s: layout %v (%d lines)", name, took, len(l.Lines))
		best = min(best, took)
	}

	return best, l
}

func TestPathological(t *testing.T) {
	skipTiming(t)

	words := strings.Repeat("lorem ipsum dolor sit amet ", (4<<20)/27)

	var nested strings.Builder
	for range 3000 {
		nested.WriteString("<details open>\n<summary>s</summary>\n\n")
	}

	tests := []struct {
		name     string
		src      string
		minLines int
		contains string // text some line must hold
	}{
		{"open brackets", strings.Repeat("[", 100_000), 1000, ""},
		{"quote markers", strings.Repeat(">", 10_000), 0, ""}, // empty nested quotes draw nothing
		{"quote markers with text", strings.Repeat(">", 10_000) + " deep", 1, "deep"},
		{"list items", strings.Repeat("- a\n", 100_000), 100_000, ""},
		{"one 4 MiB paragraph", words, 40_000, ""},
		{"3000 nested details", nested.String(), 3000, ""},
		{"one cluster across 32000 styled segments", "e" + strings.Repeat("**\u0301**~~\u0302~~", 16_000), 1, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			best, l := fastestLayout(t, tt.name, []byte(tt.src))
			if best >= 200*time.Millisecond {
				t.Errorf("fastest layout took %v, want under 200ms", best)
			}

			if len(l.Lines) < tt.minLines || (tt.minLines == 0 && len(l.Lines) != 0) {
				t.Errorf("layout has %d lines, want at least %d", len(l.Lines), tt.minLines)
			}

			if tt.contains != "" && !slices.ContainsFunc(l.Plain(), func(s string) bool { return strings.Contains(s, tt.contains) }) {
				t.Errorf("no line holds %q", tt.contains)
			}
		})
	}
}

func TestFourMiB(t *testing.T) {
	skipTiming(t)

	var b []byte

	docs := corpusDocs(t)
	docs = docs[:len(docs)-1] // the hostile document leaves a fold unclosed and would swallow the rest

	for i := 0; ; i++ {
		next := append(append([]byte{}, docs[i%len(docs)].src...), '\n', '\n')
		if len(b)+len(next) >= MaxBytes {
			break
		}

		b = append(b, next...)
	}

	t.Logf("document is %d bytes", len(b))

	best, l := fastestLayout(t, "corpus repeated", b)
	if best >= time.Second {
		t.Errorf("fastest layout took %v, want under 1s", best)
	}

	if len(l.Lines) < 100_000 {
		t.Errorf("layout has %d lines, want at least 100000", len(l.Lines))
	}
}
