# mardp

An RDP host for macOS, so that RDP clients such as Remmina on Linux can connect to a Mac: they see its desktop, control it and hear its sound, well enough to watch video. It contains its own RDP server library (`rdp/`), written in Go from scratch, and its window is built with [Glutter](https://github.com/rm4n0s/glutter).

The design is in [docs/SPEC.md](docs/SPEC.md), the plan in [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md) and what is done in [docs/IMPLEMENTED.md](docs/IMPLEMENTED.md).

## Requirements

- A Mac with macOS 14 or newer, Apple silicon or Intel (Intel is built but not tested).
- To build it: Go 1.27 and Xcode's command line tools (`xcode-select --install`). Nothing from Homebrew.
- The client: Remmina on Linux, with a FreeRDP that decodes H.264 (see [The client](#the-client-linux)).

## Installing

mardp is built from source and signed with a signing identity of your own, made once (see [The signing identity](#the-signing-identity)):

```sh
scripts/make-identity.sh
scripts/build-app.sh
```

This makes `build/mardp.app`. Copy it to `/Applications` (or run it from `build/`):

```sh
ditto build/mardp.app /Applications/mardp.app
open /Applications/mardp.app
```

`mardp -version` prints the version of a build:

```sh
/Applications/mardp.app/Contents/MacOS/mardp -version
```

### The first start

The window shows a checklist until three things are done:

1. **Screen Recording**, for the picture and the system sound. The server does not start without it. Its button asks macOS, which shows its question only the first time, and opens the pane of System Settings where you turn mardp on.
2. **Accessibility**, for the client's keyboard and mouse. Without it a session is view only. A server that started without it needs Stop and Start once it is granted.
3. **A user name and a password** (at least 8 characters), which the client logs in with.

Then press **Start**. macOS asks once whether mardp may accept incoming connections: allow it. The window shows the addresses to type into Remmina (each address of the Mac with the port, 3389 unless you change it) and the certificate's fingerprint.

macOS also asks once, at the first connection, whether mardp may send notifications. It notifies when a client connects or disconnects, and when an address has to wait after failed logins.

### macOS asks again about Screen Recording

macOS asks again, from time to time, whether an app that records the screen without its picker may go on: "mardp is requesting to bypass the system private window picker and directly access your screen and audio", with Allow and Open System Settings. It can come back after a few hours or after a month, and a new build may bring it back. The question is answered at the Mac, not from the client. A running session goes on while the question waits. If the permission is withdrawn, the session ends with that reason and the checklist comes back.

### Using it

- Closing the window keeps mardp and the server running; the menu-bar icon (a screen on a stand: empty when stopped, a dot when listening, filled when a client is connected) has Start or Stop, Disconnect, Show window and Quit. A click on the Dock icon also brings the window back. Quit ends the app and the server. Cmd+Q only hides the window (Glutter cannot tell it from the close button).
- **Settings**: the port and address, the display, caps on the height, the frame rate and the bitrate, where the sound plays (off, on the client only, or on both), swapping Ctrl and Command, a new certificate, "Open the app at login" and "Start the server when the app opens". Each group says when its settings apply.
- **Recent events** lists the last 50 connections, disconnections with their reason, and failed logins.
- After 5 failed logins from one address, the address waits 1 minute, doubling to at most 1 hour; a login while it waits is refused. A successful login, or a day without a failure, forgets its failures.
- The settings, the password's hash and the certificate are in `~/Library/Application Support/mardp/`.
- `-log-file <path>` writes the log to a file: `open -n /Applications/mardp.app --args -log-file /tmp/mardp.log`. `mardp -headless` runs the server without a window, with the same settings.

### While you are away from the Mac

mardp runs in the session of the logged-in user. It cannot show the login window, and what a client sees and can do while the Mac is locked is still being checked (SPEC §19, R3). Until then, if you will use the Mac from elsewhere, turn the lock off while you are away: in System Settings › Lock Screen, set "Require password after screen saver begins or display is turned off" to Never. mardp keeps the display awake during a session. Anyone at the Mac can use it then, so do this only where that is acceptable.

## The client (Linux)

Remmina, with the RDP plugin, from your distribution. For video mardp uses H.264, which FreeRDP decodes with Cisco's OpenH264.

On Fedora the packaged FreeRDP links `libopenh264.so.8`, which two packages provide: `openh264` (from the repository `fedora-cisco-openh264`, enabled by default) decodes; `noopenh264` (from Fedora's own repository) is a stub that decodes nothing. Check which one is installed:

```bash
rpm -q openh264 noopenh264
```

If it is `noopenh264`, swap it:

```bash
sudo dnf swap noopenh264 openh264 --enablerepo=fedora-cisco-openh264
```

### The Remmina profile

- **Server**: one of the addresses the window shows, such as `192.168.1.20:3389`.
- **Username** and **Password**: the ones you set in mardp. Leave the domain empty.
- **Colour depth**: "GFX AVC420 (32 bpp)", for H.264. With another depth the session falls back to bitmaps, which are slower; the window shows which video path a session uses.
- **Security protocol negotiation** (advanced): "TLS". mardp accepts only TLS; "Automatic negotiation" works too.
- **Audio output mode** (advanced): "Local", to hear the Mac's sound on the client.

On the first connection Remmina shows the certificate's fingerprint and asks whether to accept it. Compare it with the fingerprint in mardp's window before you accept. If Remmina later says that the certificate changed and you did not make a new one, do not connect.

## What mardp does not protect against

- **Someone who knows the password controls the Mac** as the logged-in user: everything you can do at the Mac, they can do. Use a password you use nowhere else.
- **A client that accepts a changed certificate without looking can be intercepted** on a hostile network. The connection is encrypted with TLS, but the certificate is self-signed: only comparing the fingerprint shows that you talk to your Mac.
- mardp is meant for a local network. Do not open its port to the internet.

## The signing identity

macOS ties the Screen Recording and Accessibility permissions to an app's code signature. An app that is signed ad hoc gets a new signature with every build, and macOS asks for both permissions again. mardp's builds are therefore signed with one stable identity: a self-signed code-signing certificate named `mardp development` in your login keychain. Create it once:

```sh
scripts/make-identity.sh
```

The script makes a certificate and key with the system's LibreSSL, imports them into the login keychain (so that `codesign` may use the key without asking each time), and trusts the certificate for code signing only; macOS asks for your password for that last part. It changes nothing when the identity already exists.

`scripts/sign.sh <bundle>` signs with it, and stops with a message when it is missing; `scripts/build-app.sh` calls it. To use a different identity, set `MARDP_SIGN_IDENTITY` to its name.

To remove the identity, delete the certificate `mardp development` (and its key) from the login keychain in Keychain Access.

## Development

```sh
go build ./...
scripts/check.sh   # every check the project requires
```

`scripts/build-app.sh -debug` builds the app with Glutter's debug build. The tests never touch your settings: they use `MARDP_HOME` with a temporary folder.
