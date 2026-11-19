# Performance

The measurements of milestone M6 against the targets of [SPEC §14](SPEC.md#14-performance-targets). Each section says how a number was measured and on what; a change that moves a number has its before and after here.

## How to measure

```sh
scripts/perf.sh                     # 60 seconds
MARDP_PERF_SECONDS=300 scripts/perf.sh
```

`scripts/perf.sh` builds mardp and runs `mardp -headless -test-pattern -measure` with its settings in a temporary folder (SPEC §13, §15.5). The test pattern is drawn into the encoder's own buffers and encoded by VideoToolbox at 60 frames a second, as a capture would hand them over; the sound is a tone through the AAC encoder. `rdptest`, on the same Mac, connects at 1920×1080 with H.264 (AVC420) and sound, acknowledges every frame at once and confirms every sound block. The script needs no permission and posts nothing to the Mac.

What it reports:

- **Frames delivered**: H.264 frames the client got and acknowledged, each second; the first second (the session's start) is left out of the mean.
- **Host delay**: from mardp's own "session video timing" log line (`-measure`): from the capture of a frame to the end of the write of its last byte to the socket, and each stage between. With the test pattern the capture time is the moment the picture was handed to the encoder.
- **Bitrate**: by mardp's log (bytes written over the span of the writes) and by the client (the H.264 bytes it got over the watch).
- **CPU**: the user and system time of the mardp process, from its resource usage when it exits, over the watch.
- **Memory**: the resident memory of the mardp process, each second (`ps`), and its largest (resource usage).

It fails when a target it measures is missed. What it cannot measure (the delay from screen to screen, the sound and the picture apart, the real screen's capture, the network) is measured by hand with the owner's Linux machine (6.3).

## First numbers (6.1, 2026-11-19)

Apple M6, macOS 27.2; 60 seconds; the default settings (60 frames a second, 20 Mbit/s cap, sound on the client).

| What | Target (§14) | Measured |
|---|---|---|
| Frames delivered | ≥ 57 of 60 a second | mean 60.0, fewest 60 in a second; 0 of 59 seconds under 57 |
| Host delay, capture to written | ≤ 25 ms for 95% | p50 5.8 ms, p95 6.1 ms, p99 6.3 ms, max 32.5 ms |
| — capture to encoded | | p50 5.7 ms, p95 6.0 ms |
| — encoded to queued | | p95 0.1 ms |
| — queued to written | | p95 0.2 ms |
| Sound blocks confirmed | no dropout | 2813, 46.9 a second (AAC: 1024 samples at 48 kHz, 46.875 a second) |
| CPU of mardp | < 60% of one core | 14.9% (8.95 s in 60 s) |
| Memory of mardp | < 300 MB, not growing | 76.2 MB after 1 s, 77.1 MB at the end, 77.2 MB at most |
| Bitrate | ≤ cap (20 Mbit/s) | 18.5 Mbit/s by the log, 18.1 Mbit/s at the client; 1 key frame |

Notes:

- Almost all of the host's delay is the encoder: VideoToolbox takes about 5.7 ms a 1080p frame. Handing the frame to `rdp` and writing it to the loopback socket take a few tenths of a millisecond.
- The maximum (32.5 ms) is the first frame, the key frame of the encoder's start.
- The test pattern's moving square is a gradient with noise, harder to encode than most desktops, so the bitrate sits near the cap: the rate control of §9.5 holds it there.
- The loopback has no network delay: the write stage is the time to hand the bytes to TLS and the kernel, not to the client. Over the LAN the write stage grows with the network (6.3).
