//go:build bench

package ui

import (
	"testing"
	"time"
)

// TestSearchMissTiming logs the cost of a search that finds nothing over a
// full history. Run it with:
//
//	CGO_ENABLED=1 go test -tags bench -run TestSearchMissTiming -v ./internal/ui/
func TestSearchMissTiming(t *testing.T) {
	p := cappedHistoryPane(t)

	p.withGrid(func(g grid) {
		res := testing.Benchmark(func(b *testing.B) {
			m := copyMode{cursor: point{row: g.Rows() - 1}}

			b.ReportAllocs()

			for range b.N {
				m.search(g, "zzz", false)
			}
		})

		t.Logf("search miss over %d rows: %v/op, %d B/op, %d allocs/op",
			g.Rows(), time.Duration(res.NsPerOp()), res.AllocedBytesPerOp(), res.AllocsPerOp())
	})
}
