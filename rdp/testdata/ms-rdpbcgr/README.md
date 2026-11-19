# Microsoft's annotated examples

PDUs from the annotated connection sequence of MS-RDPBCGR (section 4.1), as bytes, used as expected values in tests (SPEC §15.1). Each file is the whole PDU as the dump shows it, TPKT header included, unless the table says otherwise.

| File | Section | Page |
|---|---|---|
| `connect-initial.bin` | 4.1.3 Client MCS Connect Initial PDU with GCC Conference Create Request (416 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/2610fcc7-3df4-4166-85bb-2c7ae21f6151 |
| `client-info.bin` | 4.1.10 Client Info PDU: the decrypted TS_INFO_PACKET alone, without the headers before it (400 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/ac6dc9ab-6f32-471e-8374-f80caab50069 |
| `connect-response.bin` | 4.1.4 Server MCS Connect Response PDU with GCC Conference Create Response (337 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/d23f7725-876c-48d4-9e41-8288896a19d3 |
| `demand-active.bin` | 4.1.12 Server Demand Active PDU: the decrypted share PDU alone, from its Share Control Header on (359 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/084026ea-8264-4315-ac66-c77dea02b0c1 |
| `confirm-active.bin` | 4.1.13 Client Confirm Active PDU: the decrypted share PDU alone, from its Share Control Header on (492 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/54765b0a-39d4-4746-92c6-8914934023da |
| `client-synchronize.bin` | 4.1.14 Client Synchronize PDU: the decrypted share PDU alone, from its Share Control Header on (22 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/daf2afd3-c864-43b5-90c8-1156df3ca1a9 |
| `client-control-cooperate.bin` | 4.1.15 Client Control PDU - Cooperate: the decrypted share PDU alone, from its Share Control Header on (26 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/291d4548-69d0-41fa-92a0-070cbe01167c |
| `client-control-request.bin` | 4.1.16 Client Control PDU - Request Control: the decrypted share PDU alone, from its Share Control Header on (26 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/8ae890a0-3030-48b1-9982-f857b9161eff |
| `client-font-list.bin` | 4.1.18 Client Font List PDU: the decrypted share PDU alone, from its Share Control Header on (26 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/c60386e7-bf10-42bd-a9b8-70854e497935 |
| `server-synchronize.bin` | 4.1.19 Server Synchronize PDU: the decrypted share PDU alone, from its Share Control Header on (22 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/3a622400-63ef-446f-afa3-2922eafc23d4 |
| `server-control-cooperate.bin` | 4.1.20 Server Control PDU - Cooperate: the decrypted share PDU alone, from its Share Control Header on (26 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/96281439-4c3a-4826-82aa-435835612361 |
| `server-control-granted.bin` | 4.1.21 Server Control PDU - Granted Control: the decrypted share PDU alone, from its Share Control Header on (26 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/d5624241-aa4d-465a-b66d-698b19d0d826 |
| `server-font-map.bin` | 4.1.22 Server Font Map PDU: the decrypted share PDU alone, from its Share Control Header on (26 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/a75f7a91-ac1a-4012-b7f6-80de38c26c2f |

The short PDUs (X.224, the MCS domain PDUs) are written into the tests themselves, with the section they come from.

Microsoft's server examples carry known faults, which the tests of `rdp/internal/caps` list: in Demand Active a Font Capability Set of length 4 (its structure has 8 bytes) and uninitialized bytes in padding and in the Input Capability Set; in the server's finalization PDUs uninitialized pad1 bytes, a streamID of 0 or 2, an uncompressedLength equal to totalLength, and an uninitialized targetUser in Synchronize; in the client's Font List an uninitialized uncompressedLength (0xda3b).
