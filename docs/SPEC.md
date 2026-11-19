# mardp: Specification

Status: the owner answered the open decisions (§3) and the steps of `docs/IMPLEMENTATION.md` are under way; `docs/IMPLEMENTED.md` lists the finished ones.

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
- `spikes/` held M0's throwaway programs, which could import anything; step 0.9 deleted it, and these rules no longer make room for it. A package directory that is not in the table fails `internal/archtest`, so a new package means a new row here.
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
- Each session has one writer goroutine that owns the TLS connection. It takes from two queues: control, input replies and sound first, video second. Sound never waits behind a large video frame. Each queue has a fixed number of packets made at the start of the session (control 32, video 4); a packet is whole frames built by the sender, written in one write, and goes back to its queue's free list after it, keeping the room it grew to. A send that finds no free packet fails at once with a tagged error (the queue is full) instead of waiting. A failed write ends the session with its error. When the session ends, what is still queued is dropped.

## 5. Protocol scope

The library implements the server side of these Microsoft Open Specifications. Each is read from Microsoft's published document; the test vectors come from the annotated examples in them and from recorded sessions (§15).

| Document | What | 1.0 |
|---|---|---|
| MS-RDPBCGR | Connection, capabilities, fast-path output and input, pointer, legacy bitmap updates, errors, disconnect | yes |
| T.123 (TPKT), X.224, T.125 (MCS), T.124 (GCC) | The transport layers under RDP, only the parts RDP uses | yes |
| MS-RDPELE | Licensing: only the "no license needed" answer | yes |
| MS-RDPEDYC | Dynamic virtual channels (the `drdynvc` channel): the PDUs of versions 1 to 3; the server offers version 2, so neither side compresses data | yes |
| MS-RDPEGFX | Graphics pipeline: capability exchange, surfaces, frames, frame acknowledgement, AVC420; uncompressed and Planar as its fallback codecs | yes |
| MS-RDPEA | Sound (`rdpsnd`): formats, training, Wave2, Wave Confirm | yes |
| MS-RDPEGDI | Only the Planar codec (used by the fallbacks) | yes |
| MS-CSSP, MS-NLMP | NLA: CredSSP with NTLMv2 | after 1.0 |
| MS-RDPECLIP | Clipboard, text | after 1.0 |
| MS-RDPEDISP | The client asks for a new desktop size | after 1.0 |

### 5.1 Connection sequence

1. **X.224 Connection Request / Confirm**, with the RDP negotiation: the client lists the security protocols it accepts, the server selects TLS (`PROTOCOL_SSL`). A client that does not offer TLS gets the negotiation failure `SSL_REQUIRED_BY_SERVER`; so does one that sends no negotiation request at all (FreeRDP with `/sec:rdp`). What real clients send is recorded in `rdp/testdata/x224/`: FreeRDP and Remmina on automatic security offer TLS, CredSSP and CredSSP with the early authorization result (`0x0B`), and the server picks TLS from it.
2. **TLS handshake** (§8).
3. **MCS Connect Initial / Response** with the GCC blocks: the server reads the client's core data (desktop size, colour depth, keyboard layout, client name, the flag that says it supports the graphics pipeline), its channel list and its monitor block, and answers with the server core, security (no RDP encryption: TLS does it) and network blocks. Static channels the server opens: `drdynvc` and `rdpsnd`. Others the client asks for are accepted and ignored.
   The DomainParameters' integers are read as unsigned, so an INTEGER whose top bit is set is not taken as negative: Microsoft's own Connect-Initial example writes maxMCSPDUsize 65535 as `02 02 ff ff`, which strict BER reads as -1. The server writes its own integers in strict BER (65535 as `02 03 00 ff ff`).
   Whatever the client proposes, the server answers with the domain parameters of Microsoft's Connect-Response (34 channel IDs, 3 user IDs, 0 token IDs, 1 priority, throughput 0, height 1, PDUs up to 65528 bytes, protocol version 2); RDP relies on none of them beyond what they allow. Channel IDs follow Microsoft's example too: the I/O channel is 1003, the static channels the client lists get 1004 onwards in its order, and the user ID comes after them. The X.224 Connection Confirm says EXTENDED_CLIENT_DATA_SUPPORTED and DYNVC_GFX_PROTOCOL_SUPPORTED. A client core block whose serverSelectedProtocol is not TLS ends the connection: it is what a downgrade between the two steps would leave.
4. **MCS Erect Domain, Attach User, Channel Joins.** The client joins its user channel, the I/O channel and every static channel; a channel the server did not hand out is refused with rt-no-such-channel, and a join from another user ID ends the connection. The server does not offer to skip the joins.
5. **Client Info**: user name, password, time zone, flags, sent by the user ID on the I/O channel with a Basic Security Header that says SEC_INFO_PKT (a header that says SEC_ENCRYPT is refused: TLS is the only security). The login of §8.2 happens here. A client with no user name or no password, or one whose password is a smart card PIN, fails the login without the Authenticator being asked.
6. **Licensing**: the server sends the "valid client" status (STATUS_VALID_CLIENT, ST_NO_TRANSITION, an empty error blob, as in MS-RDPBCGR 4.1.11) from user 1002 on the I/O channel. There is no license server. From here the connection no longer counts among those not logged in and the login deadline no longer applies.
7. **Demand Active / Confirm Active**: capabilities (§5.2).
8. **Synchronize, Control, Font List / Font Map.** After Font Map the session is open and the library calls the app (§6).

Limits before login: at most 8 connections that have not logged in (one more is closed as soon as it is accepted), 10 seconds from the TCP connection to Client Info (a deadline on the connection, so it covers the TLS handshake and every read and write before login), and every length field read from the client is checked against the size of the PDU and a maximum. A client that breaks a limit is disconnected. No client input may make the library panic (§15.4).

### 5.2 Capabilities

- Desktop size: the server decides it (§9.2) and states it in Demand Active; the client follows.
- Colour depth 32. Fast-path output, with fragmentation and the largest request size the client states. Every fast-path PDU is at most 16,383 bytes (MS-RDPBCGR 2.2.9.1.2) and carries one update or one fragment of it, unencrypted and uncompressed; an update larger than one PDU carries (16,377 bytes) goes as FIRST, NEXT and LAST fragments, one PDU each, all in one write. An update larger than the client's MaxRequestSize, or than one PDU when it sent no Multifragment Update set, is refused with a tagged error; so is a fast-path update to a client that did not confirm fast-path output.
- Input: scancodes, Unicode, extended mouse buttons, horizontal wheel, fast-path input.
- Pointer: colour pointers with alpha (32 bpp), pointer cache, large pointers when the client has them.
- Virtual channels, sound, frame acknowledgement, Surface Commands.
- No orders, no glyph or offscreen caches, no RemoteFX: drawing is the graphics pipeline, or bitmap updates as the fallback.

What the server's Demand Active holds (from user 1002, share 0x000103ea, source descriptor "RDP"): General (OS type as in Microsoft's example, fast-path output, long credentials, no bitmap compression header, Refresh Rect and Suppress Output), Bitmap (32 bpp, the session's size, no desktop resize), Order (all orders off), Pointer (colour, cache of 25), Input (scancodes, extended mouse, Unicode, fast-path input, horizontal wheel), Virtual Channel (no compression), Share, Font, Multifragment Update (8 MiB), Large Pointer (96 and 384), Surface Commands (Surface Bits, Frame Marker, Stream Surface Bits) and Frame Acknowledge. Confirm Active is parsed leniently: sets the server does not read are skipped, and padding and uncompressedLength are not checked, since Microsoft's own examples get them wrong. The client's finalization PDUs are answered as each arrives: Synchronize with Synchronize, Control Cooperate with Cooperate, Control Request with Granted, Font List with Font Map; other Data PDUs before Font List (Persistent Key List) are ignored.

### 5.3 What the client may send during a session

Input events (fast-path, and the slow-path form too), Suppress Output (the client's window is minimized: capture pauses; it resumes with a key frame), Refresh Rect (the server sends a key frame), Shutdown Request (answered with Shutdown Request Denied: the session goes on, MS-RDPBCGR 2.2.2.2), a disconnect. Every Data PDU in a Send Data Request is handled, not only the first. Every other PDU is parsed as far as its header and ignored (relative mouse and QoE timestamps among them); malformed input ends the session with the parser's tagged error.

Static virtual channels (MS-RDPBCGR 2.2.6.1, 3.1.5.2): the chunks the client sends on `drdynvc` and `rdpsnd` are joined into messages of at most 1 MiB (the buffer grows with the chunks that arrive, not to the length the first one states) and handed to the channel's handler; a message that arrives before the channel has a handler is dropped. What arrives on the other static channels is ignored unread. A compressed chunk (the server offers no compression), chunks out of order or lengths that do not add up end the session. The server sends a message in chunks of at most 1,600 bytes (CHANNEL_CHUNK_LENGTH, since its Virtual Channel Capability Set states no VCChunkSize), all in one write, with FIRST on the first and LAST on the last as in Microsoft's example (3.1.5.2.2.1), and without CHANNEL_FLAG_SHOW_PROTOCOL, as FreeRDP's server sends them.

Dynamic virtual channels (MS-RDPEDYC): when the client joined `drdynvc`, the server sends its capabilities as the session opens: version 2, with the priority charges of Microsoft's example (936, 3276, 9362, 21845). Version 3 is not offered, because it would let the client compress what it sends, and the server does not decompress. The client has 10 seconds to answer (3.3.3.1); channels the app asked for meanwhile wait, and are created in order once it has; without an answer they are refused with HRESULT_FROM_WIN32(ERROR_TIMEOUT) and no channel is created on that session. A channel ID is used once per session (from 1 upwards) and never reused. A message the server sends goes in one Data PDU when it fits in 1,600 bytes, otherwise in a Data First stating its length and Data PDUs, each PDU one static chunk; messages from the client are joined up to 1 MiB. The client refuses a channel with a negative status in its Create Response, and the app is told. When the server closes a channel it drops the client's data on it until the client's Close; when the client closes one, the server does not answer (3.3.5.2), and a Close for a channel it does not know is ignored. A second capabilities response, a newer version than offered, a PDU for a channel the server did not create, a Create Response or data or a Close that the channel's state does not allow, compressed data, a Data First inside a message and malformed PDUs end the session with their tagged error.

### 5.4 Ending a session

The server ends a session with Set Error Info (the reason) and an MCS Disconnect Provider Ultimatum: logged out by the app's user (`CloseByHost`, ERRINFO_RPC_INITIATED_DISCONNECT 0x01), replaced by another client (`CloseReplaced`, ERRINFO_DISCONNECTED_BY_OTHERCONNECTION 0x05, D13), server stopped (`CloseServerStopped`, ERRINFO_SERVER_SHUTDOWN 0x19, sent by the library itself when `Serve`'s context ends), login failed. Session.Close queues Set Error Info and the ultimatum together on the control queue, ahead of any video still queued, and returns once they are written; when its context ends first, the connection is closed without them. After the ultimatum the server waits up to 2 seconds for the client to hang up before it closes the connection, so that a reset cannot lose the reason. A client ends a session with its own ultimatum, or by closing the connection. Set Error Info is sent only to a client whose core data says it supports it (RNS_UD_CS_SUPPORT_ERRINFO_PDU); the ultimatum's reason is rn-provider-initiated. A failed login sends ERRINFO_SERVER_FRESH_CREDENTIALS_REQUIRED (0x0A), see §20. A connection that goes silent is closed by TCP keep-alive (15 s idle, 5 s interval, 3 probes). Replacement (D13) is done by the library: when a new session has finished the connection sequence, the open one is closed with `CloseReplaced` (if its ultimatum cannot be written within 2 seconds, because its client reads nothing, its connection is closed without it), and the new session reaches the handler only once the old one has ended and its handler has returned. A client that fails the connection sequence never ends the open session. A session that is itself replaced while it waits never reaches the handler.

## 6. The library's API

The exact Go names are decided in the implementation; this is the shape.

- `rdp.NewServer(config)` and `Server.Serve(ctx, listener)`. The config holds the TLS certificate, the `Authenticator` (a function that gets the user name and password and answers yes or no), the `Handler`, the limits of §5.1 and a `slog.Logger`.
- `Handler.SessionOpened(ctx, session)` (or a `HandlerFunc`) is called on a goroutine of its own when a client has finished the connection sequence. The session is over when its context is cancelled; `Session.Close(ctx, reason)` ends it from the app (§5.4). The connection is kept until the handler has returned.
- `Config.DesktopSize(client) (width, height)` decides the session's size from what the client asked for (§9.2), before Demand Active; nil gives the client its own size. `Session.DesktopSize()` returns it.
- `Session.Client()`: what the client is: name, address, user and domain, build, the desktop size it asked for, keyboard layout and type, whether it supports the graphics pipeline, its static channels, and from its Confirm Active fast-path output, the input it sends, its pointer cache, whether it takes pointers with alpha (the New Pointer Update), the largest pointer it shows, its largest fast-path update, whether it takes bitmaps without an alpha plane, surface commands and frame acknowledgement. Whether it decodes H.264 (AVC420) and its audio formats come with the channels that negotiate them.
- **Video.** `Session.Video()` gives one of two things, chosen by the library from the client's capabilities:
  - *H.264*: `SendFrame(annexB, keyFrame)`. The library wraps it as a GFX frame (Start Frame, Wire To Surface with the AVC420 stream, End Frame). It works while `Path()` is `VideoAVC420`; the first frame on a new surface must be a key frame (the library refuses a P-frame before it, with a tagged error, and the flow sink's `KeyFrameWanted` says when one is due).
  - *Bitmaps*: `SendRects(image, rects)`. The library compresses the rectangles (Planar) and sends them through GFX, or as fast-path Bitmap Updates when the client has no graphics pipeline (§9.6). The `Image` is BGRA pixels, rows top-down `Stride` bytes apart, its top left corner the desktop's; rectangles are clipped to it and to the desktop, and rectangles that miss both send nothing.
  - `Video.Path()` says which path the session uses, for the window and for the host's choice of `SendFrame` or `SendRects` (§9.1): `VideoBitmaps`; once the graphics pipeline's surface exists, `VideoAVC420` when the client takes H.264 and `VideoPlanar` when not; and back to `VideoBitmaps` if the channel closes. `SendRects` works on every path.
  - Both report flow: `Backlog()` (frames sent through the graphics pipeline and not yet acknowledged, and the age of the oldest; zero on the bitmap path and while the client has suspended acknowledgements). The callbacks come through a `FlowSink` the app sets on the session with `Session.SetFlowSink`: `KeyFrameWanted()` (on Refresh Rect, after output resumes, and when the graphics pipeline's surface is created, since it starts black) and `OutputSuppressed(bool)` (only when the state changes; a resume is followed by `KeyFrameWanted`). `Session.OutputSuppressed()` reports the state at any time.
- **Sound.** `Session.Audio()`: the negotiated format (AAC, PCM or none) and `Write(data, timestamp)`. It reports the delay the client's confirmations show.
- **Pointer.** `Session.Pointer()`: `SetShape(image, hotX, hotY)` (the library caches shapes and sends a cached index when a shape repeats), `Hide()`, `Default()` (the client's own pointer), `Move(x, y)` (clamped to the desktop). Like `SendRects` they queue without waiting for the network, on the queue of control updates, and fail when it is full. How shapes are encoded is in §10.3.
- **Input.** The app sets an `InputSink` with `Session.SetInputSink` (nil drops input): `Key(scancode, extended, down)`, `Unicode(rune, down)`, `MouseMove(x, y)`, `MouseButton(button, down, x, y)`, `Wheel(dx, dy)`, `SyncLocks(caps, num, scroll)`. Calls arrive in the order the client sent them, on the session's reading goroutine, so a sink must not block.
  - `extended` is an `Extended` prefix, `NotExtended`, `ExtendedE0` or `ExtendedE1`, not a bool: Pause is the one key sent with the E1 prefix.
  - `Unicode` gets whole runes: a UTF-16 surrogate pair is joined, and a surrogate without its pair arrives as `utf8.RuneError`.
  - `Wheel` is in 120ths of a notch; `dy` is positive away from the user, `dx` positive to the right.
  - Input that arrives before a sink is set is dropped. A sink set later first gets the last `SyncLocks` the client sent; a flow sink set while output is suppressed first gets `OutputSuppressed(true)`. A sink takes effect before the next frame the session reads.
- Every method that does I/O takes a `ctx`. Every failure is an `errors` Tag of its own with a route test (CLAUDE.md).
- `Write`, `SendFrame` and `SendRects` do not block on the network: they queue for the session's writer (§4.2) and fail with a tagged error when the queue is full, which is the app's signal to skip (§9.5). They take no `ctx`, since they never wait. One `SendRects` call is one packet of the video queue: the client gets all of its rectangles or, when the queue is full, none of them.

## 7. The host on macOS

### 7.1 Permissions

macOS asks the user for two permissions, and mardp cannot grant them to itself:

- **Screen Recording** (also covers the system sound): without it there is no picture.
- **Accessibility**: without it mardp cannot press keys or move the mouse.

The app shows a checklist (§12.2) with the state of each and a button that opens the right pane of System Settings. The server can start without Accessibility (view only, and the window says so); it does not start without Screen Recording.

macOS ties these permissions to the app's code signature. A build that is signed ad hoc gets a new identity each time, and macOS then asks again. Development builds are therefore signed with one stable signing identity (D22).

### 7.2 The display

- The captured display is the main display, or the one chosen in the settings. A display is remembered by its UUID (`CGDisplayCreateUUIDFromDisplayID`), never by its display ID: the ID changes when the monitor comes back from sleep (§19 R1).
- While a client is connected mardp wakes the display (`IOPMAssertionDeclareUserActivity`) and holds a power assertion that keeps it awake (`PreventUserIdleDisplaySleep`); the assertion is released when the session ends. A display that was asleep is awake as soon as the first call returns, and ScreenCaptureKit delivers frames from it at once (§19 R1).
- Waking can make macOS take the monitor away and add it back with a new display ID, a moment after the wake. A stream on the old display then stops with `SCStreamErrorDomain` -3815 ("failed to find any displays or windows to capture"). So the capture follows display changes: when the stream stops with that error, or a check once a second finds that the captured display ID is no longer active or no longer the display with the UUID, it finds the display again by its UUID, or the main display, and starts a new stream at the session's size. (A check, not `CGDisplayRegisterReconfigurationCallback`: that callback arrives through the main thread's run loop, and the error comes first anyway, 0.7 s after the change in R1.) Streams that stop for other passing reasons (the capture service's connection lost, the system stopping the stream) are started again too; five failed starts or quick stops in a row end the session. The client sees a pause and then a key frame, not a dropped connection. The same path covers a monitor that is unplugged and plugged in again.
- The capture never asks for Screen Recording: it checks the permission first (`CGPreflightScreenCaptureAccess`) and ends with `CapturePermissionDenied` without it, so only the checklist's button shows macOS's question. A stream that macOS stops for the permission (SCStreamError -3801) ends the same way (§19 R6).
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
- The certificate is valid for 20 years, for the same reason. Its fingerprint is shown as lowercase hex pairs joined by colons, as FreeRDP prints it. A `cert.pem` or `key.pem` that cannot be read, or one without the other, is an error the window shows; it is never replaced without the user asking.

### 8.2 Login

- One user name and one password, set in the app. There is no default password, and the server does not start until one is set (at least 8 characters).
- The password is stored as a PBKDF2-HMAC-SHA256 hash with a random 16-byte salt (`crypto/pbkdf2` of the standard library), never in clear text, never in a log. A new hash uses 600,000 iterations (OWASP's figure); the count is stored with the hash, and a stored count above 10 million is refused as damaged. "8 characters" counts characters, not bytes.
- The client sends the user name and password in Client Info, inside TLS. The comparison takes constant time. A client that sends none, or wrong ones, gets the logon-failure error (ERRINFO_SERVER_FRESH_CREDENTIALS_REQUIRED, §5.4 and §20) and is disconnected, so Remmina asks again. The library's `Authenticator` gets the user name, the domain, the password and the client's address, so the app can make an address wait (below).
- After 5 failed logins from one address, that address waits: 1 minute, doubling to at most 1 hour. Failed logins are shown in the window and as a notification. The address is the IP without the port. A login while the address waits is refused without its password being checked, and does not count. A successful login, or a day without a failure, forgets the address's failures. At most 4096 addresses are kept; the oldest goes first.
- When NLA is added (after 1.0), the stored form of the password changes, because NTLM needs the NT hash; that is decided then (D12).

### 8.3 What mardp does not protect against

Someone who knows the password controls the Mac as the logged-in user. A client that accepts a changed certificate without looking can be intercepted on a hostile network. Both are stated in the README.

## 9. Video

### 9.1 Negotiation

1. The client says in its core data that it supports the graphics pipeline, and joins `drdynvc`.
2. The server opens the dynamic channel `Microsoft::Windows::RDS::Graphics`. The client advertises its capability sets; the server confirms the newest version both have (8.1 and the 10.x versions) and notes whether H.264 is allowed (the AVC420 flag in 8.1, the absence of "AVC disabled" in 10.x).
3. The server sends Reset Graphics, Create Surface and Map Surface To Output for one surface of the desktop's size.

The details:
- The server confirms the client's own set, flags included, and skips sets whose length does not match their version. 8.0 is not taken. A client with no set in common gets its channel closed, and the session goes on with bitmaps; so does a client that refuses the channel or closes it.
- Reset Graphics names one primary monitor of the desktop's size. The surface is ID 1, `XRGB_8888`, mapped to the output at 0, 0.
- A second Caps Advertise after a confirmed 10.3 or newer starts over: a new confirm, Reset Graphics, the surface again, and the frames on their way forgotten (MS-RDPEGFX 3.2.5.19). After an older version it is ignored.
- An H.264 frame is one Wire To Surface 1 of the whole surface with codec AVC420 (0x000B), `XRGB_8888`; its RFX_AVC420_BITMAP_STREAM has one region, the desktop, with QP 22 and quality 100, not progressive (the metablock is informational: the client SHOULD NOT use it, MS-RDPEGFX 2.2.4.4.1), then the Annex B data as the encoder made it. The encoder's coded size is a multiple of 16 and the region crops it.
- Each frame is one message on the channel: Start Frame, the frame's content, End Frame, with frame IDs from 1. A Frame Acknowledge takes its frame, and the frames sent before it, off the backlog. One whose queue depth is SUSPEND forgets the frames on their way, and frames sent after it are not counted until the client acknowledges a frame again. Cache Import Offers get a reply that imports nothing (the server keeps no cache); QoE acknowledgements and unexpected commands are ignored.

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

- Hardware encoder, real-time mode, no B-frames (no frame reordering: every frame is shown as soon as it arrives). The session is made with VideoToolbox's low-latency rate control (`kVTVideoEncoderSpecification_EnableLowLatencyRateControl`). Measured in 0.8 on this Mac, 1920x1080 with motion at about 56 frames a second: 7.3 ms a frame on average, 8.9 ms at the 99th percentile, 28 ms for the first key frame; without it 8.5 to 9 ms on average, 12 ms at the 99th percentile. Low-latency mode also writes `bitstream_restriction` with `max_num_reorder_frames = 0` into the SPS, which tells a decoder it may show each frame at once. The default mode leaves it out, and FFmpeg then assumes a reorder delay of 4 frames.
- Constrained High profile (High with `constraint_set4/5`, level 4.2 at 1080p). OpenH264 2.6.0, the decoder of Fedora's FreeRDP, decoded High, Constrained High, Main and Constrained Baseline streams from this encoder in 0.8 with no error and no lost frame, and the decoded pictures were right. Main or Constrained Baseline remain the fallback if the interoperability check of M3 finds a client that cannot decode it.
- The first frame is a key frame. After it only P-frames: the connection is TCP, nothing is lost, so periodic key frames would only cause bursts. A key frame is sent again when the client asks (Refresh Rect), when output resumes, and when the encoder is restarted.
- Colour: 4:2:0, with the matrix and range that MS-RDPEGFX states for AVC420 (expected: BT.709, full range). A colour-bar test against a real client confirms it. ScreenCaptureKit delivers `420v` (video range) unless asked for `420f` (full range) in the stream's pixel format, so the capture is asked for the range the encoder is set to. VideoToolbox signals it (0.8): the SPS carries `video_signal_type` with `video_full_range_flag` following the input's pixel format (`420v` gives 0, `420f` gives 1) and BT.709 primaries and matrix (1). Whether FreeRDP reads those flags or converts with a fixed matrix is what the colour-bar test settles.
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

The host asks ScreenCaptureKit for BGRA frames (sRGB) and the changed rectangles of each frame, and gives them to `SendRects`. The frame rate cap is 30. The rectangles are in the pixels of the scaled frame; on macOS 27 they come as the dictionary form of `CGRect`, not as `NSValue` as the header says (2.4). The first frame of a stream, and the first after frames were dropped, is sent whole. While a stream runs, macOS's capture indicator in the menu bar changes and shows up as small rectangles at the top right. These paths exist so that every client gets a picture; they are not tuned for video.

How the rectangles are sent as fast-path Bitmap Updates (MS-RDPBCGR 2.2.9.1.2.1.2):

- Each rectangle is cut into tiles of at most 64×63 pixels (4032, so that a tile fits in one Bitmap Data and in one fast-path PDU whatever its compression). Tiles are packed into updates as large as the client's largest fast-path update (§5.2); a client without multifragment updates gets one PDU per update.
- Each tile is 32 bpp, compressed with RDP 6.0 Planar (MS-RDPEGDI 2.2.2.5.1), lossless: ARGB, color loss level 0, no chroma subsampling. Its planes are run-length encoded when that is smaller, sent plain when that is smaller, and the tile goes uncompressed when even that is not smaller than its pixels.
- A client that sets `DRAW_ALLOW_SKIP_ALPHA` (`Client.SkipAlpha`) gets no alpha plane; otherwise the alpha plane is 0xFF everywhere, since the screen is opaque.
- Planar scan lines go bottom-up, as in an uncompressed bitmap; FreeRDP decodes them so (step 1.13, `docs/INTEROP.md`).

How the rectangles are sent through the graphics pipeline, when the client has it but not H.264 (and until the H.264 path starts):

- Each `SendRects` is one frame (Start Frame, the tiles, End Frame). Each rectangle is cut into tiles of at most 64×64 pixels, each a Wire To Surface 1 on surface 1, `XRGB_8888`, its rectangle's right and bottom exclusive (MS-RDPEGFX 2.2.2.1).
- Each tile is Planar (codec 0x000A) without an alpha plane, its planes run-length encoded or plain, whichever is smaller; it goes uncompressed (codec 0x0000) when even that is not smaller than its pixels, which only happens to tiles of one or two pixels.
- Planar scan lines go top-down here, unlike in Bitmap Updates (FreeRDP decodes GFX Planar without flipping it).

## 10. Sound, input, pointer

### 10.1 Sound

- The source is the system sound from ScreenCaptureKit, 48 kHz stereo, without mardp's own sounds (`excludesCurrentProcessAudio`; R2 showed it removes them completely). The sound is taken before the Mac's output volume and mute: the client hears the full level whatever the Mac's volume slider says, and turns it down on its side.
- The server offers, in this order: AAC (48 kHz stereo, 192 kbit/s, encoded by AudioToolbox), then 16-bit PCM at 48 kHz stereo.
- AAC as measured in 0.8: AudioToolbox's AudioConverter turns ScreenCaptureKit's format (48 kHz, 2 channels, 32-bit float, not interleaved) into AAC-LC. Each packet holds 1024 frames (21.33 ms), at most 1536 bytes, and is a raw access unit with no ADTS header. The AudioSpecificConfig is `11 90` (AAC-LC, 48 kHz, stereo), inside the 39-byte esds-style magic cookie, and the encoder delay is 2112 frames (44 ms) at the start. Encoding 10 s took 67 ms. FreeRDP's AAC decoder (FDK) opens with the raw transport (`TT_MP4_RAW`) and builds the same AudioSpecificConfig itself from the format's sample rate and channels, so the access units are sent as they come out, one per Wave2 PDU. HE-AAC is not offered: FreeRDP's FDK path configures AAC-LC only. The client picks from what it has. PCM is about 1.5 Mbit/s, fine on a LAN, and every client has it.
- Sound is sent in packets of about 20 ms with the Wave2 PDU (or WaveInfo and Wave for a client older than version 8 of the channel), each with its timestamp. The client confirms each packet; the host measures the delay from that.
- When the confirmed delay exceeds 500 ms, the host drops queued sound until it is back under 100 ms: a short gap is better than sound that trails the picture.
- When nothing plays, nothing is sent.
- **Where the sound plays**: "on the client only" or "on both". Playing on the client only means muting the Mac's default output device (its mute property, through Core Audio) while a client is connected and restoring the earlier state afterwards; capture is not affected by the mute (§19 R2). The default is "on the client only" (D17).
- A Core Audio process tap with `CATapMuted` (macOS 14.2) was tried too (R2): it silences the tapped processes on the Mac without touching the mute setting, and ScreenCaptureKit in the same session still gets their sound. It needs the extra permission "System Audio Recording" and macOS 14.2, so it is not used in 1.0; it is the way out if changing the device's mute turns out wrong (another app or the owner changing it during a session).
- The setting "Sound: off" opens no sound channel.

### 10.2 Picture and sound together

The server sends both as soon as it has them. The client shows a frame when it arrives, and plays sound through a small buffer of its own. The server does not delay video to match. The skew is measured by hand with a test video (a clap or a beep with a flash); the target is in §14. If the measured skew is larger, the fix is decided then and written here.

### 10.3 Pointer

- The pointer's image is read from the system when it changes and sent as a pointer shape with alpha; the library caches shapes. A hidden pointer (for example over a video in full screen) is sent as "hidden".
- How (§19 R4): macOS has no notification for a change of the system pointer, so the host reads `NSCursor.currentSystemCursor` (image, hotspot) and `CGCursorIsVisible` (deprecated, still answers on macOS 27) and compares a hash of the image with the last one sent. It reads them after each mouse event from the client (30 ms later, when the app under the pointer has changed it), after each captured frame, and on a timer of 4 a second while a client is connected (for changes with neither, such as the busy pointer); nothing runs while no client is connected. The image is taken from the representation whose pixel size matches the display's scale.
- The client draws the pointer itself, so moving the mouse feels local. The server sends a position only when something on the Mac moves the pointer: each read also gives the pointer's position on the session's display, and the host sends it (in desktop pixels) when the client's mouse has been still for 200 ms and the pointer is more than 1.5 points from where the client put it. At the start of a session the client's pointer is put where the Mac's is. Without input (view-only) the client's pointer goes back to where the Mac's is.
- The image (`internal/mac/input`) is drawn at the display's scale into a BGRA buffer with straight alpha, at most 256 pixels a side; the hash covers size, hotspot and pixels. One read takes about 0.1 ms (2.6), so reading after each frame costs little.
- If the image cannot be read on some macOS version, the fallback is drawing the pointer into the frames and hiding the client's pointer.
- Encoding (the library, MS-RDPBCGR 2.2.9.1.1.4 and 2.2.9.1.2.1.11): a client that sends a pointer cache size in its Pointer Capability Set takes the New Pointer Update, and gets shapes at 32 bpp with their alpha; any other client gets the Color Pointer Update at 24 bpp, a pixel opaque where its alpha is at least half. A transparent pixel is XOR 0 and AND 1, so the screen shows through whether the client reads the alpha or the AND mask; the server never sends a pixel that inverts the screen.
- A shape larger than the client shows (32 pixels a side, 96 or 384 when its Large Pointer Capability Set says so and such a pointer fits in its largest fast-path update) is shrunk by an integer factor, each block of pixels averaged weighted by alpha, the hotspot divided by the same factor. A shape more than 96 a side goes in a Large Pointer Update, at 32 bpp, whatever pointer update the client takes otherwise.
- The cache: the client's cache, at most 64 entries, holds shapes by a hash of size, hotspot and pixels; a repeated shape is sent as its index, a new one replaces the least recently used. A client with no cache gets every shape again, at index 0.

### 10.4 Keyboard

- The client sends PC scancodes. A table maps them to macOS virtual key codes; the events are posted with Core Graphics. Unicode events (text the client types through an input method) are posted as text.
- Modifiers, by default: Ctrl is Control, Alt is Option, the Super (Windows) key is Command. A setting swaps Ctrl and Command, for people who want Ctrl+C to copy on the Mac (D19).
- Caps Lock, Num Lock and Scroll Lock follow the client's state when it sends a synchronize event.
- When a session ends, every key and button that is still down is released.
- The table (`internal/mac/input`) covers the 105 keys of an ISO keyboard, F13 to F20 and the Japanese and Brazilian keys. Keys a Mac lacks go where an Apple extended keyboard has them: Print Screen and SysRq are F13, Scroll Lock F14, Pause F15, Insert is Help, Menu is the context-menu key. Media keys post nothing. The code a key posts when it goes down is the one its release posts. Modifier keys post flags-changed events, and every event carries the flags of the modifiers held and of Caps Lock, with each key's own numeric-pad and Fn flags as a Mac keyboard sets them.
- Num Lock is not a key on the Mac: it decides whether the keypad's digits are digits or the navigation keys (on until the client's first synchronize). Caps Lock toggles on each press. Scroll Lock's state is dropped.
- Without Accessibility macOS drops posted events silently, so the injector checks `AXIsProcessTrusted` at most once a second and fails each event without it.

### 10.5 Mouse

- Positions are scaled from the session's desktop size to the display's points. Buttons: left, right, middle, back, forward. The wheel scrolls vertically and horizontally.
- Double clicks are counted by the host from the time and distance between clicks, as macOS expects in the event.
- Positions get the display's origin in the global space added (read again at most once a second). A move with a button down is a drag of the lowest button held. The wheel posts one line per notch (120), keeping what is less than a line for the next turn the same way; whether that speed suits is a manual check.

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
- **Checklist**, shown until everything is done: Screen Recording, Accessibility, a password. Each line has its state and a button (open System Settings, or set the password). The lines update by themselves when the user grants a permission. The checklist comes back when a permission is withdrawn. The Screen Recording line explains that macOS asks again from time to time ("... requesting to bypass the system private window picker ...") and that the question is answered at the Mac. A running capture goes on while the question waits (§19 R6).
- **Settings**: user name and password; port and address; start the server when the app opens; open the app at login; display; height cap; frame rate cap (30 or 60); bitrate cap; sound (off, client only, both); swap Ctrl and Command; new certificate.
- **Recent events**: connections, disconnections with their reason, failed logins.
- Until the settings and the menu-bar icon exist (M5): the checklist's password line opens a form for the user name and the password (a set login shows "Change"), and closing the window quits the app, which stops the server (§7.3 applies from step 5.3). A checklist button asks macOS for its permission (macOS shows its question only the first time) and opens its pane of System Settings. The window asks the backend for the status once a second, by the binding's clock. A failed Start shows its message; a server that stops by itself shows why.

### 12.3 The menu-bar icon

It shows the state (stopped, listening, connected). Its menu: the state in words, Start or Stop, Disconnect (when connected), Show window, Quit.

### 12.4 Notifications

A client connected; a client disconnected; failed logins from an address (once per waiting period, not once per attempt).

## 13. Configuration and files

- Everything is under `~/Library/Application Support/mardp/`: `config.json` (the settings and the password hash), `cert.pem` and `key.pem` (mode 0600). The folder is created with mode 0700.
- The environment variable `MARDP_HOME` replaces that folder. Every test and every script uses it with a temporary folder: no test reads or writes the user's real settings.
- `config.json` has a version number; a newer program upgrades an older file, and refuses a file from a newer program with a clear message. Upgrades run one version at a time on the file's fields; a setting missing from the file takes its default, and one the program does not know is ignored. Each write replaces the file in one step (a temporary file, synced, then renamed).
- At each start the folder's mode is set to 0700 and its files' to 0600 when they are wider, and each correction is logged. A file of the folder that is a symbolic link is refused.
- `mardp -headless` runs the server without a window, with the same settings file, for tests and measurements. `mardp -version` prints the version.
  - Before it listens it checks, in this order, that a user name and a password are set and that Screen Recording is granted, and stops with a message saying what is missing; it never asks macOS for a permission. Without Accessibility it serves view-only and logs it; Accessibility granted later needs a restart. `-view-only` drops the client's input even with Accessibility (the `mardp_freerdp` test runs with it, so that FreeRDP's own lock states and pointer moves reach no one's desktop). It logs the certificate's fingerprint, and each refused login with its user name, address, count of failures and wait. SIGINT and SIGTERM stop it: the sessions end and it exits with 0. Exit codes: 0, 1 (it could not start or stopped on an error), 2 (wrong arguments).
  - `mardp` without `-headless` opens the window (§12) with the same settings and flags (`-log-file`, `-view-only`); quitting it or SIGINT/SIGTERM stops the server.
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

- **The machine.** Fedora for arm64 (the owner's Linux is Fedora, so the guest gets the same Remmina and FreeRDP packages and the same H.264 support), made from Fedora's Cloud Base image, which sets itself up on the first boot with cloud-init: no installer to click through. It runs on UTM's QEMU backend with hardware virtualization, a sound device and the accelerated display, and has an Xfce desktop that logs in by itself, so the owner can open Remmina in the machine's window and look and listen; and it has FreeRDP's command-line client, which the tests drive. SELinux is permissive in it, because enforcing confines the guest agent's commands. `docs/VM.md` has the files, versions and steps.
- **How the tests reach it.** Through UTM's own command, `utmctl` (start, stop, run a program in the guest, copy a file in and out, ask for the guest's address), which talks to the guest agent inside the machine. `scripts/vm.sh` wraps it. On the owner's Mac neither `utmctl` nor UTM's AppleScript asked for the Automation permission.
- **Setting it up** is a step of M0, done once: UTM is an app the owner agrees to download, and so is Fedora's image; `scripts/vm/create.sh` makes the machine through UTM's AppleScript dictionary, and cloud-init installs Remmina, FreeRDP, the guest agent, the desktop and the test tools on the first boot. `docs/VM.md` records every step and the versions, so the machine can be made again. Nothing of the owner's goes into the machine: no keys, no accounts, no shared folders but the tests' own temporary one.
- **The tests** carry the build tag `mardp_freerdp`. They start the library's server (or `mardp -headless`) with a test pattern and a tone, start FreeRDP in the guest against it under a virtual X display, and check from FreeRDP's log and exit status that it logged in, negotiated the expected video path and sound format, decoded frames and confirmed sound. They are skipped with a clear message when the machine is not running, and are not part of the default test run.
- **Sound and picture in the tests.** No one has to listen: FreeRDP plays into a null sink inside the guest, the test records what was played there and compares it with the tone the server sent, so a wrong format, a dropout or silence fails the test. The same is done for the picture with a screenshot inside the guest, which is also the colour check of §9.3.
- **Listening by ear in the machine.** The guest's sound comes out of the Mac's own output. mardp would capture it again and send it back, a loop. The sound source therefore leaves UTM's process out of what it captures, always: a virtual machine that plays the Mac's own sound back is never what the remote user wants to hear.
- **What the machine cannot show.** Its timing is not the timing of a real client: the Mac is capturing, encoding, running the guest and decoding at once, and the guest decodes H.264 in software. It proves that a real client connects, decodes and plays, and it lets the owner try mardp without a second computer. Whether video is smooth, the delay is low and the sound is in sync (§14) is finally judged on the owner's own Linux machine with Remmina, by the owner, as manual checks (§15.7).
- **Cleanliness.** A test run leaves the machine as it found it: a machine the run started is stopped at the end, one that was already running stays running.

### 15.4 Hostile input

Every parser of client data has a test with truncated, oversized and random input: it returns a tagged error and never panics, never allocates what a length field asks for without checking it. Fuzzing follows the limits of CLAUDE.md. One sweep (`rdp/hostile_test.go`) takes every parser of what a client sends, from the TPKT frame to the input events, and gives it every cut of real PDUs (Microsoft's examples and the requests recorded from FreeRDP and Remmina), every 2 and 4 bytes set to the largest length, random mutations, random bytes added, and random input up to the largest frame; each parse must return nil or a tagged error and allocate at most 8 KiB plus 32 bytes per byte of input.

### 15.5 The host

- `internal/host` is tested with fakes: flow control, rate control, the sound gap rule, the release of keys at the end of a session.
- The packages under `internal/mac` have tests with the build tag `mardp_mac` that use the real frameworks. They need the permissions, run alone, and are not part of the default run. `scripts/mactest.sh <package>` runs them as a signed app with the M0 probe's identifier and the development identity (D22), so they have the Screen Recording the owner granted the probe (`scripts/probe-app.sh` makes and signs such a bundle; the `mardp_freerdp` test of `cmd/mardp` runs `mardp -headless` in one); tests that need a permission the process lacks are skipped, never ask for it. A test never posts keyboard or mouse events into the user's desktop: input injection is checked by hand, or against a window the test itself opens.
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
- The client: a current Remmina, whose FreeRDP is built with H.264 for the video path. On Fedora that is the packaged FreeRDP with Cisco's `openh264` installed, not the stub `noopenh264` (§19 R5).

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

Each was answered by a small program before the real work started (M0, steps 0.4 to 0.8), because the answer changes the design. The programs were deleted in step 0.9; what they found is in the Answer column and in the sections it names. R3 still waits for the owner's own check, and 1.0 assumes its bad answer until then.

| # | Question | If the answer is bad | Answer |
|---|---|---|---|
| R1 | Does ScreenCaptureKit deliver frames when the Mac's monitor is asleep or absent, once mardp has woken the display and holds the assertion? (The owner's Mac is used over Screen Sharing with the monitor asleep.) | A virtual display moves from M7 into 1.0 | Yes, with the display asleep (2026-11-19, this Mac, macOS 27.2, one external 2560×1440 monitor; the display put to sleep with `pmset displaysleepnow`, since this Mac's own display sleep is off and its "asleep" monitor is the monitor's standby, which macOS sees as awake): after the two assertions of §7.2 the display reported awake at once, and a 1920×1080 stream gave its first frame within 30 ms, then 45 frames/s on a nearly still screen and 55 frames/s with motion, the longest gap 54 ms, no blank or suspended frames, over 30 s and in five shorter runs. Frames are `420v` (NV12, video range) IOSurfaces, BT.709 matrix, primaries and transfer function, every frame with dirty rectangles. The catch: once the monitor came back from sleep with a new display ID and the running stream stopped with error -3815 after 0.7 s; §7.2 now restarts the capture on display changes. A Mac with no monitor at all was not tried (the owner's has one), so the virtual display stays in M7 |
| R2 | Does ScreenCaptureKit still capture the system sound while the Mac's output is muted? If not, does a Core Audio tap with muting (macOS 14.2) do it? | "On the client only" becomes "on both" in 1.0 | Yes (2026-11-19, this Mac, output muted and at volume 0, a 440 Hz tone at level 0.25): ScreenCaptureKit captured the probe's own tone at 0.25 and another process's (`afplay`) at 0.25; with `excludesCurrentProcessAudio` its own tone gave 0.0000; another app left out with `excludingApplications` gave 0.0000 (an app is in the shareable list only while it has a window; a command-line player is never in it). A Core Audio tap gets the same while muted, with or without `CATapMuted`, and leaves out a process by its PID exactly; while one process held a muting tap of everything, ScreenCaptureKit in another still captured 0.25. So "on the client only" mutes the output device as §10.1 says. The unmuted case, and whether the muting tap really silences the speakers, are the owner's to hear (docs/MANUAL-CHECKS.md) |
| R3 | What does a client see and what can it do while the Mac is locked? | The README says to turn the lock off while away, or 1.0 gets a note in the window | Pending: the owner's check (docs/MANUAL-CHECKS.md, step 0.7): `probe capture -report 5 -move-at 40` logs frames, their brightness and the lock state every 5 s while the owner locks the Mac, and posts one zero-distance mouse move while it is locked. Until then 1.0 assumes the bad answer: the README says to turn the lock off while away |
| R4 | Can the pointer's current image be read from an app that is not in front, on macOS 27? | The fallback of §10.3 is the default | Yes (2026-11-19, macOS 27.2): a background instance of the probe read `NSCursor.currentSystemCursor` at 10 a second while another app set six system pointers; it saw every change within one sample, with the right size and hotspot (for example the I-beam 23×22, hotspot 12,11; the arrow 28×40, hotspot 5,5; two pointers of the same size told apart by a hash of their pixels), never nil. `CGCursorIsVisible` showed the pointer hidden while that app had hidden it. No notification exists, so §10.3 polls, within the rule of §11. Not tried: a Retina display (this Mac's is 1×), where the image has a 2× representation |
| R5 | Does Fedora's FreeRDP decode H.264 as packaged (checked in the virtual machine of §15.3), and which profiles? The owner checks the same on their own machine: Remmina's colour depth list has "GFX AVC420" | The README gets install steps for the Linux side; §9.3's profile changes | Yes (2026-11-19, test machine): FreeRDP 3.33.1 (`freerdp-3.33.1-1.fc45`) is built with `WITH_GFX_H264=ON` and links OpenH264 directly (`libopenh264.so.8`, not loaded at run time); Remmina 1.4.43 (`remmina-1.4.43-6.fc45`) offers "GFX AVC420 (32 bpp)" and "GFX AVC444 (32 bpp)". The catch: two packages provide `libopenh264.so.8`, Cisco's `openh264` (repository `fedora-cisco-openh264`, enabled by default) and the stub `noopenh264` (Fedora's own repository), which decodes nothing; the machine got the real one, 2.6.0. The README tells the Linux side to check `rpm -q openh264` and how to swap the stub. Sound: FreeRDP decodes AAC (`WITH_FDK_AAC`), Opus, GSM and MP3. OpenH264 decodes all four profiles VideoToolbox makes (High, Constrained High, Main, Constrained Baseline): checked in 0.8 with 10 s streams of the encoder's own output, every frame decoded with no error, and the pictures were right; §9.3 now names Constrained High |
| R6 | macOS asks again, from time to time, whether an app may keep recording the screen. How often on macOS 27, and does it interrupt a running session? | The window explains it; the checklist notices a withdrawn permission | The probe was granted Screen Recording in the night of 2026-11-18. On 2026-11-19 before 02:01 (macOS 27.2) the question came back, a few hours after the grant rather than a month: "Probe is requesting to bypass the system private window picker and directly access your screen and audio", with Allow and Open System Settings. The probe had been rebuilt and re-signed many times that night, so a new build may trigger it; that is not proven. **A question does not stop a running capture:** it stood on the screen through seven 10-second capture runs in step 0.8, and every run got every frame (the decoded video shows the question). It was left for the owner to answer (docs/MANUAL-CHECKS.md). From Apple's behaviour since macOS 15 (press reports, August to October 2024): an app that captures the screen without the system's picker (which mardp must: nobody sits at the Mac to pick) is asked again about once a month, "Allow for one month", and from 15.1 less often for apps used regularly; nothing public says what macOS 26 or 27 changed. The entitlement `com.apple.developer.persistent-content-capture` ("persistent access to screen capture" for VNC-type apps) would end the question, but Apple grants it, it is not for a self-signed build. Whether the question stops capture once it is denied or left unanswered for long is not known. So 1.0 takes the bad answer as the design: the window explains the question, the checklist notices a withdrawn permission, and a stream that stops with a permission error ends the session with that reason. The owner notes the date of any new question (docs/MANUAL-CHECKS.md) |

Other risks, without a check of their own:

- **The protocol is large.** Writing it from scratch means more interoperability bugs than using a library. §15.1 to §15.3 are the answer, and M1 ends with a real client, not with our own.
- **Private behaviour of clients.** FreeRDP accepts things the documents do not promise, and refuses things they allow. Every such finding gets a test and a line in `docs/INTEROP.md`.
- **cgo callbacks at 60 a second** are cheap, but a mistake in them (a Go pointer kept in C, a callback after the session ended) crashes the app. The rule of one callback per frame, and a teardown order written in the package's documentation, keep the surface small.

## 20. Open decisions

The ten questions of the first draft were answered by the owner on 2026-11-19 and are D12, D14 and D16 to D23 of §3. A new question is added here with a recommendation, and moves into §3 when it is answered.

| # | Question | Recommendation, and what is built until it is answered |
|---|---|---|
| Q1 | Which Set Error Info code does a failed login send? MS-RDPBCGR 2.2.5.1.1 has no "logon failure" code; §8.2 only says Remmina should ask again | ERRINFO_SERVER_FRESH_CREDENTIALS_REQUIRED (0x0A, "the server requires that the user enter their credentials"), followed by the Disconnect Provider Ultimatum. Built so in step 1.6. Step 1.13 found that FreeRDP 3.33 does not set RNS_UD_CS_SUPPORT_ERRINFO_PDU, so it gets the ultimatum alone and reports ERRINFO_RPC_INITIATED_DISCONNECT for a wrong password as for any other ending (`docs/INTEROP.md`). What Remmina shows is the manual check of step 1.13; the answer then moves here into §3 or changes the code |
