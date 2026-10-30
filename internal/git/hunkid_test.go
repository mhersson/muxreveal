package git

import "testing"

func TestHunkID(t *testing.T) {
	t.Parallel()

	base := func() Hunk {
		return Hunk{
			OldStart: 10, OldLines: 3, NewStart: 10, NewLines: 3,
			Section: "func a()",
			Lines: []Line{
				{Kind: Context, Old: 10, New: 10, Text: "ctx"},
				{Kind: Del, Old: 11, Text: "old"},
				{Kind: Add, New: 11, Text: "new"},
				{Kind: Context, Old: 12, New: 12, Text: "tail"},
			},
		}
	}

	tests := []struct {
		name   string
		path   string
		edit   func(*Hunk)
		wantEq bool
	}{
		{"same", "a.go", func(*Hunk) {}, true},
		{"other line numbers", "a.go", func(h *Hunk) {
			h.OldStart, h.NewStart = 99, 120
			for i := range h.Lines {
				if h.Lines[i].Old != 0 {
					h.Lines[i].Old += 89
				}

				if h.Lines[i].New != 0 {
					h.Lines[i].New += 110
				}
			}
		}, true},
		{"other context", "a.go", func(h *Hunk) { h.Lines[0].Text = "different"; h.Lines[3].Text = "x" }, true},
		{"other section", "a.go", func(h *Hunk) { h.Section = "func b()" }, true},
		{"added line changed", "a.go", func(h *Hunk) { h.Lines[2].Text = "newer" }, false},
		{"kind flipped", "a.go", func(h *Hunk) { h.Lines[1].Kind = Add }, false},
		{"other path", "b.go", func(*Hunk) {}, false},
		{"no newline", "a.go", func(h *Hunk) { h.Lines[2].NoNewline = true }, false},
	}

	want := HunkID("a.go", base())
	if len(want) != 32 {
		t.Fatalf("id %q is not 32 hex characters", want)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := base()
			tt.edit(&h)

			got := HunkID(tt.path, h)
			if (got == want) != tt.wantEq {
				t.Errorf("HunkID equal to base = %v, want %v", got == want, tt.wantEq)
			}
		})
	}
}
