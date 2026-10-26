import createMandelbrotModule from "./mandelbrot.js";

const canvas = document.querySelector("#image");
const status = document.querySelector("#status");
const download = document.querySelector("#download");
const canvasSize = 2048;
const tileSize = 64;
const tileBytes = tileSize * tileSize * 4;
const tileCount = canvasSize / tileSize;
const x0 = -2.5;
const y0 = -1.75;
const scale = 3.5 / canvasSize;
const maxIter = 1000;

try {
  const module = await createMandelbrotModule();
  const output = module._malloc(tileBytes);
  if (!output) throw new Error("WASM memory allocation failed");

  const started = performance.now();
  const pixels = new Uint8Array(canvasSize * canvasSize * 4);
  const context = canvas.getContext("2d");
  let completed = 0;
  try {
    for (let tileY = 0; tileY < tileCount; tileY += 1) {
      for (let tileX = 0; tileX < tileCount; tileX += 1) {
        const pixelX = tileX * tileSize;
        const pixelY = tileY * tileSize;
        const tileX0 = x0 + pixelX * scale;
        const tileY0 = y0 + pixelY * scale;
        module._process_tile(output, tileX0, tileY0, scale, tileSize, tileSize, maxIter);
        const tilePixels = module.HEAPU8.slice(output, output + tileBytes);

        for (let row = 0; row < tileSize; row += 1) {
          const sourceOffset = row * tileSize * 4;
          const targetOffset = ((pixelY + row) * canvasSize + pixelX) * 4;
          pixels.set(tilePixels.subarray(sourceOffset, sourceOffset + tileSize * 4), targetOffset);
        }

        context.putImageData(new ImageData(new Uint8ClampedArray(tilePixels), tileSize, tileSize), pixelX, pixelY);
        completed += 1;
        if (completed % 16 === 0) {
          status.textContent = `Rendered ${completed} / ${tileCount * tileCount} tiles…`;
          await new Promise(requestAnimationFrame);
        }
      }
    }

    const digest = await crypto.subtle.digest("SHA-256", pixels);
    const hash = [...new Uint8Array(digest)]
      .map((byte) => byte.toString(16).padStart(2, "0"))
      .join("");
    status.textContent = `Rendered ${canvasSize}×${canvasSize} image from ${completed} tiles in ${(performance.now() - started).toFixed(0)} ms\nSHA-256: ${hash}`;
    download.disabled = false;
    download.addEventListener("click", () => {
      canvas.toBlob((blob) => {
        if (!blob) return;
        const url = URL.createObjectURL(blob);
        const link = document.createElement("a");
        link.href = url;
        link.download = "mandelbrot-2048x2048.png";
        link.click();
        URL.revokeObjectURL(url);
      }, "image/png");
    });
  } finally {
    module._free(output);
  }
} catch (error) {
  status.textContent = `WASM demo failed: ${error.stack ?? error}`;
  throw error;
}
