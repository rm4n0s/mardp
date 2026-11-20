# mardp

mardp lets you use a Mac from another computer over your home network. A remote desktop program on the other computer (Remmina on Linux) shows the Mac's screen, sends your keyboard and mouse to it, and plays its sound, well enough to watch a video that plays on the Mac.

It speaks RDP, the protocol Remmina already knows, so the other computer needs nothing special. mardp contains its own RDP server library (`rdp/`), written in Go from scratch, and its window is built with [Glutter](https://github.com/rm4n0s/glutter).

- [Installing on a new Mac](#installing-on-a-new-mac)
- [The first start](#the-first-start)
- [Connecting from Linux](#connecting-from-linux)
- [Using it](#using-it)
- [Watching video](#watching-video)
- [When something is wrong](#when-something-is-wrong)
- [Updating and removing](#updating-and-removing)
- [What mardp does not protect against](#what-mardp-does-not-protect-against)
- [For developers](#for-developers)

## Installing on a new Mac

You need a Mac with macOS 14 or newer (Apple silicon; Intel is built but not tested) and about 15 minutes. mardp is not in the App Store and has no installer to download: the Mac builds the app itself from the source, with one command. You paste a few lines into Terminal; nothing here needs programming.

**1. Get the source.** Open <https://github.com/rm4n0s/mardp> in a browser. (If GitHub says the page is not found, sign in with an account that may see it.) Click the **tags** link next to the branch name, and under the newest version click **zip**. Safari unpacks it into your Downloads folder, as a folder named like `mardp-1.0.1`. If your browser leaves a `.zip` file, double-click it.

**2. Open Terminal.** Press Command+Space, type `Terminal`, press Return. A window with a text prompt opens. To run a line from this guide, copy it, paste it into that window and press Return.

**3. Go into the folder:**

```sh
cd ~/Downloads/mardp-*
```

**4. Run the installer:**

```sh
sh scripts/install.sh
```

It checks the Mac and tells you in plain words if something is missing. On a new Mac two things are, and it stops at each one:

- **Apple's command line tools.** A window from Apple opens: press **Install** and wait until it is done (some minutes). Then run the line of step 4 again.
- **Go**, the language mardp is written in. Open <https://go.dev/dl/>, download the installer for macOS that ends in `.pkg` (the script says which one), double-click it and follow it to the end. Then run the line of step 4 again.

When nothing is missing, the same command goes on by itself:

- It makes a **signing identity** for this Mac. macOS asks for your Mac's password once. ([Why](#the-signing-identity).)
- It builds the app. The first time it downloads what it needs, which takes a few minutes.
- It copies **mardp** into Applications and opens it.

That is all. From now on mardp is an app like any other: open it from Applications or with Spotlight. You can delete the folder in Downloads; keep it if you want to [update](#updating-and-removing) later without downloading again.

`sh scripts/install.sh -check` only checks what is missing and changes nothing.

## The first start

mardp's window shows a short checklist, **Before the server can start**. Three things, once:

1. **Screen Recording**, so that mardp can show the screen and capture the sound. Press its button: macOS asks, and opens System Settings at the right place. Turn **mardp** on in the list. macOS may say that mardp must quit and reopen: let it.
2. **Accessibility**, so that the other computer's keyboard and mouse work on the Mac. Press its button and turn **mardp** on in that list too. Without it you can only watch.
3. **User name and password.** Press **Set** and choose a user name and a password of at least 8 characters. They are only for mardp: the other computer logs in with them. Do not reuse your Mac's password.

Then press **Start**. macOS asks once whether mardp may accept incoming connections: **Allow**.

The window now says **Listening** and shows two things you need on the other computer:

- **the addresses** to type into Remmina, such as `192.168.1.20:3389`;
- **the certificate fingerprint**, a long line of letters and digits that proves, at the first connection, that you reached your own Mac.

Two more questions may come later, both once: whether mardp may send notifications (it tells you when someone connects or disconnects), and, when you turn on "Open the app at login", macOS's note that a login item was added.

### macOS asks again about Screen Recording

From time to time macOS asks again whether an app that records the screen may go on: "mardp is requesting to bypass the system private window picker and directly access your screen and audio", with **Allow** and **Open System Settings**. It can come back after a few hours or after a month, and after an update. It has to be answered at the Mac itself, not from the other computer. A session that is running goes on while the question waits.

## Connecting from Linux

Install **Remmina** with its RDP plugin from your distribution's software center.

### Check the video decoder (Fedora)

For video mardp sends H.264, which Remmina decodes with Cisco's OpenH264. Fedora has two packages that provide it, and one of them is an empty stand-in that decodes nothing. Check which one you have:

```bash
rpm -q openh264 noopenh264
```

If it says `noopenh264` is installed, swap it for the real one:

```bash
sudo dnf swap noopenh264 openh264 --enablerepo=fedora-cisco-openh264
```

### Make the connection profile

In Remmina press **+** (new connection profile), choose the protocol **RDP**, and fill in:

| Field | What to put |
|---|---|
| **Server** | One of the addresses mardp's window shows, such as `192.168.1.20:3389` |
| **Username**, **Password** | The ones you set in mardp. Leave **Domain** empty |
| **Colour depth** | **GFX AVC420 (32 bpp)**. This is what makes video smooth; with another choice the picture is sent as plain images, which is much slower |
| **Resolution** | "Use client resolution", or the size you want |
| Advanced › **Audio output mode** | **Local**, to hear the Mac's sound on the Linux computer |
| Advanced › **Security protocol negotiation** | **Automatic negotiation** or **TLS** |

Press **Save and Connect**.

### The first connection

Remmina shows a certificate and asks whether to accept it. Compare its **fingerprint** with the one in mardp's window: they must be the same. Then accept. Remmina remembers it.

If Remmina ever says that the certificate **changed** and you did not press "New certificate" in mardp, do not connect: something else is answering in the Mac's place.

## Using it

**The window**'s title says which version runs, such as "mardp v1.0.2". Under it the window shows the state: **Stopped**, **Listening** (waiting for a connection) or **Connected**. While someone is connected it shows who, since when, the size of the desktop, how the picture is sent (**Video:**), the frames a second, the bitrate, the sound format and the delay, and a **Disconnect** button.

**Closing the window does not stop mardp.** It keeps running, with an icon in the menu bar (a screen on a stand: empty when stopped, with a dot when listening, filled when someone is connected). The icon's menu has Start or Stop, Disconnect, **Show window** and **Quit**. Clicking mardp in the Dock also brings the window back. To really stop it, choose **Quit** from the menu-bar icon. (Command+Q only hides the window.)

**One client at a time.** A second connection takes over from the first.

**Settings**, in the window. Each group says when its changes apply:

| Setting | What it does |
|---|---|
| User name and password | What the other computer logs in with |
| Open the app at login | mardp opens when you log in to the Mac |
| Start the server when the app opens | No need to press Start |
| Port, Address | Where mardp listens. Leave them unless you know you need to change them |
| Display | Which screen to show, on a Mac with more than one |
| Height cap | Sends a smaller picture than the Mac's screen, for example 1080 on a 4K display |
| Frame rate cap | 60 or 30 frames a second |
| Bitrate cap (Mbit/s) | The most the video may use on the network (20 by default). mardp chooses the rate under it by itself |
| Sound | **Off**, **On the client only** (the Mac itself stays silent), or **On both the client and the Mac** |
| Swap Ctrl and Command | Ctrl on the Linux keyboard acts as Command on the Mac, so Ctrl+C copies |
| New certificate | Makes a new certificate; Remmina then asks to accept the new fingerprint |

With both "Open the app at login" and "Start the server when the app opens" on, the Mac is reachable after every login without touching it.

**Recent events** lists the last 50 connections, disconnections with their reason, and failed logins.

**Wrong passwords.** After 5 failed logins from one computer, that computer has to wait 1 minute, then longer each time, up to 1 hour. A successful login forgets its failures.

### While you are away from the Mac

mardp runs inside your logged-in session. It cannot show the Mac's login window, and what a client sees while the Mac is locked is still being checked. Until then, if you want to reach the Mac while nobody is at it, keep it from locking: in System Settings › Lock Screen, set "Require password after screen saver begins or display is turned off" to **Never**. mardp keeps the display awake during a session. Anyone who is at the Mac can use it then, so do this only where that is acceptable.

## Watching video

1. In Remmina's profile, **Colour depth** must be **GFX AVC420 (32 bpp)**. When connected, mardp's window must say **Video: H.264 (AVC420) through the graphics pipeline**.
2. Play the video on the Mac, full screen.
3. For sound on the Linux computer: **Audio output mode: Local** in Remmina, and **Sound: On the client only** in mardp (the default).

Remmina decodes the video with the Linux computer's processor. If the picture stutters, make its work lighter in mardp's settings:

- **Height cap: 1080** when the Mac's screen is larger than that (a 4K display has four times the pixels of 1080p).
- **Frame rate cap: 30**. Films are 24 to 30 frames a second anyway.

If the network is slow (Wi-Fi far from the router, a VPN), lower the **Bitrate cap**.

## When something is wrong

| What you see | What to do |
|---|---|
| The picture turns into smears or blocks as soon as something moves | Update to version 1.0.1 or newer ([Updating](#updating-and-removing)): 1.0.0 lowered the quality far too much for a client that decodes slower than the Mac captures. Then see [Watching video](#watching-video) |
| The picture is slow, like a slideshow | mardp's window says how the picture is sent. If **Video:** is not "H.264 (AVC420) …", set Remmina's **Colour depth** to **GFX AVC420 (32 bpp)**, and on Fedora check the [video decoder](#check-the-video-decoder-fedora) |
| The keyboard and mouse do nothing; the window says **View only** | Give mardp **Accessibility** (System Settings › Privacy & Security › Accessibility), then press **Stop** and **Start** |
| No sound on the Linux computer | Remmina: **Audio output mode: Local**. mardp: **Sound** is not **Off** |
| Remmina cannot connect | Both computers on the same network? mardp says **Listening**? The address typed as the window shows it, with `:3389`? The question about incoming connections answered with **Allow** (System Settings › Network › Firewall › Options)? |
| "The server did not start …" with a port in use | Another program uses port 3389. Quit it, or choose another **Port** in mardp's settings and use that number in Remmina |
| The checklist came back | macOS withdrew a permission (it happens after an update of macOS or of mardp). Turn mardp on again in the list the button opens |
| The login is refused although the password is right | Too many wrong tries from that computer: wait a minute and try again |

For a closer look, start mardp with a log file and read it:

```sh
open -n /Applications/mardp.app --args -log-file /tmp/mardp.log
```

## Updating and removing

**Updating.** Quit mardp (menu-bar icon › Quit). Download the newer version as in [step 1](#installing-on-a-new-mac) and run the same two lines again:

```sh
cd ~/Downloads/mardp-*
```

```sh
sh scripts/install.sh
```

(With more than one `mardp-…` folder in Downloads, type the new one's full name in the first line.) The settings, the password, the certificate and the permissions stay. macOS may ask about Screen Recording once more.

The window's title shows the version that runs; `/Applications/mardp.app/Contents/MacOS/mardp -version` prints the one that is installed.

**Removing.**

1. Quit mardp, and drag **mardp** from Applications to the Trash.
2. Its settings, the password's hash and the certificate are in `~/Library/Application Support/mardp/`: delete that folder (in the Finder: Go › Go to Folder…, paste the path).
3. In System Settings › Privacy & Security, remove mardp from **Screen & System Audio Recording** (**Screen Recording** on macOS 14) and from **Accessibility**.
4. In the app Keychain Access, under login › My Certificates, delete **mardp development**.

## What mardp does not protect against

- **Someone who knows the password controls the Mac** as you: everything you can do at the Mac, they can do. Use a password you use nowhere else.
- **A client that accepts a changed certificate without looking can be intercepted** on a hostile network. The connection is encrypted with TLS, but the certificate is self-signed: only comparing the fingerprint shows that you talk to your Mac.
- mardp is meant for a local network. Do not open its port to the internet.

## For developers

The design is in [docs/SPEC.md](docs/SPEC.md), the plan in [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md) and what is done in [docs/IMPLEMENTED.md](docs/IMPLEMENTED.md). The measured numbers are in [docs/PERFORMANCE.md](docs/PERFORMANCE.md); what still waits for a check on the owner's own machines is in [docs/MANUAL-CHECKS.md](docs/MANUAL-CHECKS.md).

Building needs Go 1.27 and Xcode's command line tools (`xcode-select --install`); nothing from Homebrew.

```sh
go build ./...
scripts/check.sh       # every check the project requires
scripts/build-app.sh   # build/mardp.app, signed; -debug for Glutter's debug build
scripts/install.sh     # the same, then copied to /Applications and opened
```

The tests never touch your settings: they use `MARDP_HOME` with a temporary folder. `mardp -headless` runs the server without a window, with the same settings.

### The signing identity

macOS ties the Screen Recording and Accessibility permissions to an app's code signature. An app that is signed ad hoc gets a new signature with every build, and macOS asks for both permissions again. mardp's builds are therefore signed with one stable identity: a self-signed code-signing certificate named `mardp development` in your login keychain. `scripts/install.sh` creates it when it is missing; by hand:

```sh
scripts/make-identity.sh
```

The script makes a certificate and key with the system's LibreSSL, imports them into the login keychain (so that `codesign` may use the key without asking each time), and trusts the certificate for code signing only; macOS asks for your password for that last part. It changes nothing when the identity already exists.

`scripts/sign.sh <bundle>` signs with it, and stops with a message when it is missing; `scripts/build-app.sh` calls it. To use a different identity, set `MARDP_SIGN_IDENTITY` to its name.
