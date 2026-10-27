package linediff

import (
	"slices"
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"trailing newline", "a\nb\n", []string{"a", "b"}},
		{"no trailing newline", "a\nb", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Split(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("Split(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLCS(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want []Op
	}{
		{"both empty", nil, nil, []Op{}},
		{
			"one line changed",
			[]string{"a", "b", "c"}, []string{"a", "x", "c"},
			[]Op{{' ', "a"}, {'-', "b"}, {'+', "x"}, {' ', "c"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LCS(tt.a, tt.b); !slices.Equal(got, tt.want) {
				t.Errorf("LCS(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestReplace(t *testing.T) {
	got := Replace([]string{"a"}, []string{"b"})
	want := []Op{{'-', "a"}, {'+', "b"}}
	if !slices.Equal(got, want) {
		t.Errorf("Replace = %v, want %v", got, want)
	}
}
