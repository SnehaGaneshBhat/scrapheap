"use strict";

const $ = (selector) => document.querySelector(selector);
const startButton = $("#start-job");
const errorLine = $("#error");
const workerLink = `${location.origin}/worker`;
$("#worker-link").href = workerLink;
$("#worker-link").textContent = workerLink;

function formatDuration(seconds) {
  if (!Number.isFinite(seconds) || seconds < 0) return "—";
  const whole = Math.floor(seconds);
  const hours = Math.floor(whole / 3600);
  const minutes = Math.floor((whole % 3600) / 60);
  const remaining = whole % 60;
  return hours ? `${hours}h ${minutes}m` : minutes ? `${minutes}m ${remaining}s` : `${remaining}s`;
}

function number(value) {
  return Number.isFinite(value) ? new Intl.NumberFormat().format(value) : "—";
}

function configText(config) {
  if (!config || !Object.values(config).some((value) => value !== 0 && value !== "" && value != null)) return "No best config yet.";
  return JSON.stringify(config);
}

function renderWorkers(workers) {
  const root = $("#workers");
  root.replaceChildren();
  if (!workers.length) {
    const empty = document.createElement("p");
    empty.className = "muted";
    empty.textContent = "No workers connected yet.";
    root.append(empty);
    return;
  }
  for (const worker of workers) {
    const card = document.createElement("article");
    card.className = `worker ${worker.status || ""}`;
    const heading = document.createElement("h3");
    heading.textContent = worker.id || "Unnamed worker";
    const details = document.createElement("p");
    details.textContent = `${worker.browser || "Browser unknown"} · ${number(worker.threads)} thread(s) / ${number(worker.cores)} cores`;
    const work = document.createElement("p");
    work.textContent = `${number(worker.tasksDone)} tasks · ${Number.isFinite(worker.speed) ? worker.speed.toFixed(2) : "—"} tasks/sec`;
    const status = document.createElement("p");
    status.textContent = `Status: ${worker.status || "unknown"}`;
    card.append(heading, details, work, status);
    root.append(card);
  }
}

function renderLeaderboard(entries) {
  const root = $("#leaderboard");
  root.replaceChildren();
  if (!entries.length) {
    root.textContent = "No training results yet.";
    return;
  }
  const list = document.createElement("ol");
  for (const entry of entries) {
    const item = document.createElement("li");
    item.textContent = `Validation accuracy ${Number(entry.valAcc).toFixed(4)} · ${JSON.stringify(entry.config)}`;
    list.append(item);
  }
  root.append(list);
}

function render(job) {
  const state = $("#job-state");
  state.textContent = job.state || "unknown";
  state.className = `pill ${job.state || ""}`;
  startButton.disabled = job.state !== "waiting";
  $("#done").textContent = number(job.done);
  $("#in-flight").textContent = number(job.inFlight);
  $("#total").textContent = number(job.total);
  $("#requeued").textContent = number(job.requeued);
  $("#elapsed").textContent = formatDuration(job.elapsedSec);
  $("#eta").textContent = job.etaSec > 0 ? formatDuration(job.etaSec) : job.state === "running" ? "Waiting for estimate" : "—";
  const total = Number(job.total) || 0;
  $("#progress").max = Math.max(1, total);
  $("#progress").value = Math.min(total, Number(job.done) || 0);
  renderWorkers(Array.isArray(job.workers) ? job.workers : []);
  renderLeaderboard(Array.isArray(job.leaderboard) ? job.leaderboard : []);
  const verify = job.verify || {};
  $("#verify-summary").textContent = `${number(verify.sampled || 0)} sampled`;
  $("#verify-detail").textContent = `${number(verify.matched || 0)} matched · ${number(verify.mismatched || 0)} mismatched`;
  $("#best-config").textContent = configText(job.best && job.best.config);
  $("#baseline").textContent = job.baseline && job.baseline.seconds > 0
    ? `${formatDuration(job.baseline.seconds)} (${job.baseline.label || "single laptop"})`
    : "Single-laptop time not measured.";
}

async function refresh() {
  try {
    const response = await fetch("/api/job", { cache: "no-store" });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    render(await response.json());
    errorLine.textContent = "";
  } catch (error) {
    errorLine.textContent = `Could not load job status: ${error.message}`;
  } finally {
    window.setTimeout(refresh, 1500);
  }
}

startButton.addEventListener("click", async () => {
  startButton.disabled = true;
  errorLine.textContent = "";
  try {
    const response = await fetch("/api/job/start", { method: "POST" });
    if (!response.ok && response.status !== 204) throw new Error(`HTTP ${response.status}`);
  } catch (error) {
    errorLine.textContent = `Could not start job: ${error.message}`;
    startButton.disabled = false;
  }
});

$("#copy-link").addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(workerLink);
    $("#copy-link").textContent = "Copied";
  } catch {
    errorLine.textContent = `Copy this worker link: ${workerLink}`;
  }
});

refresh();
