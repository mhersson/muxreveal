#!/bin/sh
# Usage: release-notes.sh <tag> <dir>, run inside the checkout.
# Prints notes=<file> when the tag has release notes and, for a final tag,
# previous=<tag> naming the final tag before it. No output means GoReleaser's
# own changelog and choice of previous tag.
set -eu
LC_ALL=C
export LC_ALL

fail() {
	echo "release-notes: $*" >&2
	exit 1
}

[ "$#" = 2 ] || fail "usage: release-notes.sh <tag> <dir>"
tag=$1
dir=$2

# Keeps shell metacharacters out of the paths and out of $GITHUB_ENV.
printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$' ||
	fail "not a release tag: $tag"
[ -n "$dir" ] || fail "empty <dir>"

top=$(git rev-parse --show-toplevel) || fail "not inside a git checkout"

base=${tag%%+*}
pre=0
case $base in
*-*) pre=1 ;;
esac

notes=
previous=

file=$top/.github/release-notes/$tag.md
if [ -f "$file" ]; then
	notes=$file
elif [ "$pre" = 1 ]; then
	mkdir -p "$dir" || fail "cannot create $dir"
	absdir=$(cd "$dir" && pwd -P) || fail "cannot resolve $dir"
	notes=$absdir/release-notes-$tag.md
	final=${base%%-*}
	printf 'AIM %s is a release candidate for %s, published to test the release before it is final.\n' "$tag" "$final" >"$notes" ||
		fail "cannot write $notes"
fi

if [ "$pre" = 0 ]; then
	git rev-parse -q --verify "refs/tags/$tag" >/dev/null || fail "no such tag: $tag"
	nums=${base#v}
	major=${nums%%.*}
	rest=${nums#*.}
	minor=${rest%%.*}
	patch=${rest#*.}
	list=$(git tag --list 'v*' --merged "$tag") || fail "cannot list the tags merged into $tag"
	previous=$(printf '%s\n' "$list" |
		grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' |
		awk -F. -v M="$major" -v N="$minor" -v P="$patch" '
			{
				a = substr($1, 2) + 0; b = $2 + 0; c = $3 + 0
				if (a < M || (a == M && (b < N || (b == N && c < P)))) print a, b, c, $0
			}' |
		sort -k1,1n -k2,2n -k3,3n |
		tail -n 1 |
		awk '{print $4}') || fail "cannot pick the previous tag"
fi

if [ -n "$notes" ]; then
	echo "notes=$notes"
fi
if [ -n "$previous" ]; then
	echo "previous=$previous"
fi
