// web/sha256.js
(function (root) {
  const K = [];
  const H0 = [];
  (function init() {
    let n = 2, count = 0;
    while (count < 64) {
      let prime = true;
      for (let d = 2; d * d <= n; d++) { if (n % d === 0) { prime = false; break; } }
      if (prime) {
        if (count < 8) H0[count] = (Math.pow(n, 1 / 2) % 1 * 4294967296) | 0;
        K[count] = (Math.pow(n, 1 / 3) % 1 * 4294967296) | 0;
        count++;
      }
      n++;
    }
  })();

  function rotr(x, n) { return (x >>> n) | (x << (32 - n)); }

  function sha256hex(data) {
    const len = data.length;
    const padLen = Math.ceil((len + 9) / 64) * 64;
    const buf = new Uint8Array(padLen);
    buf.set(data);
    buf[len] = 0x80;
    const dv = new DataView(buf.buffer);
    dv.setUint32(padLen - 8, Math.floor(len / 536870912), false);
    dv.setUint32(padLen - 4, (len << 3) >>> 0, false);
    const H = H0.slice();
    const W = new Int32Array(64);
    for (let off = 0; off < padLen; off += 64) {
      for (let i = 0; i < 16; i++) W[i] = dv.getInt32(off + i * 4, false);
      for (let i = 16; i < 64; i++) {
        const a = W[i - 15], b = W[i - 2];
        const s0 = rotr(a, 7) ^ rotr(a, 18) ^ (a >>> 3);
        const s1 = rotr(b, 17) ^ rotr(b, 19) ^ (b >>> 10);
        W[i] = (W[i - 16] + s0 + W[i - 7] + s1) | 0;
      }
      let a = H[0], b = H[1], c = H[2], d = H[3], e = H[4], f = H[5], g = H[6], h = H[7];
      for (let i = 0; i < 64; i++) {
        const S1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
        const ch = (e & f) ^ (~e & g);
        const t1 = (h + S1 + ch + K[i] + W[i]) | 0;
        const S0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
        const maj = (a & b) ^ (a & c) ^ (b & c);
        const t2 = (S0 + maj) | 0;
        h = g; g = f; f = e; e = (d + t1) | 0;
        d = c; c = b; b = a; a = (t1 + t2) | 0;
      }
      H[0] = (H[0] + a) | 0; H[1] = (H[1] + b) | 0; H[2] = (H[2] + c) | 0; H[3] = (H[3] + d) | 0;
      H[4] = (H[4] + e) | 0; H[5] = (H[5] + f) | 0; H[6] = (H[6] + g) | 0; H[7] = (H[7] + h) | 0;
    }
    return H.map((x) => (x >>> 0).toString(16).padStart(8, '0')).join('');
  }

  root.sha256hex = sha256hex;
})(globalThis);