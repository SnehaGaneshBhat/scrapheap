# Binary and JSON protocol

The worker WebSocket endpoint is `/ws/worker`; the dashboard endpoint is
`/ws/dash`. WebSocket text frames contain UTF-8 JSON control messages. WebSocket
binary frames contain TASK or RESULT. All multi-byte integers are unsigned and
little-endian. Float fields are IEEE-754 binary64, with their eight bytes in
little-endian order. There is no implicit padding or alignment. Binary offsets
are zero-based byte positions from the beginning of the frame.

## JSON control messages

JSON property names and `type` values are case-sensitive. A message may contain
only the listed properties. Every listed property is required, including fields
whose value may be zero or an empty string.

| Type | Direction | Required fields and types | Example |
| --- | --- | --- | --- |
| `HELLO` | worker to server | `deviceId`: non-empty string; `userAgent`: string; `cores`: uint32 > 0; `threads`: int > 0, number of Web Workers | `{"type":"HELLO","deviceId":"phone-1","userAgent":"Example Browser","cores":4,"threads":2}` |
| `BENCH_RESULT` | worker to server | `tilesPerSec`: finite number > 0; `hash`: 64-character hexadecimal SHA-256 digest of benchmark tile RGBA bytes | `{"type":"BENCH_RESULT","tilesPerSec":12.5,"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}` |
| `REQUEST_TASK` | worker to server | none | `{"type":"REQUEST_TASK"}` |
| `HEARTBEAT` | worker to server | none | `{"type":"HEARTBEAT"}` |
| `ACK` | worker to server | `taskId`: uint32; `attempt`: uint32 > 0; `jobId`: uint32 | `{"type":"ACK","taskId":7,"attempt":1,"jobId":42}` |
| `JOB_DONE` | worker to server | `jobId`: uint32 | `{"type":"JOB_DONE","jobId":42}` |
| `WELCOME` | server to worker | `heartbeatIntervalMs`: uint32 > 0; `heartbeatTimeoutMs`: uint32 > 0 | `{"type":"WELCOME","heartbeatIntervalMs":1000,"heartbeatTimeoutMs":5000}` |
| `JOB_STARTED` | server to all connected workers | `jobId`: uint32 | `{"type":"JOB_STARTED","jobId":42}` |

The server sends WELCOME immediately after accepting HELLO. It sends JOB_STARTED
to every connected worker whenever a job starts or resets. The server increments
`jobId` for each job start/reset and drops a RESULT whose `jobId` differs from the
currently active job ID.

On JOB_DONE or reset, the server drops all held REQUEST_TASKs. Workers send a new
REQUEST_TASK after JOB_STARTED, or after WELCOME when a job is already running.
A worker may use a 1-second timer after JOB_DONE as a fallback before requesting
again.

Unsupported JSON `type` values are rejected with `UnknownMessageTypeError` by the
Go protocol decoder. Unknown JSON properties and malformed required values are
also rejected.

## Binary frame common rules

Byte 0 is the frame type: `0x01` for TASK and `0x02` for RESULT. Unknown frame
types, truncated frames, trailing bytes, zero dimensions, dimensions greater
than `MaxTileDim` (256), zero attempts, and invalid render parameters are
rejected. The RGBA byte count must equal exactly `w*h*4`. Dimensions are checked
before using the byte count. No input pixels, halo, or edge padding are present.

## TASK (49-byte frame, no payload)

| Offset | Size (bytes) | Field | Type / encoding |
| ---: | ---: | --- | --- |
| 0 | 1 | type | uint8, value `0x01` |
| 1 | 4 | jobId | uint32, little-endian |
| 5 | 4 | taskId | uint32, little-endian |
| 9 | 4 | attempt | uint32, little-endian, > 0 |
| 13 | 2 | x | uint16, little-endian |
| 15 | 2 | y | uint16, little-endian |
| 17 | 2 | w | uint16, little-endian, 1–256 |
| 19 | 2 | h | uint16, little-endian, 1–256 |
| 21 | 8 | x0 | IEEE-754 binary64, little-endian, finite |
| 29 | 8 | y0 | IEEE-754 binary64, little-endian, finite |
| 37 | 8 | scale | IEEE-754 binary64, little-endian, finite and > 0 |
| 45 | 4 | maxIter | uint32, little-endian, > 0 |

Example TASK (jobId 42, taskId `0x01020304`, attempt 2, x 10, y 20, w 2,
h 1, x0 -2, y0 0.5, scale 1, maxIter 256):

```text
01 2a 00 00 00 04 03 02 01 02 00 00 00 0a 00 14 00
02 00 01 00 00 00 00 00 00 00 00 c0 00 00 00 00
00 00 e0 3f 00 00 00 00 00 00 f0 3f 00 01 00 00
```

## RESULT (21-byte header followed by RGBA payload)

| Offset | Size (bytes) | Field | Type / encoding |
| ---: | ---: | --- | --- |
| 0 | 1 | type | uint8, value `0x02` |
| 1 | 4 | jobId | uint32, little-endian |
| 5 | 4 | taskId | uint32, little-endian |
| 9 | 4 | attempt | uint32, little-endian, > 0 |
| 13 | 2 | w | uint16, little-endian, 1–256 |
| 15 | 2 | h | uint16, little-endian, 1–256 |
| 17 | 4 | computeMs | uint32, little-endian milliseconds |
| 21 | `w*h*4` | rgba | bytes, row-major, each pixel ordered R, G, B, A |

The payload length must equal exactly `w*h*4`; neither missing nor trailing pixel
bytes are permitted.

Example RESULT (jobId 42, taskId `0x01020304`, attempt 2, w 2, h 1,
computeMs 300, pixels `00 01 02 ff` and `10 11 12 ff`):

```text
02 2a 00 00 00 04 03 02 01 02 00 00 00 02 00 01 00 2c 01 00 00
00 01 02 ff 10 11 12 ff
```

## Kernel and benchmark contract

The kernel receives render parameters only. There are no input pixels, halo, or
edge padding. Its contract is:

```text
render_tile(x0, y0, scale, w, h, maxIter) -> Uint8Array of w*h*4 RGBA bytes
```

`proto.BenchTile()` returns the fixed benchmark parameters used for both the
speed measurement and output hash:

| Parameter | Exact value |
| --- | ---: |
| x0 | -0.75 |
| y0 | 0.1 |
| scale | 0.005 |
| w | 64 |
| h | 64 |
| maxIter | 256 |

This tile covers a Mandelbrot boundary-detail region. BENCH_RESULT.hash is the
lowercase or uppercase hexadecimal SHA-256 digest of the exact 16,384 RGBA bytes
returned for these parameters (64*64*4), with no metadata included in the hash.
