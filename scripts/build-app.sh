#!/bin/sh
# Builds build/mardp.app with Glutter's glutter-macos (SPEC §12.1, §16), with
# the icon scripts/icon draws, signed with mardp's development identity (D22),
# so that the permissions the owner grants it stay from build to build.
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

# The signing identity, as scripts/sign.sh takes it (D22).
identity="${MARDP_SIGN_IDENTITY:-mardp development}"
if ! security find-identity -v -p codesigning | grep -q "\"$identity\""; then
	cat >&2 <<MSG
build-app.sh: the signing identity "$identity" is not in your keychain.
Create it once with scripts/make-identity.sh (README, "The signing identity").
MSG
	exit 1
fi

rm -rf "$app"
mkdir -p build
# mardp's internal/mac packages need cgo (D14), which glutter-macos leaves
# to the environment; -min-macos is the minimum of the plist and of the
# build, and -sign the identity the bundle is signed with.
CGO_ENABLED=1 "$tmp/glutter-macos" build -o "$app" -name mardp -id com.github.rm4n0s.mardp \
	-version "$version" -icon "$tmp/icon.png" $debug -min-macos "$minimum" -sign "$identity" \
	-usage "NSScreenCaptureUsageDescription=mardp shows this Mac's screen to the RDP client that you connect." \
	./cmd/mardp
echo "built $app $version"
