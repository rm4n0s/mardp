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
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key><string>probe</string>
	<key>CFBundleIdentifier</key><string>com.github.rm4n0s.mardp.probe</string>
	<key>CFBundleName</key><string>mardp probe</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>LSMinimumSystemVersion</key><string>14.0</string>
	<key>LSUIElement</key><true/>
	<key>NSScreenCaptureUsageDescription</key><string>mardp's tests capture the screen.</string>
</dict>
</plist>
PLIST
scripts/sign.sh "$app" >/dev/null
out=$(mktemp "${TMPDIR:-/tmp}/mactest.XXXXXX")
open -W -n --stdout "$out" --stderr "$out" "$app" --args -test.v -test.timeout 20m -test.paniconexit0 "$@" \
	|| { cat "$out"; rm -f "$out"; exit 1; }
cat "$out"
status=0
grep -q '^PASS$' "$out" || status=1
rm -f "$out"
echo "(ran as $app)"
exit $status
