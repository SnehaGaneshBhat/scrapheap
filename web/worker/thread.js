"use strict";

let workerID = "";
let stopped = false;

self.onmessage = ({ data }) => {
  if (data.type === "worker-id") {
    workerID = data.workerID;
  } else if (data.type === "start") {
    workerID = data.workerID;
    runLoop();
  } else if (data.type === "stop") {
    stopped = true;
  }
};

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function postJSON(path, body) {
  const response = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const content = await response.json();
  if (!response.ok) throw new Error(content.error || `${path}: HTTP ${response.status}`);
  return content;
}

function runBurn(payload) {
  const iters = payload && payload.iters;
  if (!Number.isSafeInteger(iters) || iters < 0) throw new Error("burn payload needs a non-negative safe integer iters");
  const mask = (1n << 64n) - 1n;
  const prime = 1099511628211n;
  let checksum = 14695981039346656037n;
  for (let i = 0; i < iters; i++) {
    checksum = ((checksum ^ BigInt(i)) * prime) & mask;
  }
  return { checksum: checksum.toString() };
}

async function runLoop() {
  while (!stopped) {
    try {
      const assignment = await postJSON("/w/pull", { workerId: workerID });
      if (assignment.status === "done") {
        self.postMessage({ type: "status", message: "The job is complete." });
        return;
      }
      if (assignment.status === "wait") {
        await delay(Math.max(250, assignment.retryAfterMs || 1000));
        continue;
      }
      if (assignment.status !== "task") throw new Error(`unknown pull status: ${assignment.status}`);

      self.postMessage({ type: "task-started", taskId: assignment.taskId });
      let result;
      const startedAt = performance.now();
      try {
        if (assignment.kind === "burn") {
          result = runBurn(assignment.payload);
        } else if (assignment.kind === "mnist") {
          throw new Error("MNIST executor is unavailable until the trainer WASM ABI and dataset loader are defined.");
        } else {
          throw new Error(`Unsupported built-in task kind: ${assignment.kind}`);
        }
      } catch (error) {
        self.postMessage({ type: "task-abandoned", taskId: assignment.taskId, reason: error.message });
        self.postMessage({ type: "fatal", message: `${assignment.taskId}: ${error.message}` });
        return;
      }

      const reply = await postJSON("/w/result", {
        workerId: workerID,
        taskId: assignment.taskId,
        attempt: assignment.attempt,
        result,
        seconds: (performance.now() - startedAt) / 1000,
      });
      self.postMessage({ type: "task-finished", taskId: assignment.taskId, reason: reply.reason });
      if (reply.reason === "unknown") {
        self.postMessage({ type: "fatal", message: "Coordinator does not recognize this worker; reconnect the page." });
        return;
      }
    } catch (error) {
      self.postMessage({ type: "status", message: `Worker connection problem: ${error.message}` });
      await delay(1000);
    }
  }
}
