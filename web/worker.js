// web/worker.js
(function () {
  const qs = new URLSearchParams(location.search);
  const hc = navigator.hardwareConcurrency || 4;
  const mobile = /Android|iPhone|iPad|Mobile/i.test(navigator.userAgent);
  const CFG = {
    threads: parseInt(qs.get('threads')) || (mobile ? Math.min(3, hc) : Math.max(1, hc - 1)),
    prefetch: qs.has('prefetch') ? parseInt(qs.get('prefetch')) : 0, // server caps requests at threads, so keep 0
    mock: qs.get('mock') === '1',
    heartbeatMs: 1500, // used until the server announces its own interval in WELCOME
    benchRuns: 3,
    // PLACEHOLDER: Person 2 supplies the real boundary tile and maxIter (same values as native)
    bench: { x0: -0.743643887, y0: 0.131825904, scale: 4.8828125e-8, w: 64, h: 64, maxIter: 2000 },
  };

  function getId() {
    const fresh = 'dev-' + Math.random().toString(36).slice(2, 8); // randomUUID needs HTTPS
    try {
      let id = sessionStorage.getItem('sh_id');
      if (!id) { id = fresh; sessionStorage.setItem('sh_id', id); }
      return id;
    } catch (e) { return fresh; }
  }
  const deviceId = getId();

  // Readable name for the dashboard: ?name=..., else the name box, else a guess from the user agent.
  let label = '';
  function guessLabel() {
    const ua = navigator.userAgent;
    if (/iPhone/.test(ua)) return 'iPhone';
    if (/iPad/.test(ua)) return 'iPad';
    if (/Android/.test(ua)) {
      const m = ua.match(/;\s*([^;)]+?)\s+Build/); // older Chrome and WebViews show the model
      return m ? m[1] : 'Android phone';
    }
    if (/CrOS/.test(ua)) return 'Chromebook';
    if (/Windows/.test(ua)) return 'Windows laptop';
    if (/Macintosh/.test(ua)) return 'Mac laptop';
    return 'Device';
  }
  function getLabel() {
    let name = (qs.get('name') || '').trim();
    try {
      const box = document.getElementById('label');
      if (!name && box) name = box.value.trim();
      if (name) sessionStorage.setItem('sh_label', name);
      else name = sessionStorage.getItem('sh_label') || '';
    } catch (e) { /* storage can be blocked */ }
    return (name || guessLabel()).slice(0, 40);
  }

  const S = {
    ws: null, sid: 0, jobId: null, hb: null, pool: [], pending: [], requested: 0,
    jobDone: false, benchDone: false, benchPromise: null, benchTps: 0, benchHash: '', benchPixels: null,
    retries: 0, done: 0, stale: 0, computeMs: 0, maxMs: 0, stamps: [],
    acks: 0, started: false,
  };
  window.__sh = S; // debugging handle
  S.benchB64 = () => { // base64 of the benchmark pixels, for native-vs-WASM comparison
    let s = ''; const p = S.benchPixels;
    for (let i = 0; i < p.length; i++) s += String.fromCharCode(p[i]);
    return btoa(s);
  };
  const $ = (id) => document.getElementById(id);
  const log = (...a) => console.log('[scrapheap]', ...a);
  const setStatus = (t) => { $('status').textContent = t; };
  function send(o) { if (S.ws && S.ws.readyState === 1) S.ws.send(JSON.stringify(o)); }

  // ---------- Web Worker pool ----------
  function makePool() {
    for (let i = 0; i < CFG.threads; i++) {
      const th = new Worker('kernel-worker.js' + (CFG.mock ? '?mock=1' : ''));
      const slot = { th, busy: false, ok: false, benchMs: 0, hash: '', benchResolve: null };
      slot.ready = new Promise((res) => { slot.markReady = res; });
      th.onmessage = (e) => onSlotMsg(slot, e.data);
      th.onerror = (e) => { log('worker error', e.message); slot.markReady(false); };
      S.pool.push(slot);
    }
  }

  function onSlotMsg(slot, m) {
    if (m.t === 'ready') { slot.ok = true; slot.markReady(true); }
    else if (m.t === 'error') { log('kernel load failed:', m.msg); setStatus('Kernel failed: ' + m.msg); slot.markReady(false); }
    else if (m.t === 'bench') {
      slot.benchMs = m.avgMs; slot.hash = m.hash;
      if (!S.benchPixels) S.benchPixels = new Uint8Array(m.pixels);
      if (slot.benchResolve) slot.benchResolve();
    }
    else if (m.t === 'taskfail') { log('task failed:', m.msg); slot.busy = false; pump(); }
    else if (m.t === 'result') {
      slot.busy = false;
      const current = m.sid === S.sid && (S.jobId === null || m.jobId === S.jobId);
      if (current && S.ws && S.ws.readyState === 1) {
        S.ws.send(m.buf);
        S.done++; S.computeMs += m.ms; S.maxMs = Math.max(S.maxMs, m.ms);
        S.stamps.push(performance.now());
      } else {
        S.stale++; // old session or old job: the server would reject it, so do not send it
      }
      pump();
    }
  }

  async function runBench() {
    const live = S.pool.filter((s) => s.ok);
    await Promise.all(live.map((s) => new Promise((res) => {
      s.benchResolve = res;
      s.th.postMessage({ t: 'bench', p: CFG.bench, n: CFG.benchRuns });
    })));
    S.benchTps = live.reduce((sum, s) => sum + 1000 / s.benchMs, 0); // all threads together
    const hashes = live.map((s) => s.hash);
    S.benchHash = hashes[0];
    if (new Set(hashes).size > 1) log('WARNING: threads disagree on the benchmark hash', hashes);
    S.benchDone = true;
    log('bench tiles/sec', S.benchTps.toFixed(2), 'sha256', S.benchHash);
  }

  // ---------- WebSocket ----------
  function startHeartbeat(ms) {
    clearInterval(S.hb);
    S.hb = setInterval(() => send({ type: 'HEARTBEAT' }), ms || CFG.heartbeatMs);
  }

  function connect() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(proto + '://' + location.host + '/ws/worker');
    ws.binaryType = 'arraybuffer';
    S.ws = ws;

    ws.onopen = async () => {
      S.sid++; S.retries = 0; S.pending = []; S.requested = 0; S.jobDone = false;
      const live = S.pool.filter((s) => s.ok).length;
      send({ type: 'HELLO', deviceId, label, userAgent: navigator.userAgent, cores: hc, threads: live, mock: CFG.mock });
      startHeartbeat(CFG.heartbeatMs); // WELCOME may replace the interval
      if (!S.benchDone) {
        setStatus('Benchmarking...');
        S.benchPromise = S.benchPromise || runBench();
        await S.benchPromise;
      }
      if (ws !== S.ws || ws.readyState !== 1) return; // connection changed while benchmarking
      send({ type: 'BENCH_RESULT', tilesPerSec: S.benchTps, perThread: S.benchTps / live, threads: live, hash: S.benchHash });
      setStatus('Working');
      pump();
    };

    ws.onmessage = (e) => {
      if (typeof e.data === 'string') {
        let m; try { m = JSON.parse(e.data); } catch (err) { return; }
        if (m.type === 'WELCOME') { startHeartbeat(m.heartbeatIntervalMs); log('server heartbeat timeout ms', m.heartbeatTimeoutMs); }
        else if (m.type === 'JOB_STARTED') onJobStarted(m.jobId);
        else if (m.type === 'ACK') S.acks++;
        else if (m.type === 'JOB_DONE') onJobDone();
        return;
      }
      S.jobId = SH_PROTO.taskJobId(e.data); // a TASK frame: remember which job it belongs to
      S.jobDone = false;
      S.requested = Math.max(0, S.requested - 1);
      S.pending.push(e.data);
      pump();
    };

    ws.onclose = () => {
      clearInterval(S.hb);
      setStatus('Disconnected. Reconnecting...');
      setTimeout(connect, Math.min(3000, 500 * Math.pow(2, S.retries++)));
    };
    ws.onerror = () => {}; // onclose handles recovery
  }

  function onJobStarted(jobId) {
    S.jobId = jobId; S.jobDone = false; S.pending = []; S.requested = 0; // server dropped old held requests
    setStatus('Working');
    pump(); // ask again
  }

  function onJobDone() {
    S.jobDone = true; S.pending = []; S.requested = 0;
    setStatus('Job done. Waiting for the next job...');
    setTimeout(() => { if (S.jobDone) { S.jobDone = false; pump(); } }, 1000); // fallback if JOB_STARTED never comes
  }

  // ---------- Pull loop ----------
  function pump() {
    for (const slot of S.pool) { // 1) give waiting tasks to idle threads
      if (!slot.ok || slot.busy || S.pending.length === 0) continue;
      const buf = S.pending.shift();
      slot.busy = true;
      slot.th.postMessage({ t: 'task', buf, sid: S.sid }, [buf]);
    }
    const ws = S.ws; // 2) ask for more, up to threads x (1 + prefetch)
    if (!ws || ws.readyState !== 1 || !S.benchDone || S.jobDone) return;
    const live = S.pool.filter((s) => s.ok).length;
    const busy = S.pool.filter((s) => s.busy).length;
    const cap = live * (1 + CFG.prefetch);
    for (let have = S.pending.length + busy + S.requested; have < cap; have++) {
      send({ type: 'REQUEST_TASK' });
      S.requested++;
    }
  }

  // ---------- Keep awake ----------
  let noSleep = null;
  async function keepAwake() {
    try {
      if ('wakeLock' in navigator) { await navigator.wakeLock.request('screen'); return; }
    } catch (e) { /* fall through */ }
    try { if (window.NoSleep) { noSleep = noSleep || new NoSleep(); noSleep.enable(); } } catch (e) { log('NoSleep failed'); }
  }
  document.addEventListener('visibilitychange', () => {
    log('visibility', document.visibilityState);
    if (document.visibilityState === 'visible' && S.started) keepAwake();
  });

  // ---------- UI ----------
  function renderStats() {
    const now = performance.now();
    S.stamps = S.stamps.filter((t) => now - t < 5000);
    $('tps').textContent = (S.stamps.length / 5).toFixed(1);
    $('done').textContent = S.done;
    $('avg').textContent = S.done ? Math.round(S.computeMs / S.done) + ' ms' : '-';
    $('max').textContent = S.done ? Math.round(S.maxMs) + ' ms' : '-';
    $('bench').textContent = S.benchDone ? S.benchTps.toFixed(2) + ' tiles/s' : '-';
    $('threads').textContent = S.pool.filter((s) => s.ok).length;
    $('acks').textContent = S.acks;
    $('dev').textContent = label || deviceId;
  }

  // ---------- Start ----------
  async function start() {
    if (S.started) return;
    S.started = true;
    label = getLabel(); // read the name box before hiding it
    $('join').hidden = true; $('label').hidden = true; $('stats').hidden = false;
    keepAwake(); // must run inside the tap, before any await
    setStatus('Loading kernel...');
    makePool();
    await Promise.all(S.pool.map((s) => s.ready));
    if (!S.pool.some((s) => s.ok)) { setStatus('No kernel could load'); return; }
    setInterval(renderStats, 500);
    connect();
  }

  $('join').addEventListener('click', start);
  if (qs.get('auto') === '1') start(); // laptop and benchmark runs skip the tap
})();