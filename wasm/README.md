# Mandelbrot WASM kernel

`mandelbrot.c` implements the rendering function from `docs/protocol.md`:

```c
void process_tile(uint8_t *out, double x0, double y0, double scale,
                  int w, int h, int max_iter);
```

The caller allocates `w * h * 4` bytes. The kernel writes pixels in row-major
RGBA order and sets alpha to 255. It computes each complex coordinate directly
as `x0 + px * scale` and `y0 + py * scale`; it does not increment coordinates
through a loop. Mandelbrot points that do not escape by `max_iter` are black.
Escaping points use a deterministic integer palette based on their escape
iteration.

Compile from the repository root with Emscripten installed:

```powershell
emcc wasm/mandelbrot.c -O2 -s MODULARIZE=1 -s EXPORT_ES6=1 -s ENVIRONMENT=web,worker -s ALLOW_MEMORY_GROWTH=1 -s EXPORTED_FUNCTIONS='["_process_tile","_malloc","_free"]' -o wasm/mandelbrot.js
```

This produces `wasm/mandelbrot.js` and `wasm/mandelbrot.wasm`. The generated
JavaScript module loads the `.wasm` file, which contains the compiled C kernel.
The worker must allocate an output buffer, call `_process_tile`, copy `w*h*4`
bytes from WASM memory, then encode the protocol RESULT frame.

To connect the browser worker to the Go coordinator, start the server with the
`wasm` directory as its static root:

```powershell
go run ./cmd/server -web wasm
```

Open `http://localhost:8080/worker` on the coordinator or on a device that can
reach it over the LAN. The page opens `/ws/worker`, benchmarks the WASM kernel,
requests one tile at a time, and returns binary RESULT frames. The server sends
ACK after accepting a result. The page draws tiles accepted from that browser;
it does not yet assemble other devices' tiles. The Download PNG button saves
this browser's 2048x2048 canvas as a PNG.

The demo currently uses one Web Worker thread per browser page. Browsers on the
same LAN should use the coordinator's printed Worker URL so the page and
WebSocket use the same host. For HTTPS hosting, the client selects `wss`; for
the local HTTP demo it uses `ws`.

The system does not have Emscripten installed globally. For this task, an
Emscripten 3.1.6 toolchain was unpacked under `/tmp`; its build completed and
the resulting module was run. On another machine, install Emscripten and run
the command above to rebuild the artifacts.
