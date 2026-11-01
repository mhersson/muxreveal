#!/bin/sh
# The GoReleaser dry run: builds a snapshot into dist/ and checks the archives,
# the checksums and each binary. Needs GoReleaser 2 on PATH; publishes nothing.
set -eu
LC_ALL=C
export LC_ALL

fail() {
	echo "release-check: $*" >&2
	exit 1
}

cd "$(git rev-parse --show-toplevel)"

scratch=$(mktemp -d "${TMPDIR:-/tmp}/aim-release-check.XXXXXX")
trap 'rm -rf "$scratch"' EXIT

goreleaser --version 2>/dev/null | grep -Eq '^GitVersion:[[:space:]]+v?2\.' ||
	fail "needs GoReleaser 2 on PATH"

goreleaser check || fail "goreleaser check failed"

# Same test as Go's VCS stamp: untracked files count.
short=$(git rev-parse --short HEAD)
full=$(git rev-parse HEAD)
mark=
if [ -n "$(git status --porcelain)" ]; then
	mark=', modified'
fi

goreleaser release --snapshot --clean --skip=publish || fail "snapshot build failed"

cd dist

targets='linux_amd64 linux_arm64 darwin_amd64 darwin_arm64'
count=$(find . -maxdepth 1 -name '*.tar.gz' | wc -l | tr -d ' ')
[ "$count" = 4 ] || fail "expected 4 archives in dist/, found $count"

sums=
for f in aim_*_checksums.txt; do
	[ -f "$f" ] || fail "no aim_*_checksums.txt in dist/"
	[ -z "$sums" ] || fail "more than one checksums file in dist/"
	sums=$f
done

version=
for t in $targets; do
	n=0
	for f in aim_*_"$t".tar.gz; do
		[ -f "$f" ] || continue
		n=$((n + 1))
		v=${f#aim_}
		v=${v%_"$t".tar.gz}
		if [ -z "$version" ]; then
			version=$v
		elif [ "$version" != "$v" ]; then
			fail "archive versions differ: $version and $v"
		fi
	done
	[ "$n" = 1 ] || fail "expected one archive for $t, found $n"
done
[ "$sums" = "aim_${version}_checksums.txt" ] || fail "checksums file $sums does not match version $version"

listed=$(awk '{print $2}' "$sums" | sort | tr '\n' ' ')
want=$(for t in $targets; do echo "aim_${version}_$t.tar.gz"; done | sort | tr '\n' ' ')
[ "$listed" = "$want" ] || fail "checksums list '$listed', want '$want'"

if command -v sha256sum >/dev/null 2>&1; then
	sha256sum -c "$sums" >/dev/null || fail "checksum mismatch"
else
	shasum -a 256 -c "$sums" >/dev/null || fail "checksum mismatch"
fi

ver=v$version
printf '%s\n' "$ver" | grep -Eq "^v[0-9]+\.[0-9]+\.[0-9]+-snapshot-$short\$" ||
	fail "version $ver does not look like a snapshot of $short"

cd ..

wantfiles="aim LICENSE THIRD_PARTY.md"
for g in README* config.example*; do
	[ -e "$g" ] && wantfiles="$wantfiles $g"
done
wantfiles=$(for f in $wantfiles; do echo "$f"; done | sort | tr '\n' ' ')

native_os=$(go env GOOS)
native_arch=$(go env GOARCH)
native=
date=

for t in $targets; do
	os=${t%_*}
	arch=${t#*_}
	archive=dist/aim_${version}_$t.tar.gz
	got=$(tar -tzf "$archive" | sort | tr '\n' ' ')
	[ "$got" = "$wantfiles" ] || fail "$archive holds '$got', want '$wantfiles'"

	mkdir "$scratch/$t"
	tar -xzf "$archive" -C "$scratch/$t"
	bin=$scratch/$t/aim

	info=$(go version -m "$bin")
	for line in "GOOS=$os" "GOARCH=$arch" "CGO_ENABLED=0" "-trimpath=true" "vcs.revision=$full"; do
		printf '%s\n' "$info" | grep -Eq "[[:space:]]$line\$" ||
			fail "$t: go version -m lacks $line"
	done

	if go tool nm "$bin" 2>/dev/null | grep -q ' main\.main$'; then
		fail "$t: symbol table not stripped"
	fi
	if grep -aqE '(\.|__)z?debug_info' "$bin"; then
		fail "$t: DWARF not stripped"
	fi

	if [ "$os" = "$native_os" ] && [ "$arch" = "$native_arch" ]; then
		native=$("$bin" --version) || fail "$t: --version failed"
		printf '%s\n' "$native" | grep -Eq "^aim $ver \\(commit $short$mark, built [0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z\\)\$" ||
			fail "$t: unexpected --version output: $native"
		date=${native##*built }
		date=${date%)}
	fi
done
[ -n "$native" ] || fail "no archive for the native $native_os/$native_arch"

for t in $targets; do
	bin=$scratch/$t/aim
	grep -aqF -- "$ver" "$bin" || fail "$t: binary lacks $ver"
	grep -aqF -- "$date" "$bin" || fail "$t: binary lacks the build date $date"
done

echo "$native"
echo "release-check: 4 archives, checksums and binaries ok"
