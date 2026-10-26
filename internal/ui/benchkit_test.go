package ui

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/muxreveal/internal/term/rec"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func recs(cols, rows int, chunks ...rec.Record) []rec.Record {
	return append([]rec.Record{{Cols: cols, Rows: rows}}, chunks...)
}

func goroutineStacks() string {
	buf := make([]byte, 1<<20)

	return string(buf[:runtime.Stack(buf, true)])
}

func TestBenchKitOnSynth(t *testing.T) {
	start := time.Now()

	res, err := runBench(benchConfig{
		Panes: 2, Recordings: [][]rec.Record{rec.Synth(40, 16, 5*time.Second, 1)},
		Width: 82, Height: 20, Stagger: ms(500), Length: 3 * time.Second, FPS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	if wall := time.Since(start); wall > 2*time.Second {
		t.Errorf("bench took %v of wall time, want under 2s for 3 virtual seconds", wall)
	}

	if res.Frames.Count == 0 || res.CPUPercent <= 0 || res.BytesFed == 0 {
		t.Fatalf("frames=%d cpu=%v bytes=%d, want all positive", res.Frames.Count, res.CPUPercent, res.BytesFed)
	}

	if res.Frames.Count > res.Steps {
		t.Errorf("frames=%d exceed steps=%d", res.Frames.Count, res.Steps)
	}

	if res.Width != 82 || res.Height != 20 || len(res.PaneSizes) != 2 {
		t.Errorf("result = %dx%d with %d pane sizes", res.Width, res.Height, len(res.PaneSizes))
	}

	// Pane read loops end with the bench's app.
	eventually(t, "pane read loops to end", func() bool { return !strings.Contains(goroutineStacks(), "(*pane).readLoop") })
}

func TestBenchKitNoFrameWithoutBytes(t *testing.T) {
	r := recs(20, 5,
		rec.Record{At: ms(100), Data: []byte("x")},
		rec.Record{At: ms(1000), Data: []byte("y")},
		rec.Record{At: ms(2000), Data: []byte("z")},
	)

	res, err := runBench(benchConfig{
		Panes: 1, Recordings: [][]rec.Record{r}, Width: 40, Height: 10, Length: 2 * time.Second, FPS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.Updates != 2 {
		t.Fatalf("updates=%d, want 2 (idle panes must not be updated)", res.Updates)
	}

	if res.Steps != 120 || res.Frames.Count != 2 {
		t.Fatalf("steps=%d frames=%d, want 120 and 2", res.Steps, res.Frames.Count)
	}
}

func TestBenchKitHoldUsesVirtualClock(t *testing.T) {
	r := recs(20, 5,
		rec.Record{At: ms(100), Data: []byte("\x1b[?2026hA")},
		rec.Record{At: ms(400), Data: []byte("\x1b[?2026l")},
		rec.Record{At: ms(600), Data: []byte("\x1b[?2026hB")},
		rec.Record{At: 3 * time.Second, Data: []byte("\x1b[0m")},
	)

	var draws []time.Duration

	start := time.Now()

	_, err := runBench(benchConfig{
		Panes: 1, Recordings: [][]rec.Record{r}, Width: 40, Height: 10, Length: 3 * time.Second, FPS: 60,
		Trace: func(at time.Duration, drew bool) {
			if drew {
				draws = append(draws, at)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if wall := time.Since(start); wall > time.Second {
		t.Fatalf("bench took %v of wall time; the hold must run on the virtual clock", wall)
	}

	if len(draws) != 2 {
		t.Fatalf("draws at %v, want exactly two", draws)
	}

	if draws[0] <= ms(400) || draws[0] > ms(417) {
		t.Errorf("first draw at %v, want in (400ms, 417ms]", draws[0])
	}

	if draws[1] < ms(1600) || draws[1] > ms(1650) {
		t.Errorf("second draw at %v, want in [1600ms, 1650ms]", draws[1])
	}
}

func TestBenchKitConfigErrors(t *testing.T) {
	short := recs(20, 5, rec.Record{At: ms(500), Data: []byte("x")})

	tests := []struct {
		name string
		cfg  benchConfig
		want string
	}{
		{"no recordings", benchConfig{Panes: 1, Width: 40, Height: 10, Length: ms(100), FPS: 60}, "recording"},
		{"too many panes", benchConfig{Panes: 7, Recordings: [][]rec.Record{short}, Width: 40, Height: 10, Length: ms(100), FPS: 60}, "panes"},
		{"too short", benchConfig{
			Panes: 2, Recordings: [][]rec.Record{short}, Width: 40, Height: 10, Length: ms(400), Stagger: ms(200), FPS: 60,
		}, "pane 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := runBench(tt.cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestSummaryNearestRank(t *testing.T) {
	var f frameTimes

	for i := 100; i >= 1; i-- {
		f.add(time.Duration(i) * time.Millisecond)
	}

	s := f.summary()
	if s.Count != 100 || s.P50 != ms(50) || s.P95 != ms(95) || s.P99 != ms(99) || s.Max != ms(100) {
		t.Fatalf("summary = %+v", s)
	}

	if (&frameTimes{}).summary() != (summary{}) {
		t.Fatal("empty summary is not the zero value")
	}
}

func TestMedianOfRuns(t *testing.T) {
	mk := func(p99 time.Duration, cpu float64) benchResult {
		return benchResult{Frames: summary{P99: p99}, CPUPercent: cpu}
	}

	p99, cpu := median([]benchResult{mk(ms(3), 30), mk(ms(1), 10), mk(ms(2), 20)})
	if p99 != ms(2) || cpu != 20 {
		t.Fatalf("median = %v, %v; want 2ms, 20", p99, cpu)
	}
}
