#!/bin/sh
# Builds and signs spikes/out/Probe.app (throwaway, M0).
set -eu
cd "$(dirname "$0")/../.."
app=spikes/out/Probe.app
mkdir -p "$app/Contents/MacOS"
go build -o "$app/Contents/MacOS/probe" ./spikes/probe
cat >"$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.github.rm4n0s.mardp.probe</string>
<key>CFBundleName</key><string>mardp probe</string>
<key>CFBundleExecutable</key><string>probe</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>0.0.1</string>
<key>LSMinimumSystemVersion</key><string>14.0</string>
<key>NSScreenCaptureUsageDescription</key><string>mardp's probe checks screen capture for the RDP host.</string>
<key>NSAudioCaptureUsageDescription</key><string>mardp's probe checks system sound capture for the RDP host.</string>
</dict></plist>
PLIST
scripts/sign.sh "$app"
echo "built $app"
