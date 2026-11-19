#!/bin/sh
# Installs mardp on this Mac: checks what a build needs and says in plain
# words what is missing, makes the signing identity when there is none,
# builds the app, copies it to /Applications and opens it (README,
# "Installing on a new Mac"). Run it again to update: it replaces the app
# and keeps the settings and the permissions.
#
# usage: scripts/install.sh [-check]
#
# -check only checks what a build needs, and changes nothing.
#
# MARDP_INSTALL_DIR is the folder the app goes into (default /Applications).
set -eu
cd "$(dirname "$0")/.."

check=""
case "${1:-}" in
"") ;;
-check) check=1 ;;
*)
	echo "usage: scripts/install.sh [-check]" >&2
	exit 2
	;;
esac

dest=${MARDP_INSTALL_DIR:-/Applications}
# The oldest macOS mardp runs on (SPEC D20).
minimum=14
# Where Go's installer from go.dev puts Go. A Terminal window that was
# open before Go was installed does not have it in its PATH yet.
pkg_go=${MARDP_PKG_GO:-/usr/local/go/bin}
# The oldest Go that fetches by itself the Go version go.mod asks for.
min_go=21

say() { printf '\n==> %s\n' "$*"; }
stop() {
	printf '\n%s\n' "$*" >&2
	exit 1
}

say "Checking this Mac"
macos=$(sw_vers -productVersion)
if [ "${macos%%.*}" -lt "$minimum" ]; then
	stop "This Mac has macOS $macos. mardp needs macOS $minimum or newer."
fi
echo "macOS $macos: fine"

if ! xcode-select -p >/dev/null 2>&1; then
	if [ -z "$check" ]; then
		xcode-select --install >/dev/null 2>&1 || true
	fi
	stop "Apple's command line tools are missing (they hold the compiler that builds mardp).
A window from Apple opens: press Install and wait until it says it is done (some minutes).
If no window opens, run:  xcode-select --install
Then run this script again."
fi
echo "Apple's command line tools: fine"

if ! command -v go >/dev/null 2>&1 && [ -x "$pkg_go/go" ]; then
	PATH="$pkg_go:$PATH"
	export PATH
fi
case $(uname -m) in
arm64) kind="Apple macOS (ARM64)" ;;
*) kind="Apple macOS (x86-64)" ;;
esac
if ! command -v go >/dev/null 2>&1; then
	stop "Go is missing (the language mardp is written in).
1. Open https://go.dev/dl/ in a browser.
2. Download the installer named \"$kind\" that ends in .pkg.
3. Double-click the downloaded file and follow it to the end.
Then run this script again."
fi
have=$(go env GOVERSION 2>/dev/null | sed -n 's/^go1\.\([0-9]*\).*/\1/p')
if [ -z "$have" ] || [ "$have" -lt "$min_go" ]; then
	stop "The Go on this Mac ($(go env GOVERSION 2>/dev/null)) is too old.
Install the newest one from https://go.dev/dl/ (the \"$kind\" installer that ends in .pkg),
then run this script again."
fi
echo "Go $(go env GOVERSION | sed 's/^go//'): fine"

if [ -n "$check" ]; then
	say "Everything a build needs is here."
	exit 0
fi

if pgrep -x mardp >/dev/null 2>&1; then
	stop "mardp is running. Quit it first (its menu-bar icon, then Quit) and run this script again."
fi

if ! security find-identity -p codesigning | grep -q '"mardp development"'; then
	say "Making the signing identity"
	echo "macOS ties the permissions you will give mardp to a signature. This makes one for this Mac,"
	echo "kept in your keychain. macOS asks for your Mac's password, once, to trust it."
	sh scripts/make-identity.sh
fi

say "Building mardp (the first time it downloads what it needs: a few minutes)"
sh scripts/build-app.sh

say "Copying mardp to $dest"
if ! ditto build/mardp.app "$dest/mardp.app"; then
	stop "mardp could not be copied to $dest. It is built, in build/mardp.app: drag that into Applications in the Finder."
fi

say "Opening mardp"
open "$dest/mardp.app"
cat <<MSG

mardp is installed in $dest.
Its window shows what it still needs: follow the README from "The first start".
MSG
