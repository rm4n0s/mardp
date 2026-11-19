# What real clients do

What real RDP clients did against mardp's server that the documents (MS-RDPBCGR and the others of SPEC §2) do not say, or say otherwise, and what mardp does about it. Each entry names the client, its version and where it was seen. The tests of SPEC §15.3 (`go test -tags mardp_freerdp ./rdp/`, `docs/VM.md`) are where each one shows.

## FreeRDP 3.33.1 (xfreerdp, Fedora 45 aarch64, under Xvfb; step 1.13)

### It does not ask for Set Error Info

Its Client Core Data has earlyCapabilityFlags 0x0EB2: RNS_UD_CS_SUPPORT_ERRINFO_PDU (0x0001) is not set. The server follows MS-RDPBCGR 2.2.1.3.2 and sends Set Error Info only to a client that sets it (SPEC §5.4), so FreeRDP gets the Disconnect Provider Ultimatum alone, every time a session ends. FreeRDP turns the ultimatum's reason, rn-provider-initiated, into ERRINFO_RPC_INITIATED_DISCONNECT (0x01): "The disconnection was initiated by an administrative tool on the server in another session". It shows this whether the password was wrong, the session was replaced, the server stopped or the host ended it.

- **A wrong password** therefore does not reach FreeRDP as ERRINFO_SERVER_FRESH_CREDENTIALS_REQUIRED (SPEC §20 Q1). xfreerdp exits with status 131. Sending License Valid Client first, so that Set Error Info would come after licensing, changes nothing (tried): the PDU is not sent at all.
- **What mardp does:** nothing different. Sending Set Error Info to a client that did not ask for it is outside the documents. What Remmina shows for a wrong password is the manual check of step 1.13.

### Security negotiation

- It asks for PROTOCOL_SSL | PROTOCOL_HYBRID | PROTOCOL_HYBRID_EX (0x0B). The server selects PROTOCOL_SSL, and FreeRDP goes on with TLS without CredSSP, as the documents allow.
- Its X.224 Connection Request carries the routing token `Cookie: mstshash=<user>`.
- With `/sec:rdp` it asks for PROTOCOL_RDP only. The server answers SSL_REQUIRED_BY_SERVER, and FreeRDP reports "Protocol Security Negotiation Failure" and exits with status 133.

### What it says about itself

- Client name: the machine's host name. Client build: 18363. Keyboard layout: from the X keyboard (0x409 for US).
- Static channels by default: `rdpdr`, `rdpsnd`, `cliprdr`, `drdynvc`.
- Fast-path output, a largest request of 8 MiB, the New Pointer Update (alpha), a pointer cache of 25 and Large Pointer up to 384×384. It does not set DRAW_ALLOW_SKIP_ALPHA.

### Input

- **At focus**, and three times while it starts, it sends a Tab key-up (scancode 0x0F) with no key-down before it, then a Synchronize event with every lock off, then a Tab key-up again. MS-RDPBCGR 2.2.8.1.1.3.1.1.5 says only that a Synchronize event resets the server's keys to all up, and should be followed by key-downs for the keys that are down. It says nothing of a key-up around it. An `InputSink` must accept a key-up for a key it never saw go down. The host (step 2.1) should treat Synchronize as "every key up", as the document says.
- **Pointer moves come one event late** under Xvfb with xdotool (XTest): each move reaches the server only when the next pointer event happens. Buttons and keys come at once, and a button event carries the right position. The server is not the cause: `TestInput` sends single moves, one per PDU, and each arrives at once. The test therefore ends with one more move. Whether a real desktop does the same is part of the manual check of step 1.13.

### Bitmaps

- RDP 6.0 Planar with bottom-up scan lines (SPEC §9.6) is what FreeRDP decodes: a 1024×768 test pattern sent as fast-path Bitmap Updates matched the screenshot in the guest pixel for pixel. The pattern differs between its top and bottom halves, so an upside-down picture would not match. Its top half is flat bars, which compress, so those tiles go as Planar.

### Ending

- After the server's ultimatum it closes TLS (close notify) and the connection at once, well within the 2 seconds the server waits.
- Stopped with SIGTERM, it sends nothing more and closes the connection. The server logs "session closed by the client".
