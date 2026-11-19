#!/bin/sh
# Runs the mardp_mac tests of one package as a signed app (SPEC §15), so
# they have the permissions macOS ties to the app's signature (D22). It is
# signed as the M0 probe (the same identifier and the development
# identity), so it shares the Screen Recording the owner granted the probe;
# the tests never ask for a permission themselves.
#
# usage: scripts/mactest.sh <package> [test flags, e.g. -test.run TestMacFrames]
set -eu
cd "$(dirname "$0")/.."
pkg="${1:?usage: scripts/mactest.sh <package> [test flags]}"
shift
app=build/mactest/MardpTest.app
rm -rf "$app"
mkdir -p "$app/Contents/MacOS"
go test -c -tags mardp_mac -o "$app/Contents/MacOS/probe" "$pkg"
scripts/probe-app.sh "$app" probe
out=$(mktemp "${TMPDIR:-/tmp}/mactest.XXXXXX")
open -W -n --stdout "$out" --stderr "$out" "$app" --args -test.v -test.timeout 20m -test.paniconexit0 "$@" \
	|| { cat "$out"; rm -f "$out"; exit 1; }
cat "$out"
status=0
grep -q '^PASS$' "$out" || status=1
rm -f "$out"
echo "(ran as $app)"
exit $status
