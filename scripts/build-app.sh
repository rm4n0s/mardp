#!/bin/sh
# Builds build/mardp.app with Glutter's glutter-macos (SPEC §12.1, §16), with
# the icon scripts/icon draws, and signs it with mardp's development identity
# (scripts/sign.sh, D22), so that the permissions the owner grants it stay
# from build to build.
#
# The bundle's version is the last version tag (vX.Y.Z) before HEAD;
# `mardp -version` prints the version Go records from git, which is the same
# at a tagged commit and a pseudo-version after it. Without git (the source
# as a ZIP from GitHub), the version is the one in the folder's name
# (mardp-X.Y.Z, as GitHub names a tag's ZIP), and 0.0.0 otherwise.
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
# The oldest macOS mardp runs on (SPEC D20).
minimum=14.0
version=$(git describe --tags --match 'v[0-9]*' --abbrev=0 2>/dev/null || true)
if [ -z "$version" ]; then
	case $(basename "$PWD") in
	mardp-[0-9]*.[0-9]*.[0-9]*) version=$(basename "$PWD" | sed 's/^mardp-//') ;;
	*) version=0.0.0 ;;
	esac
fi
version=${version#v}

tmp=$(mktemp -d "${TMPDIR:-/tmp}/mardp-build-app.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM
go build -o "$tmp/glutter-macos" github.com/rm4n0s/glutter/cmd/glutter-macos
go run ./scripts/icon "$tmp/icon.png"

# Glutter: glutter-macos builds with CGO_ENABLED=0 and has no flag for the
# environment of its go build (its BuildOptions.Env has no flag), while
# mardp's internal/mac packages need cgo. A go of our own first in PATH
# turns cgo back on for glutter-macos's go build. Glutter should honor
# CGO_ENABLED of the environment, or take -env for build (docs/GLUTTER-ISSUES.md).
real_go=$(command -v go)
mkdir "$tmp/bin"
# Glutter: glutter-macos leaves the deployment target to clang, whose
# default is the macOS of the machine that builds, so the executable would
# not open on an older macOS; the shim sets MACOSX_DEPLOYMENT_TARGET. Glutter
# should build for the LSMinimumSystemVersion it writes, and take a flag for
# it (docs/GLUTTER-ISSUES.md).
cat >"$tmp/bin/go" <<SHIM
#!/bin/sh
CGO_ENABLED=1 MACOSX_DEPLOYMENT_TARGET=$minimum exec "$real_go" "\$@"
SHIM
chmod +x "$tmp/bin/go"

rm -rf "$app"
mkdir -p build
PATH="$tmp/bin:$PATH" "$tmp/glutter-macos" build -o "$app" -name mardp -id com.github.rm4n0s.mardp \
	-version "$version" -icon "$tmp/icon.png" $debug \
	-usage "NSScreenCaptureUsageDescription=mardp shows this Mac's screen to the RDP client that you connect." \
	./cmd/mardp

# Glutter: glutter-macos writes LSMinimumSystemVersion 12.0 and has no flag
# for it (docs/GLUTTER-ISSUES.md); the bundle says mardp's minimum instead,
# before it is signed.
plutil -replace LSMinimumSystemVersion -string "$minimum" "$app/Contents/Info.plist"
built=$(otool -l "$app/Contents/MacOS/mardp" | awk '$1 == "minos" { print $2; exit }')
if [ "$built" != "$minimum" ]; then
	echo "build-app.sh: the executable needs macOS $built, not $minimum" >&2
	exit 1
fi

# Glutter: glutter-macos signs ad hoc and has no flag for the signing
# identity (its SignOptions.Identity has no flag); the app is signed again
# with mardp's own (docs/GLUTTER-ISSUES.md).
sh scripts/sign.sh "$app"
echo "built $app $version"
