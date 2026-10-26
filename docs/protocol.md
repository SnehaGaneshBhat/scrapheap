# Binary and JSON protocol

The WebSocket control messages use UTF-8 JSON text frames. The task/result data
messages use WebSocket binary frames. Integers and IEEE-754 float64 values in
binary frames are little-endian. There is no implicit padding or alignment.

## JSON control messages

Every message has a string `type` property. Property names are case-sensitive.
Only the listed properties are valid for each message; all listed properties
must be present, even when their value is zero or the empty string.

| Type | Required properties | JSON example |
| --- | --- | --- |
| `HELLO` | `deviceId` string (non-empty), `userAgent` string, `cores` integer (> 0) | `{"type":"HELLO","deviceId":"phone-1","userAgent":"Example Browser","cores":4}` |
| `BENCH_RESULT` | `tilesPerSec` finite number (> 0) | `{"type":"BENCH_RESULT","tilesPerSec":12.5}` |
| `REQUEST_TASK` | none | `{"type":"REQUEST_TASK"}` |
| `HEARTBEAT` | none | `{"type":"HEARTBEAT"}` |
| `ACK` | `taskId` uint32, `attempt` uint32 (> 0) | `{"type":"ACK","taskId":7,"attempt":1}` |
| `JOB_DONE` | none | `{"type":"JOB_DONE"}` |

## Binary frame common rules

Byte 0 is the frame type: `0x01` for TASK and `0x02` for RESULT. Unknown types,
truncated frames, trailing bytes, zero dimensions, zero attempts, and invalid
render parameters are rejected. WebSocket message boundaries delimit frames.

## TASK (45-byte frame, no payload)

| Offset | Size | Field | Encoding |
| ---: | ---: | --- | --- |
| 0 | 1 | type (`0x01`) | unsigned byte |
| 1 | 4 | taskId | uint32 little-endian |
| 5 | 4 | attempt | uint32 little-endian, greater than zero |
| 9 | 2 | x | uint16 little-endian |
| 11 | 2 | y | uint16 little-endian |
| 13 | 2 | w | uint16 little-endian, greater than zero |
| 15 | 2 | h | uint16 little-endian, greater than zero |
| 17 | 8 | x0 | IEEE-754 float64 little-endian, finite |
| 25 | 8 | y0 | IEEE-754 float64 little-endian, finite |
| 33 | 8 | scale | IEEE-754 float64 little-endian, finite and greater than zero |
| 41 | 4 | maxIter | uint32 little-endian, greater than zero |

Example TASK hex dump (taskId `0x01020304`, attempt 2, x 10, y 20, w 2,
h 1, x0 -2, y0 0.5, scale 1, maxIter 256):

```text
01 04 03 02 01 02 00 00 00 0a 00 14 00 02 00 01 00
00 00 00 00 00 00 00 c0 00 00 00 00 00 00 e0 3f
00 00 00 00 00 00 f0 3f 00 01 00 00
```

## RESULT (17-byte header followed by RGBA payload)

| Offset | Size | Field | Encoding |
| ---: | ---: | --- | --- |
| 0 | 1 | type (`0x02`) | unsigned byte |
| 1 | 4 | taskId | uint32 little-endian |
| 5 | 4 | attempt | uint32 little-endian, greater than zero |
| 9 | 2 | w | uint16 little-endian, greater than zero |
| 11 | 2 | h | uint16 little-endian, greater than zero |
| 13 | 4 | computeMs | uint32 little-endian milliseconds |
| 17 | `w*h*4` | rgba | bytes, row-major pixels, each pixel R then G then B then A |

The payload length must equal exactly `w*h*4`; no payload padding is permitted.

Example RESULT hex dump (taskId `0x01020304`, attempt 2, w 2, h 1,
computeMs 300, pixels `00 01 02 ff` and `10 11 12 ff`):

```text
02 04 03 02 01 02 00 00 00 02 00 01 00 2c 01 00 00
00 01 02 ff 10 11 12 ff
```
