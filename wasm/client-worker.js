import createMandelbrotModule from "./mandelbrot.js";

const TASK_HEADER_BYTES = 48;
const RESULT_HEADER_BYTES = 20;
const BENCH_TILE = { x0: -0.75, y0: 0.1, scale: 0.005, w: 64, h: 64, maxIter: 256 };
// This is the SHA-256 of the fixed benchmark tile from the shipped WASM build.
// If the C renderer or its color rule changes, update this value and docs/protocol.md.
const BENCH_HASH_FALLBACK = "d6fa975fbe938c066f3baedf5b850e2418b6ec83e3ae563d0daa2d3170b156fc";
const THREADS = 1;

let moduleInstance;
let outputPointer;
let socket;
let socketURL;
let device;
let heartbeatTimer;
let reconnectTimer;
let registered = false;
let stopped = false;
let currentJobId = 0;
let waitingForTask = false;
let pendingResult;
let benchmark;

self.addEventListener("message", (event) => {
  if (event.data?.type === "start") void initialize(event.data);
  if (event.data?.type === "stop") stop();
});

async function initialize(config) {
  try {
    socketURL = config.socketURL;
    device = config;
    moduleInstance = await createMandelbrotModule();
    outputPointer = moduleInstance._malloc(BENCH_TILE.w * BENCH_TILE.h * 4);
    if (!outputPointer) throw new Error("WASM memory allocation failed");
    benchmark = await measureBenchmark();
    connect();
  } catch (error) {
    report(`WASM startup failed: ${error.message ?? error}`);
  }
}

async function measureBenchmark() {
  const byteLength = BENCH_TILE.w * BENCH_TILE.h * 4;
  const started = performance.now();
  let pixels;
  const repeats = 4;
  for (let i = 0; i < repeats; i += 1) {
    moduleInstance._process_tile(
      outputPointer,
      BENCH_TILE.x0,
      BENCH_TILE.y0,
      BENCH_TILE.scale,
      BENCH_TILE.w,
      BENCH_TILE.h,
      BENCH_TILE.maxIter,
    );
    pixels = moduleInstance.HEAPU8.slice(outputPointer, outputPointer + byteLength);
  }
  const elapsedSeconds = Math.max((performance.now() - started) / 1000, 0.001);
  let hash = BENCH_HASH_FALLBACK;
  if (globalThis.crypto?.subtle) {
    const digest = await crypto.subtle.digest("SHA-256", pixels);
    hash = [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
    if (hash !== BENCH_HASH_FALLBACK) throw new Error("WASM benchmark output does not match the documented kernel hash");
  }
  const perThread = repeats / elapsedSeconds;
  return { hash, perThread, tilesPerSec: perThread * THREADS };
}

function connect() {
  if (stopped) return;
  report("Connecting to Go coordinator…");
  socket = new WebSocket(socketURL);
  socket.binaryType = "arraybuffer";
  socket.addEventListener("open", () => {
    socket.send(JSON.stringify({
      type: "HELLO",
      deviceId: device.deviceId,
      userAgent: device.userAgent,
      cores: device.cores,
      threads: THREADS,
      mock: false,
    }));
  });
  socket.addEventListener("message", (event) => {
    try {
      if (typeof event.data === "string") void handleControl(JSON.parse(event.data));
      else handleTask(event.data);
    } catch (error) {
      report(`Worker protocol error: ${error.message ?? error}`);
      socket.close();
    }
  });
  socket.addEventListener("close", () => disconnected());
  socket.addEventListener("error", () => socket.close());
}

function handleControl(message) {
  switch (message.type) {
    case "WELCOME":
      registered = false;
      clearInterval(heartbeatTimer);
      heartbeatTimer = setInterval(() => sendControl({ type: "HEARTBEAT" }), message.heartbeatIntervalMs);
      sendControl({ type: "BENCH_RESULT", ...benchmark, threads: THREADS });
      registered = true;
      report(`Connected and benchmarked at ${benchmark.perThread.toFixed(1)} tiles/sec`);
      break;
    case "JOB_STARTED":
      currentJobId = message.jobId;
      pendingResult = undefined;
      waitingForTask = false;
      self.postMessage({ type: "jobStarted", jobId: currentJobId });
      requestTask();
      break;
    case "ACK":
      if (!pendingResult || message.jobId !== pendingResult.jobId || message.taskId !== pendingResult.taskId || message.attempt !== pendingResult.attempt) return;
      self.postMessage({
        type: "tileAccepted",
        jobId: pendingResult.jobId,
        taskId: pendingResult.taskId,
        x: pendingResult.x,
        y: pendingResult.y,
        w: pendingResult.w,
        h: pendingResult.h,
        rgba: pendingResult.pixels.buffer,
      }, [pendingResult.pixels.buffer]);
      pendingResult = undefined;
      requestTask();
      break;
    case "JOB_DONE":
      if (message.jobId === currentJobId) {
        waitingForTask = false;
        self.postMessage({ type: "jobDone", jobId: message.jobId });
      }
      break;
  }
}

function handleTask(buffer) {
  if (!(buffer instanceof ArrayBuffer) || buffer.byteLength !== TASK_HEADER_BYTES) throw new Error("Invalid TASK frame size");
  const view = new DataView(buffer);
  if (view.getUint8(0) !== 0x01) throw new Error("Unexpected binary frame type");

  const task = {
    attempt: view.getUint16(2, true),
    jobId: view.getUint32(4, true),
    taskId: view.getUint32(8, true),
    x: view.getUint16(12, true),
    y: view.getUint16(14, true),
    w: view.getUint16(16, true),
    h: view.getUint16(18, true),
    x0: view.getFloat64(20, true),
    y0: view.getFloat64(28, true),
    scale: view.getFloat64(36, true),
    maxIter: view.getUint32(44, true),
  };
  if (!task.w || !task.h || task.w > 256 || task.h > 256 || !task.attempt) throw new Error("Invalid TASK fields");

  waitingForTask = false;
  const byteLength = task.w * task.h * 4;
  const started = performance.now();
  moduleInstance._process_tile(outputPointer, task.x0, task.y0, task.scale, task.w, task.h, task.maxIter);
  const pixels = moduleInstance.HEAPU8.slice(outputPointer, outputPointer + byteLength);
  const result = new ArrayBuffer(RESULT_HEADER_BYTES + byteLength);
  const resultView = new DataView(result);
  resultView.setUint8(0, 0x02);
  resultView.setUint8(1, 0);
  resultView.setUint16(2, task.attempt, true);
  resultView.setUint32(4, task.jobId, true);
  resultView.setUint32(8, task.taskId, true);
  resultView.setUint16(12, task.w, true);
  resultView.setUint16(14, task.h, true);
  resultView.setFloat32(16, performance.now() - started, true);
  new Uint8Array(result, RESULT_HEADER_BYTES).set(pixels);

  pendingResult = { ...task, pixels };
  socket.send(result);
}

function requestTask() {
  if (!registered || !currentJobId || waitingForTask || pendingResult || socket?.readyState !== WebSocket.OPEN) return;
  waitingForTask = true;
  sendControl({ type: "REQUEST_TASK" });
}

function sendControl(message) {
  if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(message));
}

function disconnected() {
  registered = false;
  waitingForTask = false;
  pendingResult = undefined;
  currentJobId = 0;
  clearInterval(heartbeatTimer);
  if (stopped) return;
  report("Disconnected; reconnecting…");
  clearTimeout(reconnectTimer);
  reconnectTimer = setTimeout(connect, 1500);
}

function report(text) {
  self.postMessage({ type: "status", text });
}

function stop() {
  stopped = true;
  clearInterval(heartbeatTimer);
  clearTimeout(reconnectTimer);
  if (socket && socket.readyState < WebSocket.CLOSING) socket.close();
  if (moduleInstance && outputPointer) moduleInstance._free(outputPointer);
  self.close();
}
