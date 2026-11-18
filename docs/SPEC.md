# mardp: Specification

Status, 2026-11-19: the owner answered the open decisions (§3, D12, D14 and D16 to D23) and asked for the implementation steps, which are in `docs/IMPLEMENTATION.md`. Nothing is implemented, and work on the steps starts when the owner says so.

mardp makes a Mac an RDP host: an RDP client on another machine (first of all Remmina on Linux) shows the Mac's desktop, controls its keyboard and mouse, and plays its sound. It is a desktop app written in Go, with a user interface built on [Glutter](https://github.com/rm4n0s/glutter), and it contains its own RDP server library, written from scratch.

The measure of success is watching a video on the Mac from the Linux machine: smooth picture, sound without dropouts, picture and sound together.

## 1. Goals

1. Remmina (FreeRDP) on Linux connects to the Mac over the LAN, with a user name and a password, over TLS.
2. The remote desktop is good enough to watch video: 1080p at 60 frames a second, low delay, sound in sync (§14 gives the numbers).
3. Keyboard, mouse, wheel and the mouse pointer's shape work as on a local machine.
4. The RDP server is a library of this repository, written from scratch in Go, with no code from FreeRDP, xrdp or IronRDP. It has no cgo, no Glutter and no macOS code, so it builds and its tests run on Linux too.
5. The app is a normal Mac app: a window that shows the state and the settings, a menu-bar icon, and a checklist that leads the user through the permissions macOS asks for.
6. An idle desktop costs almost nothing: no frames are captured, encoded or sent while nothing on the screen changes.

## 2. Non-goals for 1.0

- The login window and the lock screen. mardp runs inside the session of the logged-in user (§19, R3).
- More than one client at a time, and more than one monitor in one session.
- RDP over UDP (MS-RDPEUDP), RD Gateway, RemoteApp, smart cards, printers, drives, file transfer, the microphone of the client.
- A "curtain": the Mac's own screen shows what the remote user does.
- Connections from the internet. mardp is made for a LAN or a VPN; it does not open ports on a router.
- Hosts other than macOS. The library is portable, the host is not.
- Legacy RDP security (RC4 "Standard RDP Security"). Only TLS (§8).

Features planned after 1.0 are in §17 (M7): Network Level Authentication, the clipboard, AVC444, a virtual display that follows the client's window size.

## 3. Decisions log

"Owner" is the user; "proposed" means written by Claude and confirmed when the owner accepts this document.

| # | Decision | Who, when |
|---|---|---|
| D1 | mardp is an RDP host for macOS, for the Remmina client on Linux | Owner, 2026-11-18 |
| D2 | Written in Go | Owner, 2026-11-18 |
| D3 | The project is `github.com/rm4n0s/mardp`, BSD-3-Clause | Owner, 2026-11-18 |
| D4 | The specification is written before any implementation | Owner, 2026-11-18 |
| D5 | mardp is a desktop app, with its UI on Glutter | Owner, 2026-11-19 |
| D6 | The RDP library is written from scratch | Owner, 2026-11-19 |
| D7 | It is optimized: good enough for watching video and listening to its audio | Owner, 2026-11-19 |
| D8 | Video is H.264 inside the RDP graphics pipeline (MS-RDPEGFX, AVC420), encoded by the Mac's hardware encoder through VideoToolbox. It is the only codec of RDP that reaches 1080p60 at LAN bitrates with little CPU, and FreeRDP decodes it | Proposed |
| D9 | The screen and the system sound are captured with ScreenCaptureKit, which delivers frames only when the screen changes and hands them to the encoder without a copy | Proposed |
| D10 | Sound is the RDPSND channel (MS-RDPEA): AAC when the client has it, 16-bit PCM otherwise | Proposed |
| D11 | The library does not capture or encode H.264 or AAC: the app gives it encoded data. That keeps the library pure Go and portable | Proposed |
| D12 | TLS only, with a user name and password of mardp's own (not the Mac account's password). NLA comes after 1.0: it adds CredSSP and NTLM and changes how the password is stored | Owner, 2026-11-19 |
| D13 | One session at a time. A new client that logs in replaces the connected one | Proposed |
| D14 | The macOS frameworks that Glutter does not cover (ScreenCaptureKit, VideoToolbox, AudioToolbox, Core Graphics events) are called through cgo, in the packages under `internal/mac` only, as desktop-mirror does: the compiler checks the calls against Apple's headers. The price: a C compiler to build, and a `CGO_ENABLED=0` build gets the stubs | Owner, 2026-11-19 |
| D15 | Dependencies: the standard library, `github.com/rm4n0s/errors`, `github.com/rm4n0s/glutter` and what Glutter brings in. Anything else is asked for first | Proposed |
| D16 | **The real client for tests runs in a Linux virtual machine on this Mac, in UTM** (§15.3). Claude gets no SSH access to the owner's Linux machine ("open a linux vm to test it works, I am not going to give you ssh access to a linux server"; "use utm", after a comparison with Lima, VMware Fusion and Parallels: UTM gives a full Linux desktop with sound) | Owner, 2026-11-19 |
| D17 | Sound plays on the client only by default, if R2 (§19) shows it can be done | Owner, 2026-11-19 |
| D18 | The desktop is the display scaled to fit the client's size (§9.2). A virtual display of exactly the client's size uses a private API of macOS and is a decision of its own, after 1.0 | Owner, 2026-11-19 |
| D19 | Modifiers: Ctrl is Control, Alt is Option, Super is Command; a setting swaps Ctrl and Command | Owner, 2026-11-19 |
| D20 | The minimum is macOS 14 (system sound through ScreenCaptureKit needs 13; 14.2 has the audio tap of R2) | Owner, 2026-11-19 |
| D21 | `rdp` is a public package of this repository that other programs may import, with no promise of a stable API before mardp 1.0 | Owner, 2026-11-19 |
| D22 | Development builds are signed with one stable identity: a self-signed certificate in the owner's keychain, created once by the owner (§7.1); the README gives the steps | Owner, 2026-11-19 |
| D23 | Glutter's version is the newest at the time of M2 (v1.17.7 on 2026-11-19) | Owner, 2026-11-19 |

## 4. Architecture

```
            Linux                                      Mac
 ┌──────────────────────┐   TCP 3389, TLS   ┌───────────────────────────────────────────┐
 │ Remmina (FreeRDP)    │ ◄───────────────► │ rdp        the RDP server library (Go)    │
 │  H.264 decode        │   video: GFX/AVC  │ internal/host   one session's pipeline    │
 │  sound playback      │   sound: RDPSND   │ internal/mac/*  capture, encoders, input  │
 │  keyboard and mouse  │   input: fast-path│ internal/ui     the Glutter app           │
 └──────────────────────┘                   └───────────────────────────────────────────┘
```

### 4.1 Packages

| Package | What it is | Imports |
|---|---|---|
| `cmd/mardp` | `main`: flags, wiring, starts the app. Thin | everything below |
| `rdp` | The public API of the RDP server library (§6) | standard library, `errors` |
| `rdp/internal/...` | The protocol layers, one package each: `wire` (reading and writing bytes), `tpkt`, `x224`, `mcs`, `gcc`, `sec` (client info, licensing), `caps`, `fastpath`, `input`, `pointer`, `bitmap`, `svc` (static virtual channels), `dynvc`, `gfx`, `rdpsnd` | standard library, `errors` |
| `rdp/rdptest` | A small RDP client for tests: it connects, logs in, decodes what the server sends and sends input | `rdp/internal/...` |
| `internal/host` | The session pipeline: connects a session of `rdp` to a screen source, a video encoder, a sound source, an input sink. Flow control and rate control live here (§9.5). Defines the interfaces of those parts and has fakes of them | `rdp`, `errors` |
| `internal/mac/capture` | ScreenCaptureKit: frames and system sound | cgo, standard library, `errors`, `internal/host` (the types it implements) |
| `internal/mac/vtenc` | VideoToolbox: H.264 | cgo, standard library, `errors`, `internal/host` (the types it implements) |
| `internal/mac/aacenc` | AudioToolbox: AAC | cgo, standard library, `errors`, `internal/host` (the types it implements) |
| `internal/mac/input` | Core Graphics events: keyboard and mouse; the pointer's image | cgo, standard library, `errors`, `internal/host` (the types it implements) |
| `internal/mac/system` | Permissions (Screen Recording, Accessibility), keeping the display awake, the list of displays | cgo, standard library, `errors`, `internal/host` (the types it implements) |
| `internal/config` | Settings file, password hash, TLS certificate | standard library, `errors` |
| `internal/ui` | The Glutter app: window, tray, settings. The only package that imports Glutter | `glutter`, `internal/host`, `internal/config` |
| `internal/archtest` | Tests that check this table: who may import what, and that `import "C"` appears only under `internal/mac` | |

Rules:

- `rdp` never imports `internal/...` of mardp, Glutter or cgo. `GOOS=linux go build ./rdp/...` and `go test ./rdp/...` always pass.
- Every package under `internal/mac` has a `_darwin.go` file with the real code and a stub for other systems whose constructors fail with a tagged error (`CaptureUnsupported` and the like), so `go build ./...` and the tests that use fakes pass on Linux.
- `rdp/rdptest` is imported only by test files. A package under `internal/mac` may import another one there.
- `spikes/` (M0 only) may import anything, cgo and Glutter included; nothing else imports it. A package directory that is not in the table fails `internal/archtest`, so a new package means a new row here.
- `internal/host` knows nothing about macOS: it works on interfaces. Every test of the pipeline runs with fakes (a test-pattern screen, a tone generator, a recording input sink) and needs no permission and no display.

### 4.2 One session, from capture to socket

```
ScreenCaptureKit ──frames (IOSurface, NV12)──► VideoToolbox ──H.264──► host ──► rdp: GFX ──┐
ScreenCaptureKit ──sound (48 kHz float)──► AAC or PCM ───────────────► host ──► rdp: RDPSND ┼─► TLS ─► TCP
pointer image, position ─────────────────────────────────────────────► host ──► rdp: pointer┘
TCP ─► TLS ─► rdp: input events ─► host ─► Core Graphics events
```

- A captured frame never enters Go memory on the H.264 path. ScreenCaptureKit gives an IOSurface, the C side hands it to the encoder, and only the encoded bytes cross into Go: one callback per frame.
- Whether a frame is encoded or skipped is one atomic flag that Go writes and the C callback reads, so skipping costs nothing.
- Each session has one writer goroutine that owns the TLS connection. It takes from two queues: control, input replies and sound first, video second. Sound never waits behind a large video frame.

## 5. Protocol scope

The library implements the server side of these Microsoft Open Specifications. Each is read from Microsoft's published document; the test vectors come from the annotated examples in them and from recorded sessions (§15).

| Document | What | 1.0 |
|---|---|---|
| MS-RDPBCGR | Connection, capabilities, fast-path output and input, pointer, legacy bitmap updates, errors, disconnect | yes |
| T.123 (TPKT), X.224, T.125 (MCS), T.124 (GCC) | The transport layers under RDP, only the parts RDP uses | yes |
| MS-RDPELE | Licensing: only the "no license needed" answer | yes |
| MS-RDPEDYC | Dynamic virtual channels (the `drdynvc` channel), versions 1 to 3, without compression of data | yes |
| MS-RDPEGFX | Graphics pipeline: capability exchange, surfaces, frames, frame acknowledgement, AVC420; uncompressed and Planar as its fallback codecs | yes |
| MS-RDPEA | Sound (`rdpsnd`): formats, training, Wave2, Wave Confirm | yes |
| MS-RDPEGDI | Only the Planar codec (used by the fallbacks) | yes |
| MS-CSSP, MS-NLMP | NLA: CredSSP with NTLMv2 | after 1.0 |
| MS-RDPECLIP | Clipboard, text | after 1.0 |
| MS-RDPEDISP | The client asks for a new desktop size | after 1.0 |

### 5.1 Connection sequence

1. **X.224 Connection Request / Confirm**, with the RDP negotiation: the client lists the security protocols it accepts, the server selects TLS (`PROTOCOL_SSL`). A client that does not offer TLS gets the negotiation failure `SSL_REQUIRED_BY_SERVER`.
2. **TLS handshake** (§8).
3. **MCS Connect Initial / Response** with the GCC blocks: the server reads the client's core data (desktop size, colour depth, keyboard layout, client name, the flag that says it supports the graphics pipeline), its channel list and its monitor block, and answers with the server core, security (no RDP encryption: TLS does it) and network blocks. Static channels the server opens: `drdynvc` and `rdpsnd`. Others the client asks for are accepted and ignored.
4. **MCS Erect Domain, Attach User, Channel Joins.**
5. **Client Info**: user name, password, time zone, flags. The login of §8.2 happens here.
6. **Licensing**: the server sends the "valid client" status. There is no license server.
7. **Demand Active / Confirm Active**: capabilities (§5.2).
8. **Synchronize, Control, Font List / Font Map.** After Font Map the session is open and the library calls the app (§6).

Limits before login: at most 8 connections that have not logged in, 10 seconds from the TCP connection to Client Info, and every length field read from the client is checked against the size of the PDU and a maximum. A client that breaks a limit is disconnected. No client input may make the library panic (§15.4).

### 5.2 Capabilities

- Desktop size: the server decides it (§9.2) and states it in Demand Active; the client follows.
- Colour depth 32. Fast-path output, with fragmentation and the largest request size the client states.
- Input: scancodes, Unicode, extended mouse buttons, horizontal wheel, fast-path input.
- Pointer: colour pointers with alpha (32 bpp), pointer cache, large pointers when the client has them.
- Virtual channels, sound, frame acknowledgement, Surface Commands.
- No orders, no glyph or offscreen caches, no RemoteFX: drawing is the graphics pipeline, or bitmap updates as the fallback.

### 5.3 What the client may send during a session

Input events (fast-path, and the slow-path form too), Suppress Output (the client's window is minimized: capture pauses; it resumes with a key frame), Refresh Rect (the server sends a key frame), Shutdown Request, a disconnect. Every other PDU is parsed as far as its header and ignored.

### 5.4 Ending a session

The server ends a session with Set Error Info (the reason) and an MCS Disconnect Provider Ultimatum: logged out by the app's user, replaced by another client (D13), server stopped, login failed. A connection that goes silent is closed by TCP keep-alive (15 s idle, 5 s interval, 3 probes).

## 6. The library's API

The exact Go names are decided in the implementation; this is the shape.

- `rdp.NewServer(config)` and `Server.Serve(ctx, listener)`. The config holds the TLS certificate, the `Authenticator` (a function that gets the user name and password and answers yes or no), the `Handler`, the limits of §5.1 and a `slog.Logger`.
- `Handler.SessionOpened(ctx, session)` is called when a client has finished the connection sequence. The session is over when its context is cancelled; `Session.Close(reason)` ends it from the app.
- `Session.Client()`: what the client is: name, address, the desktop size it asked for, keyboard layout, whether it decodes H.264 (AVC420), the audio formats it has, the largest pointer it shows.
- **Video.** `Session.Video()` gives one of two things, chosen by the library from the client's capabilities:
  - *H.264*: `SendFrame(annexB, keyFrame)`. The library wraps it as a GFX frame (Start Frame, Wire To Surface with the AVC420 stream, End Frame).
  - *Bitmaps*: `SendRects(image, rects)`. The library compresses the rectangles (Planar) and sends them through GFX, or as fast-path Bitmap Updates when the client has no graphics pipeline.
  - Both report flow: `Backlog()` (frames sent and not yet acknowledged, and the age of the oldest) and callbacks for "the client wants a key frame" and "output suppressed / resumed".
- **Sound.** `Session.Audio()`: the negotiated format (AAC, PCM or none) and `Write(data, timestamp)`. It reports the delay the client's confirmations show.
- **Pointer.** `SetShape(image, hotspot)` (the library caches shapes and sends a cached index when a shape repeats), `Hide()`, `Move(x, y)`.
- **Input.** The app gives an `InputSink` with `Key(scancode, extended, down)`, `Unicode(rune, down)`, `MouseMove(x, y)`, `MouseButton(button, down, x, y)`, `Wheel(dx, dy)`, `SyncLocks(caps, num, scroll)`. Calls arrive in the order the client sent them, on one goroutine.
- Every method that does I/O takes a `ctx`. Every failure is an `errors` Tag of its own with a route test (CLAUDE.md).
- `Write` and `SendFrame` do not block on the network: they queue for the session's writer (§4.2) and fail with a tagged error when the queue is full, which is the app's signal to skip (§9.5).

## 7. The host on macOS

### 7.1 Permissions

macOS asks the user for two permissions, and mardp cannot grant them to itself:

- **Screen Recording** (also covers the system sound): without it there is no picture.
- **Accessibility**: without it mardp cannot press keys or move the mouse.

The app shows a checklist (§12.2) with the state of each and a button that opens the right pane of System Settings. The server can start without Accessibility (view only, and the window says so); it does not start without Screen Recording.

macOS ties these permissions to the app's code signature. A build that is signed ad hoc gets a new identity each time, and macOS then asks again. Development builds are therefore signed with one stable signing identity (D22).

### 7.2 The display

- The captured display is the main display, or the one chosen in the settings.
- While a client is connected mardp wakes the display and holds a power assertion that keeps it awake; the assertion is released when the session ends. Whether a Mac whose monitor is asleep or absent still delivers frames is checked first (§19, R1).
- The pointer is not drawn into the captured frames. Its shape and position are sent separately (§10.3), so the client draws it with no delay.
- mardp's own window is captured like any other window.

### 7.3 Starting and running

- mardp is a normal app in `/Applications` (or run from the build folder). It runs in the user's session. "Open at login" is a setting.
- Closing the window keeps the app and the server running, with the menu-bar icon. Quit stops the server and ends the session.
- The server listens on TCP port 3389 by default, on all interfaces or on one chosen address. A port above 1024 needs no administrator rights. The first time, macOS asks whether the app may accept incoming connections.

## 8. Security

### 8.1 TLS

- TLS 1.2 or newer, from Go's `crypto/tls`.
- The certificate is self-signed, RSA 2048 (the type every RDP client accepts), created on the first start and kept in mardp's folder with mode 0600 (§13). The window shows its SHA-256 fingerprint, so the user can compare it with what Remmina shows on the first connection.
- A new certificate is made only when the user asks ("New certificate" in the settings), since clients remember the old one.

### 8.2 Login

- One user name and one password, set in the app. There is no default password, and the server does not start until one is set (at least 8 characters).
- The password is stored as a PBKDF2-HMAC-SHA256 hash with a random salt (`crypto/pbkdf2` of the standard library), never in clear text, never in a log.
- The client sends the user name and password in Client Info, inside TLS. The comparison takes constant time. A client that sends none, or wrong ones, gets the logon-failure error and is disconnected, so Remmina asks again.
- After 5 failed logins from one address, that address waits: 1 minute, doubling to at most 1 hour. Failed logins are shown in the window and as a notification.
- When NLA is added (after 1.0), the stored form of the password changes, because NTLM needs the NT hash; that is decided then (D12).

### 8.3 What mardp does not protect against

Someone who knows the password controls the Mac as the logged-in user. A client that accepts a changed certificate without looking can be intercepted on a hostile network. Both are stated in the README.

## 9. Video

### 9.1 Negotiation

1. The client says in its core data that it supports the graphics pipeline, and joins `drdynvc`.
2. The server opens the dynamic channel `Microsoft::Windows::RDS::Graphics`. The client advertises its capability sets; the server confirms the newest version both have (8.1 and the 10.x versions) and notes whether H.264 is allowed (the AVC420 flag in 8.1, the absence of "AVC disabled" in 10.x).
3. The server sends Reset Graphics, Create Surface and Map Surface To Output for one surface of the desktop's size.

Three outcomes, best first:

| Client has | Video path | Good for video |
|---|---|---|
| Graphics pipeline with H.264 | AVC420 | yes |
| Graphics pipeline, no H.264 | Planar (lossless) of the changed rectangles | no: desktop work only |
| No graphics pipeline | Fast-path Bitmap Updates of the changed rectangles | no: desktop work only |

The window shows which path the session uses. In Remmina the H.264 path needs the colour depth "GFX AVC420 (32 bpp)" in the profile, and a FreeRDP built with H.264; the README says so, and §19 R5 checks the owner's machine.

GFX data is wrapped in RDP 8 segments as the protocol requires, without compressing it: H.264 does not compress further, and the time is better spent elsewhere.

### 9.2 The desktop's size

- The client asks for a size. The server answers with the display's picture scaled down to fit inside that size, keeping the display's aspect ratio, never scaled up, with even width and height. The scaling is done by ScreenCaptureKit on the GPU.
- The settings can cap the height (for example 1080) to save bandwidth on a large display.
- The size is fixed for the session. A client that wants another size reconnects. Following the client's window is after 1.0 (D18).

### 9.3 The H.264 stream

- Hardware encoder, real-time mode, no B-frames (no frame reordering: every frame is shown as soon as it arrives).
- High profile; if the interoperability check of M3 shows a client that cannot decode it, Main or Constrained Baseline (the check decides, and this line is updated).
- The first frame is a key frame. After it only P-frames: the connection is TCP, nothing is lost, so periodic key frames would only cause bursts. A key frame is sent again when the client asks (Refresh Rect), when output resumes, and when the encoder is restarted.
- Colour: 4:2:0, with the matrix and range that MS-RDPEGFX states for AVC420 (expected: BT.709, full range). A colour-bar test against a real client confirms it.
- 4:2:0 softens coloured text a little. AVC444, which keeps full colour, is after 1.0.

### 9.4 Frame rate

- The cap is 60 frames a second (a setting: 30 or 60).
- ScreenCaptureKit delivers a frame only when the screen changed. A still screen sends nothing.

### 9.5 Flow control and rate control

The goal is that the client always shows the newest picture, never an old one that waited in a queue.

- The client acknowledges every frame. The host knows how many frames are on their way and how old the oldest is.
- **Skip before encoding.** When more than 2 frames are unacknowledged, or the session's video queue is not empty, the next captured frame is not encoded. An encoded frame is never thrown away, because the frames after it depend on it.
- **Bitrate.** It starts at 8 Mbit/s and moves between 1 Mbit/s and the cap of the settings (default 20 Mbit/s). It rises slowly while acknowledgements come back fast, and drops by a quarter when frames are skipped or the acknowledgement delay grows. The encoder takes the new bitrate without a restart.
- Lower quality comes before a lower frame rate: for video, a soft frame is better than a missing one.
- TCP: `TCP_NODELAY` is on, and one frame is written with one write, so it leaves in as few packets as possible.
- Sound is never skipped to help video; it is small, and it goes first (§4.2).

### 9.6 The fallback paths

The host asks ScreenCaptureKit for BGRA frames and the changed rectangles of each frame, and gives them to `SendRects`. The frame rate cap is 30. These paths exist so that every client gets a picture; they are not tuned for video.

## 10. Sound, input, pointer

### 10.1 Sound

- The source is the system sound from ScreenCaptureKit, 48 kHz stereo, without mardp's own sounds.
- The server offers, in this order: AAC (48 kHz stereo, 192 kbit/s, encoded by AudioToolbox), then 16-bit PCM at 48 kHz stereo. The client picks from what it has. PCM is about 1.5 Mbit/s, fine on a LAN, and every client has it.
- Sound is sent in packets of about 20 ms with the Wave2 PDU (or WaveInfo and Wave for a client older than version 8 of the channel), each with its timestamp. The client confirms each packet; the host measures the delay from that.
- When the confirmed delay exceeds 500 ms, the host drops queued sound until it is back under 100 ms: a short gap is better than sound that trails the picture.
- When nothing plays, nothing is sent.
- **Where the sound plays**: "on the client only" or "on both". Playing on the client only means muting the Mac's own output while a client is connected and restoring it afterwards; how that is done, and whether capture still works while muted, is checked first (§19, R2). The default is "on the client only" (D17).
- The setting "Sound: off" opens no sound channel.

### 10.2 Picture and sound together

The server sends both as soon as it has them. The client shows a frame when it arrives, and plays sound through a small buffer of its own. The server does not delay video to match. The skew is measured by hand with a test video (a clap or a beep with a flash); the target is in §14. If the measured skew is larger, the fix is decided then and written here.

### 10.3 Pointer

- The pointer's image is read from the system when it changes and sent as a pointer shape with alpha; the library caches shapes. A hidden pointer (for example over a video in full screen) is sent as "hidden".
- The client draws the pointer itself, so moving the mouse feels local. The server sends a position only when something on the Mac moves the pointer.
- If the image cannot be read on some macOS version, the fallback is drawing the pointer into the frames and hiding the client's pointer.

### 10.4 Keyboard

- The client sends PC scancodes. A table maps them to macOS virtual key codes; the events are posted with Core Graphics. Unicode events (text the client types through an input method) are posted as text.
- Modifiers, by default: Ctrl is Control, Alt is Option, the Super (Windows) key is Command. A setting swaps Ctrl and Command, for people who want Ctrl+C to copy on the Mac (D19).
- Caps Lock, Num Lock and Scroll Lock follow the client's state when it sends a synchronize event.
- When a session ends, every key and button that is still down is released.

### 10.5 Mouse

- Positions are scaled from the session's desktop size to the display's points. Buttons: left, right, middle, back, forward. The wheel scrolls vertically and horizontally.
- Double clicks are counted by the host from the time and distance between clicks, as macOS expects in the event.

## 11. Performance design

D7 makes speed a requirement, so the rules are part of the specification:

1. **No copies of pictures in Go.** On the H.264 path a frame goes from the capture to the encoder as an IOSurface (§4.2).
2. **No allocation per frame or per sound packet** in the steady state, in `rdp` and in `internal/host`: buffers come from pools sized at the start of the session. Benchmarks of the PDU writers assert 0 allocations per operation.
3. **One cgo call or callback per frame** and per sound packet, never per rectangle or per NAL unit.
4. **Nothing waits behind the network in the capture path**: capture and encoding never block on a socket; they see only the skip flag.
5. **The hardware does the work**: scaling on the GPU, colour conversion in the capture, H.264 in the hardware encoder, AES-GCM of TLS with the CPU's instructions.
6. **Idle is free**: no timers that wake up many times a second while the screen is still and nothing plays.
7. Every number of §14 has a measurement that produces it (§15.5), and a change that makes one worse is not merged without a reason written in `docs/PERFORMANCE.md`.

## 12. The app (Glutter)

### 12.1 Shape

- One window and a menu-bar icon (`desktop.Tray`). Glutter's own widgets; the look follows the system's light or dark mode.
- Only `internal/ui` imports Glutter. The UI is a view of the state of `internal/host` and `internal/config`; it has no logic of its own, and its tests use `gluttertest` with a fake host.
- The app is built into `mardp.app` with Glutter's `cmd/glutter-macos`.

### 12.2 The window

- **Status**: stopped, listening, or connected. When listening: the addresses to type into Remmina (each IP of the Mac with the port) and the certificate's fingerprint. When connected: the client's name and address, since when, the desktop size, the video path (§9.1), frames a second, bitrate, the sound format, the delay; and a "Disconnect" button.
- **Start / Stop** of the server.
- **Checklist**, shown until everything is done: Screen Recording, Accessibility, a password. Each line has its state and a button (open System Settings, or set the password). The lines update by themselves when the user grants a permission.
- **Settings**: user name and password; port and address; start the server when the app opens; open the app at login; display; height cap; frame rate cap (30 or 60); bitrate cap; sound (off, client only, both); swap Ctrl and Command; new certificate.
- **Recent events**: connections, disconnections with their reason, failed logins.

### 12.3 The menu-bar icon

It shows the state (stopped, listening, connected). Its menu: the state in words, Start or Stop, Disconnect (when connected), Show window, Quit.

### 12.4 Notifications

A client connected; a client disconnected; failed logins from an address (once per waiting period, not once per attempt).

## 13. Configuration and files

- Everything is under `~/Library/Application Support/mardp/`: `config.json` (the settings and the password hash), `cert.pem` and `key.pem` (mode 0600). The folder is created with mode 0700.
- The environment variable `MARDP_HOME` replaces that folder. Every test and every script uses it with a temporary folder: no test reads or writes the user's real settings.
- `config.json` has a version number; a newer program upgrades an older file, and refuses a file from a newer program with a clear message.
- `mardp -headless` runs the server without a window, with the same settings file, for tests and measurements. `mardp -version` prints the version.
- Logs go through `slog` to the standard error; `-log-file` writes them to a file. Passwords, key material and the contents of the screen never appear in a log.

## 14. Performance targets

Measured on the owner's Apple silicon Mac and Linux machine on the same LAN, with a 1920×1080 session playing a 1080p60 video in full screen, H.264 path. They are targets for 1.0; the first measurements (M6) may correct a number, with the reason recorded here.

| What | Target |
|---|---|
| Frames delivered (acknowledged by the client) | at least 57 of 60 a second, over 5 minutes |
| Delay added by the host, from the capture of a frame to its last byte written to the socket | at most 25 ms for 95% of frames |
| Delay from screen to screen (by hand, with a clock on the Mac's screen) | at most 100 ms |
| Sound | no dropout in 10 minutes |
| Picture and sound apart | at most 100 ms |
| CPU of the mardp process during the video | under 60% of one core |
| CPU with a still screen and no sound, client connected | under 2% of one core |
| Memory of the mardp process during the video | under 300 MB, and not growing |
| Bitrate of the video | at most the cap (default 20 Mbit/s) |
| From "Connect" in Remmina to the first picture | under 2 seconds |

## 15. Testing

### 15.1 The library, without a network

Every PDU has a table-driven test that writes it and compares the bytes, and parses bytes and compares the fields. The expected bytes come from the annotated examples of Microsoft's documents and from recorded sessions with FreeRDP, kept in `testdata/` with a note of where each came from.

### 15.2 The library, both ends

`rdp/rdptest` connects to the server over TLS on `127.0.0.1` and plays whole sessions: the connection sequence, a failed login, video on each of the three paths, sound in both formats, pointer shapes, every kind of input, Suppress Output, Refresh Rect, each way of ending. Since both ends are written here, these tests prove consistency, not compatibility; §15.3 proves compatibility.

### 15.3 A real client

The real client is FreeRDP, the program Remmina uses underneath, and Remmina itself, in a Linux virtual machine on the same Mac, run by [UTM](https://mac.getutm.app) (D16).

- **The machine.** A Fedora desktop guest for arm64 (the owner's Linux is Fedora, so the guest gets the same Remmina and FreeRDP packages and the same H.264 support), on UTM's QEMU backend with hardware virtualization, a sound device, and the accelerated display. It has a desktop, so the owner can open Remmina in the machine's window and look and listen; and it has FreeRDP's command-line client, which the tests drive.
- **How the tests reach it.** Through UTM's own command, `utmctl` (start, stop, run a program in the guest, copy a file in and out, ask for the guest's address), which talks to the guest agent inside the machine. `scripts/vm.sh` wraps it. The first time, macOS asks whether the terminal may control UTM; the owner allows it once.
- **Setting it up** is a step of M0, done once, partly by hand: UTM is an app the owner agrees to download; the guest is installed from Fedora's image; then Remmina, FreeRDP, the guest agent and the test tools are installed in it. `docs/VM.md` records every step and the versions, so the machine can be made again. Nothing of the owner's goes into the machine: no keys, no accounts, no shared folders but the tests' own temporary one.
- **The tests** carry the build tag `mardp_freerdp`. They start the library's server (or `mardp -headless`) with a test pattern and a tone, start FreeRDP in the guest against it under a virtual X display, and check from FreeRDP's log and exit status that it logged in, negotiated the expected video path and sound format, decoded frames and confirmed sound. They are skipped with a clear message when the machine is not running, and are not part of the default test run.
- **Sound and picture in the tests.** No one has to listen: FreeRDP plays into a null sink inside the guest, the test records what was played there and compares it with the tone the server sent, so a wrong format, a dropout or silence fails the test. The same is done for the picture with a screenshot inside the guest, which is also the colour check of §9.3.
- **Listening by ear in the machine.** The guest's sound comes out of the Mac's own output. mardp would capture it again and send it back, a loop. The sound source therefore leaves UTM's process out of what it captures, always: a virtual machine that plays the Mac's own sound back is never what the remote user wants to hear.
- **What the machine cannot show.** Its timing is not the timing of a real client: the Mac is capturing, encoding, running the guest and decoding at once, and the guest decodes H.264 in software. It proves that a real client connects, decodes and plays, and it lets the owner try mardp without a second computer. Whether video is smooth, the delay is low and the sound is in sync (§14) is finally judged on the owner's own Linux machine with Remmina, by the owner, as manual checks (§15.7).
- **Cleanliness.** A test run leaves the machine as it found it: a machine the run started is stopped at the end, one that was already running stays running.

### 15.4 Hostile input

Every parser of client data has a test with truncated, oversized and random input: it returns a tagged error and never panics, never allocates what a length field asks for without checking it. Fuzzing follows the limits of CLAUDE.md.

### 15.5 The host

- `internal/host` is tested with fakes: flow control, rate control, the sound gap rule, the release of keys at the end of a session.
- The packages under `internal/mac` have tests with the build tag `mardp_mac` that use the real frameworks. They need the permissions, run alone, and are not part of the default run. A test never posts keyboard or mouse events into the user's desktop: input injection is checked by hand, or against a window the test itself opens.
- `scripts/perf.sh` runs `mardp -headless` with a test-pattern source and `rdptest` as the client and prints the numbers of §14 that a machine can measure by itself. The others are manual checks.

### 15.6 The UI

`gluttertest` with a fake host, in a debug build (`-tags debug_mode`). What only a real window shows is a manual check done by the owner, with real clicks: on this Mac, screenshots and scripted clicks have hidden real bugs before.

### 15.7 Manual checks

Kept in `docs/MANUAL-CHECKS.md` with the exact steps: connect from Remmina; watch the test video; the clap test for picture and sound; type in every keyboard row with modifiers; the pointer's shapes; the checklist of permissions on a fresh install.

## 16. Build and requirements

- Go 1.27. Xcode's command line tools (for cgo, D14). Nothing from Homebrew.
- For the tests with a real client (§15.3): UTM and a Fedora guest, downloaded after the owner agrees to each download; about 4 CPUs, 4 GB of memory and 25 GB of disk for the machine.
- macOS 14 or newer on the Mac that runs mardp, Apple silicon or Intel (D20). Development and every measurement happen on the owner's Apple silicon Mac; Intel is built but not tested.
- Build: `go build ./...`; the app: Glutter's `glutter-macos build`, wrapped by `scripts/build-app.sh`, which also signs with the development identity (§7.1).
- On Linux, `go build ./...` and `go test ./...` pass: the library and the pipeline are tested there, the Mac parts are stubs.
- The client: a current Remmina, whose FreeRDP is built with H.264 for the video path (R5 checks the owner's).

## 17. Milestones

The steps of each are in `docs/IMPLEMENTATION.md`.

| # | Milestone | Done when |
|---|---|---|
| M0 | **Checks first.** The Linux virtual machine of §15.3 is set up and its FreeRDP runs. Small throwaway programs answer the risks R1 to R6 of §19; the answers are written into this document | The machine starts and stops from `scripts/vm.sh`, FreeRDP runs in it from a script, and `docs/VM.md` says how it was made; every risk has an answer, and the affected sections are updated |
| M1 | **Library core.** Connection sequence, TLS, login, capabilities, fast-path bitmap updates, input events, pointer, disconnect; `rdptest` | A real client connects with the password, shows a test pattern, and its keys and mouse arrive in the log |
| M2 | **The Mac host and the app's shell.** Capture, input, pointer, permissions, keep-awake; a window with Start/Stop and the checklist | Remmina shows and controls the Mac's desktop (bitmap path) |
| M3 | **H.264.** Dynamic channels, the graphics pipeline, AVC420, the hardware encoder, flow and rate control | A 1080p video plays smoothly in Remmina |
| M4 | **Sound.** RDPSND with PCM and AAC, the sound source, where the sound plays | The video's sound plays in Remmina, in sync |
| M5 | **The app.** Settings, menu-bar icon, notifications, events, open at login, the built and signed `mardp.app` | Everything of §12 works on a fresh install |
| M6 | **Performance.** Measure §14, fix what misses, write `docs/PERFORMANCE.md` | Every target is met or corrected with a reason. This is version 1.0 |
| M7 | **After 1.0**, each its own decision: NLA; text clipboard; AVC444; a virtual display that follows the client's window size; Opus | |

## 18. Licensing

mardp is BSD-3-Clause. The protocols are implemented from Microsoft's Open Specifications, which are published for that purpose. No source code of another RDP implementation is copied or ported. H.264 and AAC are encoded by macOS's own encoders; mardp ships no codec.

## 19. Risks to check first (M0)

Each is answered by a small program before the real work starts, because the answer changes the design.

| # | Question | If the answer is bad |
|---|---|---|
| R1 | Does ScreenCaptureKit deliver frames when the Mac's monitor is asleep or absent, once mardp has woken the display and holds the assertion? (The owner's Mac is used over Screen Sharing with the monitor asleep.) | A virtual display moves from M7 into 1.0 |
| R2 | Does ScreenCaptureKit still capture the system sound while the Mac's output is muted? If not, does a Core Audio tap with muting (macOS 14.2) do it? | "On the client only" becomes "on both" in 1.0 |
| R3 | What does a client see and what can it do while the Mac is locked? | The README says to turn the lock off while away, or 1.0 gets a note in the window |
| R4 | Can the pointer's current image be read from an app that is not in front, on macOS 27? | The fallback of §10.3 is the default |
| R5 | Does Fedora's FreeRDP decode H.264 as packaged (checked in the virtual machine of §15.3), and which profiles? The owner checks the same on their own machine: Remmina's colour depth list has "GFX AVC420" | The README gets install steps for the Linux side; §9.3's profile changes |
| R6 | macOS asks again, from time to time, whether an app may keep recording the screen. How often on macOS 27, and does it interrupt a running session? | The window explains it; the checklist notices a withdrawn permission |

Other risks, without a check of their own:

- **The protocol is large.** Writing it from scratch means more interoperability bugs than using a library. §15.1 to §15.3 are the answer, and M1 ends with a real client, not with our own.
- **Private behaviour of clients.** FreeRDP accepts things the documents do not promise, and refuses things they allow. Every such finding gets a test and a line in `docs/INTEROP.md`.
- **cgo callbacks at 60 a second** are cheap, but a mistake in them (a Go pointer kept in C, a callback after the session ended) crashes the app. The rule of one callback per frame, and a teardown order written in the package's documentation, keep the surface small.

## 20. Open decisions

None. The ten questions of the first draft were answered by the owner on 2026-11-19 and are D12, D14 and D16 to D23 of §3. A new question is added here with a recommendation, and moves into §3 when it is answered.
