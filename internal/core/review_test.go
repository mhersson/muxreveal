package core

import (
	"errors"
	"reflect"
	"testing"
)

func reviewState(t *testing.T) State {
	t.Helper()

	// tab 0: p1, p2; tab 1: p3, p4
	return buildTabs(t, []LayoutMode{ModeBSP, ModeBSP}, []int{2, 2})
}

func TestReviewActions(t *testing.T) {
	s := reviewState(t)

	s = dispatch(t, s, OpenReview{Pane: "p1", Mode: ReviewTurn})
	if got, want := s.Tabs[0].Review, (&Review{Pane: "p1", Mode: ReviewTurn}); !reflect.DeepEqual(got, want) {
		t.Fatalf("after open: Review = %+v, want %+v", got, want)
	}

	s = dispatch(t, s, OpenReview{Pane: "p2", Mode: ReviewBranch})
	if got, want := s.Tabs[0].Review, (&Review{Pane: "p2", Mode: ReviewBranch}); !reflect.DeepEqual(got, want) {
		t.Fatalf("after retarget: Review = %+v, want %+v", got, want)
	}

	s = dispatch(t, s, SetReviewMode{Mode: ReviewTurn})
	if got, want := s.Tabs[0].Review, (&Review{Pane: "p2", Mode: ReviewTurn}); !reflect.DeepEqual(got, want) {
		t.Fatalf("after mode: Review = %+v, want %+v", got, want)
	}

	if s.Tabs[1].Review != nil {
		t.Errorf("other tab got a drawer: %+v", s.Tabs[1].Review)
	}

	s = dispatch(t, s, CloseReview{})
	if s.Tabs[0].Review != nil {
		t.Errorf("after close: Review = %+v, want nil", s.Tabs[0].Review)
	}

	// Closing with no drawer is not an error.
	dispatch(t, s, CloseReview{})
}

func TestReviewActionErrors(t *testing.T) {
	s := reviewState(t)

	tests := []struct {
		name   string
		action Action
	}{
		{"pane in another tab", OpenReview{Pane: "p3", Mode: ReviewTurn}},
		{"unknown pane", OpenReview{Pane: "p9", Mode: ReviewTurn}},
		{"unknown mode", OpenReview{Pane: "p1", Mode: "x"}},
		{"mode without a drawer", SetReviewMode{Mode: ReviewBranch}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := moveErr(s, tt.action); err == nil {
				t.Fatal("Dispatch() error = nil")
			}
		})
	}

	open := dispatch(t, s, OpenReview{Pane: "p1", Mode: ReviewTurn})
	if err := moveErr(open, SetReviewMode{Mode: "x"}); err == nil {
		t.Error("SetReviewMode with an unknown mode: error = nil")
	}
}

func TestSetReviewTree(t *testing.T) {
	s := dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn})
	if s.Tabs[0].Review.Tree {
		t.Fatal("a new drawer starts with the tree open")
	}

	s = dispatch(t, s, SetReviewTree{Open: true})
	if got, want := s.Tabs[0].Review, (&Review{Pane: "p1", Mode: ReviewTurn, Tree: true}); !reflect.DeepEqual(got, want) {
		t.Fatalf("after open: Review = %+v, want %+v", got, want)
	}

	s = dispatch(t, s, SetReviewTree{Open: true})
	if !s.Tabs[0].Review.Tree {
		t.Error("opening an open tree closed it")
	}

	s = dispatch(t, s, SetReviewTree{Open: false})
	if got, want := s.Tabs[0].Review, (&Review{Pane: "p1", Mode: ReviewTurn}); !reflect.DeepEqual(got, want) {
		t.Fatalf("after close: Review = %+v, want %+v", got, want)
	}
}

func TestSetReviewTreeWithoutDrawer(t *testing.T) {
	s := reviewState(t)

	for _, open := range []bool{true, false} {
		if err := moveErr(s, SetReviewTree{Open: open}); !errors.Is(err, errNoReview) {
			t.Errorf("SetReviewTree{Open: %t} without a drawer: error = %v, want errNoReview", open, err)
		}
	}

	// A drawer in another tab does not count.
	s = dispatch(t, s, OpenReview{Pane: "p1", Mode: ReviewTurn})

	s = dispatch(t, s, SelectTab{Index: 1})
	if err := moveErr(s, SetReviewTree{Open: true}); !errors.Is(err, errNoReview) {
		t.Errorf("SetReviewTree on a tab without a drawer: error = %v, want errNoReview", err)
	}
}

func TestSetReviewTreeIsPerTab(t *testing.T) {
	s := reviewState(t)
	s = dispatch(t, s, OpenReview{Pane: "p1", Mode: ReviewTurn})
	s = dispatch(t, s, SelectTab{Index: 1})
	s = dispatch(t, s, OpenReview{Pane: "p3", Mode: ReviewBranch})
	s = dispatch(t, s, SetReviewTree{Open: true})

	if !s.Tabs[1].Review.Tree {
		t.Fatalf("active tab's tree is closed: %+v", s.Tabs[1].Review)
	}

	if got, want := s.Tabs[0].Review, (&Review{Pane: "p1", Mode: ReviewTurn}); !reflect.DeepEqual(got, want) {
		t.Errorf("other tab's drawer = %+v, want %+v", got, want)
	}

	s = dispatch(t, s, SelectTab{Index: 0})
	s = dispatch(t, s, SetReviewTree{Open: true})
	s = dispatch(t, s, SelectTab{Index: 1})
	s = dispatch(t, s, SetReviewTree{Open: false})

	if got, want := s.Tabs[0].Review, (&Review{Pane: "p1", Mode: ReviewTurn, Tree: true}); !reflect.DeepEqual(got, want) {
		t.Errorf("closing the other tab's tree changed this one: %+v, want %+v", got, want)
	}

	if s.Tabs[1].Review.Tree {
		t.Errorf("tab 1's tree is still open: %+v", s.Tabs[1].Review)
	}
}

func TestOpenReviewClosesTheTree(t *testing.T) {
	tests := []struct {
		name string
		pane PaneID
		mode ReviewMode
	}{
		{"another pane", "p2", ReviewTurn},
		{"the same pane", "p1", ReviewTurn},
		{"another mode", "p1", ReviewBranch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn})
			s = dispatch(t, s, SetReviewTree{Open: true})
			s = dispatch(t, s, OpenReview{Pane: tt.pane, Mode: tt.mode})

			if got, want := s.Tabs[0].Review, (&Review{Pane: tt.pane, Mode: tt.mode}); !reflect.DeepEqual(got, want) {
				t.Errorf("Review = %+v, want %+v", got, want)
			}
		})
	}
}

func TestSetReviewModeKeepsTheTree(t *testing.T) {
	s := dispatch(t, reviewState(t), OpenReview{Pane: "p2", Mode: ReviewTurn})
	s = dispatch(t, s, SetReviewTree{Open: true})
	s = dispatch(t, s, SetReviewMode{Mode: ReviewBranch})

	if got, want := s.Tabs[0].Review, (&Review{Pane: "p2", Mode: ReviewBranch, Tree: true}); !reflect.DeepEqual(got, want) {
		t.Errorf("Review = %+v, want %+v", got, want)
	}
}

func TestReviewClosePane(t *testing.T) {
	s := dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn})

	if got := dispatch(t, s, ClosePane{ID: "p2"}).Tabs[0].Review; got == nil || got.Pane != "p1" {
		t.Errorf("closing another pane: Review = %+v, want kept", got)
	}

	if got := dispatch(t, s, ClosePane{ID: "p1"}).Tabs[0].Review; got != nil {
		t.Errorf("closing the drawer's pane: Review = %+v, want nil", got)
	}
}

func TestReviewMovePane(t *testing.T) {
	tests := []struct {
		name   string
		action Action
	}{
		{"to another tab", MovePane{ID: "p1", Tab: 1, Area: modeInner}},
		{"break into a new tab", BreakPane{ID: "p1", Area: modeInner}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn})

			got := dispatch(t, s, tt.action)
			if got.Tabs[0].Review != nil {
				t.Errorf("source tab Review = %+v, want nil", got.Tabs[0].Review)
			}
		})
	}

	t.Run("another pane keeps it", func(t *testing.T) {
		s := dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn})

		got := dispatch(t, s, MovePane{ID: "p2", Tab: 1, Area: modeInner})
		if got.Tabs[0].Review == nil {
			t.Error("source tab lost its drawer")
		}
	})
}

func TestTabCloneCopiesReview(t *testing.T) {
	tab := Tab{Review: &Review{Pane: "p1", Mode: ReviewTurn}}
	c := tab.clone()
	c.Review.Mode = ReviewBranch

	if tab.Review.Mode != ReviewTurn {
		t.Error("clone shares Review with the original")
	}
}

func TestStateCloneCopiesReview(t *testing.T) {
	s := reviewState(t)
	s = dispatch(t, s, OpenReview{Pane: "p1", Mode: ReviewTurn})

	c := s.clone()
	c.Tabs[0].Review.Mode = ReviewBranch

	if s.Tabs[0].Review.Mode != ReviewTurn {
		t.Error("State.clone shares Review")
	}
}

// A layout switch rebuilds the tab but keeps its drawer.
func TestLayoutSwitchKeepsTheReviewDrawer(t *testing.T) {
	want := &Review{Pane: "p2", Mode: ReviewBranch}

	for _, from := range []LayoutMode{ModeBSP, ModeMaster, ModeScrolling} {
		for _, to := range []LayoutMode{ModeBSP, ModeMaster, ModeScrolling} {
			t.Run(string(from)+" to "+string(to), func(t *testing.T) {
				s := buildTabs(t, []LayoutMode{from, ModeBSP}, []int{2, 2})
				s = dispatch(t, s, OpenReview{Pane: "p2", Mode: ReviewBranch})
				s = dispatch(t, s, SetLayoutMode{Mode: to})

				if got := s.Tabs[0].Review; !reflect.DeepEqual(got, want) {
					t.Errorf("after the switch Review = %+v, want %+v", got, want)
				}

				if s.Tabs[1].Review != nil {
					t.Errorf("the other tab got a drawer: %+v", s.Tabs[1].Review)
				}
			})
		}
	}

	t.Run("cycle", func(t *testing.T) {
		s := reviewState(t)
		s = dispatch(t, s, OpenReview{Pane: "p2", Mode: ReviewBranch})

		for range 3 {
			s = dispatch(t, s, CycleLayoutMode{})

			if got := s.Tabs[0].Review; !reflect.DeepEqual(got, want) {
				t.Fatalf("after cycling to %q Review = %+v, want %+v", s.Tabs[0].Mode, got, want)
			}
		}
	})
}
