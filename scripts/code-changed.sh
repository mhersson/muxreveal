#!/bin/sh
# Exits 1 only when every changed file (see changed-files.sh) is non-code, so
# make check-fast can skip the Go gates; exits 0 when any file is code, when no
# file is listed and when git fails, so doubt runs the full check.
#
# Non-code is an allowlist: Markdown at the repository root, anything under
# docs/ or .github/, config.example.toml, LICENSE, .goreleaser.yaml and
# .gitignore. Everything else is code, because it can change what the Go gates
# see: Markdown or other files inside a package (testdata goldens and fixtures
# feed Go tests), the Makefile, scripts/, .golangci.yaml, go.mod and go.sum.
set -u
export LC_ALL=C

files=$(sh "$(dirname "$0")/changed-files.sh") || exit 0
[ -n "$files" ] || exit 0

printf '%s\n' "$files" | while IFS= read -r f; do
	[ -n "$f" ] || continue
	case $f in
	docs/* | .github/* | config.example.toml | LICENSE | .goreleaser.yaml | .gitignore) ;;
	*/*) exit 1 ;;
	*.md) ;;
	*) exit 1 ;;
	esac
done && exit 1

exit 0
