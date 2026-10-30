package core

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// PlanKind says where a plan came from.
type PlanKind uint8

// Plan kinds; the zero value is no kind.
const (
	PlanMode PlanKind = iota + 1 // the file ExitPlanMode names
	PlanFile                     // a markdown file under a [plans] paths folder
)

// Plan is an agent's latest plan: a markdown file, not its text.
type Plan struct {
	Path   string // the plan's markdown file; empty for none
	Root   string // for a plan file, the folder its [plans] path sits under; empty for a plan-mode plan
	Kind   PlanKind
	At     time.Time // when the plan arrived or was last rewritten
	Unread bool
}

// Valid reports whether p can be kept: an absolute, clean Path and a known
// Kind, where a plan file has an absolute, clean Root that strictly holds Path
// and a plan-mode plan has none.
func (p Plan) Valid() bool {
	if !cleanAbs(p.Path) {
		return false
	}

	switch p.Kind {
	case PlanMode:
		return p.Root == ""
	case PlanFile:
		if !cleanAbs(p.Root) {
			return false
		}

		rel, err := filepath.Rel(p.Root, p.Path)

		return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}

	return false
}

func cleanAbs(p string) bool { return filepath.IsAbs(p) && filepath.Clean(p) == p }

// NotePlan replaces an agent's latest plan. It changes nothing, and is no
// error, for a pane that is closed or a shell; an invalid Plan is an error.
type NotePlan struct {
	ID   PaneID
	Plan Plan
}

func (a NotePlan) apply(s *State) error {
	p, ok := s.Panes[a.ID]
	if !ok || p.Kind != KindAgent {
		return nil
	}

	if !a.Plan.Valid() {
		return fmt.Errorf("core: NotePlan with an invalid plan %q for pane %q", a.Plan.Path, a.ID)
	}

	p.Plan = a.Plan
	s.Panes[a.ID] = p

	return nil
}

// ReadPlan clears the unread flag of a pane's plan; it changes nothing when
// the pane has none.
type ReadPlan struct {
	ID PaneID
}

func (a ReadPlan) apply(s *State) error {
	p, ok := s.Panes[a.ID]
	if !ok || p.Plan.Path == "" {
		return nil
	}

	p.Plan.Unread = false
	s.Panes[a.ID] = p

	return nil
}
