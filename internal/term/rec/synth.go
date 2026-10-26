package rec

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

const synthInterval = 10 * time.Millisecond

var spinner = []string{"|", "/", "-", "\\"}

// Synth is a deterministic busy stream: after the initial size, every 10 ms a
// frame inside DEC 2026 brackets that homes the cursor, rewrites a spinner row
// and a status row in 256-colour and truecolor SGR with a wide character, and
// appends two scrolling text lines.
func Synth(cols, rows int, d time.Duration, seed uint64) []Record {
	rng := rand.New(rand.NewPCG(seed, 0))
	recs := []Record{{Cols: cols, Rows: rows}}
	width := max(cols-1, 1)

	for i := 0; time.Duration(i+1)*synthInterval <= d; i++ {
		var sb strings.Builder

		sb.WriteString("\x1b[?2026h\x1b[H")
		fmt.Fprintf(&sb, "\x1b[38;5;%dm%s working %d\x1b[0m\x1b[K\r\n", 16+rng.IntN(216), spinner[i%len(spinner)], i)
		fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[1m中 status %d%%\x1b[0m\x1b[K", rng.IntN(256), rng.IntN(256), rng.IntN(256), i%101)
		fmt.Fprintf(&sb, "\x1b[%d;1H", rows)

		for range 2 {
			sb.WriteString("\r\n")
			sb.WriteString(textLine(rng, width))
		}

		sb.WriteString("\x1b[?2026l")

		recs = append(recs, Record{At: time.Duration(i) * synthInterval, Data: []byte(sb.String())})
	}

	return recs
}

func textLine(rng *rand.Rand, width int) string {
	n := min(width, 20+rng.IntN(40))
	b := make([]byte, n)

	for i := range b {
		b[i] = byte('a' + rng.IntN(26))
		if rng.IntN(6) == 0 {
			b[i] = ' '
		}
	}

	return string(b)
}
