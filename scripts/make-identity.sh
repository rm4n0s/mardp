#!/bin/sh
# Creates mardp's development signing identity (SPEC §7.1, D22): a
# self-signed code-signing certificate and its key in the login keychain,
# trusted for code signing. Run it once, yourself: macOS asks for your
# password to trust the certificate. Builds signed with it keep the Screen
# Recording and Accessibility permissions across rebuilds.
set -eu

NAME="mardp development"
KEYCHAIN="$HOME/Library/Keychains/login.keychain-db"
OPENSSL=/usr/bin/openssl # the system's LibreSSL: its .p12 files import into the keychain

if security find-identity -p codesigning | grep -q "\"$NAME\""; then
	echo "the identity \"$NAME\" already exists:"
	security find-identity -p codesigning | grep "\"$NAME\""
	exit 0
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cat >"$work/req.cnf" <<CNF
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = $NAME
[ext]
basicConstraints = critical, CA:false
keyUsage = critical, digitalSignature
extendedKeyUsage = critical, codeSigning
subjectKeyIdentifier = hash
CNF

"$OPENSSL" req -x509 -newkey rsa:2048 -nodes -days 3650 -sha256 \
	-config "$work/req.cnf" -keyout "$work/key.pem" -out "$work/cert.pem" 2>/dev/null
pass=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24)
"$OPENSSL" pkcs12 -export -inkey "$work/key.pem" -in "$work/cert.pem" \
	-name "$NAME" -passout "pass:$pass" -out "$work/id.p12"

# The key may be used by codesign without a prompt each time.
security import "$work/id.p12" -k "$KEYCHAIN" -P "$pass" -T /usr/bin/codesign
echo "trusting the certificate for code signing (macOS asks for your password)"
security add-trusted-cert -r trustRoot -p codeSign -k "$KEYCHAIN" "$work/cert.pem"

security find-identity -v -p codesigning | grep "\"$NAME\""
echo "done: scripts/sign.sh signs with \"$NAME\""
