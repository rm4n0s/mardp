# mardp

An RDP host for macOS, so that RDP clients such as Remmina on Linux can connect to a Mac: they see its desktop, control it and hear its sound, well enough to watch video. It contains its own RDP server library (`rdp/`), written in Go from scratch, and its window is built with [Glutter](https://github.com/rm4n0s/glutter).

Work in progress: the design is in [docs/SPEC.md](docs/SPEC.md), the plan in [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md) and what is done in [docs/IMPLEMENTED.md](docs/IMPLEMENTED.md).

## Building

You need Go 1.27 and Xcode's command line tools (`xcode-select --install`).

```sh
go build ./...
scripts/check.sh   # every check the project requires
```

### The development signing identity

macOS ties the Screen Recording and Accessibility permissions to an app's code signature. An app that is signed ad hoc gets a new signature with every build, and macOS asks for both permissions again. mardp's builds are therefore signed with one stable identity: a self-signed code-signing certificate named `mardp development` in your login keychain. Create it once:

```sh
scripts/make-identity.sh
```

The script makes a certificate and key with the system's LibreSSL, imports them into the login keychain (so that `codesign` may use the key without asking each time), and trusts the certificate for code signing only; macOS asks for your password for that last part. It changes nothing when the identity already exists.

`scripts/sign.sh <bundle>` signs with it, and stops with a message when it is missing. To use a different identity, set `MARDP_SIGN_IDENTITY` to its name.

To remove the identity, delete the certificate `mardp development` (and its key) from the login keychain in Keychain Access.
