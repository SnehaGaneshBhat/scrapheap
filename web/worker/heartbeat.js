"use strict";

let workerID = "";
let running = [];
let busy = false;

self.onmessage = ({ data }) => {
  if (data.type === "worker-id") workerID = data.workerID;
  if (data.type === "running") running = Array.isArray(data.running) ? data.running : [];
};

async function heartbeat() {
  if (!workerID || busy) return;
  busy = true;
  try {
    const response = await fetch("/w/heartbeat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ workerId: workerID, running }),
    });
    const reply = await response.json();
    if (!response.ok) throw new Error(reply.error || `HTTP ${response.status}`);
    if (reply.unknownWorker) self.postMessage({ type: "unknown-worker" });
  } catch (error) {
    self.postMessage({ type: "error", message: error.message });
  } finally {
    busy = false;
  }
}

setInterval(heartbeat, 2000);
heartbeat();
