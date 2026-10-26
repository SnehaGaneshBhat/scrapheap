require('../web/protocol.js');
const P = globalThis.SH_PROTO;
const buf = new ArrayBuffer(48);
const dv = new DataView(buf);
dv.setUint8(0, 1); dv.setUint16(2, 3, true);
dv.setUint32(4, 9, true);    // jobId
dv.setUint32(8, 77, true);   // taskId
dv.setUint16(12, 128, true); dv.setUint16(14, 64, true);
dv.setUint16(16, 64, true);  dv.setUint16(18, 64, true);
dv.setFloat64(20, -0.7436438870371587, true);
dv.setFloat64(28, 0.13182590420531197, true);
dv.setFloat64(36, 4.8828125e-8, true);
dv.setUint32(44, 5000, true);
const t = P.decodeTask(buf);
console.log(t);
const r = P.newResult(t);
P.setComputeMs(r, 123.5);
const rv = new DataView(r);
console.log(r.byteLength, rv.getUint32(4, true), rv.getUint32(8, true), rv.getFloat32(16, true));