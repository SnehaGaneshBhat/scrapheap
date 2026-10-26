const canvas = document.querySelector("#image");
const status = document.querySelector("#status");
const download = document.querySelector("#download");
const canvasSize = 2048;
const context = canvas.getContext("2d");
let acceptedTiles = 0;
let activeJobId = 0;

const socketURL = new URL("/ws/worker", location.href);
socketURL.protocol = location.protocol === "https:" ? "wss:" : "ws:";
const worker = new Worker(new URL("./client-worker.js", import.meta.url), { type: "module" });
worker.postMessage({
  type: "start",
  socketURL: socketURL.href,
  deviceId: makeDeviceId(),
  userAgent: navigator.userAgent,
  cores: Math.max(1, navigator.hardwareConcurrency || 1),
});

worker.addEventListener("message", (event) => {
  const message = event.data;
  if (message.type === "status") {
    status.textContent = message.text;
    return;
  }
  if (message.type === "jobStarted") {
    activeJobId = message.jobId;
    acceptedTiles = 0;
    context.clearRect(0, 0, canvas.width, canvas.height);
    download.disabled = true;
    status.textContent = `Connected. Job ${activeJobId} started; waiting for tiles…`;
    return;
  }
  if (message.type === "jobDone") {
    status.textContent = `Job ${message.jobId} complete. This browser accepted ${acceptedTiles} tiles.`;
    return;
  }
  if (message.type === "tileAccepted") {
    if (message.jobId !== activeJobId) return;
    const tilePixels = new Uint8ClampedArray(message.rgba);
    context.putImageData(new ImageData(tilePixels, message.w, message.h), message.x, message.y);
    acceptedTiles += 1;
    download.disabled = false;
    status.textContent = `Connected · job ${activeJobId} · this browser accepted ${acceptedTiles} tiles (${message.w}×${message.h} each)`;
  }
});

download.addEventListener("click", () => {
  canvas.toBlob((blob) => {
    if (!blob) return;
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `mandelbrot-job-${activeJobId || "current"}-2048x2048.png`;
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }, "image/png");
});

window.addEventListener("beforeunload", () => worker.terminate());

function makeDeviceId() {
  if (crypto.getRandomValues) {
    const bytes = new Uint8Array(16);
    crypto.getRandomValues(bytes);
    return `browser-${[...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("")}`;
  }
  return `browser-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}
