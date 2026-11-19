#!/bin/sh
# Makes <app> a bundle around the executable already at
# <app>/Contents/MacOS/<name> and signs it as the M0 probe (the same
# identifier and the development identity), so it shares the permissions
# the owner granted the probe (D22). Used by mactest.sh and the
# mardp_freerdp test of cmd/mardp; nothing here asks for a permission.
#
# usage: scripts/probe-app.sh <app> <name>
set -eu
cd "$(dirname "$0")/.."
app="${1:?usage: scripts/probe-app.sh <app> <name>}"
name="${2:?usage: scripts/probe-app.sh <app> <name>}"
if [ ! -x "$app/Contents/MacOS/$name" ]; then
	echo "probe-app.sh: $app/Contents/MacOS/$name is not an executable" >&2
	exit 1
fi
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key><string>$name</string>
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
