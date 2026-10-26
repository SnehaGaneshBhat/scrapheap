// web/protocol.js
(function (root) {
  const TASK_HEADER = 48;
  const RESULT_HEADER = 20;
  const TYPE = { TASK: 1, RESULT: 2 };

  function decodeTask(buf) {
    const dv = new DataView(buf);
    return {
      type: dv.getUint8(0),
      attempt: dv.getUint16(2, true),
      jobId: dv.getUint32(4, true),
      taskId: dv.getUint32(8, true),
      x: dv.getUint16(12, true),
      y: dv.getUint16(14, true),
      w: dv.getUint16(16, true),
      h: dv.getUint16(18, true),
      x0: dv.getFloat64(20, true),
      y0: dv.getFloat64(28, true),
      scale: dv.getFloat64(36, true),
      maxIter: dv.getUint32(44, true),
    };
  }

  // Just the job id of a TASK frame (cheap, used on the main thread).
  function taskJobId(buf) {
    return new DataView(buf).getUint32(4, true);
  }

  // Allocates a RESULT frame with the header filled in. Pixels go after byte 20.
  function newResult(t) {
    const buf = new ArrayBuffer(RESULT_HEADER + t.w * t.h * 4);
    const dv = new DataView(buf);
    dv.setUint8(0, TYPE.RESULT);
    dv.setUint8(1, 0);
    dv.setUint16(2, t.attempt, true);
    dv.setUint32(4, t.jobId, true);
    dv.setUint32(8, t.taskId, true);
    dv.setUint16(12, t.w, true);
    dv.setUint16(14, t.h, true);
    return buf;
  }

  function setComputeMs(buf, ms) {
    new DataView(buf).setFloat32(16, ms, true);
  }

  root.SH_PROTO = { TASK_HEADER, RESULT_HEADER, TYPE, decodeTask, taskJobId, newResult, setComputeMs };
})(globalThis);