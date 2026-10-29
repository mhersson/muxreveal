//go:build bench

package md

import (
	"testing"
	"time"
)

const (
	pathologicalBudget = 200 * time.Millisecond
	fourMiBBudget      = time.Second
)

// fastestLayout parses src outside the timed region and returns the fastest of
// three Layout runs.
func fastestLayout(t *testing.T, name string, src []byte) time.Duration {
	t.Helper()

	if raceEnabled {
		t.Skip("timings are meaningless under the race detector")
	}

	start := time.Now()
	d := parseUncut(t, src)

	t.Logf("%s: parse %v", name, time.Since(start))

	best := time.Duration(1 << 62)

	for range 3 {
		start := time.Now()
		l := layoutPathological(d)
		took := time.Since(start)

		t.Logf("%s: layout %v (%d lines)", name, took, len(l.Lines))
		best = min(best, took)
	}

	return best
}

func TestPathologicalBudget(t *testing.T) {
	for _, tt := range pathologicalInputs() {
		t.Run(tt.name, func(t *testing.T) {
			if best := fastestLayout(t, tt.name, []byte(tt.src)); best >= pathologicalBudget {
				t.Errorf("fastest layout took %v, want under %v", best, pathologicalBudget)
			}
		})
	}
}

func TestFourMiBBudget(t *testing.T) {
	b := fourMiBCorpus(t)

	t.Logf("document is %d bytes", len(b))

	if best := fastestLayout(t, "corpus repeated", b); best >= fourMiBBudget {
		t.Errorf("fastest layout took %v, want under %v", best, fourMiBBudget)
	}
}
