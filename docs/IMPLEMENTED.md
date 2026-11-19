# mardp: Implemented steps

One entry per finished step of `docs/IMPLEMENTATION.md`, newest last: what was built, how it was proved, what differs from the plan and why, and which manual checks are pending.

## 0.1 Groundwork (2026-11-19)
- **Built:** `go.mod` (`github.com/rm4n0s/mardp`, Go 1.27.3, `github.com/rm4n0s/errors` v0.2.0). `.gitignore` entries for the binary, `build/`, `*.app` and `spikes/out/`. `scripts/check.sh` runs gofmt, `go vet` (also as Linux, so the stubs of `internal/mac` are checked), golangci-lint, the tests with `-race -tags debug_mode` and without tags, and the Linux build and vet of `rdp/...` once it has Go files; on macOS it gives the tests the resolved temporary folder. `.golangci.yml` (v2): the standard linters plus depguard (the standard `errors` package is refused), forbidigo (`fmt.Errorf` is refused), errorlint, gocritic, misspell and a few more; `spikes/` is excluded. `internal/archtest`: the rules of SPEC §4.1 as data; a package folder the table does not know is a violation.
- **Proved:** `scripts/check.sh` passes. `TestRepository` checks this tree; `TestCheckFindsPlantedViolation` plants `import "C"` in `internal/host` and an import of `internal/host` in `rdp` and finds exactly those two (while `testdata/` and a correct cgo file under `internal/mac` are left alone); `TestForbidden` covers every row of the table; `TestCheckFailures` asserts the route of each of the four tags. The lint rules were tried on a planted file that imports `errors` and calls `fmt.Errorf`: both reported.
- **Differs from the plan:** SPEC §4.1 now says what the table left open: a package under `internal/mac` may import `internal/host` (the types it implements) and its siblings; `rdp/rdptest` is imported only by test files; `spikes/` may import anything and nothing imports it.
- **Manual checks:** none.

## 0.2 The development signing identity (2026-11-19)
- **Built:** `scripts/make-identity.sh`, which the owner ran once: a self-signed RSA 2048 code-signing certificate `mardp development` (10 years) and its key, made with the system's LibreSSL, imported into the login keychain with `codesign` allowed, and trusted for code signing only. The owner also ran `security set-key-partition-list` for that key alone, so `codesign` signs without a prompt. `scripts/sign.sh <bundle>` signs with the identity (`MARDP_SIGN_IDENTITY` overrides the name), verifies the signature, and stops with a message pointing to the README when the identity is missing (tried before it existed). README section "Building".
- **Proved:** two builds of a trivial bundle with different binaries (`-X main.build=1` and `=2`), each signed by `scripts/sign.sh`, have the same designated requirement: `identifier "com.github.rm4n0s.mardp.signproof" and certificate leaf = H"a04a55a7…"`. No prompt appeared.
- **Differs from the plan:** the identity is made by a script instead of Keychain Access's Certificate Assistant, so the steps are exact. The requirement pins the certificate leaf, so a new identity (a lost keychain) means granting the permissions once more.
- **Manual checks:** none.

## 0.3 The virtual machine (2026-11-19)
- **Built:** UTM 5.0.9 in `/Applications` (signature and notarization checked). The machine `mardp-test`: Fedora 45 Cloud Base for aarch64 on UTM's QEMU backend, 4 cores, 4 GiB, 25 GiB, shared network, `virtio-gpu-gl-pci` display, `intel-hda` sound. `scripts/vm/create.sh` makes it from the image through UTM's AppleScript dictionary; `scripts/vm/user-data` (cloud-init) installs on the first boot: the guest agent, FreeRDP 3.33.1 (`xfreerdp`, `sdl-freerdp`), Remmina 1.4.43, PipeWire 1.6.11 with the PulseAudio tools (null sink, `parecord`), Xvfb, xdotool, ImageMagick, ffmpeg-free, ncat, tcpdump, Xfce with LightDM auto-login. `scripts/vm.sh`: `start`, `stop [--force]`, `status`, `exec`, `sh`, `push`, `pull`, `address`, `host-address`; it stops only a machine it started. `docs/VM.md` records the files, checksums, versions and every step.
- **Proved:** `scripts/vm.sh stop`, `status` (stopped), `start`, then `scripts/vm.sh sh 'xfreerdp /version'` printed "FreeRDP version 3.33.1", and `/buildconfig` printed the build configuration (kept in `build/freerdp-buildconfig.txt`, read in 0.4). The guest reached a TCP listener on the Mac at `192.168.64.1:13389` (the guest was `192.168.64.3`); the 11 bytes it sent arrived intact.
- **Differs from the plan:** Fedora's Cloud Base image with cloud-init instead of the desktop installer image: it sets itself up with no clicks and is 569 MiB instead of about 3 GiB; the desktop (Xfce) is installed on top, so the owner can still use Remmina in the machine's window. SELinux is permissive in the guest: enforcing keeps the guest agent's commands from running `rpm`, `journalctl` and from reading logs. No Automation prompt appeared for `utmctl` or `osascript`. SPEC §15.3 is updated. The throwaway `spikes/probe` (a signed bundle that asked for Screen Recording, Accessibility and system sound while the owner was there) and `spikes/recorder` came in on this branch; they serve 0.4 to 0.8.
- **Manual checks:** none.

## 0.4 What a real client sends, and H.264 in it (2026-11-19)
- **Built:** `rdp/testdata/x224/`: the X.224 Connection Requests of FreeRDP 3.33.1 (`xfreerdp` with no `/sec:`, `/sec:tls`, `/sec:nla`, `/sec:rdp`) and Remmina 1.4.43 (profile with `security=tls` and with automatic negotiation), recorded by `spikes/recorder` on the Mac from the test machine, with a README of their origin and what each asks for. README section "The client (Linux)". `docs/MANUAL-CHECKS.md`.
- **Proved:** the six recordings exist (35 or 43 bytes, cookie `mstshash=mardp`); automatic security in both clients asks for `0x0B` (TLS, CredSSP, CredSSP with the early authorization result), `tls` for `0x01`, `nla` for `0x03`, and `/sec:rdp` sends no negotiation request. FreeRDP's build configuration has `WITH_GFX_H264=ON` with OpenH264 linked (`ldd`: `libopenh264.so.8`, `libfdk-aac.so.2`, `libopus.so.0`), and Remmina's RDP plugin lists "GFX AVC420 (32 bpp)" (read from the plugin's strings, since its dialog cannot be opened from the guest agent). The answer to R5 is in SPEC §19.
- **Differs from the plan:** SPEC §19 gets its "Answer" column now (planned for 0.9); the risks not yet checked say which step checks them. Found: `libopenh264.so.8` is provided both by Cisco's `openh264` and by the stub `noopenh264`; the machine got the real one, and the README and the manual check tell the owner how to tell them apart. Which H.264 profiles OpenH264 decodes is left to 0.8, which has an encoder to make High-profile streams (the guest's ffmpeg has only OpenH264's own Constrained Baseline encoder). SPEC §5.1 now says a client that sends no negotiation request gets the same failure.
- **Manual checks:** pending, see docs/MANUAL-CHECKS.md.

## 0.5 Capture with the display asleep (2026-11-19)
- **Built:** `spikes/probe capture` (made in 0.3) was run as planned; no product code. Screen Recording was granted to the probe in 0.3, while the owner was there.
- **Proved:** the answer to R1 is in SPEC §19. On this Mac `displaysleep` is 0, so macOS never sleeps the display by itself: the owner's "asleep" monitor is the monitor's own standby, which macOS sees as awake, and capture works in it (30 s, 33 frames/s on a nearly still screen, no gaps over 53 ms). With the display really asleep (`pmset displaysleepnow`, `CGDisplayIsAsleep` = 1), the wake assertion woke it at once and 1920×1080 frames came within 30 ms: 30 s still (36.5 frames/s, longest gap 54 ms), then five 10 s runs asleep and awake, still and with the probe's animated window (55 frames/s with motion), no blank or suspended frames. Frames are `420v` IOSurfaces with BT.709 attachments and dirty rectangles.
- **Differs from the plan:** "while a video plays" was done with the probe's animated window, not a video player: no app was opened on the owner's desktop at night. One run from asleep failed after 0.7 s with `SCStreamErrorDomain` -3815: the monitor came back from sleep as a new display (ID 3 became 2, same vendor and model). SPEC §7.2 now says how the capture follows display changes and remembers a display by its UUID; §9.3 notes the `420v`/`420f` choice. A Mac with no monitor was not tried, so the virtual display stays in M7 as planned.
- **Manual checks:** none.

## 0.6 System sound (2026-11-19)
- **Built:** `spikes/probe` gained `tap -s -tone -mute -exclude-self -exclude-pid` (a Core Audio process tap), `capture -exclude-pid`, and `tone` (plays the tone in a process of its own; a copy of the bundle with its own identifier, `spikes/out/Tone.app`, played it as "another app").
- **Proved:** the answer to R2 is in SPEC §19, and §10.1 says how "on the client only" is done. All cases ran with the owner's output muted and at volume 0 (left as found): ScreenCaptureKit captured the probe's own tone and another process's at 0.25 (the level generated); `excludesCurrentProcessAudio` gave 0.0000; another app left out with `excludingApplications` gave 0.0000. The tap gave 0.25 while muted, with and without `CATapMuted`, and 0.0000 with the player's PID excluded; ScreenCaptureKit kept capturing 0.25 while another process held a muting tap.
- **Differs from the plan:** `probe audio` became the `capture -audio` and `tap` commands. "Output not muted" was not run: the owner asked that the volume and mute not be touched at night; it is a manual check. An app without a window is not among ScreenCaptureKit's shareable applications, so the "other process" had to be an app with a (nearly transparent, 2×2) window; `afplay` could only be left out of the tap. The muting tap works and is written into §10.1 as the alternative, but 1.0 keeps the planned device mute (no extra permission, works on macOS 14.0); §10.1 also says the client hears the level before the Mac's volume.
- **Manual checks:** pending, see docs/MANUAL-CHECKS.md.

## 0.7 Pointer, lock screen, the recurring question (2026-11-19)
- **Built:** `spikes/probe` gained `cursor-set` (makes itself the front app and sets six system pointers in turn, then hides and shows the pointer; no events are posted) and `capture -report -move-at` (frames, mean brightness and the lock state every few seconds, and one zero-distance mouse move at a given second, for the owner's lock check). `probe cursor` also prints `CGCursorIsVisible`.
- **Proved:** R4 and R6 are answered in SPEC §19, and §10.3 says how the pointer is read. A background probe saw every pointer the front one set within 0.1 s, with the right size and hotspot, and saw the pointer hidden. R6 comes from what M0 met (no second question) and published reports on macOS 15; nothing public covers macOS 26 or 27, so 1.0 designs for the question.
- **Differs from the plan:** the pointer was changed by a second app setting it, not by moving the mouse: no test posts events into the owner's desktop. R3 is the owner's check and is still open: §19 says what 1.0 assumes until then. §10.3 now polls the pointer (after client mouse events, after frames, and 4 times a second only while a client is connected), because macOS has no notification for it.
- **Manual checks:** pending, see docs/MANUAL-CHECKS.md.

## 0.8 H.264 and AAC on this Mac (2026-11-19)
- **Built:** `spikes/probe h264` (captures 1080p with the probe's animated window, encodes with a hardware VTCompressionSession in real-time mode with no frame reordering, chosen profile, optional low-latency rate control and full range, prints the SPS fields and encode times, writes Annex B) and `spikes/probe aac` (an AudioConverter from ScreenCaptureKit's float format to AAC-LC, prints the output's form). Seven 10 s streams went to the test machine.
- **Proved:** SPEC §9.3, §10.1 and §19 R5 have the numbers. At 1920×1080 with motion (about 56 frames/s, the capture's rate): 7.3 ms a frame on average and 8.9 ms at the 99th percentile with low-latency rate control, 8.5 to 9 ms and 12 ms without; level 4.2 in every profile. Low-latency mode writes `max_num_reorder_frames = 0` into the SPS; the VUI carries BT.709 and the range of the input. In the guest, OpenH264 2.6.0 (through FFmpeg's `libopenh264` decoder) decoded every frame of High, Constrained High, Main and Constrained Baseline streams with no error, and decoded pictures matched the screen. AAC: raw access units of 1024 frames (no ADTS), at most 1536 bytes, AudioSpecificConfig `11 90`, 2112 frames of encoder delay; FreeRDP's FDK decoder opens with the raw transport and builds that configuration itself (read in FreeRDP's `dsp_fdk_impl.c`).
- **Differs from the plan:** §9.3 now picks Constrained High with low-latency rate control instead of plain High. The "player" in the guest is FFmpeg with OpenH264 (no other H.264 decoder is installed there), which is the decoder FreeRDP uses. macOS's Screen Recording question came back during these runs, hours after the grant: capture went on while it stood on the screen, and R6 in §19 records it; it was left for the owner.
- **Manual checks:** pending, see docs/MANUAL-CHECKS.md (the question on the screen).

## 0.9 Close the checks (2026-11-19)
- **Built:** §19's intro says how the risks were answered; §12.2's checklist comes back when a permission is withdrawn and explains macOS's recurring Screen Recording question (R6). `spikes/` is deleted, with its exemptions in `internal/archtest`, `.golangci.yml` and `.gitignore` (a `spikes/` directory is now an unknown package that archtest rejects); the signed probe stays in `build/m0/Probe.app`, outside git, for the owner's manual checks, which say how to rebuild it from `step-0.8`. The plan is corrected: 2.4 (display UUID, wake assertions, restart after a display change or -3815, permission error ends the session), 2.6 (pointer polling), 3.6 and 3.9 (low-latency rate control, Constrained High, `max_num_reorder_frames = 0`), 4.4 (raw AAC access units), 4.5 (the device's mute, never the volume), 5.6 (README: the recurring question, `openh264`, the screen lock).
- **Proved:** `scripts/check.sh` passes with the new archtest cases. No "to be checked" is left for R1 to R6: R1, R2, R4, R5 and R6 have answers, and R3 has what 1.0 assumes until the owner's check. No step was added or dropped: no answer was bad enough to move the virtual display into 1.0, and "on the client only" stays.
- **Differs from the plan:** the "Answer" column came in 0.4. R3 stays open on the owner's check rather than blocking M0.
- **Manual checks:** pending, see docs/MANUAL-CHECKS.md (0.4 Remmina's H.264, 0.6 sound with the output on, 0.7 the lock and the question on the screen).

## 1.1 Reading and writing bytes (2026-11-19)
- **Built:** `rdp/internal/wire`. `Reader` reads bounds-checked integers (8, 16, 32, 64 bits, both byte orders), slices of the input (`Bytes`, `Skip`, `Sub`), PER (length determinant, `PERInteger16` with a lower bound, `PERUint`) and BER (tag in both forms, definite length checked against the input, `BERHeader`, `BERInteger`, `BERUint`, `BEREnumerated`, `BERBoolean`, `BEROctetString`). `Writer` appends the same forms, plus `Reserve` and patches for lengths known later, and `BERReserveLength`/`PatchBERLength` for the two-byte long form. 16 error tags, each created where the failure is found.
- **Proved:** table tests for every form, both directions (`TestReaderIntegers`, `TestPERLength`, `TestPERInteger16`, `TestPERUint`, `TestBERTag`, `TestBERLength`, `TestBERIntegers`, `TestBERUint`, `TestWriterIntegers`, `TestWriterPatch`). `TestTruncatedEveryRead` cuts a valid encoding of each of the 21 reads at every length and checks it fails with a route through that read. Every tag has a route test (`TestReaderErrors`, `TestPERErrors`, `TestBERErrors`, `TestWriterErrors`). Microsoft's bytes: the Erect Domain Request, Attach User Confirm and Channel Join Request of MS-RDPBCGR 4.1.5 to 4.1.8 (PER), and the head of the Connect-Initial of 4.1.3 up to targetParameters (BER), read and written back. `TestNoAllocations`: 0 allocations reading and writing one of every form; `BenchmarkRead` 35 ns, `BenchmarkWrite` 39 ns, 0 B/op.
- **Differs from the plan:** `BERUint` was added. Microsoft's Connect-Initial example writes maxMCSPDUsize 65535 as `02 02 ff ff`, which strict BER reads as -1, so MCS's non-negative parameters are read unsigned; SPEC §5.1 says so. The writer keeps strict BER, so writing the example back gives one byte more in that field (the test spells it out).
- **Manual checks:** none.

## 1.2 TPKT and X.224 (2026-11-19)
- **Built:** `rdp/internal/tpkt`: `Classify` tells a TPKT (first byte 3) from a fast-path PDU (two low bits 0) from anything else; `HeaderSize` and `FrameLength` give a frame's total length from its first 2 to 4 bytes, for either kind; `Begin`/`End` write a TPKT header and patch its length. `rdp/internal/x224`: `ParseConnectionRequest` (the mstshash cookie or another routing token, RDP_NEG_REQ, RDP_NEG_CORRELATION_INFO) and `WriteConnectionRequest`; `WriteConnectionConfirm` with RDP_NEG_RSP, RDP_NEG_FAILURE or nothing, and `ParseConnectionConfirm`; `WriteDataHeader`/`ReadDataHeader` (`02 f0 80`); `WriteDisconnectRequest`/`ParseDisconnectRequest`; `Code`. Both are pure byte packages on `wire`: reading frames off the connection belongs to the server (1.5). 15 error tags.
- **Proved:** Microsoft's Connection Request (MS-RDPBCGR 4.1.1, cookie `eltons`, PROTOCOL_RDP) and Connection Confirm (4.1.2, SRC-REF 0x1234) read, and written back to the same bytes, TPKT included. The six recordings of 0.4 (`rdp/testdata/x224`) read with the right cookie and protocols (none for `/sec:rdp`) and are written back byte for byte. Requests with a routing token, correlation info and no negotiation go round; confirms for TLS, SSL_REQUIRED_BY_SERVER and no negotiation are checked against spelled-out bytes. Every tag has a route test, with a truncation case for each read; 0 allocations parsing a request, writing a confirm and the data header. Coverage 100% (tpkt) and 96.5% (x224).
- **Differs from the plan:** the client-side halves (writing a request, reading a confirm) were added for the tests and for `rdptest` (1.5). A Connection Request with correlation info but without CORRELATION_INFO_PRESENT, or the flag without the info, is rejected.
- **Manual checks:** none.

## 1.3 MCS (2026-11-19)
- **Built:** `rdp/internal/mcs`. BER: `ParseConnectInitial`/`WriteConnectInitial` and `WriteConnectResponse`/`ParseConnectResponse` with their DomainParameters (read unsigned, written in strict BER with a short-form length, as Microsoft writes them). Aligned PER: Erect Domain Request, Attach User Request and Confirm, Channel Join Request and Confirm (the optional field and the 4-bit Result across two bytes), Send Data Request and Indication (priority and segmentation, the data pointing into the input), Disconnect Provider Ultimatum; `Choice` names a domain PDU from its first byte. Every reader checks the PDU kind and that nothing trails. 7 error tags.
- **Proved:** Microsoft's Connect-Initial (4.1.3) reads with all 24 parameters and its 307 bytes of user data. It is written back with the five integers Microsoft wrote in non-strict form widened, a difference the test spells out byte for byte. Microsoft's Connect-Response (4.1.4) reads and is written back byte for byte. These domain PDUs from Microsoft's examples read and write back to the same bytes: Erect Domain (4.1.5), Attach User Request and Confirm (4.1.6, 4.1.7), Channel Join Request and the user channel's Confirm (4.1.8), the Client Info's Send Data Request head with a two-byte length (4.1.10), and the License Error's Send Data Indication (4.1.11). Further cases check failure results, the ultimatum's reasons and large Erect Domain values. Hostile input: each PDU is cut at every length, and 20000 seeded random and mutated inputs go to every reader: nothing panics and every failure is tagged. Every tag has a route test, and 0 allocations are used parsing a Connect-Initial, writing a Connect-Response, reading a Send Data Request and writing a Send Data Indication. Coverage is 95.5%. The two examples are in `rdp/testdata/ms-rdpbcgr` with their sources.
- **Differs from the plan:** the client halves (writing a Connect-Initial, reading a Connect-Response and the confirms) were added for the tests and `rdptest`. The server's choice of domain parameters is left to the state machine (1.5).
- **Manual checks:** none.

## 1.4 GCC and the settings blocks (2026-11-19)
- **Built:** `rdp/internal/gcc`. T.124 PER: `ParseConferenceCreateRequest` (ConnectData's object identifier, the request's fields, the numeric conference name, the "Duca" user data set) and `WriteConferenceCreateRequest`; `WriteConferenceCreateResponse` and `ParseConferenceCreateResponse` (node ID, tag, result, the "McDn" set). Client blocks: `ParseClientData`/`WriteClientData` with the core block (`ClientCore`, its 15 optional fields counted from the block's length, `ClientCoreLen`, `Name`, `Early`, `PhysicalSize`, `ScaleFactors`), security, network (31 channels at most), cluster, monitor (1 to 16), message channel and multitransport; monitor extended and unknown blocks are skipped. Server blocks: `WriteServerData`/`ParseServerData` with core (0 to 2 optional fields), network (with the pad for an odd count) and security (none, or a random and a certificate). 13 error tags.
- **Proved:** Microsoft's Conference Create Request (MS-RDPBCGR 4.1.3) gives its 284 bytes of client data and is written back byte for byte; those blocks read field by field (core of 216 bytes with 10 optional fields, "ELTONS-DEV2", cluster 0x0d, security 0x1b, rdpdr/cliprdr/rdpsnd with their options) and are written back byte for byte. Microsoft's Conference Create Response (4.1.4) reads (node 31219, tag 1, success, 264 bytes); written back, it differs only in the connectPDU length, the real 279 (`81 17`) where Microsoft wrote 42 (`2a`). Its server blocks read (core, network 1003 with 1004 to 1006 and the pad, security with a 32-byte random and a 184-byte certificate) and are written back byte for byte. `TestClientCoreEveryLength` writes and reads a core block of each of the 16 lengths a client may send (132 to 234 bytes) and checks that every length between two field boundaries is refused. Round trips with monitors (negative coordinates), message channel and multitransport, and what mardp's server will send (core with both optional fields, network, security none). Hostile input: each input cut at every length and 20000 seeded random and mutated inputs to every parser. Every tag has a route test; 0 allocations parsing a request with its client data and writing server data with the response. Coverage 95.9%.
- **Differs from the plan:** the connectPDU length is not checked when read, because Microsoft's example gets it wrong; written, it always takes the two-byte form. A core block that ends inside a field is refused rather than read up to the last whole field. The client halves (writing a request and client data, reading a response and server data) were added for the tests and `rdptest`.
- **Manual checks:** none.

## 1.5 The server and the connection up to the channels (2026-11-19)
- **Built:** `rdp.Config` (certificate, logger, `MaxPending`, `LoginTimeout`), `rdp.NewServer`, `Server.Serve(ctx, listener)`. Each connection runs on its own goroutine, and `context.AfterFunc` closes it when the server stops. Its state machine goes through the X.224 negotiation (TLS, or the failure SSL_REQUIRED_BY_SERVER), the TLS handshake (TLS 1.2 or newer), the MCS Connect-Initial with the GCC blocks and the Connect-Response with the server core, network and security blocks, then Erect Domain, Attach User and the channel joins. The limits before login: a token for each connection that has not logged in (8 by default; one more is closed when it is accepted), and one deadline on the connection from accept (10 s by default) that covers the handshake and every read and write. Logging uses `slog`, with the error as an attribute. `tpkt.ReadFrame` reads one frame of either kind from a stream. `rdp/rdptest` begins: `Connect`, and the same sequence as step methods (`Negotiate`, `StartTLS`, `ConnectMCS`, `Attach`, `Join`, `JoinAll`). It also has raw frame access (`WriteFrame`, `SendData`, `ReadFrame`, `ReadData`, `CloseWrite`), `EditClientData` to break the client data, and `Certificate`, a self-signed RSA 2048 certificate with a client configuration that trusts only it. 7 new tags in `rdp`, 10 in `rdptest`, 2 in `tpkt`.
- **Proved:** `TestConnectionSequence`: `rdptest` reaches the end of the channel joins over TLS on 127.0.0.1 with FreeRDP's protocols and two channels, TLS only with no channels, 31 channels, no network block and an RDP 4.0 core block. It checks the server core, security none, the domain parameters, the user ID and the joined channels, and that the log has the client's data. `TestTLSRequired`: no negotiation, CredSSP only, and CredSSP with RDSTLS each get the failure code 1, and the server closes the connection. `TestPendingLimit`: the 9th connection that has not logged in is closed at once and logged, and a place comes back when a connection ends. `TestLoginTimeout` (300 ms): a silent connection, one that stops in a header, one that never starts TLS and one that stops after the joins are each closed after the timeout. `TestUnknownChannelRefused`: rt-no-such-channel, and a repeated join counts once. `TestServeStops`: Serve returns nil and closes its connections and the listener. `TestServerDomainParametersAreMicrosofts`. Every tag has a route test: the server's against `serveConn` over TCP and a pipe, the client's against a scripted fake server. Eight repeated runs with `-race` pass. Coverage: `rdp` 90.9%.
- **Differs from the plan:** "one goroutine pair per connection": the writer goroutine with its queues is step 1.8, so during the sequence the connection's goroutine writes itself, and `context.AfterFunc` closes the connection when the server stops. Until 1.6 adds login, a connection that has joined its channels reads and drops what the client sends until the login deadline closes it. Decisions now in SPEC §5.1: Microsoft's domain parameters and channel numbering, EXTENDED_CLIENT_DATA_SUPPORTED in the confirm, a connection ends when serverSelectedProtocol is not TLS, unknown channels are refused, joins are not skipped, and how the deadline works. `tpkt.ReadFrame` takes no context, like `io.ReadFull`: deadlines and closing the connection end its reads.
- **Manual checks:** none.

## 1.6 Login and licensing (2026-11-19)
- **Built:** `rdp/internal/sec`: the Basic Security Header (a SEC_ENCRYPT header is refused, the PDU's flag is required); `ParseClientInfo`/`WriteClientInfo` and `ParseInfoPacket`/`WriteInfoPacket` with every field of TS_INFO_PACKET and TS_EXTENDED_INFO_PACKET (Unicode and ANSI strings with their limits, the time zone, session ID, performance flags, auto-reconnect cookie, reserved fields, the dynamic DST key name); `Secret`, whose `String`, `GoString` and `LogValue` say "(hidden)", and `ClientInfo.LogValue`, which logs only whether a password was sent; `WriteLicenseValidClient`/`ParseLicenseError`; `WriteSetErrorInfo`/`ParseSetErrorInfo` with the ERRINFO codes the server uses. 10 tags. In `rdp`: `Config.Authenticator` (required), `Authenticator`, `LoginRequest` (user, domain, password, remote address; printed and logged without the password), `StaticAuthenticator` (SHA-256 sums compared in constant time). After the joins the connection reads Client Info from the user on the I/O channel. With no user name, no password, a smart card PIN or credentials the Authenticator refuses, it sends Set Error Info (when the client supports it) and a Disconnect Provider Ultimatum, logs "login failed" with the user and the reason, and closes. Otherwise it sends the license "valid client" from user 1002, leaves the count of connections not logged in, takes the login deadline off and logs "logged in". 4 new tags in `rdp`. `rdptest`: `Options.User/Domain/Password/EditClientInfo`, `ClientInfo()`, `Login`, 2 new tags.
- **Proved:** Microsoft's decrypted Info Packet (MS-RDPBCGR 4.1.10, now `rdp/testdata/ms-rdpbcgr/client-info.bin`) reads field by field and is written back byte for byte. Round trips for Unicode and ANSI at each of the 7 levels of optional fields, with and without a cookie. The license PDU equals the bytes of 4.1.11 behind `80 00 00 00`, and Set Error Info is checked against bytes spelled out from 2.2.5.1. Each input is cut at every length and 20000 seeded mutated inputs go to the three parsers. Every tag has a route test. Writing either PDU makes 0 allocations, and `sec` coverage is 97%. `TestLogin` covers:
  - the right password logs in, and the connection outlives the 300 ms login deadline;
  - a wrong password, a wrong user, no password, no user and a smart card PIN each get Set Error Info 0x0A and the ultimatum (rn-provider-initiated) and are closed;
  - ANSI strings log in;
  - a client without RNS_UD_CS_SUPPORT_ERRINFO_PDU gets the ultimatum alone.
  
  A test renders the whole log, at debug level, as JSON and finds neither password in it, while the user, the reason and `"password":true` are there. `TestLoggedInLeavesThePendingCount` logs in 5 clients with `MaxPending` 2. Route tests in `rdp` cover a wrong password, Client Info on a static channel and from another user, an encrypted header, another PDU instead of Client Info, a missing Authenticator, and a deadline that cannot be cleared. The `rdptest` tests cover the license, the refusal with and without Set Error Info, another license status and cut PDUs. Six repeated runs with `-race` pass. Coverage: `rdp` 91.4%.
- **Differs from the plan:** MS-RDPBCGR has no "logon failure" Set Error Info code. The server sends ERRINFO_SERVER_FRESH_CREDENTIALS_REQUIRED (0x0A) and then the ultimatum. This is open question Q1 in SPEC §20, and step 1.13 checks what FreeRDP and Remmina do with it. SPEC §5.1, §5.4 and §8.2 now spell out the login. The address wait of §8.2 is the app's job (step 2.x), and `LoginRequest.Remote` gives it the address. After login the connection reads and drops what the client sends until 1.7 adds the capability exchange.
- **Manual checks:** none.

## 1.7 Capabilities and the open session (2026-11-19)
- **Built:** `rdp/internal/caps`:
  - Share Control and Share Data Headers (`ReadPDU`, which skips T.128 FlowPDUs; `ParseData`, which refuses bulk compression; `BeginControl`/`EndControl`, `BeginData`/`EndData`).
  - Demand Active and Confirm Active with the capability sets the server reads or writes: General, Bitmap, Order (written, all orders off), Pointer, Input, Virtual Channel, Sound, Share, Font, Multifragment Update, Large Pointer, Surface Commands, Frame Acknowledge. Other sets are skipped.
  - Synchronize, Control, Font List and Font Map.
  - 6 tags.

  In `rdp`:
  - `Config.Handler` (required) and `Config.DesktopSize`; `Handler`, `HandlerFunc`, `Session` (`Client`, `DesktopSize`, `Close(ctx, reason)`), `Client`, and `CloseReason` (`CloseByHost` 0x01, `CloseReplaced` 0x05, `CloseServerStopped` 0x19).
  - After the license, the server sends Demand Active with the sets of SPEC §5.2. It reads Confirm Active, answers each finalization PDU as it arrives, and opens the session after Font Map: the handler runs on its own goroutine while the connection's goroutine reads.
  - A client ultimatum or hang-up ends the session. `Close` sends Set Error Info and the ultimatum, then gives the client 2 s to hang up. When `Serve` stops, the server closes each session with `CloseServerStopped`.
  - 5 new tags in `rdp`.

  `rdptest`: `Activate` (with FreeRDP-like capabilities and `Options.EditCaps`), `ServerCaps`/`ShareID`/`ServerSource`, `SendShare`, `AwaitDisconnect`, `Disconnect`; 1 new tag.
- **Proved:**
  - **Microsoft's examples:** the ten examples of MS-RDPBCGR 4.1.13 to 4.1.22 are now in `rdp/testdata/ms-rdpbcgr`. Demand Active and Confirm Active read field by field. Each set the server writes is written back and compared with Microsoft's bytes, except where Microsoft's bytes are uninitialized (Share pad, Input IME name, Input pad). The Font set is not compared because Microsoft's has 4 bytes against the structure's 8. The eight finalization PDUs are parsed and written. Client PDUs match exactly; server PDUs differ only in the pad1, streamID and uncompressedLength that Microsoft's server fills with garbage, and in the Synchronize targetUser. The README lists these faults.
  - **caps tests:** round trips of 2000 random capability sets, two PDUs in one buffer, and FlowPDUs. Every example is cut at every length, and 50000 mutations are fed to the parsers: every failure is tagged. Writing and parsing make 0 allocations, every tag has a route test, and coverage is 94.2%.
  - **The handler sees the client's data:** `TestSessionOpened` checks name, address, user, domain, build, the asked size, keyboard, channels, fast-path output, input flags, pointer cache, a large pointer of 384, MaxRequestSize and frame acknowledgement. It also checks what Demand Active said: 32 bpp, the session's size, the input flags and the sets.
  - **Desktop size:** `TestDesktopSize` checks that `Config.DesktopSize` sets the size, before Confirm Active is known. `TestDesktopSizeInvalid` covers 0, -1 and 8193.
  - **Ending the session:**
    - `TestSessionClose`: each reason reaches the client with its code and rn-provider-initiated, with no Set Error Info for a client without the flag, and the session's context ends.
    - `TestSessionCloseWaitsForTheClient`: a client that does not hang up is hung up on after 2 s.
    - `TestClientDisconnects` and `TestClientHangsUp`.
    - `TestServerStopClosesSessions`: the client gets 0x19.
    - Every new tag has a route test: in `rdp`, against `serveConn` for the activation errors and a connection whose read deadline fails for `Close`; in `rdptest`, against the scripted fake server.
  - Five repeated `-race` runs pass. Coverage: `rdp` 90.5%.
- **Differs from the plan:**
  - `Close` takes a `ctx` (SPEC §6: every method that does I/O does).
  - The connection writes under a mutex until the writer goroutine of step 1.8.
  - `Client` does not yet say whether the client decodes H.264 or which audio formats it has; those come with the GFX and rdpsnd channels (SPEC §6 updated).
  - What the session's client sends is read and dropped until step 1.9.
  - A bug found by the route test: `Close` had canceled the session's context before writing, so a handler passing its own context abandoned the write. The context is now canceled after the write.
  - SPEC §5.2 (what Demand Active holds and how finalization is answered), §5.4 (close codes and the 2 s wait) and §6 (names) are updated.
- **Manual checks:** none.

## 1.8 The writer and fast-path output (2026-11-19)
- **Built:** `rdp/internal/fastpath`:
  - `AppendUpdate` frames one update as Fast-Path Update PDUs (MS-RDPBCGR 2.2.9.1.2), each at most 16,383 bytes, with the one-byte length up to 127 bytes and the two-byte length beyond. An update larger than 16,377 bytes goes as FIRST, NEXT… and LAST fragments, one PDU each. `MaxUpdate` gives the largest update a client takes: its MaxRequestSize, or one PDU's worth when it sent none.
  - For the client side, which `rdptest` uses: `ParsePDU`, `ReadUpdate` (compressionFlags included) and `Reassembler`, which follows the sequence rules of 3.2.5.9.3.1.
  - 6 tags.

  In `rdp`:
  - The session's writer goroutine (`writer.go`) is the only thing that writes to the connection once the session is open. It has two queues, control (32 packets) and video (4 packets), and takes from control first. Each packet is whole frames written in one write; the packets are made when the session starts and go back to a free list after each write. A send never waits: with no free packet it fails with `TagQueueFull`, and after the session ended with `TagSessionOver`. A failed write closes the connection, and the session ends with that error.
  - Unexported `Session.sendUpdate` and `sendShare` are what the video, pointer and input steps will use; `sendUpdate` refuses a client without fast-path output (`TagFastPathUnsupported`).
  - `Close` now goes through the writer: Set Error Info and the ultimatum in one packet on the control queue, ahead of queued video. A context that ends first closes the connection (`TagCloseAbandoned`). The conn's own writes are built by `appendTPKT`/`appendData`/`appendIO`, shared by the sequence and the writer.
  - 4 new tags in `rdp`.

  `rdptest`: `ReadUpdate` reads the next fast-path update whole, joined in a buffer of the client's confirmed MaxRequestSize; `Client.Caps` holds what the client confirmed.
- **Proved:**
  - **An update larger than the fragment size arrives whole:** `TestUpdates` sends updates of 0, 4, 16,377, 16,378, 300,000 and 0x3f0000 bytes (the client's MaxRequestSize), and 16,377 bytes to a client without a Multifragment Update set. `rdptest` gets each one byte for byte, along with a control update queued after it.
  - **Control before video:** `TestWriterControlFirst` holds the writer in the write of video packet 1 while video 2 and 3 and then control 9 and 8 are queued. The writes that follow are 9, 8, 2, 3.
  - **0 allocations:** `TestWriterAllocs` sends a 200 KiB fragmented video update, a small control update and a share PDU through the running writer, 0 allocations each. `fastpath.TestAllocs` covers framing and reassembly. Benchmarks (Apple M6): `AppendUpdate` 1 MiB in 48 µs (21.6 GB/s), 0 allocs; `BenchmarkWriter` 256 KiB in 17 µs per update to the writer goroutine and back, 0 allocs.
  - **fastpath:** byte-exact PDUs worked out from the field tables (Microsoft gives no annotated example of fast-path output): no data, a pointer position, 122 and 123 bytes (the two length forms), 16,377 bytes, two and three fragments. Also 300 random round trips through framing, parsing and reassembly, every sequence fault, every cut of an update, and 5000 mutations. Coverage is 100%.
  - **Errors:** queue full on each queue (the other queue unaffected); a build error gives the packet back emptied; a failed write reaches `fail`, the waiting sender and `failure()`, then `TagSessionOver`; `TestSessionWriteFails` ends a real session through a connection whose writes fail; updates too large, without multifragment, and without fast-path output; sends after the client disconnected; `Close` with a canceled context; `rdptest.ReadUpdate`'s failures against the fake server. Every new tag has a route test.
  - Four repeated `-race` runs pass. Coverage: `rdp` 92.2%, `rdptest` 72.4%.
- **Differs from the plan:**
  - The public `Video()`/`Audio()`/pointer methods come with their steps (1.10, 1.11, M3). This step gives them `sendUpdate`/`sendShare`.
  - TCP_NODELAY needs no code: Go sets it on every TCP connection.
  - `rdptest.ReadUpdate` refuses a PDU with more than one update, which is legal but which this server never sends.
  - `conn.disconnect` now writes Set Error Info and the ultimatum in one write. The route of a sequence write failure is now `conn.send->conn.write`.
  - SPEC §4.2 (queue sizes and behavior), §5.2 (PDU size and fragmentation) and §5.4 (`Close` through the writer) are updated.
- **Manual checks:** none.

## 1.9 Input and the client's requests (2026-11-19)
- **Built:** `rdp/internal/input`:
  - Fast-path input (MS-RDPBCGR 2.2.8.1.2) and the slow-path Input Event PDU (2.2.8.1.1.3), read as iterators (`ParseFastPath`/`ParseSlowPath`, then `Next`), so reading allocates nothing. Every event kind: scancode, Unicode, mouse, extended mouse, relative mouse, synchronize, QoE timestamp (fast-path) and unused (slow-path). Encrypted fast-path input is refused.
  - Refresh Rect (2.2.11.2) and Suppress Output (2.2.11.3), and for `rdptest` the writers of all of them.
  - 7 tags.

  `rdp/internal/caps`: `WriteShutdownRequest` and `WriteShutdownDenied` (2.2.2.1, 2.2.2.2).

  In `rdp` (`input.go`):
  - `InputSink` and `FlowSink`, set with `Session.SetInputSink` and `Session.SetFlowSink`, and `Session.OutputSuppressed`. The reading goroutine picks up a new sink before each frame (a generation counter, so no lock on the path of each event) and replays the last lock state and the suppressed state to it.
  - The session's reader turns events into sink calls: keys with their `Extended` prefix, Unicode with surrogate pairs joined, pointer flags into moves, five buttons and both wheels (9-bit rotation in 120ths of a notch). Refresh Rect gives `KeyFrameWanted`; Suppress Output gives `OutputSuppressed` on a change, and a resume gives a key frame too. Shutdown Request is answered with Shutdown Request Denied on the control queue. Every Data PDU in a Send Data Request is handled. A Disconnect Provider Ultimatum from the client ends the session cleanly.
  - Ignored with a debug log: relative mouse, QoE and unused events, share PDUs that are not Data PDUs, unknown Data PDU types, static-channel data, FlowPDUs.

  `rdptest`: `SendInput` (fast-path), `SendSlowInput`, `SendRefreshRect`, `SendSuppressOutput`, `SendShutdownRequest` and `ReadShareData`.
- **Proved:**
  - **Every kind reaches the sink in order:** `TestInput` sends every key (E0 and E1 among them), Unicode, move, five buttons up and down, both wheels both ways, and lock states, first all fast-path, then all slow-path, then alternating the two forms; the sink records the same calls in the same order. `TestSurrogates` joins a pair and reports lone surrogates as `utf8.RuneError`.
  - **Requests:** `TestRequests` sends refresh, suppress twice, allow twice and refresh: the flow sink gets one key frame, one `OutputSuppressed(true)`, one `OutputSuppressed(false)` plus a key frame, and one key frame. Shutdown Request gets Shutdown Request Denied and the session goes on. `TestSinksSetLate` drops early input and replays the lock and suppressed states to sinks set later. `TestSessionIgnores` sends everything the server skips, then input that still arrives.
  - **Microsoft's examples:** the decrypted parts of the Shutdown Request, Shutdown Request Denied (4.2.1, 4.2.2) and fast-path input (4.7) examples are in `testdata/ms-rdpbcgr`, and are parsed and written byte for byte.
  - **Hostile input:** `TestHostileInput` ends a session with 13 malformed inputs (encrypted fast-path, unknown and cut events in both forms, a bad Suppress Output value, a cut Refresh Rect, another share ID, another channel, a cut share PDU, compressed data, a TPDU that is not Data, and an Attach User Request mid-session), each with its route. `input.TestHostile` tries every cut and 5000 mutations per seed, plus random bytes, without a panic.
  - **0 allocations:** reading a fast-path frame of several events into a sink, through the session (`TestInputAllocations`) and in the parser (`input.TestAllocations`). `TestSenders` checks each `rdptest` sender against the fake server.
  - Every new tag has a route test. Three repeated `-race` runs pass. Coverage: `rdp` 93.7%, `input` 95.1%, `caps` 94.2%, `rdptest` 75.0%.
- **Differs from the plan:**
  - `Key` takes an `Extended` prefix (none, E0, E1) instead of a bool, since Pause carries E1.
  - The sinks are set on the `Session` (`SetInputSink`, `SetFlowSink`), not given in `Config`, and the flow callbacks are a `FlowSink` now rather than part of the `Video()` interface that comes in 1.10, so Suppress Output and Refresh Rect are delivered from this step on.
  - Shutdown Request is answered with Shutdown Request Denied, as 2.2.2.2 says for a logged-on session; the plan only named the request.
  - SPEC §5.3 (what the session reads) and §6 (input and flow) are updated.
- **Manual checks:** none.

## 1.10 Bitmaps: Planar and fast-path Bitmap Updates (2026-11-19)
- **Built:** `rdp/internal/bitmap`:
  - The Bitmap Update Data and Bitmap Data of MS-RDPBCGR 2.2.9.1.1.3.1.2, 32 bpp, written one rectangle at a time (`BeginUpdate`, `AppendRect`, `EndUpdate`).
  - The RDP 6.0 Planar encoder (MS-RDPEGDI 2.2.2.5.1, 3.1.9), lossless (ARGB, color loss level 0, no subsampling): run-length encoded planes when smaller, plain planes when smaller, otherwise the uncompressed pixels. No alpha plane when the client sets `DRAW_ALLOW_SKIP_ALPHA`, else a constant 0xFF one. Writing allocates nothing.
  - The decoder (`ParseUpdate`, `Next`, `Draw`) for `rdptest`, which refuses anything this server never sends.
  - 8 tags.

  `wire.Writer.Truncate`. `caps`: the drawing flags of the Bitmap capability set.

  In `rdp` (`video.go`):
  - `Session.Video()`, `Video.Path()` (`VideoBitmaps`), `Image` and `Video.SendRects(img, rects)`. Rectangles are clipped to the image and the desktop, cut into 64×63 tiles, and packed into fast-path Bitmap Updates as large as the client's largest fast-path update (one PDU each without multifragment). All of one call goes in one packet of the video queue.
  - `Client.SkipAlpha`. 1 tag (`TagImageInvalid`).

  `rdptest`: `Screen` and `Screen.DrawBitmaps`, which draw what the client receives.
- **Proved:**
  - **Pixel for pixel:** `TestBitmaps` sends six rounds of random rectangles (some partly or wholly outside) of pictures with flat, gradient, striped and noisy areas, and the `rdptest` screen becomes the expected picture each round. This runs with the defaults, without multifragment, with an alpha plane, with a picture larger and smaller than the desktop, and on a 300×200 desktop. Rectangles that miss the desktop send nothing.
  - **Microsoft's examples:** `TestMicrosoftRLE` encodes the RLE examples of MS-RDPEGDI 3.1.9.2 byte for byte: AAAABBCCCCCD, the delta example, the 19 bytes of 3.1.9.2.1 and a run of 100.
  - `bitmap`: 400 random round trips, the choice of compression on flat and noisy tiles, the exact bytes of small cases, and every refused case of the decoder. 5000 mutations per seed run without a panic or a hang. Coverage is 99.5%.
  - **0 allocations:** `TestVideoAllocs` (`SendRects` through the running writer) and `bitmap.TestAllocations`. Benchmarks (Apple M6): one 64×63 tile at 671 MB/s (flat), 538 MB/s (mixed), 199 MB/s (noise); a whole 1024×768 mixed frame through `SendRects` in 8.3 ms (379 MB/s).
  - **Errors:** an image whose pixels do not hold its size, a client without fast-path output, a full queue (a client that reads nothing), and a session that is over, each with its route. Every new tag has a route test.
  - Three repeated `-race` runs pass. Coverage: `rdp` 93.3%, `rdptest` 75.6%, `bitmap` 99.5%.
- **Differs from the plan:**
  - The decoder lives in `internal/bitmap`, next to the encoder, and `rdptest` uses it: `rdptest` may import internal packages and the codec's tables are shared.
  - Planar scan lines are written bottom-up, as in an uncompressed bitmap; MS-RDPEGDI does not say for Bitmap Updates. Step 1.13 checks it with the real client.
  - Only the `VideoBitmaps` path exists. GFX, H.264 and `Backlog()` come in M3.
  - `SendRects` takes no `ctx`, since it never waits (SPEC §6 updated).
  - SPEC §6 (video, `Client`) and §9.6 (tiles, compression, alpha, scan line order) are updated.
- **Manual checks:** none.

## 1.11 Pointer (2026-11-19)
- **Built:** `rdp/internal/pointer`:
  - The pointer updates of MS-RDPBCGR 2.2.9.1.1.4 and 2.2.9.1.2.1.11: position, hidden, default, cached, Color Pointer (24 bpp), New Pointer (32 bpp with alpha) and Large Pointer (32 bpp, up to 384 a side). Masks are bottom-up with scan lines padded to 2 bytes; a transparent pixel is XOR 0 and AND 1. Writing allocates nothing.
  - `Shrink`, which makes a shape fit by an integer factor, averaging each block weighted by alpha.
  - The reader (`Parse`) for `rdptest`, which refuses anything this server never sends (wrong lengths, a size or depth outside the update, a hotspot outside, a pixel that inverts the screen). 3 tags.

  In `rdp` (`pointer.go`):
  - `Session.Pointer()` with `SetShape(img, hotX, hotY)`, `Hide()`, `Default()` and `Move(x, y)`, on the control queue. The largest side is 32, 96 or 384, from the client's Large Pointer Capability Set and its largest fast-path update; shapes larger are shrunk; a shape over 96 goes in a Large Pointer Update.
  - A cache of up to 64 entries, least recently used replaced, keyed by a hash of size, hotspot and pixels; a slot is recorded only once its update is queued.
  - `Client.AlphaPointer` (the client sent a pointer cache size, so it takes the New Pointer Update). 1 tag (`TagPointerShapeInvalid`).

  `rdptest`: `Cursor`, `Client.NewCursor` and `Cursor.Apply`, which keep the pointer a client shows, with its cache. 1 tag.
- **Proved:**
  - **Shown as sent:** `TestPointer` sets 7 shapes (with alpha, with another hotspot, one from the cache, large ones that are shrunk), hides, shows the default, comes back from the cache and moves out of the desktop; the `rdptest` cursor shows the expected shape each time. It runs for six clients: the defaults (384, 32 bpp), 96 only, no Large Pointer (32), no multifragment (32), color pointers only (24 bpp), and color pointers with 96.
  - `TestPointerCache`: a cache of 3 replaces the least recently used; a client without a cache gets index 0 every time.
  - `pointer`: the exact bytes of 2×2 New, Large and Color Pointer Updates, the mask lengths of the padding examples in 2.2.9.1.1.4.4 (3×3 → 30, 7×7 → 14), 300 random round trips, `Shrink`, every refused case, and 5000 mutations per seed without a panic. Coverage 100%.
  - **Errors:** 8 invalid shapes, a client without fast-path output (each method), a full queue (each method; a shape that was not queued is not cached) and a session that is over, each with its route. Every new tag has a route test.
  - **0 allocations:** new shapes, cached shapes, moves and hide, through the running writer (`TestPointerAllocs`) and in the writer functions.
  - Three repeated `-race` runs pass. Coverage: `rdp` 94.3%, `rdptest` 76.9%, `pointer` 100%.
- **Differs from the plan:**
  - `Default()` is added, for the client's own pointer.
  - Shapes too large for the client are shrunk rather than refused, and a shape over 96 is sent as a Large Pointer Update at 32 bpp even to a client that otherwise takes only Color Pointer Updates (the update carries its depth).
  - Control packets keep the size they grew to, so large pointers can grow the control queue's buffers (up to 32 packets of about 600 KB for a 384×384 shape).
  - Microsoft gives no example of a pointer update, so the exact bytes in the tests come from the field tables and the padding examples of 2.2.9.1.1.4.4.
  - SPEC §6 (pointer, `Client`) and §10.3 (encoding, shrinking, cache) are updated.
- **Manual checks:** none.

## 1.12 Ending, replacing, and the hostile-input sweep (2026-11-19)
- **Built:**
  - **Replacement (D13), in the library:** the server keeps one open session. When a new one has finished the connection sequence, the open one is closed with `CloseReplaced`, given 2 seconds for the ultimatum to be written (a client that reads nothing is cut off without it), and the new session reaches the handler once the old one has ended and its handler has returned. A session replaced while it waits never reaches the handler.
  - **TCP keep-alive** on every accepted connection: 15 s idle, 5 s interval, 3 probes (`net.KeepAliveConfig`). A connection where it cannot be set is logged and served all the same.
  - **The sweep** (`rdp/hostile_test.go`): one pass over every parser of client data. These are the TPKT and fast-path frames, X.224 (Connection Request, Data header, Disconnect Request), MCS (Connect-Initial and the domain PDUs), GCC and the client data blocks, the Client Info PDU, Confirm Active, the finalization and other Data PDUs, Refresh Rect, Suppress Output, and slow-path and fast-path input.
- **Proved:**
  - **Every reason:** `TestSessionClose` (by host, replaced, server stopped, a client without Set Error Info), `TestServerStopClosesSessions`, `TestLogin` (failed login), `TestClientDisconnects`, `TestClientHangsUp` and `TestSessionCloseWaitsForTheClient` already covered each way a session ends. This step adds the replacement through the server.
  - **Replacement:** `TestReplace` shows the first client told ERRINFO_DISCONNECTED_BY_OTHERCONNECTION, the first session ending before the second opens, the log, and a third login after the second has ended replacing nothing. `TestReplaceStalledClient` shows a first client that reads nothing does not hold the second up beyond about 2 s. `TestReplacedBeforeOpening` uses a first handler that does not return: the second session is replaced while it waits, its client is told so, its handler never runs, and the third session opens.
  - **Keep-alive:** `TestKeepAlive` reads SO_KEEPALIVE, the idle time, the interval and the count back from the socket (macOS and Linux). It also covers a connection that is not TCP and one where setting it fails.
  - **The sweep:** for each seed it runs every cut, every 2 and 4 bytes set to 0xFF… and 0x7F…, 1000 mutations, 100 seeds with up to 2 KiB of random bytes added, then 1000 random inputs and one random input of the largest frame. Seeds are Microsoft's examples, the Connection Requests recorded from FreeRDP and Remmina, and PDUs from the writers. No parser panicked, returned an untagged error, or allocated more than 8 KiB plus 32 bytes per input byte (measured per parse). `TestSweepCheck` shows the check catches an allocation sized by a length field and an untagged error.
  - Three repeated `-race` runs pass. Coverage: `rdp` 94.7%.
- **Differs from the plan:**
  - The spec did not say who replaces a session. The library does it, at the end of the new client's connection sequence rather than at its login, so a client that has the password but fails the sequence does not end the open session. The new session waits for the old one's handler, so the application never runs two at once. `CloseReplaced` is now sent by the server itself.
  - The sweep lives in `rdp`, the one package whose tests can import every parser, rather than in a new helper package. The per-package hostile tests of `input`, `mcs`, `gcc`, `bitmap` and `pointer` stay.
  - SPEC §5.4 (replacement) and §15.4 (the sweep) are updated.
- **Manual checks:** none.

## 1.13 A real client (2026-11-19)
- **Built:**
  - `rdp/freerdp_test.go` (tag `mardp_freerdp`): the library's server listens on the Mac's address in the test machine's network and shows a 1024×768 test pattern whose top and bottom halves differ. The guest runs `xfreerdp` under Xvfb through `scripts/vm.sh sh`, each run under `timeout 60`. The test is skipped with a message when the machine is not running.
  - `TestServe` (`MARDP_SERVE=<duration>`) serves the pattern for Remmina in the machine's window, the manual check below. `MARDP_FREERDP_LOGS=<folder>` keeps FreeRDP's logs, the screenshot and the server's log.
  - `docs/INTEROP.md`: what FreeRDP 3.33.1 did that the documents do not say.
  - `startAt` in `rdp/server_test.go` starts the test server on a given address.
- **Proved:** `go test -tags mardp_freerdp -run TestFreeRDP ./rdp/` passes against the guest in 12 s:
  - **password:** one session opens. The screenshot of FreeRDP's window matches the pattern pixel for pixel, so the Planar bottom-up rows are right (SPEC §9.6). Keys a, b and Return typed with xdotool arrive as scancodes 0x1E, 0x30 and 0x1C, down and up. Moves to 300,400 and 100,200 arrive, and so do a left button down and up at 100,200.
  - **wrong password:** xfreerdp exits nonzero (131), and the server logs "login failed".
  - **without TLS** (`/sec:rdp`): xfreerdp exits nonzero (133), and the server ends the connection on `conn.negotiate.ConnTLSRequired`.
  - FreeRDP's log has no warning or error about the server. The server's log has no warning but the two expected ones.
- **Differs from the plan:**
  - **SPEC §20 Q1 stays open.** FreeRDP does not set RNS_UD_CS_SUPPORT_ERRINFO_PDU, so for a wrong password it never sees ERRINFO_SERVER_FRESH_CREDENTIALS_REQUIRED. It reports ERRINFO_RPC_INITIATED_DISCONNECT, as for every ending. The code is unchanged (Set Error Info only to clients that ask for it), and Q1's row records this. What Remmina shows decides the rest; that is the manual check.
  - **FreeRDP sends each pointer move one event late** under Xvfb with xdotool, so the test makes one more move at the end. The server delivers single moves at once (`TestInput`).
  - The test checks the screenshot, input and exit status, not the video path or sound format that §15.3 also lists: those paths come in M2 and later milestones, with their own tests.
- **Manual checks:** pending, see docs/MANUAL-CHECKS.md (Remmina in the machine's window, and what it shows for a wrong password).

## 2.1 The pipeline, with fakes (2026-11-19)
- **Built:**
  - `internal/host/parts.go`: the interfaces of a session's parts: `ScreenSource` (with `Display` and `Frame`), `VideoEncoder`, `SoundSource`, `SoundEncoder`, `Injector`, `PointerSource` and `System` (permissions, displays, keeping the display awake).
  - `internal/host/host.go`: `Host`, the server's `Handler`, and its `DesktopSize` for `rdp.Config` (SPEC §9.2: fit inside the client's size, the display's aspect ratio, never scaled up, the height cap, even sides; the client's own size when the display cannot be read). A session gets the display, keeps it awake, sets the input and flow sinks, runs the capture at the session's size, and ends the session (CloseByHost) when the display or the capture fails.
  - `internal/host/bitmap.go`: the bitmap path's controller. It keeps a copy of the desktop and the rectangles not yet sent (up to 16, then merged into one). When the video queue is full they wait and go again from the copy, with the next frame or after one frame's time, so the client always ends with the newest picture. Refresh Rect sends the whole desktop from a timer, not the reading goroutine. Nothing is sent while output is suppressed. No allocation per frame.
  - `internal/host/pointer.go`: the pointer poller of SPEC §10.3: a read at the start, 4 a second, after each frame and 30 ms after each mouse event; a shape or "hidden" sent when the hash or the visibility changes; a full queue tried again at the next read; it stops when the client takes no fast-path output.
  - `internal/host/input.go`: the input relay: positions scaled to the display's points and clamped, double clicks counted by time (500 ms) and distance (4 points), the keys, characters and buttons held down kept. A Synchronize event releases the held keys before it sets the locks. The session's end releases everything still down and drops what comes after. An injector failure is logged at Warn once, then at Debug.
  - `internal/host/fake.go`: `PatternScreen` (color bars, gradients and a moving square, with the changed rectangles of each frame), `PatternFrame`, `ToneSource` (a sine in 20 ms buffers at 48 kHz), `RecordingInjector`, `StaticPointer` and `FakeSystem`.
  - `rdp/rdptest/plain.go`: helpers for tests outside `rdp`, which cannot import its internal packages: input events from plain values, Refresh Rect and Suppress Output by size, and `Screen.Draw` for a fast-path update.
- **Proved:**
  - `TestSession`, with `rdptest` against a real `rdp.Server`: a 1280×800 client of a 3840×2160 display (1920×1080 points) gets a 1280×720 desktop. The pattern arrives pixel for pixel with the pointer shape, and again after a Refresh Rect; a hidden pointer arrives as hidden. Keys, a character, moves (scaled by 1.5 and clamped), a double click (click counts 1 and 2) and the held buttons reach the injector. At the disconnect the held right Ctrl, the character and the right button are released, and the display is no longer kept awake.
  - `TestSessionCaptureFails`: a display or capture failure is logged with the source's own error, and the client is told why its session ends.
  - Unit tests: `TestFitSize` and `TestDesktopSize` (tables); the relay's clicks, scaling, releases and failure log; the bitmap path's frames, full queue, merging, flow (refresh before the first frame, suppress and resume), its end, and 0 allocations per frame; the poller's changes, retries, stops and wake-ups; the fakes. A route test for `HostScreenMissing`.
  - Five repeated `-race` runs pass. Coverage: `internal/host` 98.4%.
- **Differs from the plan:**
  - Only the screen source is required. A host without an injector drops input, without a pointer source leaves the client's own pointer, and without a system does not keep the display awake. The H.264 path (`VideoEncoder`) and the sound (`SoundSource`, `SoundEncoder`) have their interfaces now and their controllers in M3 and M4. `ToneSource` is the only sound fake; an encoder fake comes with the sound step.
- **Manual checks:** none.

## 2.2 Settings, password, certificate (2026-11-19)
- **Built:** `internal/config`:
  - `Dir` is `MARDP_HOME`, or `mardp` in the user's configuration folder (`~/Library/Application Support` on macOS). `Open` makes the folder with mode 0700, sets wider modes of the folder and its files back to 0700 and 0600 (logging each one), and refuses a symbolic link.
  - `Settings` hold every setting of SPEC §12.2, with `Defaults` (port 3389, 30 fps, a 20 Mbit/s cap, sound on the client only), `Validate` and `Ready`. `Load` reads `config.json` at version 1: missing settings take their defaults, unknown ones are ignored, older versions are upgraded one at a time, and a newer version is refused with a message naming both versions. `Save` replaces the file in one step.
  - `HashPassword` is PBKDF2-HMAC-SHA256 with 600,000 iterations and a 16-byte salt, for at least 8 characters. `Verify` refuses a damaged hash. `Settings.CheckLogin` compares the user name in constant time and checks the password even for a wrong user.
  - `Folder.Certificate` makes a self-signed RSA 2048 certificate on the first start (`key.pem` and `cert.pem`, mode 0600) and reads it after that. `NewCertificate` replaces it. The fingerprint is SHA-256 in colon-joined hex.
  - `Guard` is the waiting rule of SPEC §8.2. It counts failures per IP and allows 5; then a 1 minute wait doubles up to 1 hour. A login during a wait is refused without its password being checked. A success or a quiet day forgets the address, and at most 4096 addresses are kept.
- **Proved:** table tests for the folder, the modes (a 0755 folder and 0644 and 0666 files are corrected and logged), every load case (a newer version, no version, corrupt files, each setting out of range, a damaged hash), an upgrade and a failing upgrade with a test upgrade, save and load, and the failures to read and write. Also tested: the password's length in characters, its hash against `pbkdf2.Key`, damaged hashes, and the logins. The certificate's key size, lifetime, self-signature, fingerprint, reload and replacement are checked, and so is a TLS 1.2 handshake with it. Broken pairs are left as they were. The waiting rule runs through 23 steps with a fake clock, and a full table drops its oldest address. Route tests for every Tag. Coverage: `internal/config` 91.5%.
- **Differs from the plan:** the spec did not say what wrong modes lead to; they are corrected and logged rather than refused. It also did not fix the certificate's lifetime (20 years), the iteration count, or the details of the waiting rule. SPEC §8.1, §8.2 and §13 now say all of it.
- **Manual checks:** none.

## 2.3 The system (2026-11-19)
- **Built:** `internal/mac/system`, the `host.System` of macOS:
  - `Granted` asks without prompting (`CGPreflightScreenCaptureAccess`, `AXIsProcessTrusted`). `Request` shows macOS's question (`CGRequestScreenCaptureAccess`, `AXIsProcessTrustedWithOptions` with the prompt option). `OpenSettings` runs `/usr/bin/open` with the `x-apple.systempreferences:` address of the Screen Recording or the Accessibility pane.
  - `Displays` lists the active displays: the UUID (`CGDisplayCreateUUIDFromDisplayID`), the name (`NSScreen.localizedName`, or "Display N"), the size in pixels (the display mode) and in points (`CGDisplayBounds`), and which one is main.
  - `KeepAwake` declares user activity, which wakes the display, and holds a `PreventUserIdleDisplaySleep` assertion. Its release lets go of both, once, however often it is called; when the second assertion fails, the first is let go.
  - On other systems `New` and every method fail with `SystemUnsupported`. An unknown permission fails with `PermissionUnknown` everywhere.
- **Proved:**
  - Unit tests, with the framework calls replaced by a fake: the displays, their names and fallback names, a failed listing, a display without a UUID, the assertions held and released (once, nothing left after a failure), the pane addresses, and a failed `open` (a canceled context, so nothing opens). Route tests for every Tag. `TestUnsupported` checks the stub's tagged errors; it is compiled by `GOOS=linux go vet` and runs only on Linux.
  - `mardp_mac` tests on this Mac: one display (CQ32G4, 2560×1440, main, UUID stable across listings), the permissions read without a prompt (Screen Recording not granted to the test binary, Accessibility granted), and `pmset -g assertions` showing the process's 2 assertions while held and none after the release. The tests never call `Request` or `OpenSettings`.
- **Differs from the plan:** none.
- **Manual checks:** none. The buttons that open System Settings and ask for the permissions are checked when the window has them (M5).

## 2.4 Capture (2026-11-19)
- **Built:** `internal/mac/capture`, the `host.ScreenSource` of macOS:
  - `capture.go` (every system): `Source` with `Display` (the display with the chosen UUID, or the main one) and `Capture`. A capture checks Screen Recording first and never asks for it. It finds the display by its UUID (the main display's UUID is remembered at the start), starts a stream at the session's size, and follows the display: a stream that stops with -3815 (or a lost connection to the capture service, or the system stopping it) is started again, and a check once a second restarts it when its display ID is no longer the display with the UUID. With no display it waits, logging once. Five failed starts or quick stops in a row end it (`CaptureStartFailed`, `CaptureStopped`). A stream refused or stopped for the permission (-3801) ends it with `CapturePermissionDenied`, the session's reason (R6). `SetPaused` and `SetSkip` drop frames; the first frame of a stream and the first after dropped frames is whole (Dirty nil), which is how the bitmap path sends the client a full picture after a restart. The frame's image is the stream's pixel buffer itself, not a copy, valid during the call; no allocation per frame. The package documentation gives the teardown order.
  - `capture_darwin.m`: ScreenCaptureKit and Core Graphics in Objective-C (ARC): the display lookup by UUID with its name, size in pixels and points; the stream (BGRA, sRGB, no cursor, the frame rate cap, queue depth 3) with one output object that takes both callbacks under its lock while live; the stop that retires the output first, then stops the stream and waits, then removes the output. `capture_darwin.go` is the Go side: the backend, the cgo handle (deleted last) and the two exported callbacks. `capture_other.go`: `New` fails with `CaptureUnsupported`.
  - `internal/host`: `Image` (an alias of `rdp.Image`) and `MaxDesktopSide`, so the packages under `internal/mac` make frames without importing `rdp` (SPEC §4.1).
  - `scripts/mactest.sh <package> [test flags]`: builds a package's `mardp_mac` tests into an app bundle with the M0 probe's identifier, signs it with the development identity (D22) and runs it with `open -W`, so the tests have the probe's Screen Recording grant. Nothing asks for a permission: tests that need one the process lacks are skipped.
- **Proved:**
  - Unit tests with a fake backend: the frames (the first whole; rectangles clamped to the frame; frames with no rectangle inside are not delivered; frames that do not say, list too many or have the wrong size; a short buffer; pausing and skipping, each followed by a whole frame) and 0 allocations per frame; the stop (no stream left running); the default frame rate; a -3815 restart onto the same UUID with a new display ID while the main display changed, whose first frame is whole; a display change found by the check; a chosen display that goes away (the main one is captured); waiting for a display, logged once, and canceled while waiting; start retries; every error, with route tests for every Tag. `TestUnsupported` checks the stub (compiled by `GOOS=linux go vet`, run on Linux).
  - `mardp_mac`, unsigned (`go test -tags mardp_mac`): without Screen Recording the capture ends with `CapturePermissionDenied` and no question appears.
  - `mardp_mac` through `scripts/mactest.sh` on this Mac (2560×1440 display captured at 1280×720): the first frame, whole, opaque and not black, came 54 ms after the start, and every later one had its rectangles, inside the frame (38 frames in 3 s). On a nearly still screen (3 s): 38 frames, the whole first, 21 that change only the top 24 rows (macOS's capture indicator, which animates while a stream runs), and 16 with changes lower down: this desktop was not idle while the test ran, so "none on a still screen" is shown only as far as that goes: no frame came without a change (ScreenCaptureKit's idle frames are not delivered). A thousand starts and stops took 26.3 s (101 frames seen) with no callback after `Capture` returned.
- **Differs from the plan:** display changes are found by a check once a second plus the -3815 error, not with `CGDisplayRegisterReconfigurationCallback`, whose callback comes through the main thread's run loop (which the tests do not run); SPEC §7.2 says so. The dirty rectangles come as the dictionary form of `CGRect`, not the `NSValue` the header names; they are in the pixels of the scaled frame (SPEC §9.6). The wake and the power assertion stay with `host.System` (2.3), which the session holds; the capture follows the display they wake. The H.264 path's key frame after a restart comes with the encoder (M3): here the whole frame does that job. SPEC §15 names `scripts/mactest.sh`.
- **Manual checks:** none.

## 2.5 Keyboard and mouse (2026-11-19)
- **Built:** `internal/mac/input`, the `host.Injector` of macOS:
  - `keymap.go` (every system): the scancode table, a 128-entry array for the keys without a prefix and a switch for the E0 keys, covering the 105 keys of an ISO keyboard, F13 to F20 and the Japanese and Brazilian keys. Print Screen and SysRq post F13, Scroll Lock F14, Pause (E1 0x1D, whose following 0x45 is dropped) F15, Insert Help, Menu the context-menu key; media keys post nothing. The swap of Ctrl and Command (D19). With Num Lock off the keypad's digits are the navigation keys. The modifier flags (family and left/right device bits) and each key's own numeric-pad and Fn flags.
  - `input.go` (every system): `Input` keeps the code each key posted when it went down (its release posts the same), the modifiers held, Caps Lock (toggled on each press, set by `SetLocks`) and Num Lock (a state, never posted). Modifier keys post flags-changed events; repeats carry the autorepeat field. Mouse positions get the display's origin; moves with a button down are drags of the lowest button held; button events carry the host's click count. The wheel turns 120ths of a notch into lines and keeps the rest, dropped when the direction turns. Unicode posts UTF-16, with surrogate pairs. `ReleaseAll` releases the keys and buttons still held. Accessibility and the display's origin are read at most once a second; without the permission each event fails with `InputNotPermitted`.
  - `input_darwin.m`: one C function posts an event with Core Graphics (`CGEventCreateKeyboardEvent`, `CGEventCreateMouseEvent` with `kCGMouseEventClickState`, `CGEventCreateScrollWheelEvent2` in lines, `CGEventKeyboardSetUnicodeString`) to the HID event tap; the display's origin by UUID. `input_other.go`: `New` fails with `InputUnsupported`.
  - `internal/host`: the aliases `Extended`, `MouseButton` and their constants, so the package does not import `rdp`.
- **Proved:**
  - A table test of all 105 ISO keys (each maps to its expected code, no two share a scancode or a code), and of the other keys, the swap, the keypad with Num Lock off, the modifier and key flags.
  - Sequences through a fake poster: Shift+A, both Shifts, Ctrl+C plain and swapped, AltGr, repeats, a release without a press, a media key, Num Lock pressed and synchronized, a key's code kept across a Num Lock change, Pause and its dropped 0x45, Caps Lock pressed, held and synchronized; Command-click; all five buttons with their down, up and drag types and the click counts (1, 2, 3); the wheel's lines, rests and reversals both ways; Unicode including surrogates and invalid runes; `ReleaseAll` (and that a move after it is not a drag), going on after a failed post and after a lost permission; the once-a-second rechecks. Route tests for every Tag. Coverage: `internal/mac/input` 93%. The click counting itself is the host's, tested in 2.1.
  - `mardp_mac` through `scripts/mactest.sh`: Accessibility read without a prompt (granted), the main display's origin at 0,0, an unknown UUID not found. No test posts an event (rule 11).
- **Differs from the plan:** the plan put the click counting here; the host already counts clicks (2.1) and hands the count over, so this package only puts it in the event. The keys a Mac lacks, Num Lock, the Accessibility check and the wheel's line per notch were not in the spec; SPEC §10.4 and §10.5 now say them.
- **Manual checks:** posting, pending, see docs/MANUAL-CHECKS.md (runnable once `mardp -headless` exists, step 2.7).

## 2.6 The pointer source (2026-11-19)
- **Built:**
  - `internal/mac/input`: `Pointer`, the `host.PointerSource` of macOS. Each `Current` reads `CGCursorIsVisible`, `NSCursor.currentSystemCursor` (image and hotspot) and the pointer's position (`CGEventGetLocation`) relative to the session's display (by UUID, else the main one). The image comes from the representation whose pixel size matches the display's scale (else the largest, scaled), drawn into a reused 256×256 BGRA buffer and turned to straight alpha; the hash (maphash) covers size, hotspot and pixels. No allocation per read in Go. `NewPointer` fails with `InputUnsupported` on other systems. SPEC §4.1 already put the pointer's image in this package.
  - `internal/host`: `PointerShape` gained `OnDisplay`, `X`, `Y`. The poller (which already read after mouse events, frames and 4 times a second, and sent a shape only when its hash changed, from 2.1) now also sends the position (`rdp.Pointer.Move`, in desktop pixels) when the Mac moved the pointer: the client's mouse has been still for 200 ms and the pointer is more than 1.5 points from where the client put it. The relay tells it where the client put the pointer; the session starts the poller after the relay is linked.
- **Proved:**
  - `mardp_mac` through `scripts/mactest.sh`, from a goroutine that is not the main thread: the arrow read as 28×40 with hotspot 5,5 (as R4 saw), 233 pixels not transparent, on the main display at the pointer's position; a one-pixel buffer fails with `PointerTooLarge`. A read takes 93 µs (benchmark), about 3 ms a second at 30 frames a second.
  - Unit tests with a fake reader: the image's size, stride and hotspot, the hash equal for the same pointer and different for other pixels, hotspot or size, the hidden pointer, a read error, 0 allocations per read, and route tests for both new Tags. The poller's position rule step by step (the start, still, moved by the Mac, the client's move too recent, settled, inside the slack, the Mac not following the client, off the display, a full queue tried again, another failure logged once, the session's end stopping it, no scale sending nothing), and a whole session where the client gets the position (600,300 points as 400,200 pixels). Coverage: `internal/host` 98.5%, `internal/mac/input` 94.6%.
- **Differs from the plan:** the position rule (200 ms, 1.5 points, the start, view-only) was not in the spec; SPEC §10.3 says it now, with the image's format and the cost of a read. The step's proof says "the rest by hand": that is the manual check below.
- **Manual checks:** the shapes, hiding and the Mac moving the pointer in Remmina, pending, see docs/MANUAL-CHECKS.md (runnable with step 2.7).

## 2.7 `mardp -headless` (2026-11-19)
- **Built:** `cmd/mardp`:
  - `main.go` (thin): SIGINT and SIGTERM cancel the context; `run` returns the exit code.
  - `run.go`: the flags `-headless`, `-version` (the module's version from the build information), `-log-file` (appended, mode 0600) and `-view-only`; `slog` text to the standard error or the file. `serve` opens the folder (`MARDP_HOME` or Application Support), loads the settings, refuses to start without a user name and password (`MardpNoPassword`) or without Screen Recording (`MardpScreenRecordingMissing`), goes view-only without Accessibility (logged), loads or makes the certificate and logs its fingerprint, makes the parts, the host and the server, and listens on the settings' address and port (`MardpListenFailed`). The authenticator checks the login through `config.Guard` (the wait after failed logins) and logs each refusal without the password.
  - `mac.go`: the real parts (`capture`, `input.NewPointer`, `input.New` only when input is posted, `system`) behind a small interface, which the tests fill with `internal/host`'s fakes.
  - `scripts/probe-app.sh`: makes and signs a bundle as the M0 probe around an executable; `scripts/mactest.sh` uses it now.
- **Proved:**
  - Unit tests: every flag and its exit code; each refusal with its route (no password, no user, no Screen Recording, a corrupt `config.json`, the system or the parts failing, no screen source, the port taken); a whole session through `run -headless` with the test pattern: an `rdptest` client pinned to mardp's own certificate logs in, sees the pattern, its key reaches the injector; a wrong password is refused and logged; neither password is in the log; SIGINT's cancel ends it with 0. View-only without Accessibility and with `-view-only`; the log file and its mode; the authenticator's wait after five failures. The real parts are made on this Mac without posting or asking anything. Coverage: `cmd/mardp` 87%.
  - `mardp_freerdp` (`TestFreeRDPHeadless`): `mardp -headless -view-only`, built into a probe-signed bundle and started with `open`, served the Mac's real screen to xfreerdp in the test machine: the 1024×768 screenshot had over 1,300 colours and showed this Mac's desktop (2560×1440 scaled to 1024×576); the log said logged in, session opened, view-only, and has no password. The app was stopped with SIGTERM and nothing was left running.
- **Differs from the plan:** `-view-only` is new, so that the FreeRDP test cannot post FreeRDP's own lock states and pointer moves into the owner's desktop (rule 11); SPEC §13 now says it, with the checks' order, the logging and the exit codes. Until step 2.8, `mardp` without `-headless` says the window is not built and exits with 2. The first run of the bundle made macOS ask whether "MardpHeadless" may bypass the private window picker (the recurring Screen Recording question, R6); the capture worked while it was asked, and the owner allowed it.
- **Manual checks:** Remmina shows and controls the desktop, pending, see docs/MANUAL-CHECKS.md (runnable with step 2.8's app, which sets the password; 2.5 and 2.6 are checked the same way).

## 2.8 The app's shell (2026-11-19)
- **Built:**
  - Glutter v1.17.7, its newest version, in `go.mod`.
  - `internal/ui`: `Backend` (the window's view of the server: `Status`, `Start`, `Stop`, `SetLogin`, `OpenSettings`) and `Status`; `NewViewModel`, the view model (the state's text, the addresses and fingerprint while running, Start and Stop enabled, the checklist of Screen Recording, Accessibility and the login, with what each is for and its button); the Material app and its window: the status card with Start and Stop, and the checklist card, whose login line opens a form for the user name and password. The window asks the backend for the status once a second by the binding's timer, and rebuilds only when it changed; actions run on goroutines (`State.Go`) and come back through `SetState`. `Run` opens it with mardp's `slog` as Glutter's logger.
  - `internal/host`: `Host.Sessions`, the number of open sessions, for Connected.
  - `cmd/mardp`: without `-headless` it opens the window (`app.go`): the app is the backend, starts the same server as `-headless` (the checks, the certificate, the parts and the listener now in `prepare`, shared by both) on a context of its own, reports Listening or Connected with the addresses of the Mac's interfaces (IPv4 first, without loopback and link-local) and the fingerprint, saves a new login (a running server checks the next login against it), and stops the server when the window closes (`MardpStopTimedOut` after 10 seconds). A server that stops by itself shows why.
  - `scripts/build-app.sh [-debug]`: builds `build/mardp.app` with `glutter-macos` (version from the last `v` tag, identifier `com.github.rm4n0s.mardp`, the Screen Recording usage text) and signs it with `scripts/sign.sh`.
  - SPEC §12.2 and §13 updated: what the window does until M5's settings and menu-bar icon; `mardp` without `-headless` opens the window.
- **Proved:**
  - `internal/ui` tests with `gluttertest` against a fake backend, in a debug build and in a release build (`-race`): the view model's cases; first start (the checklist, Start disabled); each permission button asks once and the line ticks when the status changes, by the fake clock; setting the login (a short password shows its error under the field, Cancel clears it, Change prefills the user); Start and Stop (the addresses, the fingerprint, the busy state); a failed Start shows its message; view-only without Accessibility; a permission withdrawn while stopped brings the checklist back. Coverage 98.6% (only `Run` is not covered).
  - `cmd/mardp` tests: the window path end to end with a fake window: the backend starts the server, an `rdptest` client logs in and the status says Connected, and the port is closed after the window returns; each refusal with its route (`MardpUserMissing`, `MardpStopTimedOut`, a corrupt `config.json`, no Screen Recording, the system or the window failing); a new login works on the running server and the old one fails, and no password reaches the log; a failed save; a server that stops by itself; the addresses' filter. Coverage 92.2%.
  - The bundle: `build/mardp.app` is version 0.2.0, identifier `com.github.rm4n0s.mardp`, signed by "mardp development", and links ScreenCaptureKit; started with `open -n` and a `MARDP_HOME` of the test, it presented its first frame and SIGTERM stopped it, with nothing left running.
- **Differs from the plan:**
  - Until M5 (the settings, the menu-bar icon at 5.3), closing the window quits mardp and stops the server; the user name and password are set from a form in the checklist; the permission buttons ask macOS first (`Request`), then open the pane. SPEC §12.2 says it.
  - A corrupt `config.json` or a failing system makes `mardp` exit with 1 before the window opens, as `-headless` does, rather than show it in the window.
  - Two Glutter workarounds in `scripts/build-app.sh`, the first entries of the new `docs/GLUTTER-ISSUES.md`: `glutter-macos build` forces `CGO_ENABLED=0` (a `go` shim in `PATH` turns cgo back on), and it signs only ad hoc (the app is signed again with `scripts/sign.sh`).
  - `mardp.app` is a new identity for macOS (not the probe's), so its permissions are asked for afresh.
- **Manual checks:** the checklist on first start, Start, a connection from Remmina, with real clicks: pending, see docs/MANUAL-CHECKS.md.

## 3.1 Static virtual channels (2026-11-19)
- **Built:**
  - `rdp/internal/svc`: the Channel PDU Header and its flags; `Split`, which cuts a message into chunks with their headers; `Joiner`, which joins chunks into a message, keeping its buffer and growing it with the chunks that arrive, up to a maximum. Tags `SvcCompressed`, `SvcChunkSequence`, `SvcLengthInvalid`, `SvcMessageTooLarge`, `SvcChunkSizeInvalid`.
  - `rdp`: the session opens `drdynvc` and `rdpsnd` when the client joined them (`channel.go`), one joiner and one handler each; the reading goroutine hands each whole message to the channel's handler (a handler's error ends the session); `sendChannel` queues a message in chunks of 1,600 bytes, one Send Data Indication each, in one packet. Data on other static channels is ignored as before.
  - `rdptest`: `ChannelID`, `SendChannel`, `SendChannelChunk` and `ReadChannel`, which joins what the server sends on a channel and reports each chunk's header and length.
  - SPEC §5.3 says how static channel data is joined, limited and sent.
- **Proved:**
  - `svc` tests (coverage 100%): Microsoft's annotated Virtual Channel PDU (MS-RDPBCGR 4.4, its decrypted channel data) read and joined, and written back byte for byte by `Split`; Microsoft's chunking example (3.1.5.2.2.1: 2,062 bytes in chunks of 1,000 give FIRST, none, LAST and 1000/1000/62 bytes); 200 seeded random messages and chunk sizes split and joined; every sequence fault with its route, and the joiner starting over after one.
  - `rdp` tests (TestSessionIgnores now sends a valid chunk on `rdpsnd` and its unread bytes on `cliprdr`): a 5,000-byte message from rdptest on `drdynvc` reaches the server's handler whole, and its echo arrives in four chunks (1600, 1600, 1600, 200 bytes; FIRST, none, none, LAST; each stating 5,000) that rdptest joins; a short message is one chunk with FIRST and LAST; data on `cliprdr` is ignored and on `rdpsnd` without a handler dropped; a handler's error ends the session with its route; a compressed chunk, a chunk without a first one, a message over 1 MiB, lengths that do not add up and a cut header each end the session with their route.
- **Differs from the plan:** the chunks the server sends carry no CHANNEL_FLAG_SHOW_PROTOCOL, although 3.1.5.2.1 says chunked data "MUST" have it: Microsoft's own example of chunking and FreeRDP's server leave it out, and FreeRDP's client ignores it (SPEC §5.3). The 1 MiB limit on a message from the client is new (SPEC §5.3).
- **Manual checks:** none.

## 3.2 Dynamic virtual channels (2026-11-19)
- **Built:**
  - `rdp/internal/dynvc`: the DVC PDU header (cbId, Sp/Pri/Len, Cmd), `ParseClient` and `ParseServer` (capabilities of versions 1 to 3 and their response, Create Request and Response, Data First and Data with each length form, Close; compressed data and unknown commands are errors), the writers of each PDU (the smallest ID and length forms), `Split` (one Data, or Data First and Data PDUs of at most 1,600 bytes) and `Joiner`. Tags `DynvcChannelIDSizeInvalid`, `DynvcLengthSizeInvalid`, `DynvcCommandUnknown`, `DynvcCompressed`, `DynvcVersionInvalid`, `DynvcNameInvalid`, `DynvcSequence`, `DynvcLengthInvalid`, `DynvcMessageTooLarge`.
  - `rdp/dvc.go`: the DVC manager on `drdynvc`. It sends the capabilities (version 2) as the session opens and waits 10 seconds for the answer; `openDVC` creates a channel (at once, or once the capabilities are answered) and a listener gets opened, refused, each whole message and closed; `send` splits a message into one packet; `close` sends Close and drops data until the client's. Tags `SessionDVCUnavailable`, `SessionDVCNotOpen`, `SessionDVCCapsUnexpected`, `SessionDVCVersionInvalid`, `SessionDVCUnknown`, `SessionDVCStateInvalid`.
  - `rdptest`: `ReadDVC`, `SendDVC`, `AnswerDVCCaps`, `AnswerDVCCreate`, `SendDVCMessage`, `ReadDVCMessage` (with the number of PDUs a message came in).
  - SPEC §5 and §5.3 say what the server offers and how channels open, close and fail.
- **Proved:**
  - `dynvc` tests (coverage 100%): Microsoft's annotated examples (capabilities of version 2, its response, Create Request and Response for "testdvc", Close; the 3,195-byte Data First and Data sequence on channel 3 written by `Split` and joined back); capabilities of each version; each length form; seeded random messages split and joined; every parse and sequence fault with its route.
  - `rdp` tests: rdptest answers the capabilities and checks version 2 and the charges; a channel opened before the answer is created after it; a 5,000-byte message reaches the listener whole and the server's 5,000 bytes arrive in four PDUs (one for a short message); a refused channel is reported with its status and logged, and sending on it fails with its route; a server close drops the client's data until its Close; a client close is reported, an unknown one ignored; IDs are not reused; a listener's error ends the session with its route. Without `drdynvc`, or after the capabilities went unanswered (the waiting channel is refused with the timeout status, and a late answer ignored), `openDVC` fails with its route. A second capabilities response, version 3, unknown channels in a Create Response and in data, data or a Close before the Create Response, a second Create Response, a Data First inside a message and compressed data each end the session with their route. `dynvc.ParseClient` joined the hostile-input sweep.
- **Differs from the plan:**
  - The server offers version 2, not 3: version 3 would let the client compress, which the server does not decompress (SPEC §5).
  - Channel IDs are never reused within a session, and a Close for an unknown channel is ignored rather than ending the session (it can cross the server's own Close).
  - A client that hangs up with data from the server unread resets the connection; the server now logs that as closed by the client, as it does EOF (the capabilities sent at session start made TestClientHangsUp see it).
  - TestStaticChannels uses `rdpsnd`, now that `drdynvc` belongs to the DVC manager; rdptest's activation skips what arrives on static channels; the tests that read until the server closes read past it.
- **Manual checks:** none.

## 3.3 The graphics pipeline (2026-11-19)
- **Built:**
  - `rdp/internal/gfx`: the RDPGFX header and command IDs, capability sets of 8.0 to 10.7 (`Valid`, `Flags`, `AVC420`), `ReadClient` (Caps Advertise, Frame Acknowledge, QoE Frame Acknowledge, Cache Import Offer; other commands with their body) and `ReadServer` (Caps Confirm, Reset Graphics, Create Surface, Map Surface To Output, Start Frame, End Frame, Cache Import Reply), the writers of each, `Begin`/`End` for a PDU whose length is patched, `Timestamp`, and the RDP 8 segments: `Wrap` (one segment up to 65,535 bytes, multipart above, uncompressed) and `Unwrap`. Tags `GfxLengthInvalid`, `GfxCapsInvalid`, `GfxCountInvalid`, `GfxSegmentInvalid`, `GfxCompressed`, `GfxSizeInvalid`.
  - `rdp/gfx.go`: the session opens `Microsoft::Windows::RDS::Graphics` when the client supports the pipeline and joined `drdynvc`; the capability exchange (the newest of 8.1 and 10.x, the client's set echoed, H.264 allowed or not), Reset Graphics, Create Surface and Map Surface To Output; frames (Start Frame, content, End Frame, in one message on the video queue); acknowledgements, suspension, the second Caps Advertise, the Cache Import Reply; `Video.Backlog()`. Tag `VideoGFXNotReady`.
  - The X.224 Connection Confirm also says DYNVC_GFX_PROTOCOL_SUPPORTED.
  - `rdptest`: `ReadGFX`, `SendGFX`, `StartGFX`, `AcceptGFX`, `AdvertiseGFX`, `AckFrame`.
  - SPEC §9.1 says what the server confirms, the surface, the second advertise and how acknowledgements move the backlog; §6 says when `Backlog()` is zero.
- **Proved:**
  - `gfx` tests (coverage 89%): every PDU written byte for byte against the layout of MS-RDPEGFX 2.2 (Microsoft gives no examples of these) and read back; several PDUs in one message; which sets are valid and allow H.264; the timestamp's layout; data of sizes around a segment's wrapped and unwrapped, with the bytes of the single and multipart forms; every fault with its route; a fuzz target with seeds (run as seeds only).
  - `rdp` tests: rdptest goes through the exchange for 8.1 (with and without AVC420), 10, 10 without AVC, 10.1 to 10.7, the newest of FreeRDP's list, and a list whose newest set is malformed; each time the confirm, Reset Graphics (one primary monitor of the desktop's size), the surface and the map are checked. The backlog follows the acknowledgements on a moved clock: three frames 10 ms apart, one acknowledged, then frame 3 covering frame 2, an unknown frame logged, a suspension emptying it and keeping it empty, an acknowledgement resuming it. A frame before the confirm, after no common version (the channel closed), after a refusal and after the client closed the channel fails with its route; a second advertise after 10.3 starts over and after 10.2 is ignored; a client without the pipeline or without `drdynvc` has none. Three malformed graphics PDUs end the session with their route; `gfx.ReadClient` joined the hostile-input sweep.
- **Differs from the plan:**
  - 8.0 is not confirmed (SPEC §9.1 names 8.1 and 10.x); a set whose length does not match its version is skipped.
  - The DVC tests' client no longer says it supports the pipeline, so that the server's own channel does not take ID 1.
  - `Video.Path()` stays `VideoBitmaps` until 3.4 sends pictures through the pipeline.
- **Manual checks:** none.

## 3.4 Planar through the graphics pipeline (2026-11-19)
- **Built:**
  - `rdp/internal/bitmap`: `AppendTile` (a tile of up to 64×64 for the graphics pipeline: Planar without an alpha plane, its scan lines top-down, or the pixels uncompressed when that is not smaller) and `DrawTile`, its decoder. `MaxTile`.
  - `rdp/internal/gfx`: Wire To Surface 1, written with `BeginWireToSurface1`/`EndWireToSurface1` (the codec and the lengths patched once the bitmap data is in) and read by `ReadServer`; the codec IDs Uncompressed, Planar and AVC420.
  - `rdp`: once the pipeline's surface exists, `SendRects` sends one frame of Wire To Surface 1 PDUs, tiles of 64×64; it falls back to Bitmap Updates before that and after the channel closes. `Video.Path()` returns the new `VideoPlanar` while the surface exists. The flow sink gets `KeyFrameWanted` when the surface is created, since it starts black.
  - `rdptest`: `Screen.DrawGFX`, which draws Wire To Surface 1 on surface 1 and notes the frames that end. Tag `ClientGFXUnsupported`.
  - SPEC §6 (`Path`, `KeyFrameWanted`) and §9.6 (the tiles through the pipeline).
- **Proved:**
  - `bitmap` tests: 400 random tiles of five kinds of picture drawn back pixel for pixel; Planar is chosen for every tile of more than two pixels; the bytes of an uncompressed tile, of plain planes and of RLE planes show the scan lines top-down; every fault of both functions with its route; cuts and mutations of tiles without a panic; no allocations.
  - `gfx` tests: Wire To Surface 1 byte for byte and read back; a wrong bitmap data length and a cut PDU with their routes.
  - `rdp` TestGFXBitmaps, the pixel-for-pixel test of 1.10 over the pipeline: for the default desktop, a picture larger and smaller than it, and a small desktop, six rounds of random rectangles each arrive as exactly one frame that makes the client's screen the picture; frame IDs go 1 to 6, the acknowledgements empty the backlog, both codecs are used, the flow sink is asked for a key frame once the surface exists, and after the client closes the channel the path is bitmaps again and a rectangle arrives as a Bitmap Update.
  - `rdptest` TestDrawGFX: three tiles drawn, XRGB's fourth byte drawn as 0xFF, and each refusal with its route.
- **Differs from the plan:**
  - Without an alpha plane, plain Planar planes are always smaller than the pixels, so only tiles of one or two pixels go uncompressed.
  - The `KeyFrameWanted` when the surface is created is new: without it a desktop that does not change would stay black after the switch (SPEC §6).
  - Whether FreeRDP shows these tiles is for 3.9's interop test.
  - Not part of the step: CLAUDE.md and rule 14 now say that the session chooses the version of a publish without asking (the owner, 2026-11-19), the next minor version for a milestone.
- **Manual checks:** none.

## 3.5 AVC420 (2026-11-19)
- **Built:**
  - `rdp/internal/gfx`: `AVC420` (regions, quantization and quality, the Annex B data), `AppendAVC420` and `ReadAVC420` for RFX_AVC420_BITMAP_STREAM and its metablock; `Rect16`, which Wire To Surface 1 now takes; tags `GfxRectInvalid` and `GfxQuantInvalid`.
  - `rdp`: `Video.SendFrame(annexB, keyFrame)`, one graphics frame with a Wire To Surface 1 of the whole surface (QP 22, quality 100); `Video.Path()` decides the three outcomes of §9.1 (`VideoAVC420`, `VideoPlanar`, `VideoBitmaps`). Tags `VideoH264Unavailable`, `VideoKeyFrameWanted` (a P-frame before the surface's first key frame), `VideoFrameEmpty`.
  - SPEC §6 (`SendFrame`, `Path`) and §9.1 (the AVC420 frame).
- **Proved:**
  - `gfx` tests: two bitmap streams byte for byte against 2.2.4.4 and its sub-structures (one region of 1920×1080 with QP 22 and quality 100; two regions, one progressive, with the extremes of QP and quality) and read back; the reserved bit ignored; every fault of writing and reading with its route; cuts and mutations without a panic.
  - `rdp` tests: rdptest gets back the same Annex B data it was sent, for a key frame of 40,000 bytes, a P-frame of 300 and one of 200,000 (multipart segments, many DVC PDUs), each one frame with IDs 1 to 3, the region of the desktop and the metablock's values; the acknowledgement empties the backlog; Planar rectangles still go through on this path. A P-frame before the key frame, a frame before the surface, after a re-advertise without H.264, for a client without the pipeline, and an empty frame fail with their routes; after a 10.3+ re-advertise the surface needs a key frame again. TestGFX checks the path of every version (AVC420 or Planar); the client without the pipeline gets bitmaps. The key frame request waits while output is suppressed and comes with the resume.
- **Differs from the plan:** the refusal of a P-frame before the key frame is new (a decoder cannot show it), and so is the metablock's fixed QP and quality (SPEC §9.1).
- **Manual checks:** none.

## 3.6 The hardware encoder (2026-11-19)
- **Built:** `internal/mac/vtenc`: `New(Options, frame)` makes a hardware VTCompressionSession with low-latency rate control, real time, no frame reordering, Constrained High (Main and Constrained Baseline as options for the profile test of 3.9), BT.709 and the range of the NV12 input, no periodic key frames; `Encoder` implements `host.VideoEncoder` (`SetBitrate` without a restart, `ForceKeyFrame`, `SetSkip`) with `Stats` and `Close`. The C side (`mardpEncSubmit`, which 3.7's capture will call) reads the skip flag and a pending key frame at each frame; the output callback crosses into Go once per frame, where the access unit is turned into Annex B in a reused buffer, SPS and PPS in front of key frames. Tags `EncoderUnsupported`, `EncoderConfigInvalid`, `EncoderCreateFailed`, `EncoderClosed`, `EncoderBitrateInvalid`, `EncoderBitrateFailed`, `EncoderSampleInvalid`. SPEC §9.3 (the encoder, dropped frames) and §9.5 (why the bitrate falls by a quarter at a time).
- **Proved:**
  - Tests on every system, with a fake VideoToolbox: options, defaults and refusals with their routes; the setters reach the session and stop at `Close`; the Annex B of key and P-frames byte for byte, the buffer reused and no allocation a frame; malformed access units (lengths cut short, past the end, empty, a length size of 3) with their route. A test H.264 reader (SPS through the VUI's bitstream restriction, HRD and scaling lists; slice types; emulation prevention) checked against a bit writer.
  - `mardp_mac` tests with VideoToolbox and generated frames (`go test -tags mardp_mac ./internal/mac/vtenc`; no permission needed): for 720p and 1080p, both ranges, and each profile, the first frame is a key frame (SPS, PPS, IDR) and the next ones P-frames with no B-slice and in order; the SPS has `profile_idc` 100 with `constraint_set4/5` (77 for Main, 66 with `constraint_set1` for Constrained Baseline), `max_num_reorder_frames = 0`, BT.709, the input's range and the size; a forced key frame comes with the same SPS; skipped frames encode nothing and a key frame forced meanwhile comes with the first frame encoded; nothing comes after `Close`. The bitrate follows 2, 8, 6, 4.5, 3.4 and 6 Mbit/s within 10% (1.83, 7.68, 6.06, 4.54, 3.19, 5.97) without a dropped frame or a new key frame; also under the race detector.
- **Differs from the plan:** two findings went into the spec: a frame that does not fit the bitrate even at the coarsest quantizer is dropped by VideoToolbox (pure noise at 720p, 1 to 8 Mbit/s), and a fall of the bitrate to an eighth at once drops about half the frames for three seconds, so §9.5 falls by a quarter at a time. The tests make their own NV12 frames from the session's pixel buffer pool instead of capturing the screen; capture into the encoder is 3.7.
- **Manual checks:** none.

## 3.7 Capture straight into the encoder (2026-11-19)
- **Built:**
  - `internal/mac/capture`:
    - `CaptureVideo(ctx, cfg, VideoOutput)` runs the same restartable stream as `Capture`, but in NV12 (`420f` or `420v` as the encoder is set) with the BT.709 colour matrix.
    - Its ObjC callback calls the encoder's C submit function under the output's lock while the stream is live, so a stopped stream never reaches the encoder; frames with no dirty rectangle are not submitted.
    - `VideoSink`, `VideoOutput`, `VideoStats` (encoded, skipped, refused, crossings into Go) and `DefaultVideoFPS` (60).
  - `internal/mac/vtenc`: `Encoder.Submitter()`, the C function and its argument.
  - Tests only: a `mardp_mac` AppKit test window that animates on demand, and capture of that window alone.
  - SPEC §4.2 (the NV12 path) and §9.4 (what was measured).
- **Proved:**
  - Fakes: `CaptureVideo` passes the encoder's submit function, the range and 60 fps to the stream; no Go frame callback is made; it restarts after a stream error; the stats of every stream are summed. Refusals with their routes: no submit function or a bad size (`Source.CaptureVideo.CaptureConfigInvalid`), no permission.
  - `mardp_mac` TestMacVideo, ScreenCaptureKit into VideoToolbox, capturing the test's own window:
    - 113 frames encoded in 2 s of motion, 0 while skipped, 55 in the second after resuming, and 1 in 2 s while the window was still;
    - 0 crossings into Go from the capture, and the encoder's outputs equal the frames the capture submitted (224), so the encoder's callback is the frame's only crossing;
    - one key frame, nothing dropped or failed.
  - TestMacStillScreen now captures its still window: 2 frames in 3 s, the first whole.
- **Differs from the plan:**
  - The still-screen proof captures the test's own window alone. The owner's display changed all the time during the runs (something redrew one small area about 60 times in 3 s). A display filter that includes only the test window still reported those changes as dirty rectangles, so only a window filter shows a still picture. The same change made TestMacStillScreen pass on a busy screen.
  - "Nothing" is at most one or two frames: ScreenCaptureKit sends the odd frame of an unchanged window.
- **Manual checks:** none.

## 3.8 Flow and rate control (2026-11-19)
- **Built:**
  - `rdp`: `Backlog.Queued`, the packets of the video queue the writer has not written yet, on every path.
  - `rdp/rdptest`: `AVC420Caps`, `PlanarCaps` and `ReadGFXFrame` (one GFX frame's H.264 and tiles; tag `PDUUnexpected` for a PDU outside a frame).
  - `internal/host`:
    - `VideoSource` (`CaptureVideo(ctx, VideoConfig, started, frame)`), `VideoConfig`, `EncodedFrame`, and `Parts.Video`.
    - The H.264 path (`avcPath`): skip before encoding (more than 2 frames unacknowledged, or video waiting in the queue at a tick), the bitrate rule (a quarter down, a sixteenth up, once a second; 8 Mbit/s to start, 1 to `Options.MaxBitrate`, default 20), key frames on Refresh Rect, on resume and after a frame lost to a full queue, skipping while output is suppressed.
    - The frame-rate cap: `Options.VideoFPS` (default and most 60), passed to the capture.
    - The session switches between the H.264 path and the bitmap paths whenever `Video.Path()` changes (`flowSwitch`), with a new capture each time, and logs "video path".
    - `PatternVideo`, a fake video source whose frames carry their number and the time they were made, and whose size follows the bitrate.
  - SPEC §6 (`Queued`), §9.4 (`VideoFPS`) and §9.5 (the numbers).
- **Proved:**
  - The bitrate rule as a table; the H.264 path with a fake session and encoder (skips, the skip lasting a frame's time, key frames, errors, suppression, the end of the path); the switch's hand-over of a suppressed state.
  - TestSessionH264, a whole session with `rdptest` and `PatternVideo`: about 60 frames a second at full speed while the bitrate rises; with a client that acknowledges a frame each 50 ms the oldest frame on its way stays at 101 ms (limit 200), 60 frames are skipped before encoding, and the bitrate falls a quarter at a time; at full speed again it recovers; a Refresh Rect brings a key frame within 10 frames; while suppressed the encoder skips, and the resume brings a key frame within 5.
  - TestSessionPathSwitch: no capture before the graphics pipeline; a key frame first on AVC420 with the cap of 30 fps and the bitrate bounded by `MaxBitrate`; tiles after the client re-advertises without H.264.
  - A failing video source ends the session with its route logged; the options' defaults. All under the race detector, five times over.
- **Differs from the plan:**
  - "Sound keeps flowing while video is throttled" is not tested: sound is M4, and 4.3 extends TestSessionH264 with it.
  - The skip needed `Backlog.Queued` and a ticker beside the check after each send: right after a send the frame just queued is in the queue, so that check counts only unacknowledged frames; and a skip shorter than a frame interval (the writer finishing a frame) does not lower the bitrate.
  - The real `VideoSource` (capture into vtenc) is wired into the app in 3.9.
- **Manual checks:** none.

## 3.9 A real client decodes it (2026-11-19)
- **Built:**
  - `internal/mac/video`: the host's `VideoSource` on macOS. For each capture it makes a VideoToolbox encoder of the session's size, frame rate and bitrate, full range, and runs `capture.CaptureVideo` into it; the encoder is closed after the capture stops. The app's parts use it, and `host.Options` take `VideoFPS` and `MaxBitrate` from the settings.
  - `internal/mac/vtenc`: `Close` no longer holds the setters' lock while VideoToolbox finishes its last frames, because their frame function (the H.264 path) may call `ForceKeyFrame` or `SetSkip` meanwhile, which deadlocked; `SubmitNV12`, under the build tag `mardp_freerdp`, encodes a picture made in Go.
  - The window shows the connected session's video path: `host.Host.VideoPath()`, named in `cmd/mardp`, shown as "Video: …".
  - `internal/config`: version 2 of `config.json`. The `maxFps` setting caps the H.264 path, default 60; a 30 written by version 1 (its default, which the window could not change) becomes 60.
  - SPEC §4.1 (`internal/mac/video`), §9.3 (the colour and profile results), §9.4 (`maxFps`), §15.3 (how the H.264 tests get their picture).
- **Proved:**
  - `mardp_freerdp` TestFreeRDPAVC, FreeRDP 3.33.1 in the test machine against colour bars served by the test through the host and VideoToolbox: the session takes the AVC420 path (GFX 8.1 with AVC420), FreeRDP gets 160 to 165 AVC420 frames in 5 s at 30 fps, which needs its acknowledgements (without them the flow control stops after 3), logs no codec, drawing or channel error, and its screenshot has the bars within 2 levels of their colours for Constrained High, Main and Constrained Baseline at full range. The same bars at video range come back with white at 235 and black at 16: FreeRDP converts with BT.709 at full range whatever the SPS says.
  - `mardp_freerdp` TestFreeRDPHeadless now runs twice: as before (bitmaps), and with `/gfx:AVC420`, where `mardp -headless` sends the Mac's real screen through ScreenCaptureKit into VideoToolbox (`path=avc420`, encoder 1024×576 at 60 fps, a screenshot of over 1000 colours, no stopped capture).
  - Fakes: the video source's options, the encoder handed to the session and to the capture, frames passed through, the encoder closed after the capture, an encoder or capture failure with its route; `Close` with the setters called from the frames it waits for; `Host.VideoPath` through bitmaps, AVC420, Planar and the end of the session; the window's text; the config upgrade.
- **Differs from the plan:**
  - The colour-bar test serves its own picture instead of going through `mardp -headless`: the real app shows the owner's screen, which has no colour bars. The real app's H.264 path is checked by the second run of TestFreeRDPHeadless.
  - The colour result removed a choice: the capture and the encoder are always full range.
  - Not planned but needed: the encoder's `Close` deadlock, the config upgrade, and FreeRDP's flag (`/gfx:AVC420` advertises GFX 8.0, 8.1 and FreeRDP's own set, so 8.1 is confirmed).
- **Manual checks:** pending, see docs/MANUAL-CHECKS.md (3.9, a 1080p video in Remmina).

## 4.1 RDPSND (2026-11-19)
- **Built:**
  - `rdp/internal/rdpsnd`: the PDUs of MS-RDPEA the server sends and reads: the server's and client's formats (AUDIO_FORMAT with its extra bytes), Quality Mode, Training and its confirm, WaveInfo with Wave, Wave2, Wave Confirm, Close.
  - `Config.AudioFormats` and `PCMFormat`; `Session.Audio()` with `Format`, `Write`, `Delay` and `Close` (SPEC §6). When the session opens the server sends its formats (version 8), takes the first of them the client has, trains, and then sends each `Write` as one Wave2, or WaveInfo and Wave in one packet for a client older than version 8, on the control queue. The delay is from `Write` to the block's Wave Confirm.
  - `rdptest`: `NegotiateAudio`, `Audio.ReadBlock`, `Audio.Confirm`, `Audio.Read`, `Audio.Send`.
  - Static channel messages are chunked by `appendChannelMessage`, shared by `sendChannel` and `Write`.
  - SPEC §6 (the sound API) and §10.1 (the channel as built).
- **Proved:**
  - The bytes of Microsoft's examples 4.1.1–4.2.4 (server and client formats, Training and its confirm, WaveInfo, the start of Wave, the Wave2 header, Wave Confirm), read and written; truncated and malformed PDUs with their routes.
  - TestAudio: `rdptest` negotiates PCM and AAC with clients of versions 8, 6 and 5; the server takes the first of its formats in the client's list with the client's index; blocks numbered from 0 of 5, 1920 and 7000 bytes (several chunks) come back whole, as Wave2 with their audio timestamp or as WaveInfo and Wave; each confirm empties the unconfirmed count and gives a delay; Close sends the Close PDU and `Write` then fails.
  - TestAudioUnavailable: no `rdpsnd`, no formats, a client that plays nothing, none or no common format, and a Training not confirmed: no format, `Write` fails with `TagAudioUnavailable`. TestAudioIgnores: a confirm of another Training, malformed PDUs, PDUs out of turn and confirms of blocks never sent are ignored and the sound goes on.
  - `NewServer` refuses formats that do not fit one PDU; `Write` makes no allocations (both PDU kinds).
- **Differs from the plan:** nothing.
- **Manual checks:** none (a real client plays the sound in 4.6).

## 4.2 The sound source (2026-11-19)
- **Built:**
  - `internal/mac/capture`: `NewSound` and `Sound`, the host's `SoundSource` on macOS: a ScreenCaptureKit stream of sound on the display (48 kHz, two channels, float, not interleaved), without mardp's own sound and without the apps of `Options.ExcludeApps`, UTM's (`com.utmapp.`) by default. It follows the display as the picture's capture does, starts again when the apps it leaves out change (checked every 5 s), and drops buffers of exact silence. Each buffer goes to Go in place, with its time from the capture's first buffer.
  - The stub for other systems (`TagCaptureUnsupported`), and, under `mardp_mac`, a tone the test process plays itself (AVAudioEngine) and its bundle identifier.
  - SPEC §10.1 (the capture as built).
- **Proved:**
  - `mardp_mac` TestMacSound, run as the signed test app with the owner's output muted and its volume untouched: a 1 kHz tone at 0.1 played by the test's own process comes back at 0.0995 with its own sound let in, at 48 kHz (91,200 frames in 1.88 s, times never backwards); with the default options (own sound left out) nothing comes; with the test's own app among the apps left out, nothing comes, and ScreenCaptureKit lists that one app as matching. The rest of the package's Mac tests pass with the changed stream start.
  - With the fake backend: the stream's mode (the default and given apps, the 16×16 picture at 1 fps), a new stream when the apps left out change, none when ScreenCaptureKit does not answer; buffers delivered with their time, silent and broken ones not, no allocation per buffer.
- **Differs from the plan:**
  - UTM's own sound was not played: the exclusion is proved with the test app standing in for UTM, through the same filter. Whether UTM plays the guest's sound from its windowed app (left out) or from its QEMU helper (which has no window, so ScreenCaptureKit cannot leave it out) is checked in 4.6 by ear-free measurement if the guest can play through its sound device, else by hand.
  - The sound is not wired into the session yet: the pipeline (`host.Parts.Sound`) is 4.3.
- **Manual checks:** none.

## 4.3 PCM, end to end (2026-11-19)
- **Built:**
  - `internal/host`:
    - `Parts.Sound`, and `Host.AudioFormats` for the server's `Config.AudioFormats` (16-bit PCM, 48 kHz, stereo, when there is a sound source).
    - `PCMEncoder`: 20 ms packets carrying their first frame's time; a gap pads the packet being filled.
    - The sound path: it waits for the channel, then encodes and writes packets under the gap rule. A packet refused by a full queue is dropped, and the path stops when the session has no sound.
  - `cmd/mardp`: the app offers sound and captures it with `capture.NewSound`, unless the setting is "off".
  - `rdp`:
    - `Audio.Wait`: waits for the channel to be set up, with `TagAudioWaitAbandoned`.
    - `Audio.Backlog`: the age of the oldest block not yet confirmed.
    - A Wave Confirm also confirms the blocks before it, and a block unconfirmed for 5 s is no longer counted.
  - `rdptest`:
    - `KeepChannel` and `ChannelPending`, so a test can read two static channels without losing either.
    - `Audio.Keep` and `Audio.Pending`.
    - A message's arrival time (`ChannelMessage.Received`, used as `AudioBlock.Received`).
  - SPEC §10.1: the rule and the packets as built.
- **Proved:**
  - TestSessionSound runs ToneSource at 1 kHz through the host into `rdptest`.
    - Every block is 20 ms of PCM whose samples are exactly the tone's at the block's time stamp.
    - A client that holds 20 blocks unconfirmed and then stalls for 400 ms passes 500 ms. The host drops sound once and resumes after the client catches up. The time stamps show exactly one gap, at least 300 ms long, and otherwise run 20 ms apart.
  - TestSessionH264 now carries sound as well. While the video is throttled to a client at a third of the rate, sound arrives at full pace: at least 60 blocks in 1.5 s, no gap in about 4 s, nothing dropped.
  - Fakes:
    - The gap rule at its edges: 500 ms is not over, 100 ms is not under, and the number dropped is reported.
    - A full queue drops the packet; a session without sound stops the capture.
  - The encoder:
    - buffers of 441 and 960 frames, jitter under 10 ms, gaps forward and back, channels of different lengths, empty buffers;
    - rounding, clipping and NaN;
    - no allocation per buffer.
  - A client that plays nothing gets no sound, and the capture never starts.
  - `rdp`:
    - `Wait`: set up, no `rdpsnd`, plays nothing, closed while training, abandoned.
    - `Backlog`: a missing confirm covered by a later one, and a lost one dropped after the limit.
    - `KeepChannel`: the DVC capabilities are kept while the sound is read, and lost without it.
- **Differs from the plan:**
  - The delay the rule uses is the age of the oldest block not yet confirmed, not the last confirmed delay. The confirmed delay goes stale exactly when a client stalls. SPEC §10.1 says so.
  - Sound is dropped before it is queued, not taken out of the queue.
  - Not planned: carrying sound in TestSessionH264 exposed a race in `flowSwitch`. A key frame wanted between the picture choosing its path and setting it was lost, so the session stayed on bitmaps (about 1 run in 6 under load). `set` now ends a path at once when the path to run has changed. TestFlowSwitch covers it, and 12 loaded runs of `rdp` and `host` passed.
- **Manual checks:** none (a real client plays it in 4.6).

## 4.4 AAC (2026-11-19)
- **Built:**
  - `rdp.AACFormat(samplesPerSec, channels, bitrate)`: the WAVE_FORMAT_AAC_MS format block, its HEAACWAVEINFO and AudioSpecificConfig (an explicit rate for one that has no index).
  - `internal/mac/aacenc`: AudioToolbox's AudioConverter from the capture's float format to AAC-LC at 192 kbit/s. It sends raw access units, one per packet, timed by the captured frame each decodes to (less the 2112-frame delay); short gaps are filled with silence, long or backward gaps bring the encoder's sound out and start again. It allocates nothing per buffer, and a stub stands in on other systems.
  - `internal/host`: `Parts.AAC`, `AACFormat`, `AACBitrate`; `AudioFormats` offers AAC then PCM when the host has an AAC encoder; the sound path chooses the encoder by the negotiated format and closes it when the sound ends.
  - `cmd/mardp`: tries AudioToolbox's encoder once at start; without it the host offers PCM alone, with a warning.
  - `rdptest.AudioTags`: a client that takes some of the server's formats by tag.
  - SPEC §10.1: AAC as built.
- **Proved:**
  - `mardp_mac` (`go test -tags mardp_mac ./internal/mac/aacenc`, no permission needed, nothing played): 1 s of a 1 kHz tone through AudioToolbox's encoder.
    - priming 2112, bitrate 192000, largest packet 1536, a cookie holding `11 90`;
    - every packet 1–1536 bytes, none starting with an ADTS sync word, each at the time of the frame it decodes to;
    - the whole tone comes out when a long gap follows; decoded again with AudioToolbox, the tone past the delay is 38 dB above its error.
    - The routes of `AACEncoderCreateFailed` (1 bit/s) and `AACEncodeFailed` (no room for a packet).
  - Fakes: the delay, a start later than 0, chunks of 4096 frames, jitter, the longest gap filled, a long gap, a backward gap, empty buffers, channels of different lengths, a converter's error returned untouched, Close twice, `AACEncoderClosed`, no allocation per buffer. `AACEncoderUnsupported` on other systems.
  - `rdp`: the format blocks for 48 kHz, 44.1 kHz, mono, six channels and a rate with no index.
  - `host`: the formats offered with and without sound and AAC; a client with AAC and PCM, or AAC alone, gets the AAC encoder's packets, one a block, and the encoder is closed when the session ends; a client with PCM alone gets the tone in PCM; an AAC encoder that fails to start is logged and no sound is sent.
- **Differs from the plan:**
  - `aacenc.Format` became `host.AACFormat`: `internal/mac` packages may not import `rdp` (archtest).
  - Not planned: AudioToolbox, given less room than a packet, takes the frames and returns nothing, with no error. The encoder now fails instead (it never gives too little room).
- **Manual checks:** none (a real client plays it in 4.6).

## 4.5 Where the sound plays (2026-11-19)
- **Built:**
  - `internal/host`:
    - the `Output` interface (the default device's UID, reading and setting its mute) and `MuteMarker`;
    - `Parts.Output`, `Options.MuteMarker`;
    - the muter: it mutes the default device when the first session's sound starts, unless it is muted already, and unmutes it when the last ends, with the marker written before and removed after;
    - `RecoverOutput` unmutes the device a marker names, at start;
    - `FakeOutput`.
  - `internal/mac/system`: Core Audio's default output device, its UID, the device of a UID, and its mute (`OutputFailed`, `OutputUnknown`, `OutputMuteUnsupported`).
  - `internal/config`: the marker `muted.json` (`MarkMuted`, `MutedMark`, `ClearMuted`, `ConfigMuteMarkCorrupt`).
  - `cmd/mardp`: each start of the server recovers a marker; with "On the client only" and sound, the host gets the system as its output.
  - SPEC §10.1 as built; the `internal/mac/system` row of §4.1.
- **Proved:**
  - The muter with fakes:
    - mutes and unmutes, with the marker;
    - leaves a muted device alone;
    - no default device, no mute to read or to set, and no marker: nothing muted;
    - the default changing during a session: the device muted is the one unmuted;
    - the owner unmuting during a session;
    - an unplugged device, or a marker that cannot be removed, keeps the marker;
    - two sessions mute once and unmute when the last ends, with two writes in all.
  - `RecoverOutput`: no marker, a muted device unmuted, an unplugged device, an unreadable marker, a marker that cannot be removed.
  - Sessions in `host`: a client that plays the sound mutes the output and leaves it unmuted, marker gone, when it leaves; one that plays nothing changes nothing.
  - `cmd/mardp` (TestServeMute): a marker left with the device muted is recovered when the server starts; with "On the client only" a client's sound mutes the output, and stopping the server unmutes it and removes the marker; with "On both" nothing is muted.
  - Core Audio:
    - with fakes, every route of the three tags;
    - `mardp_mac`, read-only (it never sets the mute): the default output `BuiltInSpeakerDevice` (muted, as the owner left it), and `OutputUnknown` for a UID no device has;
    - the routes of `SystemUnsupported` on other systems.
  - The marker: written with the folder's file mode, read, cleared, and every failure route.
- **Differs from the plan:**
  - The output is muted when a session's sound starts, not when a client connects: a client that plays no sound would otherwise leave the sound nowhere.
  - A device muted before mardp is never touched.
  - "On both" still recovers a marker left from before.
- **Manual checks:** the muting itself, the crash recovery and "on both": pending, see docs/MANUAL-CHECKS.md (4.5).


## 4.6 A real client plays it (2026-11-19)
- **Built:**
  - `cmd/mardp/freerdp_sound_test.go` (`mardp_freerdp`):
    - the library's server serves a 1 kHz tone (`host.ToneSource`), as PCM, and as AAC through AudioToolbox;
    - FreeRDP in the test machine plays it with `/sound:sys:alsa,dev:mardpfile`, an ALSA file plugin over the null device, so the guest writes what it plays to `sound.wav` and nothing is heard anywhere;
    - the test pulls the recording and compares it with the tone.
  - `compareTone` and its self-test TestCompareTone (no test machine needed). From the sound's first loud sample to its last, less 50 ms at each end, every 10 ms window, taken every 5 ms, of each channel must be the tone at its level (RMS 0.15–0.2) with 99 % of it at 1 kHz. The self-test shows it catches 20 ms of silence, half a period dropped, a louder tone and added noise.
  - The AVC test's server helper is now `serveHost`, shared with the sound test, and it passes the host's sound formats to the server.
  - SPEC §15.3: the sink as built.
- **Proved** (`scripts/vm.sh start`, `go test -tags mardp_freerdp -run 'FreeRDPSound|CompareTone' -v ./cmd/mardp/`, `scripts/vm.sh stop`), FreeRDP 3.33.1 on Fedora 45:
  - PCM: format 1 negotiated and sent; 8.46 s of sound at 48 kHz, two channels; 1670 windows, none off the tone; RMS 0.177; 1.0000 of it at 1 kHz.
  - AAC: format 0xA106 negotiated and sent; FreeRDP opened its device with `WAVE_FORMAT_AAC_MS [backend WAVE_FORMAT_PCM]` (it decodes the AAC itself); 8.41 s of sound; 1661 windows, none off the tone; RMS 0.177; 1.0000 of it at 1 kHz.
  - mardp's log has no dropped packet, no full queue, no stopped sound.
  - TestFreeRDPAVC still passes after the refactor.
- **Differs from the plan:**
  - The "null sink" is ALSA's file plugin over the null device, not a PulseAudio or PipeWire null sink. The guest agent runs as root with no sound server, and FreeRDP's ALSA backend writes straight into the file.
  - Seen, not changed: FreeRDP 3.33 confirms each Wave2 block twice, so mardp logs the second confirm at Debug as "a Wave Confirm of no block ignored". The first confirm is matched, so the backlog and the gap rule work. FreeRDP also drops the first block while it opens its device (the file plugin has no mixer).
- **Manual checks:** a video with sound for 10 minutes and the clap test on the owner's Linux machine: pending, see docs/MANUAL-CHECKS.md (4.6).


## 5.1 Settings (2026-11-19)
- **Built:**
  - `internal/ui/settings.go`: the settings form, which keeps the text fields as typed and the bitrate in Mbit/s. It turns the form into `config.Settings` with an error per field (config's own validation, through its metadata "field"). It also builds the choices of the display, height cap, frame rate and sound menus, always with the saved value among them.
  - `internal/ui/settings_card.go`: the settings card, grouped by when the settings apply, each group saying so:
    - at once: the user name and password, with a Change button that opens the login form, and "Open the app at login";
    - at the next session: port, address, display, height cap, frame rate cap, bitrate cap, sound, swap Ctrl and Command, and "New certificate";
    - the next time the app opens: "Start the server when the app opens".

    "Save settings" and "Undo changes" are enabled only with changes. Messages show under the fields that are not valid. A note says that the settings are saved, or pending while the server runs. Settings saved elsewhere show in the form unless it has changes of its own.
  - `ui.Backend` gains `SaveSettings` and `NewCertificate`; `ui.Status` gains the saved settings, the Mac's displays and `Pending`.
  - `cmd/mardp` app:
    - `SaveSettings` keeps the login, checks that the address is one of the Mac's (new tags `MardpAddressNotLocal`, with metadata field "address", and `MardpInterfacesFailed`), and saves.
    - When a setting of a session changed (or the certificate is new) and the server runs, settings are pending. A goroutine polls four times a second and starts the server again once no client is connected. Stopping the server drops what is pending.
    - Quitting the window closes the app, so that a restart cannot start the server again after it.
  - SPEC §12.2: the settings as built.
- **Proved:**
  - `gluttertest`, against a fake backend:
    - each of the ten settings changed in the window and saved, with nothing else changed;
    - Save and Undo disabled without changes;
    - the port, address and bitrate messages, with nothing saved, and Undo clearing them;
    - an address the backend refuses (by its "field");
    - another save failure shown;
    - the pending note while the server runs, gone once it started again;
    - settings saved elsewhere followed, and a change in the form kept;
    - New certificate;
    - the login changed from the settings card;
    - saved values the menus do not offer.
  - Unit tests of the form (24 cases: every field, valid and not), `FieldOf`, and the choices.
  - `cmd/mardp`, with a temporary `MARDP_HOME`:
    - the settings saved with the login kept, in `config.json` and the status, with the displays;
    - the refused address (not the Mac's, not an IP), port and bitrate, with their routes and fields, nothing saved;
    - the interfaces failing;
    - a running server with a client connected: "open at login" and "start server" leave nothing pending; a frame rate change and a new certificate stay pending while the client is connected; after it disconnects, the server runs again with 30 frames a second and a new fingerprint;
    - Stop dropping what is pending;
    - the certificate failing;
    - no start after the app closed.
- **Differs from the plan:**
  - Nothing is applied to a running session: the plan's "at the next session" is a restart of the server once no client is connected, which keeps one place that reads the settings (prepare).
  - The settings card is always shown, under the status and the checklist.
- **Manual checks:** the card in the real window, and a restart for saved settings with Remmina: pending, see docs/MANUAL-CHECKS.md (5.1).

## 5.2 Status and events (2026-11-19)
- **Built:**
  - `rdp`: `Session.Stats` (bytes and frames written, frames acknowledged on the graphics pipeline, the smoothed acknowledgement delay) and `Session.ClosedBy`, exported from the former `closedBy`. When the server stops, a session's context now ends only after the stop has set the reason (it no longer derives from the server's context), so the handler always finds `CloseServerStopped`; before, it could see the context end first and take the session as ended by the client.
  - `internal/host`: `Host.Session` returns the running session's details and numbers, taken once a second from `Stats`, and the sound format once the sound starts. `Host.Disconnect` closes the session for the owner. `Options.OnEvent` is told when a session opens and when it ends, with why (`host.Ended`, from `ClosedBy` and whether Disconnect ended it).
  - `cmd/mardp`: the app keeps the last 50 events (connections, disconnections, refused logins from the authenticator), fills `Status.Session` and `Status.Events`, and implements `Backend.Disconnect`. `prepare` takes an `observer` for the events; the headless server passes none.
  - `internal/ui`: the session's lines and a Disconnect button in the status card, and a "Recent events" card, the newest first.
  - SPEC §12.2: the status and events as built.
- **Proved:**
  - `gluttertest`, with a fake backend whose state changes: the session's lines and the events shown, the newest first; the numbers and the video path following the backend; Disconnect ending the session and its event shown; "Nothing yet." and no Disconnect without a session.
  - Unit tests of the session's lines and of every event's text.
  - `internal/host`, with a real `rdp` server and the test client: the details and numbers on the bitmap path; the frames, bitrate and delay on the H.264 path; the sound format, AAC or PCM; the events of a session that the client, the owner (Disconnect, with Set Error Info to the client) and a failed capture end; Disconnect without a session.
  - `rdp`: the stats on the bitmap and graphics paths; `ClosedBy` for each close reason, and 0 while open and when the client disconnects; `ClosedBy` is `CloseServerStopped` when the handler's context ends because the server stops.
  - `cmd/mardp`, with a temporary `MARDP_HOME`: a refused login, a session's details, Disconnect, and a session the server's stop ends, in the events in order; the limit of 50.
- **Differs from the plan:** the plan's fake host is the fake backend of the window's tests; the real host is tested with the test client instead.
- **Manual checks:** the numbers with Remmina: pending, see docs/MANUAL-CHECKS.md (5.2).

## 5.3 The menu-bar icon (2026-11-19)
- **Built:**
  - `internal/ui`: the menu-bar icon (`desktop.SystemTray`) around the window, with an icon for each state (`TrayIcon`: a template image of a screen on a stand, empty, with a dot, or filled) and its menu (`NewTrayMenu`: the state, Start or Stop, Disconnect while connected, Show window, Quit), rebuilt with the window's status. Closing the window hides it (`AppLifecycleListener`); Quit ends the app, which stops the server as before.
  - `internal/mac/system`: `WatchPowerOff` calls a function when macOS says it logs out, restarts or shuts down (`NSWorkspaceWillPowerOffNotification`); a no-op on other systems.
  - `cmd/mardp`: the window's context ends at a logout or shutdown, so mardp quits instead of hiding its window and keeping the logout waiting.
  - SPEC §12.3: the icon and the menu as built; the package table: `internal/mac/system` has the notice of a logout.
- **Proved:**
  - Unit tests of the menu for each state, busy or not, and of the three icons (five sizes, black as a template image, each different).
  - `gluttertest` with the fake desktop: the menu follows the server (Start, Stop, Disconnect from the menu reach the backend; the icon changes with the state); closing the window hides it with no exit request; Show window brings it back; Quit asks the app to exit, with the window shown or hidden.
  - `internal/mac/system`: the function is called when the notification is posted in the process, and not after stop.
  - The built app on this Mac: it starts with the icon and no tray error in its log, and a SIGTERM still ends it at once (exit 0).
- **Differs from the plan:**
  - Cmd+Q hides the window as the close button does: Glutter gives the app no way to tell them apart (docs/GLUTTER-ISSUES.md). Quit is in the icon's menu.
  - Not in the plan: quitting at a logout or shutdown, which the hidden window would otherwise block (`WatchPowerOff`).
  - The window's tests now run on Glutter's fake Linux desktop, the only one with a tray (docs/GLUTTER-ISSUES.md); they need `-tags debug_mode`, as before.
- **Manual checks:** the icon and its menu with real clicks, closing and Quit, a logout: pending, see docs/MANUAL-CHECKS.md (5.3).

## 5.4 Notifications and failed logins (2026-11-19)
- **Built:**
  - `internal/ui`: the window posts the notifications of the events it has not seen (`Status.EventTotal`), through Glutter's `desktop.ShowNotification`: a client connected, a client disconnected, and failed logins from an address when one makes it wait (`NotificationOf`). A click brings the window back; a failure to notify is shown in the events card. A failed login's event line has the count and the wait it starts, and the waiting rule (`WaitRuleText`, from `config`'s constants) shows under events with failed logins.
  - `internal/config`: `LoginResult.Checked` says whether the password was checked (false while the address waits); the doc of `Wait` was wrong about a failure that starts a wait.
  - `cmd/mardp`: the authenticator hands the guard's result to the app, which records only the failures that were checked, with their count and wait, and counts every event.
  - SPEC §12.4: the notifications as built.
- **Proved:**
  - Unit tests of each notification and of the events without one, of the wait in words and of the waiting rule.
  - `gluttertest` with the fake desktop as the notifier: no notification for the events before the window opened; a connection; five failed logins with one notification, at the one that starts the wait; the waiting rule shown; a click on a notification bringing the hidden window back; Disconnect's notification.
  - `cmd/mardp`: the authenticator tells the app the fifth failure's wait and the unchecked login while waiting; the app records no event for the latter and counts the events; a failed login's event has its count.
  - `internal/config`: `Checked` at each step of the guard's table.
- **Differs from the plan:** the plan's fake notifier is Glutter's fake desktop, which serves the notifications the window posts.
- **Manual checks:** the notifications in macOS's notification center and the failed logins with Remmina: pending, see docs/MANUAL-CHECKS.md (5.4).

## 5.5 Starting by itself (2026-11-19)
- **Built:**
  - `internal/mac/system`: `LoginItem`, which makes the running app a login item through `SMAppService.mainApp`, takes it out, and tells its state (off, on, waiting for the user's approval, app not found); a stub elsewhere.
  - `cmd/mardp`: the app makes itself a login item, or takes itself out, as "open the app at login" says, when it opens and when the setting is saved, leaving an item that waits for approval; it asks for the item's state every 3 seconds while the setting is on, on a goroutine, and turns the setting off when the user takes the app out of the login items in System Settings. "Start the server when the app opens" starts the server before the window shows, if a login is set and Screen Recording granted.
  - `internal/ui`: the settings card says when macOS waits for the user to allow the login item and when the login items could not be changed; the status card says why the server did not start as the app opened.
  - SPEC §7.3: both settings as built.
- **Proved:**
  - `internal/mac/system`: reading the state from a test binary (not an app bundle) answers "not found" in about 4 ms; the errors of registering and unregistering by a fake call, with their routes. No test registers anything: that would change the owner's login items.
  - `cmd/mardp`, with a fake of the login items: each pairing of the setting and the item's state, what is asked and what the window is told; the item set as the app opens; saving the setting changes the item and saving others does not; the window seeing an approval, and the setting turned off and saved when the item is taken out.
  - `cmd/mardp`: the server started as the app opens, not started with the setting off, without Screen Recording or without a login, and a taken port said in the window.
  - `gluttertest`: the three messages shown, and gone once over.
- **Differs from the plan:** the plan names the two settings only. Also built: the setting follows an item the user takes out in System Settings, and an item waiting for approval is said in the window.
- **Manual checks:** logging out and in with both settings on, and the login items in System Settings: pending, see docs/MANUAL-CHECKS.md (5.5).

## 5.6 The bundle and the README (2026-11-19)
- **Built:**
  - `scripts/icon`: draws the app's icon, 1024 pixels with macOS's margins and smooth edges; `scripts/build-app.sh` passes it to `glutter-macos -icon`.
  - `scripts/build-app.sh`: builds for macOS 14 (D20): the executable's deployment target through the `go` shim, `LSMinimumSystemVersion` written before signing, and a check of the executable's `minos`. Until now the bundle said 12.0 and the executable needed the building Mac's macOS (27.2).
  - The README: requirements, installing, the first start and its checklist, macOS's recurring Screen Recording question (R6), using the app, the screen lock while away (R3 is still pending, so it says to turn the lock off), the Linux side (`openh264`, not `noopenh264`; R5), the Remmina profile (TLS, "GFX AVC420 (32 bpp)", sound, the fingerprint), what mardp does not protect against (§8.3), the signing identity.
  - SPEC §4.1: the row of `scripts/icon` (standard library only), which `internal/archtest` checks; SPEC §16 as built.
- **Proved:**
  - A clone of the repository with nothing else in it builds `build/mardp.app` with `scripts/build-app.sh`; the bundle passes `codesign --verify --deep --strict`, has the icon, `minos` 14.0 and `LSMinimumSystemVersion` 14.0, prints its version with `-version`, and starts (with `MARDP_HOME` in a temporary folder, its log says it started; stopped with SIGTERM, exit 0).
  - A compile of everything for macOS 14 with `-Wunguarded-availability` gives no warning: nothing newer than macOS 14 is called unguarded.
  - The icon's test: its size, the clear margin and corners, the white frame and foot, the blue body.
- **Differs from the plan:** also built: the minimum macOS of the bundle (a Glutter workaround, docs/GLUTTER-ISSUES.md), and a row of the package table for the icon's program.
- **Manual checks:** the icon in the Dock and Finder, and reading the README: pending, see docs/MANUAL-CHECKS.md (5.6).

## 5.7 A fresh install (2026-11-19)
- **Built:** the check list of a fresh install (SPEC §15.7), in docs/MANUAL-CHECKS.md (5.7): a new user account on the owner's Mac, with none of mardp's settings, permissions or login items; the app from `/Applications`; the checklist with each permission and the password; Start and the firewall's question; Remmina with the README's profile, the fingerprint, the notifications' question; video with sound, the keyboard, the pointer; closing the window, Disconnect and Quit from the menu bar; deleting the account.
- **Proved:** by the automated tests up to here, the first start's checklist (`internal/ui` TestFirstStart), a bundle built from a clean checkout that verifies and starts (5.6). The owner's run of the list: pending.
- **Differs from the plan:** M5 is published with the owner's answers pending, as the earlier milestones were (CLAUDE.md: manual checks don't stop the work); they are recorded here when they come.
- **Manual checks:** the fresh install: pending, see docs/MANUAL-CHECKS.md (5.7).

## 6.1 Measuring (2026-11-19)
- **Built:**
  - `rdp`: `Video.SetWriteHook`: the session's writer tells a hook, after each video packet it wrote, when the packet was queued, when its write ended and its length (`VideoWrite`).
  - `internal/mac/vtenc`: each frame's capture time and the time the encoder handed it over (`Frame.Captured`, `Frame.Encoded`): the C callback takes the frame's age on the host clock, which ScreenCaptureKit's presentation times are on, and Go subtracts it from the hand-over time (unknown when negative or over 10 s). `SubmitPattern(n)` draws picture `n` of a moving test pattern (bands, and a square of a noisy gradient that moves 8 pixels a frame) into a buffer of the encoder's pool and submits it, stamped after drawing.
  - `internal/mac/video`: `NewPattern`, a video source that submits the test pattern at the session's frame rate, without capture; the frames' times pass through `CaptureVideo`.
  - `internal/host`: `EncodedFrame.Captured` and `Encoded`; `Options.OnFrameTiming`, told of each H.264 frame when its write ends (`FrameTiming`: captured, encoded, queued, written, bytes, key), the host pairing writes with the frames it sent in order; `Timings`, which gathers them into percentiles of each stage without allocating (`TimingSummary`, `Spread`).
  - `cmd/mardp`: `-test-pattern` and `-measure`, with `-headless` only. `-test-pattern` serves the pattern through VideoToolbox and a tone, with a system that grants Screen Recording and not Accessibility and has no output to mute. `-measure` logs "video timing" every 10 seconds and "session video timing" when a session ends.
  - `scripts/perf.sh` and its test (`cmd/mardp`, build tag `mardp_perf`): builds and runs `mardp -headless -test-pattern -measure`, connects `rdptest` at 1920×1080 with H.264 and sound for `MARDP_PERF_SECONDS` (default 60), and prints frames delivered each second, sound blocks, the host's delay, the bitrate, CPU and memory; it fails when a target of §14 that it measures is missed.
  - `docs/PERFORMANCE.md` with the first numbers; SPEC §6 (`SetWriteHook`), §13 (the two flags) and §15.5 as built.
- **Proved:**
  - `scripts/perf.sh` ran unattended for 60 seconds on the Apple M6: 60.0 frames a second delivered (fewest 60), host delay p95 6.1 ms (max 32.5 ms, the first key frame), 18.5 Mbit/s under the 20 Mbit/s cap, 14.9% of one core, 77 MB not growing. The numbers are in docs/PERFORMANCE.md.
  - `rdp`: the hook told of each video write in the writer's order with its times and length, and not of control packets; a removed hook is told nothing.
  - `internal/mac/vtenc`: the capture time from each age (table); with the real VideoToolbox (`mardp_mac`), 120 pictures of the pattern at 1080p60 all encoded, the first a key frame, each with a capture time, the oldest under 100 ms.
  - `internal/mac/video`: the pattern's pictures submitted on each tick, a run of refused pictures logged once, the encoder closed at the end; the frames' times passed through.
  - `internal/host`: pairing writes with frames, a frame not queued forgotten, another path's write ignored, the ring's overflow; the percentiles, the frames without a capture time, `Take` starting anew, no allocation in `Add`; a session over the loopback telling of every frame in order, each written after it was queued, encoded and captured.
  - `cmd/mardp`: `mardp -headless -test-pattern -measure` with no permission granted serves H.264, makes the pattern's parts and not the screen's, is view-only, and logs the session's timing; the measurer logs each period with frames and each session once; both flags without `-headless` are refused with exit code 2.
- **Differs from the plan:** with the test pattern the capture time is the moment the picture is handed to the encoder; the real capture's delay before that (ScreenCaptureKit's) is in the numbers of 6.3. The plan names the script; also built: the two flags of `mardp -headless` it needs, and the timing log, which the owner's runs of 6.3 use too.
- **Manual checks:** none.

## 6.2 Allocations (2026-11-19)
- **Built:**
  - `rdp`: `BenchmarkSendFrame` (H.264 frames of 4, 64 and 512 KiB, framed, written and acknowledged, with the measurement's write hook), `BenchmarkSendTiles` (a frame of tiles on the graphics pipeline), `BenchmarkAudioWrite` (PCM and AAC sizes); `BenchmarkSendRects` and `BenchmarkWriter` now assert too. Each fails when its loop allocates, in any goroutine, more than once in a thousand operations. `TestSendFrameAllocs`: 0 allocations per H.264 frame, with the hook and without.
  - `internal/host`: `BenchmarkAVCFrame` (the H.264 path's handling of a frame, measured into `Timings`) and `BenchmarkSoundBuffer` (a buffer encoded as PCM and written), asserting the same; `TestPathAllocs` asserts 0 with `testing.AllocsPerRun`.
  - The fix: the sound path made a method value of its packet function at each captured buffer (one allocation every 10 ms); it makes it once.
  - SPEC §11 rule 2 as built; docs/PERFORMANCE.md with the numbers.
- **Proved:** every benchmark passes with 0 B and 0 allocations per operation on the Apple M6 (docs/PERFORMANCE.md); the tests pass in the default run. The packages under `internal/mac` already had tests asserting 0 allocations on their paths (the capture's delivery of a frame and of sound, the encoder's delivery, the AAC encoder, the pointer).
- **Differs from the plan:** the benchmarks allow the runtime's own background allocations, once in a thousand operations (seen: about once in 100 000); an operation that allocates allocates every time and fails them. Also covered: the bitmap and tile paths and the writer, beyond frames of H.264 and sound packets.
- **Manual checks:** none.

## 6.3 The real numbers (2026-11-19)
- **Built:**
  - `scripts/perf.sh` also measures the time from the TCP connection to the first picture (TLS, login, activation, the graphics pipeline and the sound), and fails over 2 s.
  - docs/PERFORMANCE.md: the table of §14, each row with what this Mac measures and a column for the owner's numbers.
  - docs/MANUAL-CHECKS.md (6.3): the exact steps for the owner: `mardp -headless -measure` from the app bundle, a 1080p60 video full screen, Remmina with the README's profile, the time to the first picture, `top` on the mardp process during the video and with a still screen, photos of both clocks for the delay from screen to screen, the clap test, the "video timing" log lines.
- **Proved:** on this Mac: the first picture 0.26 s after the connection; the other numbers as in 6.1 (60 frames a second, host delay p95 6 ms, 15% of one core, 77 MB, under the bitrate cap).
- **Differs from the plan:** the owner's numbers are pending, so the constants of §9.5 are not tuned yet: on the loopback nothing is skipped and the bitrate never falls, so there is nothing to tune them against. They are tuned, with their before and after in docs/PERFORMANCE.md, when the owner's numbers come.
- **Manual checks:** the numbers of §14 on the owner's machines: pending, see docs/MANUAL-CHECKS.md (6.3).

## 6.4 Idle (2026-11-19)
- **Built:**
  - `internal/host`: the H.264 path's backlog ticker (half a frame's time, 120 wake-ups a second at 60 frames a second) runs only while the backlog holds the encoder back, and stops as soon as it clears; otherwise the path wakes once a second, for the bitrate, and checks then that the session is still on the H.264 path. `QuietSource`, a sound source where nothing plays.
  - `mardp -headless -test-pattern -still`: the pattern is a still picture, encoded once and again for each key frame the session forces, and nothing plays (`internal/mac/video` `Options.Still`).
  - `scripts/perf.sh idle` (`TestPerfIdle`): an idle session with `-still`, on the H.264 path with sound, for 300 s after 10 s to settle; prints the frames and sound blocks that came, the CPU of mardp (`ps`) and its wake-ups from idle in 30 s (`top`); fails at 2% of one core or if anything was sent. `scripts/perf.sh screen` (`TestPerfScreen`): the same on the real screen and its sound, through a bundle signed as the M0 probe, view-only and with the sound on both, reporting only.
  - SPEC §9.5, §11 rule 6, §13 (`-still`) and §15.5 as built; docs/PERFORMANCE.md.
- **Proved:**
  - `scripts/perf.sh idle` on the Apple M6, 5 minutes: nothing sent, 0.03% of one core (80 ms), woken 9.7 times a second. Before the change (60 s): 0.60% of one core, woken 321 times a second. `scripts/perf.sh screen` (60 s, the real screen, not still: 287 frames): 1.68% of one core. The numbers are in docs/PERFORMANCE.md.
  - `internal/host`: with frames going freely and then none, the backlog is read once per frame and never by the ticker; behind a backlog the ticker reads it until it drains, the skip clears, and the ticker stops. The sessions on the H.264 path (fast, slow and fast again, the path switches, the timing) pass as before.
  - `internal/mac/video`: the still pattern submits picture 0 once and again for each forced key frame, and makes no clock.
  - `cmd/mardp`: `-still` serves the still pattern with no permission; `-still` without `-test-pattern` is refused with exit code 2.
- **Differs from the plan:** "every periodic wake-up" is read as SPEC §11 rule 6 says it, no timers that wake many times a second: what still runs while idle is the pointer read 4 times a second (SPEC §10.3: macOS has no notification for a change of the pointer; one read is about 0.1 ms) and, once a second, the session's numbers, the bitrate decision and the capture's check of its display. A still screen already sent nothing (ScreenCaptureKit's idle frames and frames without dirty rectangles are dropped in C) and silence already sent no sound. The proof's idle desktop is the still test pattern, since this Mac's screen is not still while the session works on it (the app's own animations, about 5 frames a second); the real screen's number is reported beside it.
- **Manual checks:** none new; the owner's idle CPU with Remmina is part of 6.3 (docs/MANUAL-CHECKS.md).

## 6.5 A long run (2026-11-19)
- **Built:**
  - `internal/host`: `Host.Captures`, the screen, video and sound captures running now (each video capture holds an encoder session).
  - `mardp -measure` logs the process every 10 seconds ("process"): goroutines, the Go heap in use, the memory Go got from the system, the sessions and the captures running.
  - `scripts/perf.sh long` (`TestPerfLong`): one session of the moving pattern and the tone for 30 minutes (`MARDP_PERF_SECONDS`), then 100 sessions in turn that each connect, get the first picture and disconnect; fails on a dropout, memory growth, a session, capture or goroutine left after the sessions, or an error in mardp's log.
  - SPEC §13 (the "process" line) and §15.5 as built; docs/PERFORMANCE.md.
- **Proved:**
  - `scripts/perf.sh long` on the Apple M6: 30 minutes at a mean of 60.0 frames a second (fewest 59), 84 375 sound blocks with the longest wait 52 ms; memory 76.4 MB after 10 s, 78.6 MB at most in the last 5 minutes; 100 sessions in turn in 10 s, memory 99.7 MB after 10 and 100.1 MB after 100; goroutines 4 before any session and 4 after each stretch; 0 sessions and 0 captures left. The numbers are in docs/PERFORMANCE.md.
  - `internal/host`: the captures counted on the bitmap path, on the H.264 path with sound, and none after the session ends.
  - `cmd/mardp`: the measurer logs the process line with the host's sessions and captures.
- **Differs from the plan:** "no encoder session left" is proved through the captures: the H.264 encoder session lives inside the video capture and is closed when it returns (tested in `internal/mac/video`), so no video capture running means no encoder session. The run uses the test pattern (VideoToolbox, AAC), not the real screen, so it needs no permission and never touches the Mac's sound.
- **Manual checks:** none.
