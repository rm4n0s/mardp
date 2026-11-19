# mardp: Implementation plan

The steps that build what `docs/SPEC.md` describes, in order. A section sign (§) points into the specification; D and R numbers are its decisions and risks.

Status: see `docs/IMPLEMENTED.md` for the finished steps.

## Rules

1. **Work starts when the owner says so.** Writing this plan was not the signal to begin.
2. Steps are done one at a time, in the order of this file, by one agent.
3. Before a step, read the sections of the specification it names. If the step is ambiguous, or the specification is wrong or silent on something the step needs, stop and ask (rule 9).
4. Each step is done on its own branch, `step/<id>` (for example `step/1.2`), made from the local `main`.
5. A step is finished when: its "Proof" holds; the Definition-of-done checks of CLAUDE.md pass (`scripts/check.sh` runs them); every new error Tag has a route test; and its entry is in `docs/IMPLEMENTED.md` on the branch. The entry says what was built, how it was proved (test names, measured numbers), what differs from the plan and why, and which manual checks are pending.
6. A finished step is fast-forward merged into the local `main`, without a PR, and tagged `step-<id>` locally. The next step starts in the same turn.
7. When a step changes a decision, or learns something the specification should say, the specification is changed on the same branch.
8. A **manual check** does not stop the work: it goes into `docs/MANUAL-CHECKS.md` with the step's id and the exact steps for the owner, the step's entry says "pending, see docs/MANUAL-CHECKS.md", and the work goes on. When the owner answers, the answer is recorded and the item removed.
9. **Stop and ask** only for these: an ambiguous step or a wrong specification; a tool that is missing; a dependency that CLAUDE.md does not allow; anything that needs `sudo` or the owner's password; a download (name the file, the source and the size); a permission prompt of macOS that only the owner can answer; a check that still fails after three honest attempts to fix it; a step marked **ask the user**. After the answer, continue with the same step.
10. Nothing is left running: every server, client and test program a step starts is stopped when the step no longer needs it, and the virtual machine is left as it was found (§15.3).
11. No test, script or check uses the owner's real settings: each sets `MARDP_HOME` to a temporary folder (§13). No test posts keyboard or mouse events into the owner's desktop (§15.5).
12. Code under `spikes/` is throwaway: it is never imported by the product, and step 0.9 deletes it. What it taught lives in the specification.
13. The protocol is written from Microsoft's documents (§5, §18). No code of another RDP implementation is read for copying or porting. Reading FreeRDP's *log output*, or recording its bytes on the wire, to see what a real client does is allowed and expected.
14. **GitHub gets milestones, not steps.** When the last step of a milestone is merged, `main` and the step tags are pushed once, with a version tag whose number the session chooses without asking: the next minor version (CLAUDE.md, "Git and GitHub"). If the push keeps failing, stop and ask.

## M0. Checks first

The answers of this milestone change the design, so nothing of the product is written before them. Spec: §17 (M0), §19.

### 0.1 Groundwork
- **Do:** `go.mod` (`github.com/rm4n0s/mardp`, Go 1.27) with `github.com/rm4n0s/errors`; read that module's `AI_GUIDE.md`. `.gitignore` entries for build output, `*.app` and `spikes/out/`. `scripts/check.sh` (gofmt, vet, golangci-lint, the tests in both modes, the Linux build of `rdp/...`). A `golangci-lint` configuration. `internal/archtest` with the import rules of §4.1 (it passes on an empty tree and fails on a planted violation, which its own test proves). `docs/IMPLEMENTED.md` with its heading. The first commit holds `CLAUDE.md`, `docs/SPEC.md` and this file.
- **Proof:** `scripts/check.sh` passes; the planted-violation test of `archtest` passes.

### 0.2 The development signing identity (**ask the user**)
- **Spec:** §7.1, D22.
- **Do:** Write the steps for creating a self-signed code-signing certificate in the owner's login keychain (README section "Building"); the owner creates it. `scripts/sign.sh <bundle>` signs with that identity and fails with a clear message when it is missing.
- **Proof:** a trivial bundle signed twice from two builds has the same designated requirement (`codesign -d -r-`), which is what macOS ties permissions to.

### 0.3 The virtual machine (**ask the user** for each download)
- **Spec:** §15.3, D16.
- **Do:** UTM into `/Applications`. A Fedora desktop guest for arm64 on the QEMU backend with sound and the accelerated display. In the guest: Remmina, FreeRDP's command-line client, the QEMU guest agent, a virtual X display, the sound tools for a null sink and recording, a screenshot tool, a tool that types keys and moves the mouse inside the guest. `scripts/vm.sh` with `start`, `stop`, `status`, `exec`, `push`, `pull`, `address`. `docs/VM.md` records every step and version.
- **Proof:** `scripts/vm.sh start`, then `scripts/vm.sh exec` prints FreeRDP's version and build configuration, then `scripts/vm.sh stop`; the guest reaches a TCP listener on the Mac (the address is written into `docs/VM.md`).

### 0.4 What a real client sends, and H.264 in it (R5)
- **Do:** In the guest, read FreeRDP's build configuration for H.264 and for its sound codecs (AAC, Opus); check that Remmina's profile offers "GFX AVC420". A small listener under `spikes/` records the first bytes FreeRDP and Remmina send (the X.224 Connection Request) with each security setting; the recordings go to `rdp/testdata/` with a note of their origin. The owner checks the same Remmina setting on their own Linux machine (manual check).
- **Proof:** the answer to R5 is in the specification, with the client versions; the recordings exist.

### 0.5 Capture with the display asleep (R1)
- **Do:** One spike app, `spikes/probe`, built as a signed bundle (0.2) so the owner grants Screen Recording once for all spikes of this milestone (**ask the user** to grant it). Its `capture` command wakes the display, holds the assertion, captures the main display with ScreenCaptureKit for 30 seconds as NV12 IOSurfaces at a given size, and prints: frames delivered, frames marked idle, whether changed rectangles arrive, the pixel format and colour attachments. Run it with the monitor asleep, as the owner's Mac normally is, and while a video plays.
- **Proof:** the answer to R1 is in the specification. If it is bad, the virtual display moves into 1.0 (§19) and this plan gets its steps before M1 starts: stop and ask.

### 0.6 System sound (R2)
- **Do:** `probe audio` plays a generated tone through the system, captures the system sound for 10 seconds and prints the level of what it captured, in four cases: output not muted; output muted; its own process excluded; another process (the tone's player) excluded. If muted capture is silent, the same with a Core Audio tap that mutes.
- **Proof:** the answer to R2 is in the specification, and §10.1 says how "on the client only" is done or that 1.0 has only "on both".

### 0.7 Pointer, lock screen, the recurring question (R4, R3, R6)
- **Do:** `probe cursor` prints the pointer's image size and hotspot every time it changes while the app is not in front. `probe lock` logs frames and tries one harmless event (a mouse move of zero distance) while the owner locks the screen (**ask the user**: a manual check). For R6: what macOS 27 shows for an app that records the screen, how often, and whether a running capture is interrupted, from what the spikes met and from Apple's documentation.
- **Proof:** the answers to R3, R4 and R6 are in the specification.

### 0.8 The encoders
- **Do:** `probe h264` feeds captured frames to VideoToolbox with the settings of §9.3 and prints the encode time per frame at 1080p60, the profile and level produced, and writes an Annex B file that the guest's player decodes. `probe aac` encodes the captured sound with AudioToolbox and prints the frame size and the exact form of the output (raw frames or with headers), which decides what RDPSND's format block must say.
- **Proof:** the numbers and the output forms are in the specification (§9.3, §10.1); the guest plays the H.264 file.

### 0.9 Close the checks
- **Do:** §19 gets an "Answer" column; every section an answer touches is updated; `spikes/` is deleted; this plan is corrected where an answer changed it (new or dropped steps), and the owner is told what changed.
- **Proof:** no "to be checked" is left in the specification for R1 to R6. **Milestone M0 is published** (rule 14).

## M1. The library core

A real client logs in, sees a test pattern, and its keys and mouse arrive. Spec: §5, §6, §8, §15.1 to §15.4.

### 1.1 Reading and writing bytes
- **Do:** `rdp/internal/wire`: a bounds-checked reader and an appending writer for the integer forms RDP uses (little and big endian, the PER and BER lengths and integers of MCS), with tagged errors and no allocation. Every later parser is built on it.
- **Proof:** table tests; a test with truncated input for every read; a benchmark with 0 allocations.

### 1.2 TPKT and X.224
- **Spec:** §5.1 step 1.
- **Do:** `tpkt` (the 4-byte header, and telling a TPKT from a fast-path PDU by the first byte) and `x224` (Connection Request with the RDP negotiation request, Connection Confirm with the response or the failure, Data, Disconnect Request).
- **Proof:** bytes of Microsoft's examples and of the recordings of 0.4, both directions.

### 1.3 MCS
- **Spec:** §5.1 steps 3 and 4.
- **Do:** `mcs`: Connect Initial and Connect Response (BER), Erect Domain, Attach User and its confirm, Channel Join and its confirm, Send Data Request and Indication, Disconnect Provider Ultimatum (PER).
- **Proof:** bytes of Microsoft's examples; hostile-input tests.

### 1.4 GCC and the settings blocks
- **Spec:** §5.1 step 3.
- **Do:** `gcc`: the Conference Create Request and Response, the client blocks (core, security, network, cluster, monitor, message channel, multitransport: unknown blocks are skipped) and the server blocks (core, security with no encryption, network).
- **Proof:** bytes of Microsoft's examples; a client block of every known length (old clients send shorter core blocks).

### 1.5 The server and the connection up to the channels
- **Spec:** §5.1 steps 1 to 4 and its limits, §6, §8.1.
- **Do:** `rdp.Server`, its config, `Serve(ctx, listener)`; one goroutine pair per connection; the negotiation (TLS or the failure), the TLS handshake, MCS and the channel joins as a state machine; the limits before login; logging through `slog`. `rdp/rdptest` begins: a client that does the same steps.
- **Proof:** `rdptest` reaches the end of the channel joins over TLS on `127.0.0.1`; a client without TLS gets the failure; the 9th unauthenticated connection and a silent one are closed.

### 1.6 Login and licensing
- **Spec:** §5.1 steps 5 and 6, §8.2.
- **Do:** `sec`: Client Info (every optional field), the `Authenticator`, the license "valid client" PDU, Set Error Info. A wrong or missing password ends the connection with the logon failure.
- **Proof:** `rdptest` with a right, a wrong and no password; the password never appears in the log (a test greps the log).

### 1.7 Capabilities and the open session
- **Spec:** §5.1 steps 7 and 8, §5.2, §6.
- **Do:** `caps`: Demand Active with the sets of §5.2, parsing Confirm Active (unknown sets are skipped), Synchronize, Control, Font List and Font Map. `Handler.SessionOpened`, `Session`, `Session.Client()`, `Session.Close`.
- **Proof:** `rdptest` opens a session and the handler sees the client's data; bytes of Microsoft's examples for each PDU.

### 1.8 The writer and fast-path output
- **Spec:** §4.2, §11.
- **Do:** The session's writer goroutine with its two queues, the pools, fast-path update framing with fragmentation to the client's largest size, one write per update.
- **Proof:** an update larger than the fragment size arrives whole in `rdptest`; an ordering test (control before video); benchmarks of the framing with 0 allocations.

### 1.9 Input and the client's requests
- **Spec:** §5.3, §6.
- **Do:** `input`: fast-path and slow-path events (scancode, Unicode, mouse, extended mouse, wheels, synchronize) delivered to the `InputSink` in order; Suppress Output, Refresh Rect, Shutdown Request; everything else skipped by its header.
- **Proof:** `rdptest` sends every kind and the sink records them; hostile-input tests.

### 1.10 Bitmap updates
- **Spec:** §6, §9.1 (third path).
- **Do:** `bitmap`: the Planar codec (encoder; the decoder lives in `rdptest`) and uncompressed bitmaps; `Session.Video().SendRects` over fast-path Bitmap Updates, splitting rectangles that are too large.
- **Proof:** `rdptest` rebuilds the picture pixel for pixel from random rectangles of a test image; Planar bytes of Microsoft's example.

### 1.11 Pointer
- **Spec:** §6, §10.3.
- **Do:** `pointer`: shapes with alpha, the cache with its index, hidden, default, position, large pointers when the client has them.
- **Proof:** `rdptest` sees each; a repeated shape is sent as a cache index.

### 1.12 Ending, replacing, and the hostile-input sweep
- **Spec:** §5.4, D13, §15.4.
- **Do:** Every way a session ends, with its reason; a second login replaces the first; TCP keep-alive. Then one pass over every parser of client data: truncated, oversized, random input.
- **Proof:** a test per reason; the replacement test; the sweep finds no panic and no allocation sized by an unchecked field.

### 1.13 A real client
- **Spec:** §15.3.
- **Do:** Tests with the tag `mardp_freerdp`: the library's server shows a test pattern; FreeRDP in the guest connects with the password, with a wrong password, and without TLS; a screenshot in the guest is compared with the pattern; keys typed and the mouse moved inside the guest arrive in the sink. `docs/INTEROP.md` starts with what the real client did differently from the documents.
- **Proof:** the tests pass against the guest. Manual check: Remmina in the guest connects from its own window. **Milestone M1 is published.**

## M2. The Mac host and the app's shell

Remmina shows and controls the Mac's desktop, on the bitmap path. Spec: §4, §7, §10.3 to §10.5, §12, §13.

### 2.1 The pipeline, with fakes
- **Spec:** §4.1, §4.2.
- **Do:** `internal/host`: the interfaces (screen source, video encoder, sound source, sound encoder, input injector, pointer source, system) and their fakes (a moving test pattern, a tone, a recording injector); the session controller for the bitmap path; the release of every held key and button when a session ends.
- **Proof:** with `rdptest`: the pattern arrives, input reaches the injector, held keys are released at the end.

### 2.2 Settings, password, certificate
- **Spec:** §8, §13.
- **Do:** `internal/config`: `MARDP_HOME`, `config.json` with its version and upgrade rule, the PBKDF2 hash, the certificate and its fingerprint, the folder and file modes, the waiting rule for failed logins.
- **Proof:** table tests, including a file from a newer version and wrong modes; the waiting rule with a fake clock.

### 2.3 The system
- **Spec:** §7.1, §7.2.
- **Do:** `internal/mac/system`: the state of both permissions and the request for them, opening the two panes, the list of displays, waking the display and the assertion. Stubs for other systems.
- **Proof:** tests with the tag `mardp_mac`; the stubs' tagged errors on Linux (`GOOS=linux go vet`).

### 2.4 Capture
- **Spec:** §7.2, §9.2, §9.6, D9.
- **Do:** `internal/mac/capture`: a display captured at the session's size, BGRA with changed rectangles for the bitmap path, without the pointer; start, pause, resume, stop; the skip flag; the order of teardown written in the package's documentation. From R1 (§7.2): the display is remembered by its UUID, woken with the two power assertions, and followed through display changes: on a reconfiguration or `SCStreamError` -3815 the capture finds the display again and restarts, and tells the host so it can send a key frame. From R6: a stream that stops with a permission error ends with a tagged error the session reports as its reason.
- **Proof:** `mardp_mac` tests: frames arrive, a still screen delivers none, stop leaves no callback running (a test that stops a thousand times); the restart logic with a fake stream that fails with -3815.

### 2.5 Keyboard and mouse
- **Spec:** §10.4, §10.5, D19.
- **Do:** `internal/mac/input`: the scancode table and the modifier mapping in plain Go; posting the events through Core Graphics in the cgo file; Unicode text; the lock keys; buttons, wheels, the double-click count; release-all.
- **Proof:** table tests of the mapping for every key of a 105-key keyboard and of the click counting; posting is a manual check (rule 11).

### 2.6 The pointer source
- **Spec:** §10.3, the answer to R4.
- **Do:** The pointer's image and hotspot when it changes, hidden state, the position when the Mac moves it, read by polling as §10.3 says (R4: macOS has no notification for it): `currentSystemCursor` and `CGCursorIsVisible` 30 ms after a client mouse event, after frames, and 4 times a second only while a client is connected; a new image is sent only when its hash changes.
- **Proof:** a `mardp_mac` test reads the current image; the rest by hand.

### 2.7 `mardp -headless`
- **Spec:** §13.
- **Do:** `cmd/mardp`: flags, logging, wiring the real parts into the pipeline, the checks before start (a password, Screen Recording), view-only without Accessibility.
- **Proof:** a `mardp_freerdp` test: FreeRDP in the guest connects to `mardp -headless` and its screenshot is not one flat colour. Manual check: Remmina shows the Mac's desktop and controls it.

### 2.8 The app's shell
- **Spec:** §12.1, §12.2 (status, Start/Stop, checklist), D23.
- **Do:** Add Glutter at its newest version. `internal/ui`: the window with the status, Start and Stop, and the checklist with its buttons; a view model fed by the host; `scripts/build-app.sh` builds `mardp.app` with `glutter-macos` and signs it (0.2).
- **Proof:** `gluttertest` tests against a fake host, in a debug build. Manual check, by the owner with real clicks: the checklist on first start, Start, a connection from Remmina. **Milestone M2 is published.**

## M3. H.264

A 1080p video plays smoothly. Spec: §9, §11.

### 3.1 Static virtual channels
- **Do:** `rdp/internal/svc`: the channel PDU header, splitting into chunks and joining them, one handler per joined channel.
- **Proof:** a message larger than a chunk in both directions with `rdptest`; bytes of Microsoft's example.

### 3.2 Dynamic virtual channels
- **Spec:** §5 (MS-RDPEDYC).
- **Do:** `dynvc`: capabilities (versions 1 to 3), create and its response, Data First and Data with every length form, close.
- **Proof:** `rdptest` opens a channel and exchanges a large message; a refused channel is reported.

### 3.3 The graphics pipeline
- **Spec:** §9.1.
- **Do:** `gfx`: the RDP 8 segment wrapper (uncompressed, single and multipart), the capability exchange for versions 8.1 and 10.x, Reset Graphics, Create Surface, Map Surface To Output, Start Frame and End Frame, the client's acknowledgements, `Backlog()`.
- **Proof:** `rdptest` goes through the exchange for each version; the backlog follows the acknowledgements.

### 3.4 The second path
- **Spec:** §9.1 (second path), §9.6.
- **Do:** Uncompressed and Planar through Wire To Surface; `SendRects` chooses GFX when the client has it.
- **Proof:** the pixel-for-pixel test of 1.10 over GFX.

### 3.5 AVC420
- **Spec:** §9.1, §9.3.
- **Do:** The AVC420 bitmap stream (the region, the quality values, the H.264 data) and `SendFrame`; the three outcomes of §9.1 decided from the client's capabilities.
- **Proof:** bytes checked field by field; `rdptest` gets back the same Annex B data it was sent.

### 3.6 The hardware encoder
- **Spec:** §9.3, §4.2.
- **Do:** `internal/mac/vtenc`: the session with the settings of §9.3 and the findings of 0.8 (low-latency rate control, Constrained High, the range the capture is set to); Annex B output into pooled buffers; a new bitrate without a restart; a forced key frame; one callback per frame.
- **Proof:** `mardp_mac` tests: the first frame is a key frame, a forced one is too, the bitrate follows; no B-frames in the stream, and the SPS says `max_num_reorder_frames = 0`.

### 3.7 Capture straight into the encoder
- **Spec:** §4.2, §11 rules 1 and 3.
- **Do:** Capture in NV12 with the colour settings of §9.3; frames go from the capture callback to the encoder in C, with the skip flag read there.
- **Proof:** a `mardp_mac` test counts cgo crossings per frame (one); a still screen encodes nothing.

### 3.8 Flow and rate control
- **Spec:** §9.4, §9.5.
- **Do:** In `internal/host`: skip before encoding, the bitrate rule, key frames on Refresh Rect and on resume, the frame-rate cap.
- **Proof:** with fakes and a slow `rdptest`: no frame older than the limit is sent, the bitrate falls and recovers, sound keeps flowing while video is throttled (once M4 exists, the test is extended).

### 3.9 A real client decodes it
- **Spec:** §9.3, §15.3.
- **Do:** `mardp_freerdp` tests: FreeRDP in the guest negotiates AVC420, acknowledges frames, and its screenshot of colour bars is within tolerance (which fixes the matrix and range); the profile test (Constrained High, else Main, else Constrained Baseline; OpenH264 decoded all of them in 0.8) records its result in §9.3. The window shows the video path.
- **Proof:** the tests pass. Manual checks: a 1080p video in Remmina, in the guest and on the owner's Linux machine. **Milestone M3 is published.**

## M4. Sound

The video's sound plays in Remmina, in sync. Spec: §10.1, §10.2.

### 4.1 RDPSND
- **Do:** `rdpsnd`: the server's formats and version, the client's formats, quality mode, training and its confirm, Wave2, and WaveInfo with Wave for older clients, Wave Confirm, close; `Session.Audio()` with the delay it measures.
- **Proof:** `rdptest` negotiates each format, confirms, and gets back the samples it was sent; bytes of Microsoft's examples.

### 4.2 The sound source
- **Spec:** §10.1, §15.3 (the loop).
- **Do:** System sound from the capture at 48 kHz stereo, without mardp's own and without UTM's; silence sends nothing.
- **Proof:** `mardp_mac` test with a generated tone; the fake source's tests in the pipeline.

### 4.3 PCM, end to end
- **Spec:** §10.1.
- **Do:** 20 ms packets with timestamps through the pipeline; the gap rule (over 500 ms, back to under 100 ms).
- **Proof:** with a slow `rdptest` the rule drops and recovers; the samples are otherwise exact.

### 4.4 AAC
- **Spec:** §10.1, the findings of 0.8.
- **Do:** `internal/mac/aacenc`: AudioToolbox's AAC-LC, whose raw access units (1024 frames, no ADTS) go out one per Wave2 PDU as 0.8 found; the AAC format block; the choice between AAC and PCM from the client's list.
- **Proof:** `mardp_mac` test of the encoder; `rdptest` negotiates AAC when it offers it and PCM when it does not.

### 4.5 Where the sound plays
- **Spec:** §10.1, D17, the answer to R2.
- **Do:** "On the client only": the default output device's mute property (Core Audio; the volume is never touched) is set while a client is connected and restored afterwards, also after a crash (a marker in the settings folder is cleared on the next start). R2 showed capture is not affected by the mute. "On both" and "off".
- **Proof:** the marker's logic with fakes; the muting itself by hand.

### 4.6 A real client plays it
- **Spec:** §15.3, §10.2.
- **Do:** `mardp_freerdp` tests: FreeRDP in the guest plays into the null sink, the recording is compared with the tone, for PCM and for AAC.
- **Proof:** the tests pass. Manual checks on the owner's Linux machine: a video with sound; the clap test for picture and sound. **Milestone M4 is published.**

## M5. The app

Everything of §12 on a fresh install.

### 5.1 Settings
- **Spec:** §12.2.
- **Do:** Every setting of §12.2 with its validation; which ones apply at once and which at the next session, said in the window.
- **Proof:** `gluttertest` tests for each setting against a fake host and a temporary `MARDP_HOME`.

### 5.2 Status and events
- **Spec:** §12.2.
- **Do:** The connected client's details and live numbers (frames a second, bitrate, delay, video path, sound format), Disconnect, the list of recent events.
- **Proof:** `gluttertest` tests with a fake host that changes state.

### 5.3 The menu-bar icon
- **Spec:** §12.3, §7.3.
- **Do:** The icon with its three states and its menu; closing the window keeps the app running; Quit stops the server.
- **Proof:** tests of the menu's model; the icon itself by hand, with real clicks.

### 5.4 Notifications and failed logins
- **Spec:** §12.4, §8.2.
- **Do:** The three notifications; failed logins in the events and the waiting rule shown.
- **Proof:** tests with a fake notifier.

### 5.5 Starting by itself
- **Spec:** §7.3.
- **Do:** "Start the server when the app opens" and "Open at login".
- **Proof:** the setting's logic by test; logging out and in by hand.

### 5.6 The bundle and the README
- **Spec:** §16, §8.3.
- **Do:** The icon, the bundle's information and version (`-version`); the final `scripts/build-app.sh`; the README: install, the permissions and macOS's recurring Screen Recording question (R6), the Linux side (Cisco's `openh264`, not `noopenh264`; R5), the Remmina profile (TLS, the colour depth for H.264), turning the screen lock off while away (R3, unless the owner's check says otherwise), the signing identity, what mardp does not protect against.
- **Proof:** a bundle built from a clean checkout starts and passes `codesign --verify`.

### 5.7 A fresh install
- **Do:** The manual check list of §15.7 for a Mac that has never run mardp, written into `docs/MANUAL-CHECKS.md` and done by the owner.
- **Proof:** the owner's answers are recorded. **Milestone M5 is published.**

## M6. Performance

Every number of §14 is met, or corrected with a reason. Spec: §11, §14, §15.5.

### 6.1 Measuring
- **Do:** Timestamps through the pipeline (capture, encode done, queued, written); `scripts/perf.sh` runs `mardp -headless` with a moving test pattern and a tone against `rdptest` and prints frames delivered, the delay's distribution, bitrate, CPU and memory; `docs/PERFORMANCE.md` with the first numbers.
- **Proof:** the script runs unattended and its numbers are in the document.

### 6.2 Allocations
- **Spec:** §11 rule 2.
- **Do:** Benchmarks that assert 0 allocations per frame and per sound packet in `rdp` and `internal/host`; fix what allocates.
- **Proof:** the benchmarks pass.

### 6.3 The real numbers
- **Spec:** §14.
- **Do:** With the owner's Linux machine (manual checks, with the exact steps): frames delivered, the delay from screen to screen, picture and sound apart. Tune the constants of §9.5 and the encoder; every change with its before and after in `docs/PERFORMANCE.md`.
- **Proof:** the table of §14 with a measured value in every row.

### 6.4 Idle
- **Spec:** §11 rule 6.
- **Do:** Find and remove every periodic wake-up while the screen is still and nothing plays.
- **Proof:** CPU under the target over 5 minutes with a client connected.

### 6.5 A long run
- **Do:** Thirty minutes of video: memory not growing, no dropout, no goroutine or encoder session left after disconnect; a hundred connect and disconnect cycles.
- **Proof:** the numbers at the start and the end are in `docs/PERFORMANCE.md`.

### 6.6 Version 1.0 (**ask the user**)
- **Do:** The last corrections to the specification and the README.
- **Proof:** every check passes on a clean checkout. **Milestone M6 is published as 1.0**, with the owner's word.

## M7. After 1.0

Not planned here. Each item of §17 (M7) is a decision of the owner; when one is taken, its steps are added to this file.
