package core

import "testing"

func TestStoreSetTheme(t *testing.T) {
	tests := []struct {
		name      string
		action    SetTheme
		wantErr   bool
		wantTheme string
	}{
		{"valid name", SetTheme{Name: "catppuccin-latte"}, false, "catppuccin-latte"},
		{"empty name", SetTheme{}, true, "catppuccin-mocha"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStore(State{Theme: "catppuccin-mocha"})
			err := s.Dispatch(tt.action)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Dispatch() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := s.State().Theme; got != tt.wantTheme {
				t.Errorf("State().Theme = %q, want %q", got, tt.wantTheme)
			}
		})
	}
}
