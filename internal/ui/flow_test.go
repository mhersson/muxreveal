package ui

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest/v2"
)

func TestQuitFlow(t *testing.T) {
	tm := teatest.NewTestModel(t, newTestApp(t), teatest.WithInitialTermSize(100, 30))
	tm.Send(keyLeader)
	tm.Send(keyRune('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}
