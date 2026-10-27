package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// listLimit bounds the listing's output; it holds no file content.
const listLimit = 64 << 20

// listFiles runs the listing pass: status, modes, blob ids and line counts of
// every changed file between two trees, limited to paths (and what lies under
// them) when any are given.
func listFiles(ctx context.Context, r Repo, oldTree, newTree string, paths ...string) ([]FileDiff, error) {
	args := []string{
		"diff", "--raw", "--numstat", "-z", "-M", "-C", "--histogram", "--no-abbrev",
		"--no-color", "--no-ext-diff", "--no-textconv", "--no-relative", "--submodule=short", "--ignore-submodules=none", oldTree, newTree,
	}

	if len(paths) > 0 {
		args = append(args, "--")
		for _, p := range paths {
			args = append(args, ":(literal)"+p)
		}
	}

	out, err := run(ctx, call{
		dir:   r.Root,
		args:  args,
		read:  true,
		limit: listLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("list changed files: %w", err)
	}

	files, err := parseListing(out)
	if err != nil {
		return nil, fmt.Errorf("list changed files: %w", err)
	}

	return files, nil
}

// parseListing parses `git diff --raw --numstat -z`: every raw record
// (":om nm oo no STATUS", then one path, or two for a rename or copy), then
// every numstat record ("add\tdel\tpath", or "add\tdel\t" followed by the old
// and new path for a rename or copy), in the same order.
func parseListing(out []byte) ([]FileDiff, error) {
	toks := strings.Split(string(out), "\x00")
	if n := len(toks); n > 0 && toks[n-1] == "" {
		toks = toks[:n-1]
	}

	var (
		files []FileDiff
		i     int
	)

	for i < len(toks) && strings.HasPrefix(toks[i], ":") {
		f, paths, err := parseRaw(toks[i])
		if err != nil {
			return nil, err
		}

		if i+paths >= len(toks) {
			return nil, fmt.Errorf("truncated raw record %q", toks[i])
		}

		if paths == 2 {
			f.OldPath, f.Path = toks[i+1], toks[i+2]
		} else {
			f.Path = toks[i+1]
		}

		files = append(files, f)
		i += 1 + paths
	}

	for k := range files {
		f := &files[k]

		if i >= len(toks) {
			return nil, fmt.Errorf("missing line counts for %q", f.Path)
		}

		fields := strings.SplitN(toks[i], "\t", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("malformed line count record %q", toks[i])
		}

		i++

		oldPath, newPath := fields[2], fields[2]

		if fields[2] == "" {
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("truncated line count record for %q", f.Path)
			}

			oldPath, newPath = toks[i], toks[i+1]
			i += 2
		}

		if newPath != f.Path || (f.OldPath != "" && oldPath != f.OldPath) {
			return nil, fmt.Errorf("line counts for %q do not match %q", newPath, f.Path)
		}

		if fields[0] == "-" && fields[1] == "-" {
			f.Binary = true

			continue
		}

		var err error
		if f.Added, err = strconv.Atoi(fields[0]); err != nil {
			return nil, fmt.Errorf("line count %q: %w", fields[0], err)
		}

		if f.Removed, err = strconv.Atoi(fields[1]); err != nil {
			return nil, fmt.Errorf("line count %q: %w", fields[1], err)
		}
	}

	if i != len(toks) {
		return nil, fmt.Errorf("unexpected record %q after the listing", toks[i])
	}

	return files, nil
}

// parseRaw parses one ":om nm oo no STATUS" record and returns the file with
// the number of paths that follow it.
func parseRaw(rec string) (FileDiff, int, error) {
	fields := strings.Fields(rec)
	if len(fields) != 5 || fields[4] == "" {
		return FileDiff{}, 0, fmt.Errorf("malformed raw record %q", rec)
	}

	f := FileDiff{
		OldMode: modeOrEmpty(strings.TrimPrefix(fields[0], ":")),
		NewMode: modeOrEmpty(fields[1]),
		OldOID:  oidOrEmpty(fields[2]),
		NewOID:  oidOrEmpty(fields[3]),
	}
	paths := 1

	switch fields[4][0] {
	case 'A':
		f.Status = Added
	case 'D':
		f.Status = Deleted
	case 'R':
		f.Status, paths = Renamed, 2
	case 'C':
		f.Status, paths = Copied, 2
	default:
		f.Status = Modified
		if f.OldOID == f.NewOID && f.OldMode != f.NewMode {
			f.Status = ModeChanged
		}
	}

	return f, paths, nil
}

func modeOrEmpty(m string) string {
	if m == "000000" {
		return ""
	}

	return m
}

// oidOrEmpty maps the all-zero id git prints for a missing side to "".
func oidOrEmpty(oid string) string {
	if strings.Trim(oid, "0") == "" {
		return ""
	}

	return oid
}
