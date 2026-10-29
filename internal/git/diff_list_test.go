package git

import (
	"reflect"
	"strings"
	"testing"
)

const (
	oidA = "e8823e1766638e70fd9e260913a383f8fe68a237"
	oidB = "56fa4bce51a08ea5cba246881f22487725848262"
	oid0 = "0000000000000000000000000000000000000000"
)

func raw(om, nm, oo, no, status string, paths ...string) string {
	return ":" + om + " " + nm + " " + oo + " " + no + " " + status + "\x00" + strings.Join(paths, "\x00") + "\x00"
}

func TestParseListing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []FileDiff
	}{
		{name: "empty"},
		{
			name: "modified",
			in:   raw("100644", "100644", oidA, oidB, "M", "a.txt") + "3\t1\ta.txt\x00",
			want: []FileDiff{{Path: "a.txt", Status: Modified, OldOID: oidA, NewOID: oidB, OldMode: "100644", NewMode: "100644", Added: 3, Removed: 1}},
		},
		{
			name: "added and deleted",
			in: raw("000000", "100644", oid0, oidB, "A", "n.txt") + raw("100755", "000000", oidA, oid0, "D", "d.sh") +
				"2\t0\tn.txt\x00" + "0\t4\td.sh\x00",
			want: []FileDiff{
				{Path: "n.txt", Status: Added, NewOID: oidB, NewMode: "100644", Added: 2},
				{Path: "d.sh", Status: Deleted, OldOID: oidA, OldMode: "100755", Removed: 4},
			},
		},
		{
			name: "rename with an empty path field",
			in:   raw("100644", "100644", oidA, oidB, "R087", "old name", "new\nname") + "1\t2\t\x00old name\x00new\nname\x00",
			want: []FileDiff{{Path: "new\nname", OldPath: "old name", Status: Renamed, OldOID: oidA, NewOID: oidB, OldMode: "100644", NewMode: "100644", Added: 1, Removed: 2}},
		},
		{
			name: "copy next to a modified file",
			in: raw("100644", "100644", oidA, oidB, "M", "a") + raw("100644", "100644", oidA, oidA, "C100", "a", "b") +
				"1\t1\ta\x00" + "0\t0\t\x00a\x00b\x00",
			want: []FileDiff{
				{Path: "a", Status: Modified, OldOID: oidA, NewOID: oidB, OldMode: "100644", NewMode: "100644", Added: 1, Removed: 1},
				{Path: "b", OldPath: "a", Status: Copied, OldOID: oidA, NewOID: oidA, OldMode: "100644", NewMode: "100644"},
			},
		},
		{
			name: "mode only",
			in:   raw("100644", "100755", oidA, oidA, "M", "run.sh") + "0\t0\trun.sh\x00",
			want: []FileDiff{{Path: "run.sh", Status: ModeChanged, OldOID: oidA, NewOID: oidA, OldMode: "100644", NewMode: "100755"}},
		},
		{
			name: "binary and type change",
			in: raw("100644", "100644", oidA, oidB, "M", "img") + raw("120000", "100644", oidA, oidB, "T", "link") +
				"-\t-\timg\x00" + "1\t1\tlink\x00",
			want: []FileDiff{
				{Path: "img", Status: Modified, OldOID: oidA, NewOID: oidB, OldMode: "100644", NewMode: "100644", Binary: true},
				{Path: "link", Status: Modified, OldOID: oidA, NewOID: oidB, OldMode: "120000", NewMode: "100644", Added: 1, Removed: 1},
			},
		},
		{
			name: "a path that looks like a record",
			in:   raw("000000", "100644", oid0, oidB, "A", ":100644 x") + "1\t0\t:100644 x\x00",
			want: []FileDiff{{Path: ":100644 x", Status: Added, NewOID: oidB, NewMode: "100644", Added: 1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseListing([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestParseListingRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	ok := raw("100644", "100644", oidA, oidB, "M", "a") + "1\t1\ta\x00"
	rename := raw("100644", "100644", oidA, oidB, "R090", "o", "n") + "1\t1\t\x00o\x00n\x00"

	bad := map[string]string{
		"short raw":                 ":100644 100644 " + oidA + "\x00a\x00",
		"missing path":              raw("100644", "100644", oidA, oidB, "M") + "1\t1\ta\x00",
		"missing counts":            raw("100644", "100644", oidA, oidB, "M", "a"),
		"counts for another path":   raw("100644", "100644", oidA, oidB, "M", "a") + "1\t1\tb\x00",
		"non-numeric count":         raw("100644", "100644", oidA, oidB, "M", "a") + "x\t1\ta\x00",
		"count record without tabs": raw("100644", "100644", oidA, oidB, "M", "a") + "11a\x00",
		"trailing record":           ok + "1\t1\tz\x00",
		"rename paths swapped":      raw("100644", "100644", oidA, oidB, "R090", "o", "n") + "1\t1\t\x00n\x00o\x00",
		"rename counts cut short":   raw("100644", "100644", oidA, oidB, "R090", "o", "n") + "1\t1\t\x00o\x00",
	}

	for name, in := range bad {
		t.Run(name, func(t *testing.T) {
			if got, err := parseListing([]byte(in)); err == nil {
				t.Errorf("accepted %q: %+v", in, got)
			}
		})
	}

	// No cut of a valid listing may panic.
	for _, full := range []string{ok, rename} {
		for n := range len(full) {
			_, _ = parseListing([]byte(full[:n]))
		}
	}
}
