#!/bin/sh
# Fails on what must never reach git in the commits between a base and a head:
# milestone, plan, task, ruling or requirement IDs and attribution trailers in
# the messages, IDs in added Go comments, em-dashes in the messages or added
# lines, and blobs over 500 KB (recordings belong outside the repository).
# Usage: scan-commits.sh [base [head]]; base defaults to origin/main, head to
# HEAD. Word boundaries are spelled out: \b is a GNU extension to POSIX ERE.
set -u
export LC_ALL=C

base=${1:-origin/main}
head=${2:-HEAD}
b='(^|[^A-Za-z0-9])'
e='([^A-Za-z0-9]|$)'
ids="\\(([A-Z]{2}-[0-9]+(, )?)+\\)|$b(AG|PR|DF|WT|GH|GA|MD|UX|TH)-[0-9]|${b}M[0-9][a-f]?$e|${b}(Task|Part) [0-9]|${b}[RD][0-9]+$e|Review Focus|Claude-Session|Co-Authored-By|Generated with"
dash=$(printf '\342\200\224')

# Prove the patterns match before an empty result is trusted.
printf 'x (AG-5) Task 3 M2b R14\n' | grep -Eq "$ids" || { echo "scan-commits: grep cannot match the ID pattern"; exit 1; }
printf 'a\342\200\224b\n' | grep -qF "$dash" || { echo "scan-commits: grep cannot match an em-dash"; exit 1; }

git rev-parse --verify --quiet "$base^{commit}" >/dev/null || { echo "scan-commits: no commit $base"; exit 1; }
git rev-parse --verify --quiet "$head^{commit}" >/dev/null || { echo "scan-commits: no commit $head"; exit 1; }

fail=0
report() {
	echo "scan-commits: $1:"
	printf '%s\n' "$2"
	fail=1
}

msgs=$(git log --no-merges --format='%h %s%n%b' "$base..$head")
added=$(git diff "$base...$head" | grep '^+' | grep -v '^+++') || :

hits=$(printf '%s\n' "$msgs" | grep -En "$ids") && report "IDs or trailers in commit messages" "$hits"
hits=$(printf '%s\n' "$msgs" | grep -nF "$dash") && report "em-dashes in commit messages" "$hits"
hits=$(git diff "$base...$head" -- '*.go' | grep '^+' | grep -v '^+++' | grep -E '//' | grep -E "$ids") && report "IDs in added Go comments" "$hits"
hits=$(printf '%s\n' "$added" | grep -nF "$dash") && report "em-dashes in added lines" "$hits"
hits=$(git rev-list --objects "$base..$head" | git cat-file --batch-check='%(objecttype) %(objectsize) %(rest)' | awk '$1 == "blob" && $2 > 500000' | grep .) && report "blobs over 500 KB" "$hits"

[ "$fail" = 0 ] && echo "scan-commits: $(git rev-list --count "$base..$head") commits clean"
exit "$fail"
