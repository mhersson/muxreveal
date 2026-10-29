#!/bin/sh
# Prints the module's Go packages that a change touches, for make check-fast:
# the changes in the working tree and index against HEAD, or the last commit's
# when those are clean. A changed go.mod or go.sum prints ./... (everything);
# files outside every package (docs, CI, the Makefile) print nothing.
set -eu
export LC_ALL=C

if [ -n "$(git status --porcelain --untracked-files=all)" ]; then
	files=$(git diff --name-only --no-renames HEAD; git ls-files --others --exclude-standard)
else
	files=$(git diff --name-only --no-renames HEAD~1 HEAD 2>/dev/null || :)
fi

if printf '%s\n' "$files" | grep -Eqx 'go\.(mod|sum)'; then
	echo ./...
	exit 0
fi

root=$(go list -m -f '{{.Dir}}')
dirs=$(go list -f '{{.Dir}}' ./...)

# A file belongs to the nearest package directory above it, so testdata files
# count for the package that reads them.
printf '%s\n' "$files" | while IFS= read -r f; do
	[ -n "$f" ] || continue
	d=$(dirname "$f")
	while [ "$d" != . ]; do
		if printf '%s\n' "$dirs" | grep -Fqx "$root/$d"; then
			echo "./$d"
			break
		fi
		d=$(dirname "$d")
	done
done | sort -u | tr '\n' ' '
