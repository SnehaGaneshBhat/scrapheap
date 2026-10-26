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

To view a full-resolution browser render after compiling, serve the repository root
(opening the HTML directly can block the ES module and WASM fetch):

```powershell
python -m http.server 8000
```

Then open `http://localhost:8000/wasm/demo.html`. The page calls the compiled
renderer for all 1,024 64x64 tiles, assembles their pixels into a 2048x2048
image, and prints its runtime and image hash. The coordinator's current entry
point is still a placeholder; this is a local kernel check, not a distributed
worker demo. Once rendering completes, the Download PNG button saves the full
2048x2048 canvas as a PNG file.

The system does not have Emscripten installed globally. For this task, an
Emscripten 3.1.6 toolchain was unpacked under `/tmp`; its build completed and
the resulting module was run. On another machine, install Emscripten and run
the command above to rebuild the artifacts.
