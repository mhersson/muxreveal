package core

import (
	"maps"
	"testing"
)

func TestMarkActions(t *testing.T) {
	t.Parallel()

	const repo = "/r/.git"

	seed := func(days map[string]string) *Store {
		st := NewStore(InitialState(DefaultConfig()))
		if days != nil {
			if err := st.Dispatch(SetMarks{Repo: repo, Days: days}); err != nil {
				panic(err)
			}
		}

		return st
	}

	tests := []struct {
		name     string
		seed     map[string]string
		act      Action
		want     map[string]string
		wantRev  uint64
		reviewed map[string]bool
	}{
		{"set creates the entry even when empty", nil, SetMarks{Repo: repo, Days: map[string]string{}}, map[string]string{}, 1, nil},
		{"set replaces", map[string]string{"a": "d1"}, SetMarks{Repo: repo, Days: map[string]string{"b": "d2"}}, map[string]string{"b": "d2"}, 2, map[string]bool{"a": false, "b": true}},
		{"set same changes nothing", map[string]string{"a": "d1"}, SetMarks{Repo: repo, Days: map[string]string{"a": "d1"}}, map[string]string{"a": "d1"}, 1, nil},
		{"mark on", map[string]string{"a": "d1"}, MarkHunks{Repo: repo, IDs: []string{"b", "c"}, Reviewed: true, Day: "d2"}, map[string]string{"a": "d1", "b": "d2", "c": "d2"}, 2, nil},
		{"mark on again with the same day", map[string]string{"a": "d1"}, MarkHunks{Repo: repo, IDs: []string{"a"}, Reviewed: true, Day: "d1"}, map[string]string{"a": "d1"}, 1, nil},
		{"mark off", map[string]string{"a": "d1", "b": "d1"}, MarkHunks{Repo: repo, IDs: []string{"a", "zz"}}, map[string]string{"b": "d1"}, 2, map[string]bool{"a": false}},
		{"mark off nothing", map[string]string{"a": "d1"}, MarkHunks{Repo: repo, IDs: []string{"zz"}}, map[string]string{"a": "d1"}, 1, nil},
		{"touch only existing", map[string]string{"a": "d1"}, TouchMarks{Repo: repo, IDs: []string{"a", "n"}, Day: "d2"}, map[string]string{"a": "d2"}, 2, nil},
		{"touch same day", map[string]string{"a": "d2"}, TouchMarks{Repo: repo, IDs: []string{"a", "n"}, Day: "d2"}, map[string]string{"a": "d2"}, 1, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := seed(tt.seed)
			before := st.State()
			beforeDays := maps.Clone(before.Marks[repo].Days)

			if err := st.Dispatch(tt.act); err != nil {
				t.Fatal(err)
			}

			got := st.State().Marks[repo]
			if got.Rev != tt.wantRev || !maps.Equal(got.Days, tt.want) {
				t.Errorf("marks = %+v, want days %v rev %d", got, tt.want, tt.wantRev)
			}

			if !maps.Equal(before.Marks[repo].Days, beforeDays) {
				t.Errorf("an earlier state changed: %v, was %v", before.Marks[repo].Days, beforeDays)
			}

			for id, want := range tt.reviewed {
				if r := st.State().Reviewed(repo, id); r != want {
					t.Errorf("Reviewed(%s) = %v, want %v", id, r, want)
				}
			}
		})
	}
}
