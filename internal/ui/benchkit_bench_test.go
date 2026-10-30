//go:build bench

package ui

import (
	"testing"
	"time"
)

// The synth bench of three virtual seconds must take under two seconds of wall
// time. Run it with:
//
//	CGO_ENABLED=1 go test -tags bench ./internal/ui -run TestBenchKitOnSynthWallTime -v
func TestBenchKitOnSynthWallTime(t *testing.T) {
	start := time.Now()

	if _, err := runBench(synthBenchConfig()); err != nil {
		t.Fatal(err)
	}

	if wall := time.Since(start); wall > 2*time.Second {
		t.Errorf("bench took %v of wall time, want under 2s for 3 virtual seconds", wall)
	}
}

// A synchronized-output hold must run on the virtual clock: three virtual
// seconds take under one second of wall time. Run it with:
//
//	CGO_ENABLED=1 go test -tags bench ./internal/ui -run TestBenchKitHoldWallTime -v
func TestBenchKitHoldWallTime(t *testing.T) {
	start := time.Now()

	if _, err := runBench(holdBenchConfig(nil)); err != nil {
		t.Fatal(err)
	}

	if wall := time.Since(start); wall > time.Second {
		t.Errorf("bench took %v of wall time; the hold must run on the virtual clock", wall)
	}
}
