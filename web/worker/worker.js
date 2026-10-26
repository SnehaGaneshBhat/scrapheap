"use strict";

const form = document.querySelector("#join-form");
const nameInput = document.querySelector("#worker-name");
const threadInput = document.querySelector("#thread-count");
const joinButton = document.querySelector("#join-button");
const statusLine = document.querySelector("#status");
const visibilityLine = document.querySelector("#visibility");
const events = document.querySelector("#events");

const hardwareThreads = Math.max(1, Number(navigator.hardwareConcurrency) || 1);
const nameKey = "scrapheap.worker-name";
let workerName = localStorage.getItem(nameKey);
if (!workerName) {
  workerName = `laptop-${Math.random().toString(36).slice(2, 8)}`;
  localStorage.setItem(nameKey, workerName);
}
nameInput.value = workerName;
threadInput.value = Math.max(1, hardwareThreads - 1);
threadInput.max = hardwareThreads;

let workerID = "";
let heartbeatMs = 10000;
let workers = [];
let heartbeatTimer = 0;
let joining = false;
const running = new Map();

function note(message) {
  const item = document.createElement("li");
  item.textContent = `${new Date().toLocaleTimeString()} ${message}`;
  events.prepend(item);
  while (events.children.length > 12) events.lastElementChild.remove();
}

function setStatus(message) {
  statusLine.textContent = message;
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

async function register(threads = workers.length || Number(threadInput.value)) {
  const reply = await postJSON("/w/hello", {
    name: workerName,
    cores: hardwareThreads,
    threads,
    browser: navigator.userAgent,
  });
  workerID = reply.workerId;
  heartbeatMs = Math.max(1000, Number(reply.heartbeatSec) * 1000);
  for (const worker of workers) worker.postMessage({ type: "worker-id", workerID });
  setStatus(`Connected as ${workerName} (${reply.jobKind}); ${workers.length || Number(threadInput.value)} thread(s).`);
  return reply;
}

async function sendHeartbeat() {
  if (!workerID) return;
  try {
    const reply = await postJSON("/w/heartbeat", { workerId: workerID, running: [...running.values()] });
    if (reply.unknownWorker) {
      note("Coordinator restarted; registering again.");
      await register();
    }
  } catch (error) {
    setStatus(`Coordinator connection problem: ${error.message}`);
  }
}

function stopWorkers() {
  clearInterval(heartbeatTimer);
  heartbeatTimer = 0;
  for (const worker of workers) worker.terminate();
  workers = [];
  running.clear();
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (joining || workers.length) return;
  joining = true;
  joinButton.disabled = true;
  workerName = nameInput.value.trim();
  if (!workerName) {
    setStatus("Enter a worker name.");
    joining = false;
    joinButton.disabled = false;
    return;
  }
  localStorage.setItem(nameKey, workerName);
  const threadCount = Number(threadInput.value);
  if (!Number.isInteger(threadCount) || threadCount < 1 || threadCount > hardwareThreads) {
    setStatus(`Choose between 1 and ${hardwareThreads} threads.`);
    joining = false;
    joinButton.disabled = false;
    return;
  }
  try {
    let hello = await register(threadCount);
    let activeThreadCount = threadCount;
    let mnistFiles = [];
    if (hello.jobKind === "mnist") {
      const available = new Set((hello.dataset || []).map((item) => item.file));
      const candidates = [
        ["train-images-idx3-ubyte.gz", "train-images.idx3-ubyte"],
        ["train-labels-idx1-ubyte.gz", "train-labels.idx1-ubyte"],
      ];
      mnistFiles = candidates.map((options) => options.find((file) => available.has(file)));
      if (mnistFiles.some((file) => !file)) {
        throw new Error("Coordinator needs train-images and train-labels in raw IDX or gzip IDX format.");
      }
      if (activeThreadCount > 1) {
        setStatus(`Starting ${activeThreadCount} trainer threads. Each uses ~100 MB RAM. Preparing dataset...`);
      }
    }
    heartbeatTimer = setInterval(sendHeartbeat, heartbeatMs / 2);
    const workerDataset = hello.jobKind === "mnist"
      ? (hello.dataset || []).filter((item) => item.file.startsWith("train-"))
      : (hello.dataset || []);
    await ScrapHeapDataset.prepareManifest(workerDataset, setStatus);
    note(hello.rejoined ? "Rejoined the coordinator." : "Joined the coordinator.");
    for (let index = 0; index < activeThreadCount; index++) {
      const worker = new Worker("/worker/thread.js");
      worker.onmessage = ({ data }) => {
        if (data.type === "task-started") {
          running.set(index, data.taskId);
          note(`Thread ${index + 1} started ${data.taskId}.`);
        } else if (data.type === "task-finished") {
          running.delete(index);
          note(`Thread ${index + 1} finished ${data.taskId} (${data.reason}).`);
        } else if (data.type === "task-abandoned") {
          running.delete(index);
          note(`Thread ${index + 1} abandoned ${data.taskId}: ${data.reason}`);
        } else if (data.type === "status") {
          setStatus(data.message);
        } else if (data.type === "fatal") {
          note(data.message);
          setStatus(data.message);
        }
      };
      worker.onerror = (error) => {
        note(`Thread ${index + 1} error: ${error.message}`);
      };
      workers.push(worker);
      worker.postMessage({ type: "start", workerID, mnistFiles });
    }
    joinButton.textContent = "Leave job";
    joinButton.disabled = false;
    joinButton.onclick = (click) => {
      click.preventDefault();
      stopWorkers();
      joinButton.onclick = null;
      joinButton.textContent = "Join job";
      setStatus("Left. The coordinator will recover running tasks after their leases expire.");
      note("Stopped worker threads.");
    };
  } catch (error) {
    stopWorkers();
    setStatus(`Could not join: ${error.message}`);
    joinButton.disabled = false;
  } finally {
    joining = false;
  }
});

document.addEventListener("visibilitychange", () => {
  visibilityLine.textContent = document.hidden
    ? "This tab is hidden. Bring it to the front to avoid browser throttling."
    : "This tab is visible.";
});
visibilityLine.textContent = document.hidden ? "This tab is hidden." : "This tab is visible.";
