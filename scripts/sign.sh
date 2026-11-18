#!/bin/sh
# Signs a bundle (or a single binary) with mardp's development signing
# identity (SPEC §7.1, D22). macOS ties the Screen Recording and
# Accessibility permissions to the designated requirement, which stays the
# same from build to build only with this one identity.
set -eu

NAME="${MARDP_SIGN_IDENTITY:-mardp development}"

if [ $# -ne 1 ]; then
	echo "usage: scripts/sign.sh <bundle.app or binary>" >&2
	exit 2
fi
target=$1
if [ ! -e "$target" ]; then
	echo "sign.sh: $target does not exist" >&2
	exit 1
fi
if ! security find-identity -v -p codesigning | grep -q "\"$NAME\""; then
	cat >&2 <<MSG
sign.sh: the signing identity "$NAME" is not in your keychain.
Create it once with scripts/make-identity.sh (README, "Building").
MSG
	exit 1
fi

codesign --force --sign "$NAME" --timestamp=none "$target"
codesign --verify --strict "$target"
