package core

import "maps"

// RepoMarks holds a repository's reviewed-hunk marks: hunk id to the UTC day
// (2006-01-02) it was last set or shown. Rev changes with every change, so
// views rebuild on it. Copies of the state share Days, so actions build a new
// map and never edit one in place.
type RepoMarks struct {
	Days map[string]string
	Rev  uint64
}

// Reviewed reports whether the hunk id is marked in the repository.
func (s State) Reviewed(repo, id string) bool {
	_, ok := s.Marks[repo].Days[id]

	return ok
}

// replaceMarks stores days as repo's marks with the next Rev.
func (s *State) replaceMarks(repo string, days map[string]string) {
	marks := make(map[string]RepoMarks, len(s.Marks)+1)
	maps.Copy(marks, s.Marks)
	marks[repo] = RepoMarks{Days: days, Rev: s.Marks[repo].Rev + 1}
	s.Marks = marks
}

// SetMarks replaces a repository's marks, as loaded from its file. The first
// SetMarks for a repository always creates its entry: that records the marks
// as loaded.
type SetMarks struct {
	Repo string
	Days map[string]string
}

func (a SetMarks) apply(s *State) error {
	if cur, ok := s.Marks[a.Repo]; ok && maps.Equal(cur.Days, a.Days) {
		return nil
	}

	days := maps.Clone(a.Days)
	if days == nil {
		days = map[string]string{}
	}

	s.replaceMarks(a.Repo, days)

	return nil
}

// MarkHunks marks the hunk ids reviewed on Day, or unmarks them.
type MarkHunks struct {
	Repo     string
	IDs      []string
	Reviewed bool
	Day      string
}

func (a MarkHunks) apply(s *State) error {
	cur := s.Marks[a.Repo].Days
	days := maps.Clone(cur)

	if days == nil {
		days = map[string]string{}
	}

	for _, id := range a.IDs {
		if a.Reviewed {
			days[id] = a.Day
		} else {
			delete(days, id)
		}
	}

	if maps.Equal(days, cur) {
		return nil
	}

	s.replaceMarks(a.Repo, days)

	return nil
}

// TouchMarks sets Day on the ids that are already marked with another day.
type TouchMarks struct {
	Repo string
	IDs  []string
	Day  string
}

func (a TouchMarks) apply(s *State) error {
	cur := s.Marks[a.Repo].Days

	var days map[string]string

	for _, id := range a.IDs {
		if d, ok := cur[id]; ok && d != a.Day {
			if days == nil {
				days = maps.Clone(cur)
			}

			days[id] = a.Day
		}
	}

	if days == nil {
		return nil
	}

	s.replaceMarks(a.Repo, days)

	return nil
}
