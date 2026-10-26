require('../web/sha256.js');
const crypto = require('crypto');
const ref = (u8) => crypto.createHash('sha256').update(u8).digest('hex');
const cases = [new Uint8Array(0), new TextEncoder().encode('abc'), new Uint8Array(55), new Uint8Array(56), new Uint8Array(64), new Uint8Array(16384).map((_, i) => (i * 31 + 7) & 255)];
let ok = true;
for (const c of cases) {
  const a = globalThis.sha256hex(c), b = ref(c);
  if (a !== b) { ok = false; console.log('MISMATCH len', c.length, a, b); }
}
console.log(ok ? 'sha256 OK' : 'sha256 FAILED');