#!/bin/sh
# Fails on what must never reach git in the commits between a base and a head:
# milestone, plan, task, ruling or requirement IDs and attribution trailers in
# the messages, IDs in added Go comments, em-dashes in the messages or in added
# Go and Markdown lines, and blobs over 500 KB (recordings stay outside).
# Usage: scan-commits.sh [base [head]]; base defaults to origin/main, head to
# HEAD. Word boundaries are spelled out: \b is a GNU extension to POSIX ERE.
set -u
export LC_ALL=C

base=${1:-origin/main}
head=${2:-HEAD}
b='(^|[^A-Za-z0-9])'
e='([^A-Za-z0-9]|$)'
ids="\\(([A-Z]{2}-[0-9]+(, )?)+\\)|$b(AG|PR|DF|WT|GH|GA|MD|UX|TH)-[0-9]|${b}M[0-9]+[a-f]?$e|${b}[RD][0-9]+$e|Review Focus"
# Matched without case: git trailers are case-insensitive, and so is the prose.
loose="${b}(task|part) [0-9]|claude-session|co-authored-by|generated with"
dash=$(printf '\342\200\224')

# Prove each pattern matches before an empty result is trusted.
for s in 'x (AG-5) y' 'x PR-1 y' 'in M2b' 'see R14' 'per D6'; do
	printf '%s\n' "$s" | grep -Eq "$ids" || { echo "scan-commits: grep cannot match the ID pattern on '$s'"; exit 1; }
done
for s in 'in Task 3' 'Co-authored-by: x'; do
	printf '%s\n' "$s" | grep -Eiq "$loose" || { echo "scan-commits: grep cannot match the trailer pattern on '$s'"; exit 1; }
done
printf 'a\342\200\224b\n' | grep -qF "$dash" || { echo "scan-commits: grep cannot match an em-dash"; exit 1; }

for r in "$base" "$head"; do
	git rev-parse --verify --quiet "$r^{commit}" >/dev/null || { echo "scan-commits: no commit $r"; exit 1; }
done

# Plain diffs whatever the user's config, so the added-line filter holds.
gdiff() { git diff --no-color --no-ext-diff --src-prefix=a/ --dst-prefix=b/ "$base...$head" -- "$@"; }
added() { grep '^+' | grep -Ev '^\+\+\+ (b/|/dev/null)'; }

msgs=$(git log --no-merges --format='%h %s%n%b' "$base..$head") || exit 1
godiff=$(gdiff '*.go') || exit 1
textdiff=$(gdiff '*.go' '*.md') || exit 1
objects=$(git rev-list --objects "$base..$head") || exit 1
sizes=$(printf '%s\n' "$objects" | git cat-file --batch-check='%(objecttype) %(objectsize) %(rest)') || exit 1

fail=0
report() {
	echo "scan-commits: $1:"
	printf '%s\n' "$2"
	fail=1
}

hits=$(printf '%s\n' "$msgs" | grep -En "$ids") && report "IDs in commit messages" "$hits"
hits=$(printf '%s\n' "$msgs" | grep -Ein "$loose") && report "task or part numbers, or trailers, in commit messages" "$hits"
hits=$(printf '%s\n' "$msgs" | grep -nF "$dash") && report "em-dashes in commit messages" "$hits"
hits=$(printf '%s\n' "$godiff" | added | grep -F '//' | grep -E "$ids") && report "IDs in added Go comments" "$hits"
hits=$(printf '%s\n' "$godiff" | added | grep -F '//' | grep -Ei "$loose") && report "task or part numbers in added Go comments" "$hits"
hits=$(printf '%s\n' "$textdiff" | added | grep -nF "$dash") && report "em-dashes in added Go and Markdown lines" "$hits"
hits=$(printf '%s\n' "$sizes" | awk '$1 == "blob" && $2 > 500000' | grep .) && report "blobs over 500 KB" "$hits"

[ "$fail" = 0 ] && echo "scan-commits: $(git rev-list --count "$base..$head") commits clean"
exit "$fail"
