#!/bin/sh
# The GoReleaser dry run of the changelog and the release notes: real releases
# (nothing published) of synthetic history in a scratch clone, then the cases of
# scripts/release-notes.sh. Needs GoReleaser 2 on PATH; touches no tag or branch
# of this repository.
set -eu
LC_ALL=C
export LC_ALL

fail() {
	echo "release-notes-check: $*" >&2
	exit 1
}

tag_re='^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'

# check_names <dir>: every entry is a non-empty file named <valid tag>.md.
check_names() {
	_bad=
	_names=$(ls -A "$1") || fail "cannot list $1"
	for _n in $_names; do
		_ok=1
		case $_n in
		*.md) printf '%s\n' "${_n%.md}" | grep -Eq "$tag_re" || _ok=0 ;;
		*) _ok=0 ;;
		esac
		if [ "$_ok" = 1 ] && { [ ! -f "$1/$_n" ] || [ ! -s "$1/$_n" ]; }; then
			_ok=0
		fi
		if [ "$_ok" = 0 ]; then
			_bad="$_bad $_n"
		fi
	done
	if [ -n "$_bad" ]; then
		echo "release-notes-check: bad entries in $1:$_bad" >&2
		return 1
	fi
}

goreleaser --version 2>/dev/null | grep -Eq '^GitVersion:[[:space:]]+v?2\.' ||
	fail "needs GoReleaser 2 on PATH"

top=$(git rev-parse --show-toplevel) || fail "not inside a git checkout"
common=$(git rev-parse --path-format=absolute --git-common-dir) || fail "cannot find the git dir"
head=$(git rev-parse HEAD) || fail "cannot resolve HEAD"

if [ -d "$top/.github/release-notes" ]; then
	check_names "$top/.github/release-notes" || exit 1
fi

tmp=
cleanup() {
	if [ -n "$tmp" ]; then
		rm -rf "$tmp"
	fi
}
trap cleanup EXIT
trap 'exit 1' INT TERM
tmp=$(mktemp -d "${TMPDIR:-/tmp}/release-notes-check.XXXXXX") || fail "cannot create a scratch dir"
tmp=$(cd "$tmp" && pwd -P) || fail "cannot resolve the scratch dir"

GIT_CONFIG_GLOBAL=/dev/null
GIT_CONFIG_NOSYSTEM=1
GIT_AUTHOR_NAME=release-check
GIT_AUTHOR_EMAIL=release-check@example.invalid
GIT_COMMITTER_NAME=release-check
GIT_COMMITTER_EMAIL=release-check@example.invalid
export GIT_CONFIG_GLOBAL GIT_CONFIG_NOSYSTEM GIT_AUTHOR_NAME GIT_AUTHOR_EMAIL GIT_COMMITTER_NAME GIT_COMMITTER_EMAIL
unset GITHUB_TOKEN GITLAB_TOKEN GITEA_TOKEN GORELEASER_CURRENT_TAG GORELEASER_PREVIOUS_TAG

git clone -q --shared --no-tags --no-checkout "$common" "$tmp/repo" || fail "cannot clone the repository"
cd "$tmp/repo" || fail "cannot enter the scratch clone"
git config maintenance.auto false || fail "git config failed"
git config gc.auto 0 || fail "git config failed"
git checkout -q -b check "$head" || fail "cannot check out $head"
printf 'dist/\n' >>.git/info/exclude || fail "cannot write .git/info/exclude"
repo=$(pwd -P) || fail "cannot resolve the scratch clone"

commit() {
	git commit -q --allow-empty -m "$1" || fail "cannot commit '$1'"
}

tagit() {
	git tag "$1" || fail "cannot tag $1"
}

git_do() {
	"$@" || fail "$* failed"
}

tagit v0.0.1
commit 'feat(cli): add a flag'
commit 'feat(ui)!: drop the old layout'
commit 'fix: mend a crash'
commit 'fix(git)!: change the snapshot ref'
commit 'perf(ui): cache rows'
commit 'docs: explain the flag'
commit 'test(ui): cover the flag'
commit 'chore: tidy'
commit 'ci: run more checks'
commit 'build(make): strip the binary'
commit 'style: format'
commit 'refactor(core): split the store'
commit 'revert: undo the flag'
commit 'Revert "feat(cli): add a flag"'
commit 'fixup! fix: mend a crash'
commit 'update the readme'
git_do git checkout -q -b side
commit 'feat(side): side work'
git_do git checkout -q check
git_do git merge -q --no-ff -m "Merge branch 'side' into check" side
tagit v0.0.2

# notes <tag>: runs the notes script as the workflow does; sets out.
notes() {
	out=$(sh "$top/scripts/release-notes.sh" "$1" "$tmp/notes") || fail "release-notes.sh $1 failed"
}

expect_eq() {
	if [ "$2" != "$3" ]; then
		printf 'release-notes-check: %s\n--- got\n%s\n--- want\n%s\n' "$1" "$2" "$3" >&2
		exit 1
	fi
}

# release <tag>: a real release without publishing, started from $out.
release() {
	_notes=$(printf '%s\n' "$out" | sed -n 's/^notes=//p')
	_prev=$(printf '%s\n' "$out" | sed -n 's/^previous=//p')
	set -- release --clean --skip=publish -f "$top/.goreleaser.yaml"
	if [ -n "$_notes" ]; then
		set -- "$@" "--release-notes=$_notes"
	fi
	if [ -n "$_prev" ]; then
		export GORELEASER_PREVIOUS_TAG=$_prev
	else
		unset GORELEASER_PREVIOUS_TAG
	fi
	GORELEASER_CURRENT_TAG=$_tag goreleaser "$@" >"$tmp/goreleaser.log" 2>&1 || {
		tail -n 40 "$tmp/goreleaser.log" >&2
		fail "goreleaser release $_tag failed"
	}
}

changelog_list() {
	[ -f dist/CHANGELOG.md ] || fail "no dist/CHANGELOG.md"
	awk '
		/^#+ / { h = $0; sub(/^#+ +/, "", h); next }
		/^[*-] / { s = $0; sub(/^[*-] +/, "", s); sub(/^[0-9a-f]+ /, "", s); printf "%s\t%s\n", h, s }
	' dist/CHANGELOG.md | sort
}

lines() {
	for _l in "$@"; do
		printf '%s\n' "$_l"
	done | sort
}

tab=$(printf '\t')

# Run A: the changelog.
_tag=v0.0.2
notes "$_tag"
expect_eq "run A: release-notes.sh $_tag" "$out" "previous=v0.0.1"
release
got=$(changelog_list) || exit 1
want=$(lines \
	"Features${tab}feat(cli): add a flag" \
	"Features${tab}feat(ui)!: drop the old layout" \
	"Features${tab}feat(side): side work" \
	"Fixes${tab}fix: mend a crash" \
	"Fixes${tab}fix(git)!: change the snapshot ref" \
	"Performance${tab}perf(ui): cache rows")
expect_eq "run A: changelog list" "$got" "$want"

# Run B: a notes file.
mkdir -p .github/release-notes || fail "cannot create .github/release-notes"
printf 'Notes for the v0.0.3 check.\n' >.github/release-notes/v0.0.3.md || fail "cannot write the notes file"
git_do git add .github/release-notes/v0.0.3.md
commit 'docs(release): add the v0.0.3 notes'
tagit v0.0.3
_tag=v0.0.3
notes "$_tag"
expect_eq "run B: release-notes.sh $_tag" "$out" "notes=$repo/.github/release-notes/v0.0.3.md
previous=v0.0.2"
release
if [ -e dist/CHANGELOG.md ]; then
	cmp -s dist/CHANGELOG.md .github/release-notes/v0.0.3.md ||
		fail "run B: dist/CHANGELOG.md is not the notes file"
fi

# Run C: a release candidate in between.
commit 'feat(cli): before the rc'
tagit v0.0.4-rc.1
commit 'fix(cli): after the rc'
tagit v0.0.4-rc.2
tagit v0.0.4
notes v0.0.4-rc.1
expect_eq "run C: release-notes.sh v0.0.4-rc.1" "$out" "notes=$tmp/notes/release-notes-v0.0.4-rc.1.md"
got=$(command cat "$tmp/notes/release-notes-v0.0.4-rc.1.md") || fail "run C: no rc notes file"
expect_eq "run C: the rc notes" "$got" "AIM v0.0.4-rc.1 is a release candidate for v0.0.4, published to test the release before it is final."
_tag=v0.0.4
notes "$_tag"
expect_eq "run C: release-notes.sh $_tag" "$out" "previous=v0.0.3"
release
got=$(changelog_list) || exit 1
want=$(lines \
	"Features${tab}feat(cli): before the rc" \
	"Fixes${tab}fix(cli): after the rc")
expect_eq "run C: changelog list" "$got" "$want"
[ -f dist/aim_0.0.4_linux_amd64.tar.gz ] ||
	fail "run C: dist/ lacks aim_0.0.4_linux_amd64.tar.gz, so the release did not take v0.0.4"

# Notes cases: no more GoReleaser runs.
# case_out <want> <args...>: stdout is exactly <want> and the status is 0, or
# the status is non-zero and stdout empty when <want> is FAIL.
case_out() {
	_want=$1
	shift
	_st=0
	_got=$(sh "$top/scripts/release-notes.sh" "$@" 2>/dev/null) || _st=$?
	if [ "$_want" = FAIL ]; then
		[ "$_st" != 0 ] || fail "notes case ($*): expected a failure"
		_want=
	else
		[ "$_st" = 0 ] || fail "notes case ($*): exit status $_st"
	fi
	expect_eq "notes case ($*)" "$_got" "$_want"
}

printf 'Notes for rc.3.\n' >.github/release-notes/v0.0.4-rc.3.md || fail "cannot write the rc.3 notes"
case_out "notes=$repo/.github/release-notes/v0.0.4-rc.3.md" v0.0.4-rc.3 "$tmp/notes"
case_out "" v0.0.1 "$tmp/notes"
tagit 'v0.0.5+build-7'
case_out "previous=v0.0.4" 'v0.0.5+build-7' "$tmp/notes"
case_out FAIL v0.0.9 "$tmp/notes"
case_out FAIL 1.0.0 "$tmp/notes"
case_out FAIL v1.0 "$tmp/notes"
case_out FAIL 'v1.0.0;true' "$tmp/notes"
case_out FAIL v1.0.0/x "$tmp/notes"
case_out FAIL "" "$tmp/notes"
case_out FAIL v0.0.4
case_out FAIL

mkdir "$tmp/names" || fail "cannot create the names fixture"
: >"$tmp/names/1.0.0.md"
: >"$tmp/names/v1.0.0.MD"
: >"$tmp/names/v9.9.9.md"
printf 'x\n' >"$tmp/names/v1.2.3.md"
names_err=$(check_names "$tmp/names" 2>&1) && fail "check_names accepted bad names"
expect_eq "check_names" "$names_err" "release-notes-check: bad entries in $tmp/names: 1.0.0.md v1.0.0.MD v9.9.9.md"

echo "release-notes-check: changelog groups, notes, previous tags and notes cases ok"
