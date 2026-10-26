(() => {
  'use strict';

  const POLL_MS = 2000;
  const $ = (id) => document.getElementById(id);
  const number = (value) => Number.isFinite(value) ? value : null;
  const count = (value) => Number.isFinite(value) && value >= 0 ? Math.floor(value) : null;
  const safeText = (value, fallback = '—') => typeof value === 'string' && value.trim() ? value.trim() : fallback;
  const escapeHTML = (value) => String(value).replace(/[&<>"']/g, (char) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
  const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;
  const raf = window.requestAnimationFrame?.bind(window) ?? ((fn) => setTimeout(() => fn(Date.now()), 16));
  const cancelRaf = window.cancelAnimationFrame?.bind(window) ?? clearTimeout;
  const fmtDuration = (seconds) => {
    const n = number(seconds);
    if (n === null || n < 0) return '—';
    const s = Math.floor(n), h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), r = s % 60;
    if (h) return `${h}h ${String(m).padStart(2, '0')}m`;
    if (m) return `${m}m ${String(r).padStart(2, '0')}s`;
    return `${r}s`;
  };
  const fmtAccuracy = (value) => {
    const n = number(value);
    return n === null || n < 0 || n > 1 ? '—' : `${(n * 100).toFixed(2)}%`;
  };
  const fmtDecimal = (value) => {
    const n = number(value);
    return n === null || n < 0 ? '—' : new Intl.NumberFormat(undefined, {maximumFractionDigits: 2}).format(n);
  };
  const configChips = (value) => {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return '<span class="config-chip">Configuration unavailable</span>';
    const fields = [['hidden','hidden'],['lr','lr'],['epochs','epochs'],['batch','batch'],['seed','seed']];
    const available = fields.filter(([key]) => value[key] !== undefined && value[key] !== null && (typeof value[key] === 'string' || Number.isFinite(value[key])));
    return available.length ? available.map(([key,label]) => `<span class="config-chip">${label} ${escapeHTML(value[key])}</span>`).join('') : '<span class="config-chip">Configuration unavailable</span>';
  };
  const configKey = (value) => {
    if (!value || typeof value !== 'object') return '';
    return JSON.stringify(['hidden','lr','epochs','batch','seed'].map(key => value[key] ?? null));
  };

  const params = new URLSearchParams(location.search);
  const demoMode = params.get('demo');
  const demoScenarios = new Set(['waiting','running','lost-worker','rejoined','mismatch','done','api-failure','sequence']);
  const isDemo = demoScenarios.has(demoMode);
  const isSequence = demoMode === 'sequence';
  if (isDemo) {
    $('mode-badge').hidden = false;
    $('poll-indicator').textContent = isSequence ? 'DEMO SEQUENCE · MOCK DATA' : `DEMO FIXTURE · ${demoMode.toUpperCase()}`;
    if (isSequence) $('replay-demo').hidden = false;
  }
  const workerLink = new URL('/worker', location.origin).href;
  $('worker-url').textContent = workerLink;
  $('worker-url').href = workerLink;
  if (['localhost','127.0.0.1','::1','[::1]'].includes(location.hostname.toLowerCase())) {
    $('join-network-note').textContent = 'This localhost address works only on this device. Open the dashboard using the coordinator’s LAN address to share it.';
    $('join-network-note').dataset.state = 'warning';
  }
  $('copy-worker-link').addEventListener('click', async () => {
    const status = $('copy-status');
    try {
      if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(workerLink);
      else throw new Error('Clipboard API unavailable');
      status.textContent = 'Worker link copied.';
    } catch {
      const input = document.createElement('textarea');
      input.value = workerLink; input.setAttribute('readonly',''); input.className = 'copy-fallback';
      document.body.appendChild(input); input.select();
      const copied = document.execCommand?.('copy') === true;
      input.remove();
      status.textContent = copied ? 'Worker link copied.' : 'Copy failed. Select the worker link and copy it manually.';
    }
  });

  const config = (hidden, lr, epochs, batch, seed) => ({hidden, lr, epochs, batch, seed});
  const worker = (id, browser, cores, threads, tasksDone, speed, status = 'active') => ({id,browser,cores,threads,tasksDone,speed,status});
  const demoWorkers = [
    worker('tab-b','Chrome · Windows',4,3,41,1),
    worker('tab-c','Chrome · Linux',8,7,36,1.42),
    worker('tab-d','Firefox · macOS',8,7,28,1.21),
  ];
  const demoJob = (scenario) => {
    const base = {jobId:'demo-search-01',state:'running',total:360,done:120,inFlight:8,requeued:0,elapsedSec:95,etaSec:190,
      workers:demoWorkers.map(w=>({...w})),
      leaderboard:[{config:config(128,.001,8,64,42),valAcc:.972},{config:config(96,.0008,10,64,17),valAcc:.968},{config:config(128,.0015,8,32,9),valAcc:.961}],
      verify:{sampled:12,matched:12,mismatched:0},baseline:{seconds:0,label:'1 laptop, all browser threads'},best:{config:config(128,.001,8,64,42),valAcc:.972}};
    if (scenario==='waiting') return {...base,jobId:'demo-waiting',state:'waiting',done:0,inFlight:0,requeued:0,elapsedSec:0,etaSec:null,workers:[],leaderboard:[],verify:{sampled:0,matched:0,mismatched:0},best:null};
    if (scenario==='lost-worker') return {...base,done:118,inFlight:7,requeued:3,workers:[...base.workers,worker('tab-a','Chrome · Windows',4,3,15,.73,'lost')]};
    if (scenario==='rejoined') return {...base,workers:[...base.workers,worker('tab-a','Chrome · Windows',4,3,15,.73,'rejoined')]};
    if (scenario==='mismatch') return {...base,verify:{sampled:12,matched:10,mismatched:2}};
    if (scenario==='done') return {...base,state:'done',done:360,inFlight:0,elapsedSec:418,etaSec:0,verify:{sampled:36,matched:35,mismatched:1}};
    return base;
  };

  const animationByKey = new Map();
  const workerRecords = new Map();
  const classTimers = new WeakMap();
  let pollTimer = null, toastTimer = null, busy = false, stopped = false;
  let finishedSnapshot = false;
  let firstSnapshot = true, firstWorkerSnapshot = true;
  let previousMismatches = 0, previousBestKey = '';
  let previousLeaderboardKeys = new Set(), leaderboardSignature = '';
  let emptyWorkerState = null;
  const failStartedAt = Date.now();

  function animateMetric(key, element, target, format, effect = () => {}) {
    if (target === null || !Number.isFinite(target)) {
      const old = animationByKey.get(key);
      if (old?.frame !== null && old?.frame !== undefined) cancelRaf(old.frame);
      animationByKey.delete(key);
      element.textContent = '—';
      return;
    }
    const old = animationByKey.get(key);
    if (old && old.target === target) {
      element.textContent = format(old.current);
      effect(old.current);
      return;
    }
    const startValue = old ? old.current : (old?.current ?? 0);
    if (old?.frame !== null && old?.frame !== undefined) cancelRaf(old.frame);
    const animation = {target,current:startValue,frame:null};
    animationByKey.set(key,animation);
    const draw = (value) => { animation.current=value; element.textContent=format(value); effect(value); };
    if (reducedMotion || Math.abs(target-startValue) < 0.0001) { draw(target); animation.frame=null; return; }
    const started = Date.now(), duration = 520;
    const tick = () => {
      if (animationByKey.get(key)!==animation) return;
      const progress = Math.min(1,(Date.now()-started)/duration), eased=1-Math.pow(1-progress,3);
      draw(startValue+(target-startValue)*eased);
      if (progress<1) animation.frame=raf(tick); else animation.frame=null;
    };
    animation.frame=raf(tick);
  }

  function forgetMetric(key) {
    const animation=animationByKey.get(key);
    if (animation?.frame!==null && animation?.frame!==undefined) cancelRaf(animation.frame);
    animationByKey.delete(key);
  }

  function pulseClass(element, className, duration = 900) {
    const old=classTimers.get(element);
    if (old) clearTimeout(old);
    element.classList.remove(className);
    void element.offsetWidth;
    element.classList.add(className);
    classTimers.set(element,setTimeout(()=>{element.classList.remove(className);classTimers.delete(element);},duration));
  }

  function showToast(message, kind = 'info') {
    const toast=$('event-toast');
    if (toastTimer) clearTimeout(toastTimer);
    toast.textContent=message;
    toast.dataset.kind=kind;
    toast.hidden=false;
    pulseClass(toast,'toast-enter',180);
    toastTimer=setTimeout(()=>{toast.hidden=true;},2800);
  }

  async function readJSON(response,path) {
    if (!response.ok) throw new Error(`${path} returned HTTP ${response.status}`);
    try { return await response.json(); } catch { throw new Error(`${path} returned invalid JSON`); }
  }

  async function fetchSnapshot() {
    if (isDemo) {
      if (demoMode==='api-failure' && Date.now()-failStartedAt<8000) throw new Error('Demo connection failure: coordinator is unreachable');
      return demoJob(demoMode==='api-failure'?'running':demoMode);
    }
    return readJSON(await fetch('/api/job',{headers:{'Accept':'application/json'},cache:'no-store'}),'/api/job');
  }

  function setConnection(state,label) {
    $('connection').dataset.state=state; $('connection-text').textContent=label;
    $('api-alert').hidden=state!=='error';
    $('api-alert-text').textContent=state==='error'?'Coordinator unavailable. Retrying the live snapshot…':'';
  }

  function animateLayout(before) {
    if (reducedMotion || !before.size) return;
    const moves=[];
    for (const [id,record] of workerRecords) {
      if (!record.present || !before.has(id)) continue;
      const from=before.get(id), to=record.shell.getBoundingClientRect();
      const dx=from.left-to.left, dy=from.top-to.top;
      if (Math.abs(dx)>1 || Math.abs(dy)>1) moves.push({shell:record.shell,dx,dy});
    }
    if (!moves.length) return;
    for (const move of moves) { move.shell.style.transition='none'; move.shell.style.transform=`translate(${move.dx}px, ${move.dy}px)`; }
    raf(()=>raf(()=>{for(const move of moves){move.shell.style.transition='transform 320ms cubic-bezier(.2,.7,.2,1)';move.shell.style.transform='';}}));
  }

  function makeWorkerRecord(id) {
    const shell=document.createElement('div'); shell.className='worker-card-shell'; shell.dataset.workerId=id;
    shell.innerHTML='<button class="worker-card" type="button" aria-pressed="false"><span class="worker-card-inner"><span class="worker-face worker-front"></span><span class="worker-face worker-back" aria-hidden="true"></span></span></button>';
    const card=shell.querySelector('.worker-card');
    card.addEventListener('click',()=>{
      const flipped=card.classList.toggle('is-flipped');
      card.setAttribute('aria-pressed',String(flipped));
      card.setAttribute('aria-label',`${id}. ${flipped?'Browser session details':'Worker overview'}. Press to flip.`);
      shell.querySelector('.worker-front').setAttribute('aria-hidden',String(flipped));
      shell.querySelector('.worker-back').setAttribute('aria-hidden',String(!flipped));
    });
    return {id,shell,card,front:shell.querySelector('.worker-front'),back:shell.querySelector('.worker-back'),status:null,present:true,exitTimer:null,enterTimer:null,highlightTimer:null};
  }

  function renderWorkers(workers) {
    const container=$('workers-list');
    if(!emptyWorkerState)emptyWorkerState=container.querySelector('.worker-empty');
    if (!Array.isArray(workers)) { $('worker-summary').textContent='Worker data unavailable';for(const key of ['join-sessions','join-threads'])forgetMetric(key);$('join-session-count').textContent='—';$('join-thread-count').textContent='—';return; }
    const valid=workers.filter(w=>w&&typeof w==='object'&&!Array.isArray(w));
    const unique=[]; const ids=new Set();
    for(const w of valid){const id=safeText(w.id,'Unnamed worker');if(!ids.has(id)){ids.add(id);unique.push({...w,_displayId:id});}}
    const before=new Map();
    for(const [id,record] of workerRecords) if(record.present) before.set(id,record.shell.getBoundingClientRect());
    const connected=unique.filter(w=>w.status==='active'||w.status==='rejoined');
    const active=unique.filter(w=>w.status==='active').length,lost=unique.filter(w=>w.status==='lost').length,rejoined=unique.filter(w=>w.status==='rejoined').length;
    animateMetric('worker-count',$('worker-count'),unique.length,v=>String(Math.round(v)).padStart(2,'0'));
    $('worker-summary').textContent=unique.length?`${active} active${rejoined?` · ${rejoined} rejoined`:''}${lost?` · ${lost} lost`:''}`:'Waiting for browser workers';
    const connectedThreads=connected.map(w=>count(w.threads));
    const allThreadCountsKnown=connectedThreads.every(v=>v!==null);
    const threadTotal=connectedThreads.reduce((sum,v)=>sum+(v??0),0);
    animateMetric('join-sessions',$('join-session-count'),connected.length,v=>String(Math.round(v)));
    animateMetric('join-threads',$('join-thread-count'),allThreadCountsKnown?threadTotal:null,v=>String(Math.round(v)));
    if(unique.length&&emptyWorkerState){emptyWorkerState.remove();emptyWorkerState=null;}
    if(!unique.length&&!emptyWorkerState){
      emptyWorkerState=document.createElement('div');emptyWorkerState.className='empty-state worker-empty';
      emptyWorkerState.innerHTML='<span class="empty-glyph waiting-glyph" aria-hidden="true">◌</span><strong>Waiting for browser workers to connect</strong><span>Open the worker link on another laptop to join.</span>';
      container.appendChild(emptyWorkerState);
    }
    const speedValues=unique.map(w=>number(w.speed)).filter(v=>v!==null&&v>=0),maxSpeed=Math.max(0,...speedValues);
    const contributionCounts=unique.map(w=>count(w.tasksDone));
    const contributionsKnown=contributionCounts.every(v=>v!==null);
    const contributionTotal=contributionCounts.reduce((sum,v)=>sum+(v??0),0);
    for(const w of unique){
      const id=w._displayId, status=['active','lost','rejoined'].includes(w.status)?w.status:'unknown';
      let record=workerRecords.get(id),isNew=!record;
      if(!record){record=makeWorkerRecord(id);workerRecords.set(id,record);container.appendChild(record.shell);record.shell.classList.add('worker-enter');record.enterTimer=setTimeout(()=>record.shell.classList.remove('worker-enter'),520);}
      else if(!record.present){if(record.exitTimer)clearTimeout(record.exitTimer);record.exitTimer=null;record.present=true;record.shell.classList.remove('worker-exit');container.appendChild(record.shell);}
      else if(record.shell.parentElement!==container)container.appendChild(record.shell);
      record.present=true;
      const oldStatus=record.status,changed=oldStatus!==null&&oldStatus!==status;
      if(changed){
        record.shell.classList.remove('status-lost-flash','status-rejoined-flash');
        if(status==='lost'){record.shell.classList.add('status-lost-flash');showToast(`Worker lost · ${id}`,'warning');}
        else if(status==='rejoined'){record.shell.classList.add('status-rejoined-flash');showToast(`Worker rejoined · ${id}`,'success');}
        else if(oldStatus==='lost')showToast(`Worker active · ${id}`,'success');
        if(record.highlightTimer)clearTimeout(record.highlightTimer);
        record.highlightTimer=setTimeout(()=>record.shell.classList.remove('status-lost-flash','status-rejoined-flash'),1100);
      } else if(isNew&&!firstWorkerSnapshot) showToast(`Worker connected · ${id}`,'success');
      record.status=status;record.shell.dataset.state=status;record.card.dataset.state=status;
      const done=count(w.tasksDone),speed=number(w.speed),cores=count(w.cores),threads=count(w.threads),browser=safeText(w.browser,'Browser information unavailable');
      record.card.setAttribute('aria-label',`${id}. ${status}. Press to view browser session details.`);
      const share=done!==null&&contributionsKnown&&contributionTotal>0?done/contributionTotal*100:null;
      record.front.innerHTML=`<span class="worker-top"><strong class="worker-name" title="${escapeHTML(id)}">${escapeHTML(id)}</strong><span class="worker-state" data-state="${status}"><i class="state-dot"></i>${status.toUpperCase()}</span></span><span class="worker-browser-line" title="${escapeHTML(browser)}">${escapeHTML(browser)} <i>·</i> ${threads===null?'—':threads} threads</span><span class="worker-front-meta"><span class="worker-activity" aria-hidden="true"><i></i>${status==='active'?'Active':'Connected'}</span><span class="worker-speed-value">${speed===null||speed<0?'—':escapeHTML(fmtDecimal(speed))}</span></span><span class="worker-stats"><span>Tasks complete</span><strong class="worker-task-count">—</strong></span><span class="speed-label">Reported speed · relative in pool</span><span class="speed-track"><i class="speed-fill"></i></span><span class="contribution-row"><span>Contribution · reported completions</span><strong>${share===null?'—':`${share.toFixed(0)}%`}</strong></span><span class="contribution-track"><i class="contribution-fill"></i></span><span class="flip-hint">CLICK TO VIEW SESSION DETAILS <span aria-hidden="true">↻</span></span>`;
      record.back.innerHTML=`<span class="back-heading"><span>BROWSER SESSION</span><span class="back-status" data-state="${status}">${status.toUpperCase()}</span></span><strong class="back-worker-name">${escapeHTML(id)}</strong><span class="hardware-row"><span>Browser</span><strong title="${escapeHTML(browser)}">${escapeHTML(browser)}</strong></span><span class="hardware-row"><span>Reported cores</span><strong>${cores===null?'Unavailable':cores}</strong></span><span class="hardware-row"><span>Worker threads</span><strong>${threads===null?'Unavailable':threads}</strong></span><span class="hardware-row"><span>Tasks completed</span><strong class="worker-task-count-back">—</strong></span><span class="hardware-row"><span>Reported speed</span><strong>${speed===null||speed<0?'Unavailable':escapeHTML(fmtDecimal(speed))}</strong></span><span class="flip-hint">CLICK TO RETURN <span aria-hidden="true">↻</span></span>`;
      const taskFront=record.front.querySelector('.worker-task-count'),taskBack=record.back.querySelector('.worker-task-count-back');
      animateMetric(`worker:${id}:tasks`,taskFront,done,v=>String(Math.round(v)));
      taskBack.textContent=done===null?'—':String(done);
      const fill=record.front.querySelector('.speed-fill');
      const speedWidth=speed!==null&&speed>=0&&maxSpeed>0?Math.max(0,Math.min(100,speed/maxSpeed*100)):0;
      fill.style.width=`${speedWidth}%`;
      const contributionFill=record.front.querySelector('.contribution-fill');
      contributionFill.style.width=`${share===null?0:Math.max(0,Math.min(100,share))}%`;
      if(status==='lost')record.shell.classList.add('worker-is-lost');else record.shell.classList.remove('worker-is-lost');
    }
    for(const [id,record] of workerRecords){
      if(ids.has(id)||!record.present)continue;
      record.present=false;record.shell.classList.add('worker-exit');
      record.exitTimer=setTimeout(()=>{if(!record.present){const layoutBefore=new Map();for(const [otherId,other] of workerRecords)if(other.present)layoutBefore.set(otherId,other.shell.getBoundingClientRect());for(const key of ['enterTimer','highlightTimer'])if(record[key])clearTimeout(record[key]);record.shell.remove();workerRecords.delete(id);forgetMetric(`worker:${id}:tasks`);animateLayout(layoutBefore);}},220);
    }
    firstWorkerSnapshot=false;
    animateLayout(before);
  }

  function renderLeaderboard(rows,best){
    const list=$('leaderboard-list');
    const sorted=Array.isArray(rows)?rows.filter(r=>r&&typeof r==='object'&&!Array.isArray(r)).slice().sort((a,b)=>(number(b.valAcc)??-Infinity)-(number(a.valAcc)??-Infinity)).slice(0,6):[];
    const keys=sorted.map(row=>configKey(row.config));
    const bestKey=best&&typeof best==='object'?configKey(best.config):'';
    const signature=JSON.stringify([keys,sorted.map(r=>r.valAcc),bestKey]);
    const newKeys=new Set(firstSnapshot?[]:keys.filter(key=>key&&!previousLeaderboardKeys.has(key)));
    const bestChanged=!firstSnapshot&&bestKey&&previousBestKey&&bestKey!==previousBestKey;
    if(signature!==leaderboardSignature){
      if(!sorted.length)list.innerHTML='<div class="empty-state compact"><span class="empty-glyph">⌁</span><strong>No configurations yet</strong><span>Results will appear as tasks complete.</span></div>';
      else list.innerHTML=sorted.map((row,index)=>{
        const key=configKey(row.config),isBest=key===bestKey||(!bestKey&&index===0);
        const classes=['leader-row',newKeys.has(key)?'is-new':'',bestChanged&&key===bestKey?'best-updated':''].filter(Boolean).join(' ');
        return `<div class="${classes}" data-config-key="${escapeHTML(key)}"><div class="config-line"><span class="config-rank">${String(index+1).padStart(2,'0')}</span>${configChips(row.config)}</div><span class="leader-accuracy">${fmtAccuracy(row.valAcc)}</span></div>`;
      }).join('');
      leaderboardSignature=signature;
    }
    const bestContainer=$('best-config');
    if(best&&best.config){bestContainer.hidden=false;bestContainer.innerHTML=`<div class="best-config-label">★ BEST SO FAR · VALIDATION ${escapeHTML(fmtAccuracy(best.valAcc))}</div><div class="best-config-values">${configChips(best.config)}</div>`;}
    else bestContainer.hidden=true;
    previousLeaderboardKeys=new Set(keys);if(bestKey)previousBestKey=bestKey;
  }

  function renderVerification(verify){
    const panel=$('verification-panel'),badge=$('verification-state'),content=$('verification-content');
    if(!verify||typeof verify!=='object'||Array.isArray(verify)){for(const key of ['verify-sampled','verify-matched','verify-mismatched'])forgetMetric(key);panel.dataset.state='pending';badge.dataset.state='pending';badge.innerHTML='<i class="state-dot"></i>Pending';content.innerHTML='<span class="verification-placeholder">Verification data unavailable</span>';return;}
    const sampled=count(verify.sampled),matched=count(verify.matched),mismatched=count(verify.mismatched);
    if(sampled===null||matched===null||mismatched===null||sampled===0){for(const key of ['verify-sampled','verify-matched','verify-mismatched'])forgetMetric(key);panel.dataset.state='pending';badge.dataset.state='pending';badge.innerHTML='<i class="state-dot"></i>Pending';content.innerHTML='<span class="verification-placeholder">Pending · no samples reported yet</span>';return;}
    const warning=mismatched>0;panel.dataset.state=warning?'mismatch':'matched';badge.dataset.state=warning?'mismatch':'matched';badge.innerHTML=`<i class="state-dot"></i>${warning?'Mismatch detected':'Matched'}`;
    if(!content.querySelector('.verify-sampled'))content.innerHTML='<span class="verify-number verify-sampled">0</span> sampled · <span class="verify-number verify-matched">0</span> matched · <span class="verify-number verify-mismatched">0</span> mismatched';
    animateMetric('verify-sampled',content.querySelector('.verify-sampled'),sampled,v=>String(Math.round(v)));
    animateMetric('verify-matched',content.querySelector('.verify-matched'),matched,v=>String(Math.round(v)));
    animateMetric('verify-mismatched',content.querySelector('.verify-mismatched'),mismatched,v=>String(Math.round(v)));
    if(mismatched>previousMismatches&&previousMismatches>=0&&!firstSnapshot)showToast(`${mismatched-previousMismatches} verification mismatch${mismatched-previousMismatches===1?'':'es'} reported`,'warning');
    previousMismatches=mismatched;
  }

  function renderPerformance(baseline){
    const seconds=baseline&&typeof baseline==='object'?number(baseline.seconds):null;
    $('baseline-time').textContent=seconds!==null&&seconds>0?fmtDuration(seconds):'—';
    $('baseline-name').textContent=baseline&&typeof baseline==='object'?safeText(baseline.label,'One laptop, all browser threads'):'Awaiting measured baseline';
    $('pool-time').textContent='—';$('speedup-value').textContent='—';
    $('performance-note').textContent=seconds!==null&&seconds>0?'Single-laptop baseline received. Pool runtime is not in the proposed /api/job contract, so speedup is withheld.':'Runtime comparison appears when both measurements are available for the same workload.';
  }

  function renderSnapshot(job){
    if(!job||typeof job!=='object'||Array.isArray(job))throw new Error('/api/job returned an incomplete snapshot');
    const state=['waiting','running','done'].includes(job.state)?job.state:'waiting';
    $('start-job').disabled=state!=='waiting';
    $('job-state').dataset.state=state;$('job-state').innerHTML=`<i class="state-dot"></i><span>${state}</span>`;$('job-id').textContent=`JOB ${safeText(job.jobId,'—')}`;
    const total=count(job.total),done=count(job.done),inflight=count(job.inFlight),requeued=count(job.requeued);
    animateMetric('total',$('total-count'),total,v=>String(Math.round(v)));
    animateMetric('done',$('done-count'),done,v=>String(Math.round(v)));
    animateMetric('inflight',$('inflight-count'),inflight,v=>String(Math.round(v)));
    animateMetric('requeued',$('requeued-count'),requeued,v=>String(Math.round(v)));
    const percent=total!==null&&total>0&&done!==null?Math.max(0,Math.min(100,done/total*100)):null;
    animateMetric('progress',$('progress-percent'),percent,v=>`${v.toFixed(1)}%`,v=>{$('progress-fill').style.width=`${Math.max(0,Math.min(100,v))}%`;});
    if(percent===null)$('progress-fill').style.width='0%';
    $('progress-label').textContent=percent===null?'Progress unavailable':state==='done'?'All tasks complete':`${done} of ${total} tasks`;
    $('elapsed-time').textContent=fmtDuration(job.elapsedSec);$('eta-time').textContent=state==='done'?'0s':fmtDuration(job.etaSec);
    $('requeued-note').textContent=requeued>0?'Tasks returned after worker loss':'Returned to the queue';
    renderWorkers(job.workers);renderLeaderboard(job.leaderboard,job.best);renderVerification(job.verify);renderPerformance(job.baseline);
    $('last-updated').textContent=new Intl.DateTimeFormat(undefined,{hour:'2-digit',minute:'2-digit',second:'2-digit'}).format(new Date());
    setConnection('connected','Connected');
    if(state==='done'&&!finishedSnapshot){finishedSnapshot=true;renderFinalSummary(job);}
    if(state!=='done')finishedSnapshot=false;
    firstSnapshot=false;
  }

  function renderFinalSummary(job){
    const content=$('results-content'),best=job.best&&typeof job.best==='object'?job.best:null;
    $('results-panel').dataset.state='done';$('results-badge').textContent='JOB DONE';
    content.innerHTML=`<div class="result-pending"><span class="result-mark">✓</span><div><strong>All ${escapeHTML(count(job.done)??'reported')} tasks complete</strong><span>${best?`Best validation accuracy: ${escapeHTML(fmtAccuracy(best.valAcc))}. `:''}The protocol does not define a held-out final-result schema yet.</span>${best?`<div class="result-config">${configChips(best.config)}</div>`:''}</div></div>`;
  }

  function demoSequence(){
    const cfgA=config(64,.001,4,32,3),cfgB=config(128,.0008,6,64,7),cfgC=config(128,.001,8,64,42);
    const a=worker('tab-a','Chrome · Windows',4,3,0,.74),b=worker('tab-b','Chrome · Linux',8,7,0,1.22),c=worker('tab-c','Firefox · Windows',4,3,0,1),d=worker('tab-d','Safari · macOS',8,7,0,1.34);
    const mismatch=params.get('mismatch')==='1';
    const steps=[
      {jobId:'demo-sequence',state:'waiting',total:24,done:0,inFlight:0,requeued:0,elapsedSec:0,etaSec:null,workers:[],leaderboard:[],verify:{sampled:0,matched:0,mismatched:0},baseline:{seconds:48,label:'1 laptop, all browser threads'},best:null},
      {jobId:'demo-sequence',state:'running',total:24,done:0,inFlight:2,requeued:0,elapsedSec:2,etaSec:42,workers:[{...a,tasksDone:0}],leaderboard:[],verify:{sampled:0,matched:0,mismatched:0},baseline:{seconds:48,label:'1 laptop, all browser threads'},best:null},
      {jobId:'demo-sequence',state:'running',total:24,done:2,inFlight:4,requeued:0,elapsedSec:4,etaSec:31,workers:[{...a,tasksDone:1},{...b,tasksDone:1}],leaderboard:[],verify:{sampled:0,matched:0,mismatched:0},baseline:{seconds:48,label:'1 laptop, all browser threads'},best:null},
      {jobId:'demo-sequence',state:'running',total:24,done:5,inFlight:6,requeued:0,elapsedSec:6,etaSec:24,workers:[{...a,tasksDone:2},{...b,tasksDone:2},{...c,tasksDone:1}],leaderboard:[{config:cfgA,valAcc:.91}],verify:{sampled:0,matched:0,mismatched:0},baseline:{seconds:48,label:'1 laptop, all browser threads'},best:{config:cfgA,valAcc:.91}},
      {jobId:'demo-sequence',state:'running',total:24,done:9,inFlight:8,requeued:0,elapsedSec:8,etaSec:17,workers:[{...a,tasksDone:3},{...b,tasksDone:3},{...c,tasksDone:2},{...d,tasksDone:1}],leaderboard:[{config:cfgB,valAcc:.94},{config:cfgA,valAcc:.91}],verify:{sampled:0,matched:0,mismatched:0},baseline:{seconds:48,label:'1 laptop, all browser threads'},best:{config:cfgB,valAcc:.94}},
      {jobId:'demo-sequence',state:'running',total:24,done:10,inFlight:5,requeued:3,elapsedSec:10,etaSec:14,workers:[{...a,tasksDone:3},{...b,tasksDone:4},{...c,tasksDone:2},{...d,tasksDone:1,status:'lost'}],leaderboard:[{config:cfgB,valAcc:.94},{config:cfgA,valAcc:.91}],verify:{sampled:0,matched:0,mismatched:0},baseline:{seconds:48,label:'1 laptop, all browser threads'},best:{config:cfgB,valAcc:.94}},
      {jobId:'demo-sequence',state:'running',total:24,done:13,inFlight:6,requeued:3,elapsedSec:12,etaSec:9,workers:[{...a,tasksDone:4},{...b,tasksDone:5},{...c,tasksDone:3},{...d,tasksDone:1,status:'rejoined'}],leaderboard:[{config:cfgB,valAcc:.94},{config:cfgA,valAcc:.91}],verify:{sampled:0,matched:0,mismatched:0},baseline:{seconds:48,label:'1 laptop, all browser threads'},best:{config:cfgB,valAcc:.94}},
      {jobId:'demo-sequence',state:'running',total:24,done:18,inFlight:3,requeued:3,elapsedSec:15,etaSec:5,workers:[{...a,tasksDone:6},{...b,tasksDone:6},{...c,tasksDone:4},{...d,tasksDone:2,status:'active'}],leaderboard:[{config:cfgC,valAcc:.958},{config:cfgB,valAcc:.94},{config:cfgA,valAcc:.91}],verify:{sampled:4,matched:mismatch?3:4,mismatched:mismatch?1:0},baseline:{seconds:48,label:'1 laptop, all browser threads'},best:{config:cfgC,valAcc:.958}},
      {jobId:'demo-sequence',state:'done',total:24,done:24,inFlight:0,requeued:3,elapsedSec:21,etaSec:0,workers:[{...a,tasksDone:8},{...b,tasksDone:8},{...c,tasksDone:5},{...d,tasksDone:3,status:'active'}],leaderboard:[{config:cfgC,valAcc:.958},{config:cfgB,valAcc:.94},{config:cfgA,valAcc:.91}],verify:{sampled:4,matched:mismatch?3:4,mismatched:mismatch?1:0},baseline:{seconds:48,label:'1 laptop, all browser threads'},best:{config:cfgC,valAcc:.958}},
    ];
    return steps;
  }

  let demoTimers=[];
  function startDemoSequence(){
    demoTimers.forEach(clearTimeout);demoTimers=[];stopped=false;finishedSnapshot=false;previousMismatches=0;
    firstSnapshot=true;firstWorkerSnapshot=true;previousBestKey='';previousLeaderboardKeys=new Set();leaderboardSignature='';
    if(toastTimer)clearTimeout(toastTimer);$('event-toast').hidden=true;
    for(const [id,record] of workerRecords){clearTimeout(record.exitTimer);clearTimeout(record.enterTimer);clearTimeout(record.highlightTimer);forgetMetric(`worker:${id}:tasks`);}
    workerRecords.clear();$('workers-list').replaceChildren();emptyWorkerState=null;
    for(const key of ['worker-count','done','inflight','requeued','total','progress','verify-sampled','verify-matched','verify-mismatched']){const animation=animationByKey.get(key);if(animation?.frame!==null&&animation?.frame!==undefined)cancelRaf(animation.frame);animationByKey.delete(key);}
    $('results-panel').dataset.state='pending';$('results-badge').textContent='NOT READY';$('results-content').innerHTML='<div class="result-pending"><span class="result-mark">↗</span><div><strong>Final score appears when the job is done</strong><span>Held-out test accuracy is reported once for the winning configuration.</span></div></div>';
    const steps=demoSequence();steps.forEach((step,index)=>{const timer=setTimeout(()=>renderSnapshot(step),index*1650);demoTimers.push(timer);});
  }

  async function poll(){
    if(busy||stopped)return;busy=true;
    try{const job=await fetchSnapshot();renderSnapshot(job);if(job.state==='done')stopped=true;}
    catch(error){setConnection('error','Disconnected');}
    finally{busy=false;if(!stopped)pollTimer=setTimeout(poll,POLL_MS);}
  }

  $('replay-demo').addEventListener('click',startDemoSequence);
  $('start-job').addEventListener('click',async()=>{const button=$('start-job');button.disabled=true;try{const response=await fetch('/api/job/start',{method:'POST'});if(!response.ok&&response.status!==204)throw new Error(`HTTP ${response.status}`);showToast('Job start requested','success');}catch(error){setConnection('error','Disconnected');showToast(`Could not start job: ${error.message}`,'warning');button.disabled=false;}});
  window.addEventListener('pagehide',()=>{stopped=true;if(pollTimer)clearTimeout(pollTimer);demoTimers.forEach(clearTimeout);if(toastTimer)clearTimeout(toastTimer);for(const animation of animationByKey.values())if(animation.frame!==null)cancelRaf(animation.frame);for(const record of workerRecords.values())for(const key of ['exitTimer','enterTimer','highlightTimer'])if(record[key])clearTimeout(record[key]);},{once:true});
  if(isSequence)startDemoSequence();else poll();
})();
