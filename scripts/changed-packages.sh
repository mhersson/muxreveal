#!/bin/sh
# Prints the module's Go packages that a change touches, for make check-fast:
# the changes in the working tree and index against HEAD, or the last commit's
# when those are clean. A changed go.mod or go.sum prints ./... (everything);
# files outside every package (docs, CI, the Makefile) print nothing.
set -eu
export LC_ALL=C
cd "$(git rev-parse --show-toplevel)"

files=$(sh "$(dirname "$0")/changed-files.sh")

if printf '%s\n' "$files" | grep -Eqx 'go\.(mod|sum)'; then
	echo ./...
	exit 0
fi

root=$(go list -m -f '{{.Dir}}')
dirs=$(go list -e -f '{{.Dir}}' ./...)

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
