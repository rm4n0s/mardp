#!/bin/sh
# Builds build/mardp.app with Glutter's glutter-macos (SPEC §12.1) and signs
# it with mardp's development identity (scripts/sign.sh, D22), so that the
# permissions the owner grants it stay from build to build.
#
# usage: scripts/build-app.sh [-debug]
#
# -debug builds with -tags debug_mode (Glutter's debug build), not stripped.
set -eu
cd "$(dirname "$0")/.."

debug=""
case "${1:-}" in
"") ;;
-debug) debug=-debug ;;
*)
	echo "usage: scripts/build-app.sh [-debug]" >&2
	exit 2
	;;
esac

app=build/mardp.app
version=$(git describe --tags --match 'v[0-9]*' --abbrev=0 2>/dev/null || echo v0.0.0)
version=${version#v}

tmp=$(mktemp -d "${TMPDIR:-/tmp}/mardp-build-app.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM
go build -o "$tmp/glutter-macos" github.com/rm4n0s/glutter/cmd/glutter-macos

# Glutter: glutter-macos builds with CGO_ENABLED=0 and has no flag for the
# environment of its go build (its BuildOptions.Env has no flag), while
# mardp's internal/mac packages need cgo. A go of our own first in PATH
# turns cgo back on for glutter-macos's go build. Glutter should honor
# CGO_ENABLED of the environment, or take -env for build (docs/GLUTTER-ISSUES.md).
real_go=$(command -v go)
mkdir "$tmp/bin"
cat >"$tmp/bin/go" <<SHIM
#!/bin/sh
CGO_ENABLED=1 exec "$real_go" "\$@"
SHIM
chmod +x "$tmp/bin/go"

rm -rf "$app"
mkdir -p build
PATH="$tmp/bin:$PATH" "$tmp/glutter-macos" build -o "$app" -name mardp -id com.github.rm4n0s.mardp \
	-version "$version" $debug \
	-usage "NSScreenCaptureUsageDescription=mardp shows this Mac's screen to the RDP client that you connect." \
	./cmd/mardp

# Glutter: glutter-macos signs ad hoc and has no flag for the signing
# identity (its SignOptions.Identity has no flag); the app is signed again
# with mardp's own (docs/GLUTTER-ISSUES.md).
scripts/sign.sh "$app"
echo "built $app $version"
