#!/bin/sh
# Prints the files a change touches, one root-relative path per line, for the
# scripts behind make check-fast: the changes in the working tree and index
# against HEAD, or the last commit's when those are clean.
set -eu
export LC_ALL=C
cd "$(git rev-parse --show-toplevel)"

# Unquoted, root-relative paths, so non-ASCII names and untracked files map too.
g() { git -c core.quotePath=false "$@"; }
if [ -n "$(g status --porcelain --untracked-files=all)" ]; then
	g diff --name-only --no-renames HEAD
	g ls-files --full-name --others --exclude-standard
else
	g diff --name-only --no-renames HEAD~1 HEAD 2>/dev/null || :
fi
