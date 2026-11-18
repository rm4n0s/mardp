# X.224 Connection Requests of real clients

The first TPKT each client sent to a TCP listener (step 0.4, `spikes/recorder`, removed in step 0.9) on the Mac, 2026-11-19. The client ran in the test machine of `docs/VM.md` (Fedora 45, aarch64) under `xvfb-run`, with user name `mardp`, so the routing token is the cookie `Cookie: mstshash=mardp`. The listener closed each connection after the first TPKT.

| File | Client | Setting | `requestedProtocols` |
|---|---|---|---|
| `cr-freerdp-default.bin` | `xfreerdp` 3.33.1 (`freerdp-3.33.1-1.fc45`) | no `/sec:` | `0x0000000B`: TLS, CredSSP, CredSSP with Early User Authorization Result |
| `cr-freerdp-tls.bin` | same | `/sec:tls` | `0x00000001`: TLS |
| `cr-freerdp-nla.bin` | same | `/sec:nla` | `0x00000003`: TLS, CredSSP |
| `cr-freerdp-rdp.bin` | same | `/sec:rdp` | none: no RDP Negotiation Request at all (35 bytes, the cookie only) |
| `cr-remmina-tls.bin` | Remmina 1.4.43 (`remmina-1.4.43-6.fc45`), RDP plugin on the same FreeRDP | profile `security=tls` | `0x00000001`: TLS |
| `cr-remmina-auto.bin` | same | no `security` key (Remmina's "Automatic negotiation") | `0x0000000B` |

In every request with a negotiation block the flags byte is `0x00`. Both clients connect again with the same bytes when the server closes the connection this early; only the first connection of each run is kept.

These are bytes recorded on the wire, used as expected values in tests (SPEC §15.1); no code of FreeRDP was read.
