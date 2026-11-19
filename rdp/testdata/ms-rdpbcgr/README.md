# Microsoft's annotated examples

PDUs from the annotated connection sequence of MS-RDPBCGR (section 4.1), as bytes, used as expected values in tests (SPEC §15.1). Each file is the whole PDU as the dump shows it, TPKT header included.

| File | Section | Page |
|---|---|---|
| `connect-initial.bin` | 4.1.3 Client MCS Connect Initial PDU with GCC Conference Create Request (416 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/2610fcc7-3df4-4166-85bb-2c7ae21f6151 |
| `connect-response.bin` | 4.1.4 Server MCS Connect Response PDU with GCC Conference Create Response (337 bytes) | https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/d23f7725-876c-48d4-9e41-8288896a19d3 |

The short PDUs (X.224, the MCS domain PDUs) are written into the tests themselves, with the section they come from.
