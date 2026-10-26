// web/kernel-worker.js
importScripts('protocol.js', 'sha256.js');
const MOCK = new URL(self.location.href).searchParams.get('mock') === '1';

// Mock kernel: a real, small Mandelbrot in plain JS. Plumbing tests only, never benchmarks.
// Each pixel is computed fresh from x0 + px*scale (never by adding up steps), like the real kernel.
function mockProcessTile(out, x0, y0, scale, w, h, maxIter) {
  for (let py = 0; py < h; py++) {
    const cy = y0 + py * scale;
    for (let px = 0; px < w; px++) {
      const cx = x0 + px * scale;
      let zx = 0, zy = 0, i = 0;
      while (i < maxIter && zx * zx + zy * zy <= 4) {
        const t = zx * zx - zy * zy + cx;
        zy = 2 * zx * zy + cy;
        zx = t;
        i++;
      }
      const o = (py * w + px) * 4;
      const v = i === maxIter ? 0 : 40 + ((i * 7) % 215);
      out[o] = v; out[o + 1] = (v * 3) % 256; out[o + 2] = 255 - v; out[o + 3] = 255;
    }
  }
}

async function loadKernel() {
  if (MOCK) { self.process_tile = mockProcessTile; return; }
  importScripts('wasm_exec.js');
  const go = new Go();
  const resp = await fetch('kernel.wasm');
  const bytes = await resp.arrayBuffer();
  const result = await WebAssembly.instantiate(bytes, go.importObject);
  go.run(result.instance); // runs forever (Go main blocks), so do not await it
  for (let i = 0; i < 200 && typeof self.process_tile !== 'function'; i++) {
    await new Promise((r) => setTimeout(r, 25));
  }
  if (typeof self.process_tile !== 'function') throw new Error('process_tile not registered');
}

const ready = loadKernel()
  .then(() => { postMessage({ t: 'ready' }); return true; })
  .catch((e) => { postMessage({ t: 'error', msg: String(e) }); return false; });

onmessage = async (e) => {
  if (!(await ready)) return;
  const m = e.data;

  if (m.t === 'task') {
    try {
      const t = SH_PROTO.decodeTask(m.buf);
      const res = SH_PROTO.newResult(t);
      const pixels = new Uint8Array(res, SH_PROTO.RESULT_HEADER);
      const t0 = performance.now();
      self.process_tile(pixels, t.x0, t.y0, t.scale, t.w, t.h, t.maxIter);
      const ms = performance.now() - t0; // kernel time only
      SH_PROTO.setComputeMs(res, ms);
      postMessage({ t: 'result', buf: res, ms, sid: m.sid, jobId: t.jobId, taskId: t.taskId }, [res]);
    } catch (err) {
      postMessage({ t: 'taskfail', msg: String(err), sid: m.sid });
    }
  }

  if (m.t === 'bench') {
    const p = m.p;
    const out = new Uint8Array(p.w * p.h * 4);
    self.process_tile(out, p.x0, p.y0, p.scale, p.w, p.h, p.maxIter); // warm-up, not timed
    const t0 = performance.now();
    for (let k = 0; k < m.n; k++) self.process_tile(out, p.x0, p.y0, p.scale, p.w, p.h, p.maxIter);
    const avgMs = (performance.now() - t0) / m.n;
    postMessage({ t: 'bench', avgMs, hash: sha256hex(out), pixels: out.buffer }, [out.buffer]);
  }
};