#include <stdint.h>

/*
 * Render one row-major RGBA Mandelbrot tile.
 *
 * Keep the signature in sync with docs/protocol.md. The caller owns `out` and
 * must allocate exactly w*h*4 bytes. WebSocket framing and memory allocation
 * belong to the browser worker, not this kernel.
 */
#if defined(__EMSCRIPTEN__)
#include <emscripten/emscripten.h>
#define WASM_EXPORT EMSCRIPTEN_KEEPALIVE
#else
#define WASM_EXPORT
#endif

WASM_EXPORT void process_tile(uint8_t *out, double x0, double y0, double scale,
                              int w, int h, int max_iter) {
  if (out == 0 || w <= 0 || h <= 0 || max_iter <= 0 || scale <= 0.0) {
    return;
  }

  for (int py = 0; py < h; ++py) {
    const double cy = y0 + (double)py * scale;
    for (int px = 0; px < w; ++px) {
      const double cx = x0 + (double)px * scale;
      double zx = 0.0;
      double zy = 0.0;
      int iter = 0;

      while (iter < max_iter && zx * zx + zy * zy <= 4.0) {
        const double next_zx = zx * zx - zy * zy + cx;
        zy = 2.0 * zx * zy + cy;
        zx = next_zx;
        ++iter;
      }

      const uint32_t offset = ((uint32_t)py * (uint32_t)w + (uint32_t)px) * 4U;
      if (iter == max_iter) {
        /* Points that did not escape are black. */
        out[offset] = 0;
        out[offset + 1U] = 0;
        out[offset + 2U] = 0;
      } else {
        /* A bright, deterministic integer palette based on escape iteration. */
        const uint32_t shade = 32U + (uint32_t)(
            ((uint64_t)(uint32_t)iter * 223U) / (uint32_t)max_iter);
        out[offset] = (uint8_t)shade;
        out[offset + 1U] = (uint8_t)((shade * 3U) & 0xffU);
        out[offset + 2U] = (uint8_t)(255U - shade / 2U);
      }
      out[offset + 3U] = 255;
    }
  }
}
