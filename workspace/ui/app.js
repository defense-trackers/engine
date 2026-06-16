// Private bid workspace UI. Reads /api/opportunities (scored) + /api/state, lets
// Jesse triage and track pursuits. State changes POST to /api/state (local file).

let OPPS = [];
let STATE = {};
let VIEW = 'today';
let BRIEF = null;

const $ = (s, r = document) => r.querySelector(s);
const el = (t, c, txt) => { const e = document.createElement(t); if (c) e.className = c; if (txt != null) e.textContent = txt; return e; };

// live Zulu clock in the status bar
function tickClock() {
  const c = $('#clock');
  if (!c) return;
  const d = new Date(), p = (n) => String(n).padStart(2, '0');
  c.innerHTML = `<b>${p(d.getUTCHours())}:${p(d.getUTCMinutes())}:${p(d.getUTCSeconds())}</b>Z`;
}
setInterval(tickClock, 1000);

// --- optional sound design (WebAudio synth; off by default) ---
let SOUND_ON = localStorage.getItem('snd') === '1';
let AC = null;
function actx() { try { if (!AC) AC = new (window.AudioContext || window.webkitAudioContext)(); if (AC.state === 'suspended') AC.resume(); } catch { } return AC; }
function blip(freq, dur, type, gain, slideTo) {
  if (!SOUND_ON) return; const ac = actx(); if (!ac) return;
  const o = ac.createOscillator(), g = ac.createGain();
  o.type = type || 'sine'; o.frequency.setValueAtTime(freq, ac.currentTime);
  if (slideTo) o.frequency.exponentialRampToValueAtTime(slideTo, ac.currentTime + dur);
  g.gain.setValueAtTime(gain, ac.currentTime);
  g.gain.exponentialRampToValueAtTime(.0001, ac.currentTime + dur);
  o.connect(g).connect(ac.destination); o.start(); o.stop(ac.currentTime + dur);
}
const snd = {
  tick: () => blip(880, .03, 'square', .02),
  tab: () => { blip(520, .05, 'triangle', .045); setTimeout(() => blip(820, .06, 'triangle', .035), 45); },
  enter: () => { blip(330, .12, 'sine', .06, 660); setTimeout(() => blip(660, .2, 'sine', .05, 990), 120); },
  lock: () => blip(1300, .012, 'sine', .01),
  send: () => { blip(440, .06, 'triangle', .05, 880); setTimeout(() => blip(880, .1, 'sine', .04, 1320), 55); },
  recv: () => { blip(990, .07, 'sine', .035, 660); setTimeout(() => blip(1320, .12, 'sine', .03, 880), 60); },
  apply: () => { blip(660, .05, 'triangle', .05, 990); setTimeout(() => blip(1320, .14, 'sine', .045, 1760), 50); },
  err: () => { blip(220, .14, 'sawtooth', .045, 140); setTimeout(() => blip(160, .18, 'sawtooth', .04, 100), 90); },
  mic: () => { blip(740, .05, 'sine', .04, 1180); },
  micoff: () => { blip(1180, .06, 'sine', .03, 560); },
};

// --- idle attract mode ---
let idleTimer;
function resetIdle() { document.body.classList.remove('idle'); clearTimeout(idleTimer); idleTimer = setTimeout(() => document.body.classList.add('idle'), 35000); }

// token-reactive Claude core — flares with each streamed token (throttled; glow only,
// so it doesn't fight the spin transform)
let _coreT = 0;
function corePulse() {
  const c = document.querySelector('.ccore'); if (!c) return;
  const now = performance.now(); if (now - _coreT < 55) return; _coreT = now;
  c.style.boxShadow = '0 0 54px 6px rgba(143,179,196,.95)'; c.style.filter = 'brightness(1.45)';
  clearTimeout(c._decay); c._decay = setTimeout(() => { c.style.boxShadow = ''; c.style.filter = ''; }, 130);
}

// ============================================================
// VOICE — on-device, private (Phase 1)
//   STT: native SpeechRecognition (Chrome on-device when available); the
//        transcript routes through the existing sendAssist() — same grounding,
//        same [[do:]] tool-use + confirm chips. Audio never leaves the machine.
//   TTS: SpeechSynthesis reads streamed replies sentence-by-sentence,
//        interruptible on the next send / mic / Esc.
// ============================================================
const VOICE = {
  rec: null, listening: false, supported: false, base: '', autosend: false,
  speak: localStorage.getItem('rz-speak') === '1',
  voice: null, ttsSpoken: 0,
};

function voiceInit() {
  const SR = window.SpeechRecognition || window.webkitSpeechRecognition;
  VOICE.supported = !!SR;
  if (SR) {
    const r = new SR();
    r.lang = 'en-US'; r.interimResults = true; r.continuous = false; r.maxAlternatives = 1;
    try { r.processLocally = true; } catch { }
    r.onresult = (e) => {
      let interim = '', fin = '';
      for (let i = e.resultIndex; i < e.results.length; i++) {
        const t = e.results[i][0].transcript;
        if (e.results[i].isFinal) fin += t; else interim += t;
      }
      const inp = $('#assist-input');
      if (inp) inp.value = (VOICE.base + ' ' + (fin || interim)).replace(/\s+/g, ' ').trim();
      if (fin) VOICE.base = (VOICE.base + ' ' + fin).replace(/\s+/g, ' ').trim();
    };
    r.onend = () => {
      const wasAuto = VOICE.autosend; VOICE.autosend = false; micStop(false);
      const inp = $('#assist-input');
      if (wasAuto && inp && inp.value.trim()) sendAssist();
    };
    r.onerror = (e) => { micStop(false); if (e.error !== 'no-speech' && e.error !== 'aborted') { toast('voice: ' + e.error); snd.err(); } };
    VOICE.rec = r;
  }
  const pick = () => {
    if (!('speechSynthesis' in window)) return;
    const vs = speechSynthesis.getVoices();
    VOICE.voice = vs.find((v) => /en-US/i.test(v.lang) && /Google|Natural|Neural|Samantha|Aria|Jenny|Zira/i.test(v.name))
      || vs.find((v) => /en[-_]/i.test(v.lang)) || vs[0] || null;
  };
  if ('speechSynthesis' in window) { pick(); speechSynthesis.onvoiceschanged = pick; }
}

function micStart() {
  if (!VOICE.rec || VOICE.listening) return;
  ttsCancel();
  VOICE.base = ($('#assist-input')?.value || '').trim();
  try { VOICE.rec.start(); } catch { return; }
  VOICE.listening = true; actx(); snd.mic();
  document.getElementById('mic-btn')?.classList.add('on');
  document.getElementById('assist')?.classList.add('listening');
  micMeterStart();
}
function micStop(userToggled) {
  if (VOICE.listening) { try { VOICE.rec.stop(); } catch { } }
  VOICE.listening = false;
  document.getElementById('mic-btn')?.classList.remove('on');
  document.getElementById('assist')?.classList.remove('listening');
  micMeterStop();
  if (userToggled) snd.micoff();
}
function micToggle(autosend) {
  if (!VOICE.supported) { toast('Voice input needs Chrome (on-device speech)'); return; }
  if (VOICE.listening) micStop(true);
  else { VOICE.autosend = !!autosend; micStart(); }
}

// ---- TTS feeder: speak complete sentences as they stream in ----
function ttsStrip(s) {
  return s.replace(/\[\[do:[^\]]+\]\]/g, '').replace(/```[\s\S]*?```/g, ' code block ')
    .replace(/[`*#_>|~]/g, '').replace(/\s+/g, ' ').trim();
}
function ttsCancel() {
  if ('speechSynthesis' in window) speechSynthesis.cancel();
  VOICE.ttsSpoken = 0;
  document.getElementById('assist')?.classList.remove('speaking');
}
function ttsSay(text) {
  if (!VOICE.speak || !('speechSynthesis' in window)) return;
  const clean = ttsStrip(text); if (!clean) return;
  const u = new SpeechSynthesisUtterance(clean);
  if (VOICE.voice) u.voice = VOICE.voice;
  u.rate = 1.05; u.pitch = 1; u.lang = 'en-US';
  u.onstart = () => document.getElementById('assist')?.classList.add('speaking');
  u.onend = () => { if (!speechSynthesis.pending && !speechSynthesis.speaking) document.getElementById('assist')?.classList.remove('speaking'); };
  speechSynthesis.speak(u);
}
function ttsFeed(acc) {
  if (!VOICE.speak) return;
  const pending = acc.slice(VOICE.ttsSpoken);
  let idx = -1; const re = /[.!?](\s|$)/g; let m;
  while ((m = re.exec(pending))) idx = m.index + 1;
  if (idx > 0) { ttsSay(pending.slice(0, idx)); VOICE.ttsSpoken += idx; }
}
function ttsFlush(acc) {
  if (!VOICE.speak) return;
  const rest = acc.slice(VOICE.ttsSpoken);
  if (rest.trim()) { ttsSay(rest); VOICE.ttsSpoken = acc.length; }
}

// ---- waveform: idle breathing / listening (live mic) / streaming (token kicks) ----
const WAVE = { cv: null, ctx: null, raf: 0, bars: 44, kick: 0, an: null, data: null, stream: null, mode: 'idle' };
function waveInit() {
  const cv = document.getElementById('wave'); if (!cv) return;
  WAVE.cv = cv; WAVE.ctx = cv.getContext('2d');
  const dpr = Math.min(2, window.devicePixelRatio || 1);
  const resize = () => { const r = cv.getBoundingClientRect(); cv.width = Math.max(1, r.width * dpr); cv.height = Math.max(1, r.height * dpr); WAVE.ctx.setTransform(dpr, 0, 0, dpr, 0, 0); };
  resize(); try { new ResizeObserver(resize).observe(cv); } catch { }
  if (!matchMedia('(prefers-reduced-motion: reduce)').matches) waveLoop();
}
function waveKick() { WAVE.kick = Math.min(1, WAVE.kick + .5); }
let _wt = 0;
function waveLoop() {
  WAVE.raf = requestAnimationFrame(waveLoop);
  const ctx = WAVE.ctx, cv = WAVE.cv; if (!ctx) return;
  const w = cv.clientWidth, h = cv.clientHeight; if (!w || !h) return;
  ctx.clearRect(0, 0, w, h); _wt += .05; WAVE.kick *= .92;
  if (WAVE.an && WAVE.mode === 'listening') WAVE.an.getByteFrequencyData(WAVE.data);
  const n = WAVE.bars, mid = h / 2, bw = w / n, cap = h * .46;
  for (let i = 0; i < n; i++) {
    const x = i * bw + bw / 2; let amp;
    if (WAVE.mode === 'listening') {
      const f = WAVE.an ? (WAVE.data[Math.floor(i / n * WAVE.data.length)] / 255) : (.35 + .55 * Math.abs(Math.sin(_wt * 3 + i * .5)));
      amp = (.12 + f * .88) * cap;
    } else if (WAVE.mode === 'streaming') {
      amp = (.1 + (.22 + WAVE.kick * .72) * Math.abs(Math.sin(_wt * 4 + i * .55))) * cap;
    } else {
      amp = (.06 + .06 * Math.abs(Math.sin(_wt * .8 + i * .28))) * cap;
    }
    const g = ctx.createLinearGradient(0, mid - amp, 0, mid + amp);
    g.addColorStop(0, 'rgba(143,179,196,.9)'); g.addColorStop(.5, 'rgba(110,150,168,.45)'); g.addColorStop(1, 'rgba(143,179,196,.9)');
    ctx.fillStyle = g; ctx.fillRect(x - bw * .26, mid - amp, bw * .52, amp * 2);
  }
}
async function micMeterStart() {
  WAVE.mode = 'listening';
  try {
    const ac = actx(); if (!ac) return;
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    WAVE.stream = stream;
    const src = ac.createMediaStreamSource(stream);
    const an = ac.createAnalyser(); an.fftSize = 128; an.smoothingTimeConstant = .7; src.connect(an);
    WAVE.an = an; WAVE.data = new Uint8Array(an.frequencyBinCount);
  } catch { WAVE.an = null; } // synthetic fallback — still animates
}
function micMeterStop() {
  if (WAVE.mode === 'listening') WAVE.mode = 'idle';
  if (WAVE.stream) { try { WAVE.stream.getTracks().forEach((t) => t.stop()); } catch { } WAVE.stream = null; }
  WAVE.an = null; WAVE.data = null;
}

// chromatic-aberration glitch burst (on enter + view change)
function glitchBurst() {
  if (matchMedia('(prefers-reduced-motion: reduce)').matches) return;
  document.body.classList.remove('glitch');
  void document.body.offsetWidth; // restart the animation
  document.body.classList.add('glitch');
  setTimeout(() => document.body.classList.remove('glitch'), 340);
}

// scramble/decrypt flicker for live data values (numbers settle from noise)
function scrambleText(node, finalText, dur = 520) {
  if (matchMedia('(prefers-reduced-motion: reduce)').matches) { node.textContent = finalText; return; }
  const glyphs = '0123456789/#%';
  const keep = ' $,.KZ%';
  const t0 = performance.now();
  const step = (now) => {
    const p = Math.min(1, (now - t0) / dur), rev = Math.floor(p * finalText.length);
    let out = '';
    for (let k = 0; k < finalText.length; k++) {
      const ch = finalText[k];
      out += (k < rev || keep.includes(ch)) ? ch : glyphs[(Math.floor(now / 24) + k * 5) % glyphs.length];
    }
    node.textContent = out;
    if (p < 1) requestAnimationFrame(step); else node.textContent = finalText;
  };
  requestAnimationFrame(step);
}

// --- custom line-icon set (zero emoji; stroke = currentColor) ---
const _s = (p) => `<svg class="ic-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">${p}</svg>`;
const ICON = {
  clock: _s('<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/>'),
  chat: _s('<path d="M4 6.5A1.5 1.5 0 0 1 5.5 5h13A1.5 1.5 0 0 1 20 6.5v8A1.5 1.5 0 0 1 18.5 16H9l-4 3v-3H5.5A1.5 1.5 0 0 1 4 14.5z"/><path d="M9 10.5h6M9 7.5h2"/>'),
  spark: _s('<path d="M12 3v6M12 15v6M3 12h6M15 12h6"/><path d="M7.5 7.5l2.2 2.2M14.3 14.3l2.2 2.2M16.5 7.5l-2.2 2.2M9.7 14.3l-2.2 2.2"/>'),
  link: _s('<path d="M9 12a3 3 0 0 1 3-3h2.5a3.5 3.5 0 0 1 0 7H13"/><path d="M15 12a3 3 0 0 1-3 3H9.5a3.5 3.5 0 0 1 0-7H11"/>'),
  arrow: _s('<path d="M5 12h13"/><path d="M13 6l6 6-6 6"/>'),
  chip: _s('<rect x="7" y="7" width="10" height="10" rx="1.5"/><path d="M10 2.5v3M14 2.5v3M10 18.5v3M14 18.5v3M2.5 10h3M2.5 14h3M18.5 10h3M18.5 14h3"/>'),
  wave: _s('<path d="M2.5 8c2 0 2 1.6 4 1.6S8.5 8 10.5 8s2 1.6 4 1.6S16.5 8 18.5 8s2 1.6 3 1.6"/><path d="M2.5 13c2 0 2 1.6 4 1.6S8.5 13 10.5 13s2 1.6 4 1.6S16.5 13 18.5 13s2 1.6 3 1.6"/><path d="M2.5 18c2 0 2 1.6 4 1.6S8.5 18 10.5 18s2 1.6 4 1.6S16.5 18 18.5 18s2 1.6 3 1.6"/>'),
  shield: _s('<path d="M12 3l7 2.5v5c0 4.5-3 8-7 10-4-2-7-5.5-7-10v-5z"/><path d="M9 12l2 2 4-4"/>'),
  globe: _s('<circle cx="12" cy="12" r="8.5"/><path d="M3.5 12h17M12 3.5c2.5 2.4 2.5 14.6 0 17M12 3.5c-2.5 2.4-2.5 14.6 0 17"/>'),
  doc: _s('<path d="M7 3.5h7L18 8v12.5H7z"/><path d="M13.5 3.5V8H18M9.5 12h6M9.5 15h6M9.5 9h2"/>'),
  send: _s('<path d="M20.5 3.5L10 14"/><path d="M20.5 3.5l-6.5 17-3.5-6.5L4 10.5z"/>'),
  target: _s('<circle cx="12" cy="12" r="8.5"/><circle cx="12" cy="12" r="4.5"/><circle cx="12" cy="12" r="1"/>'),
  radar: _s('<circle cx="12" cy="12" r="8.5"/><path d="M12 12l5.5-3.5"/><path d="M12 12a6 6 0 1 0 5-2.8" opacity=".5"/>'),
};
function svg(name) { return ICON[name] || ''; }

// --- motion polish ---
function staggerIn() {
  // Native scroll-driven reveals handle entrance when supported — don't fight them.
  if (window.CSS && CSS.supports && CSS.supports('animation-timeline: view()')) return;
  const els = document.querySelectorAll('.view:not([hidden]) .card, .view:not([hidden]) .tcard, .view:not([hidden]) .stat');
  els.forEach((e, i) => { e.style.animationDelay = Math.min(i * 35, 420) + 'ms'; });
}
function animateCounts(root) {
  root.querySelectorAll('.stat .n').forEach((node) => {
    const raw = node.textContent;
    const m = raw.match(/-?[\d,]+/);
    if (!m) return;
    const target = parseInt(m[0].replace(/,/g, ''), 10);
    if (isNaN(target)) return;
    const pre = raw.slice(0, m.index), post = raw.slice(m.index + m[0].length);
    const dur = 850, t0 = performance.now();
    const tick = (now) => {
      const p = Math.min(1, (now - t0) / dur), e = 1 - Math.pow(1 - p, 3);
      node.textContent = pre + Math.round(target * e).toLocaleString() + post;
      if (p < 1) requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  });
}

const STAGES = ['watching', 'qualifying', 'drafting', 'submitted', 'won', 'pilot', 'transition', 'pom', 'program', 'lost', 'pass'];
const COLS = [
  { key: 'discovery', label: 'Discovery', match: ['watching', 'qualifying'], drop: 'watching' },
  { key: 'bid', label: 'Bid', match: ['drafting', 'submitted'], drop: 'drafting' },
  { key: 'pilot', label: 'Award · Pilot', match: ['won', 'pilot'], drop: 'won' },
  { key: 'transition', label: 'Transition · POM', match: ['transition', 'pom'], drop: 'transition' },
  { key: 'program', label: 'Program of Record', match: ['program'], drop: 'program' },
  { key: 'closed', label: 'Closed', match: ['lost', 'pass'], drop: 'pass' },
];
function toast(msg) {
  const c = document.getElementById('toasts'); if (!c) return;
  const t = el('div', 'toast'); t.textContent = msg; c.append(t);
  setTimeout(() => { t.classList.add('out'); setTimeout(() => t.remove(), 400); }, 2600);
}
const WALLS = ['money', 'requirements', 'contracts', 'incentives'];
const WALL_LABEL = { money: 'Money', requirements: 'Requirements', contracts: 'Contracts', incentives: 'Incentives' };

let ASSIST = { enabled: false, model: '' };
let CUR_OPP = null;

// --- WebGL nebula background (domain-warped FBM, Thornveil steel palette) ---
function initBG() {
  const cv = document.getElementById('bg');
  if (!cv) return;
  const gl = cv.getContext('webgl', { antialias: false, alpha: false, powerPreference: 'high-performance' });
  if (!gl) { cv.style.display = 'none'; return; } // CSS #fx fallback shows through
  const vsrc = 'attribute vec2 p;void main(){gl_Position=vec4(p,0.,1.);}';
  const fsrc = `precision highp float;uniform vec2 u_res;uniform float u_t;uniform vec2 u_m;
  float hash(vec2 p){p=fract(p*vec2(123.34,456.21));p+=dot(p,p+45.32);return fract(p.x*p.y);}
  float noise(vec2 p){vec2 i=floor(p),f=fract(p);float a=hash(i),b=hash(i+vec2(1,0)),c=hash(i+vec2(0,1)),d=hash(i+vec2(1,1));vec2 u=f*f*(3.-2.*f);return mix(a,b,u.x)+(c-a)*u.y*(1.-u.x)+(d-b)*u.x*u.y;}
  float fbm(vec2 p){float v=0.,a=.5;mat2 m=mat2(1.6,1.2,-1.2,1.6);for(int i=0;i<6;i++){v+=a*noise(p);p=m*p;a*=.5;}return v;}
  void main(){
    vec2 uv=(gl_FragCoord.xy-.5*u_res)/u_res.y;
    float t=u_t*.025;
    vec2 q=vec2(fbm(uv*1.1+t),fbm(uv*1.1+vec2(5.2,1.3)-t));
    vec2 r=vec2(fbm(uv*1.4+1.7*q+vec2(1.7,9.2)+t*.6),fbm(uv*1.4+1.7*q+vec2(8.3,2.8)-t*.6));
    float f=fbm(uv*1.3+2.2*r+t);
    vec3 c1=vec3(.022,.038,.066);
    vec3 c2=vec3(.07,.15,.27);
    vec3 c3=vec3(.20,.33,.41);
    vec3 c4=vec3(.56,.66,.74);
    vec3 col=mix(c1,c2,smoothstep(.05,.7,f));
    col=mix(col,c3,smoothstep(.45,1.05,length(r)));
    col=mix(col,c4,smoothstep(.82,1.12,f)*.5);
    float md=length(uv-u_m);col+=c4*.05/(md*md+.25);
    col*=1.0-.62*length(uv*vec2(.7,1.0));
    col+=(hash(gl_FragCoord.xy+u_t)-.5)*.03;
    col*=.72;
    gl_FragColor=vec4(col,1.);
  }`;
  const mk = (ty, src) => { const s = gl.createShader(ty); gl.shaderSource(s, src); gl.compileShader(s); return s; };
  const prog = gl.createProgram();
  gl.attachShader(prog, mk(gl.VERTEX_SHADER, vsrc));
  gl.attachShader(prog, mk(gl.FRAGMENT_SHADER, fsrc));
  gl.linkProgram(prog);
  if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) { cv.style.display = 'none'; return; }
  gl.useProgram(prog);
  const buf = gl.createBuffer();
  gl.bindBuffer(gl.ARRAY_BUFFER, buf);
  gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
  const loc = gl.getAttribLocation(prog, 'p');
  gl.enableVertexAttribArray(loc); gl.vertexAttribPointer(loc, 2, gl.FLOAT, false, 0, 0);
  const uRes = gl.getUniformLocation(prog, 'u_res'), uT = gl.getUniformLocation(prog, 'u_t'), uM = gl.getUniformLocation(prog, 'u_m');
  const mouse = { x: 0, y: 0 };
  addEventListener('pointermove', (e) => { mouse.x = (e.clientX - innerWidth / 2) / innerHeight; mouse.y = -(e.clientY - innerHeight / 2) / innerHeight; }, { passive: true });
  const dpr = () => Math.min(devicePixelRatio || 1, 1.75);
  const resize = () => { const w = innerWidth * dpr(), h = innerHeight * dpr(); cv.width = w; cv.height = h; gl.viewport(0, 0, w, h); };
  addEventListener('resize', resize); resize();
  let mx = 0, my = 0;
  const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const t0 = performance.now();
  const frame = (now) => {
    mx += (mouse.x - mx) * .04; my += (mouse.y - my) * .04;
    gl.uniform2f(uRes, cv.width, cv.height);
    gl.uniform1f(uT, reduce ? 6 : (now - t0) / 1000);
    gl.uniform2f(uM, mx, my);
    gl.drawArrays(gl.TRIANGLES, 0, 3);
    if (!reduce) requestAnimationFrame(frame);
  };
  requestAnimationFrame(frame);
}

// Cinematic "systems online" boot sequence — runs, then waits for the user to enter.
function bootSequence() {
  const boot = document.getElementById('boot');
  if (!boot) return;
  let entered = false;
  const enter = () => { if (entered) return; entered = true; snd.enter(); glitchBurst(); boot.classList.add('gone'); setTimeout(() => boot.remove(), 950); };
  boot.addEventListener('click', enter);
  const con = document.getElementById('bconsole');
  const pct = document.getElementById('bpct');
  const steps = [
    ['AUTH', 'SUBSCRIPTION LINKED'], ['CAPABILITY GRAPH', 'GROUNDED'],
    ['DSIP UPLINK', 'LIVE'], ['SAM RADAR', 'ARMED'],
    ['SIGNET GATE', 'NOMINAL'], ['COMMAND DECK', 'ONLINE'],
  ];
  const ready = () => { boot.classList.add('ready'); if (pct) pct.textContent = '100'; };
  if (matchMedia('(prefers-reduced-motion: reduce)').matches) {
    steps.forEach(([k, v]) => { const l = el('div', 'bline'); l.style.animation = 'none'; l.style.opacity = '1'; l.style.transform = 'none'; l.innerHTML = `<span>${k}</span><span class="ok">${v}</span>`; con.append(l); });
    ready();
    return; // still waits for click — just no animation
  }
  // count the percentage up across the sequence
  const t0 = performance.now(), dur = 2500;
  const grow = (now) => { const p = Math.min(1, (now - t0) / dur); if (pct && !entered) pct.textContent = Math.round(p * 100); if (p < 1 && !entered) requestAnimationFrame(grow); };
  requestAnimationFrame(grow);
  let i = 0;
  const tick = () => {
    if (entered) return;
    if (i < steps.length) {
      const [k, v] = steps[i];
      const line = el('div', 'bline');
      line.innerHTML = `<span>${k}</span><span class="pend">··· init</span>`;
      con.append(line);
      setTimeout(() => { const p = line.querySelector('.pend'); if (p) { p.className = 'ok'; p.textContent = v; } }, 200);
      i++;
      setTimeout(tick, 280);
    } else {
      setTimeout(ready, 360); // hold here — wait for the click
    }
  };
  setTimeout(tick, 640);
}

// Decrypt/resolve effect for the hero headline — text materializes from noise.
let HERO_DECODED = false;
function decrypt(node, finalText, dur = 850) {
  if (matchMedia('(prefers-reduced-motion: reduce)').matches) { node.textContent = finalText; return; }
  const glyphs = 'ABCDEFGHJKLMNPQRSTUVWXYZ0123456789/<>=-_::';
  const t0 = performance.now();
  const step = (now) => {
    const p = Math.min(1, (now - t0) / dur);
    const reveal = Math.floor(p * finalText.length);
    let out = '';
    for (let k = 0; k < finalText.length; k++) {
      const ch = finalText[k];
      if (k < reveal || ch === ' ') out += ch;
      else out += glyphs[(Math.floor(now / 28) + k * 7) % glyphs.length];
    }
    node.textContent = out;
    if (p < 1) requestAnimationFrame(step); else node.textContent = finalText;
  };
  requestAnimationFrame(step);
}

// Custom HUD targeting-reticle cursor (precise dot + lagging ring that locks on).
function initCursor() {
  if (!matchMedia('(pointer: fine)').matches) return;
  const dot = document.getElementById('cdot'), ring = document.getElementById('cring');
  if (!dot || !ring) return;
  document.body.classList.add('hascursor');
  let mx = -100, my = -100, rx = -100, ry = -100, lock = false;
  const lockSel = 'a,button,.tab,.tcard,.card,.kc,select,textarea,input,label,.realize,.hwtoggle,.bskip';
  addEventListener('pointermove', (e) => {
    mx = e.clientX; my = e.clientY;
    dot.style.transform = `translate(${mx}px,${my}px)`;
    const il = !!(e.target.closest && e.target.closest(lockSel));
    if (il !== lock) { lock = il; document.body.classList.toggle('lock', il); if (il) snd.lock(); }
  }, { passive: true });
  addEventListener('pointerdown', () => document.body.classList.add('down'));
  addEventListener('pointerup', () => document.body.classList.remove('down'));
  (function loop() { rx += (mx - rx) * .2; ry += (my - ry) * .2; ring.style.transform = `translate(${rx}px,${ry}px)`; requestAnimationFrame(loop); })();
}

// Magnetic pull on .magnetic buttons (cursor attraction).
function initMagnetic() {
  if (matchMedia('(prefers-reduced-motion: reduce)').matches) return;
  document.addEventListener('pointermove', (e) => {
    const m = e.target.closest && e.target.closest('.magnetic');
    document.querySelectorAll('.magnetic.pull').forEach((el2) => { if (el2 !== m) { el2.style.transform = ''; el2.classList.remove('pull'); } });
    if (m) { const r = m.getBoundingClientRect(); const dx = e.clientX - (r.left + r.width / 2), dy = e.clientY - (r.top + r.height / 2); m.style.transform = `translate(${dx * .22}px,${dy * .22}px)`; m.classList.add('pull'); }
  }, { passive: true });
}

// Cursor-reactive spotlight + subtle 3D tilt on cards.
function initCardFX() {
  const onMove = (e) => {
    const c = e.target.closest && e.target.closest('.card, .tcard');
    if (!c) return;
    const r = c.getBoundingClientRect();
    const x = (e.clientX - r.left) / r.width, y = (e.clientY - r.top) / r.height;
    c.style.setProperty('--mx', (x * 100) + '%');
    c.style.setProperty('--my', (y * 100) + '%');
    c.style.transform = `perspective(900px) rotateX(${(0.5 - y) * 4}deg) rotateY(${(x - 0.5) * 5}deg) translateY(-3px)`;
  };
  const onLeave = (e) => { const c = e.target.closest && e.target.closest('.card, .tcard'); if (c) c.style.transform = ''; };
  document.addEventListener('pointermove', onMove, { passive: true });
  document.addEventListener('pointerout', onLeave, { passive: true });
}

// Sliding glow indicator under the active nav tab.
function moveIndicator() {
  const ind = document.querySelector('.tab-ind');
  const active = document.querySelector('.tab.active');
  if (!ind || !active) return;
  ind.style.opacity = '1';
  ind.style.width = active.offsetWidth + 'px';
  ind.style.transform = `translateX(${active.offsetLeft - 4}px)`;
}

async function boot() {
  initBG();
  bootSequence();
  initCursor();
  initMagnetic();
  initCardFX();
  addEventListener('resize', moveIndicator);
  voiceInit();
  waveInit();
  // status-bar toggles (delegated, since the status bar re-renders)
  $('#statusbar').addEventListener('click', (e) => {
    if (e.target.closest && e.target.closest('#sndtoggle')) {
      SOUND_ON = !SOUND_ON; localStorage.setItem('snd', SOUND_ON ? '1' : '0');
      const b = document.querySelector('#sndtoggle b'); if (b) b.textContent = SOUND_ON ? 'ON' : 'OFF';
      if (SOUND_ON) { actx(); snd.tick(); }
      return;
    }
    if (e.target.closest && e.target.closest('#spktoggle')) {
      VOICE.speak = !VOICE.speak; localStorage.setItem('rz-speak', VOICE.speak ? '1' : '0');
      const b = document.querySelector('#spktoggle b'); if (b) b.textContent = VOICE.speak ? 'ON' : 'OFF';
      if (!VOICE.speak) ttsCancel(); else { actx(); snd.recv(); }
    }
  });
  // idle attract mode
  ['pointermove', 'pointerdown', 'keydown', 'wheel'].forEach((ev) => addEventListener(ev, resetIdle, { passive: true }));
  resetIdle();
  // subtle grid parallax for depth
  if (!matchMedia('(prefers-reduced-motion: reduce)').matches) {
    const grid = document.getElementById('grid');
    if (grid) addEventListener('pointermove', (e) => {
      const x = (e.clientX / innerWidth - .5) * -14, y = (e.clientY / innerHeight - .5) * -14;
      grid.style.transform = `translate(${x}px,${y}px)`;
    }, { passive: true });
  }
  // resolve Claude backend BEFORE first paint so the status bar shows the real
  // state (load() renders the status bar, which reads ASSIST.backend).
  ASSIST = await fetch('/api/assist-status').then((r) => r.json()).catch(() => ({ enabled: false }));
  await load();
  document.querySelectorAll('.tab').forEach((t) =>
    t.addEventListener('click', () => switchView(t.dataset.view)));
  initPalette();
  $('#refresh').addEventListener('click', async (e) => {
    e.target.textContent = '…'; await fetch('/api/refresh', { method: 'POST' }); await load(); render(); e.target.textContent = '↻ Refresh';
  });
  $('#assist-close').addEventListener('click', closeAssist);
  $('#log-export')?.addEventListener('click', exportLog);
  $('#overlay').addEventListener('click', closeAssist);
  $('#assist-send').addEventListener('click', () => sendAssist());
  $('#assist-input').addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sendAssist(); } });
  const mic = $('#mic-btn');
  if (mic) {
    if (!VOICE.supported) mic.classList.add('off');
    mic.addEventListener('click', (e) => micToggle(e.shiftKey)); // shift-click = speak & auto-send
    mic.title = 'Speak (on-device) · shift-click = auto-send';
  }
  addEventListener('keydown', (e) => {
    if (e.key === 'Escape') { if (VOICE.listening) micStop(true); ttsCancel(); }
    // Ctrl+M (or ⌘M): toggle mic from anywhere the assist is open
    if ((e.ctrlKey || e.metaKey) && (e.key === 'm' || e.key === 'M') && ASSIST.enabled && CUR_OPP) { e.preventDefault(); micToggle(false); }
  });
  render();
  requestAnimationFrame(moveIndicator);
  setTimeout(moveIndicator, 500); // after web fonts settle
}

// ---- Claude bid assistant ----
const QUICK = [
  { a: 'bidpass', label: 'Bid or pass?' },
  { a: 'wintheme', label: 'Win theme' },
  { a: 'outline', label: 'Outline volume' },
  { a: 'draft', label: 'Draft tech approach' },
  { a: 'gaps', label: 'Gaps' },
];

function convo(id) { try { return JSON.parse(localStorage.getItem('assist:' + id) || '[]'); } catch { return []; } }
function saveConvo(id, h) { localStorage.setItem('assist:' + id, JSON.stringify(h.slice(-20))); }

const TQUICK = [
  { a: 'deepresearch', label: 'Deep research' },
  { a: 'teaming', label: 'Find a prime / team' },
  { a: 'transition', label: 'Structure for transition' },
  { a: 'sponsor', label: 'Who owns the money' },
  { a: 'outreach', label: 'Outreach + draft message' },
  { a: 'pom', label: 'POM readiness' },
  { a: 'pmadopt', label: 'PM adoption pitch' },
  { a: 'nextstep', label: 'Next best action' },
];

function openAssist(o) {
  CUR_OPP = o;
  const via = ASSIST.backend === 'subscription' ? ' · via Max subscription' : ASSIST.backend === 'api' ? ' · via API key' : '';
  $('.ah .who').innerHTML = svg('spark') + 'CLAUDE · BID STRATEGIST' + escapeHtml(via);
  $('#assist-title').textContent = o.title;
  $('#assist-meta').innerHTML = [o.source, o.type, o.agency, o.matched_asset ? 'fit: ' + o.matched_asset : '', daysLabel(o)].filter(Boolean).join(' · ');
  // real, source-provided POCs + the sanctioned channel (anti-spam)
  const poc = $('#assist-poc'); poc.textContent = '';
  if ((o.contacts && o.contacts.length) || o.channel) {
    if (o.contacts) o.contacts.forEach((c) => {
      const d = el('div', 'poc'); d.innerHTML = `<b>${c.name}</b> ${c.role || ''}${c.email ? ' · <a href="mailto:' + c.email + '">' + c.email + '</a>' : ''}`;
      poc.append(d);
    });
    if (o.channel) { const d = el('div', 'poc chan'); d.textContent = '↳ ' + o.channel; poc.append(d); }
  }
  const qa = $('#assist-qa'); qa.textContent = '';
  qa.append(scorecard(o)); // four-walls readiness works with or without Claude
  if (ASSIST.enabled) {
    const bidRow = el('div', 'qarow');
    QUICK.forEach((q) => { const b = el('button', null, q.label); b.addEventListener('click', () => sendAssist(q.a)); bidRow.append(b); });
    const draftBtn = el('button', 'mv'); draftBtn.innerHTML = svg('doc') + 'Draft volume → files';
    draftBtn.title = 'Claude writes the full submittable volume to editable files (runs on your subscription)';
    draftBtn.addEventListener('click', () => draftVolume(o));
    bidRow.append(draftBtn);
    const intelBtn = el('button', null); intelBtn.innerHTML = svg('radar') + 'Competitive field';
    intelBtn.title = 'Who has won DoD SBIR/STTR awards in this space (SBIR.gov)';
    intelBtn.addEventListener('click', () => competitiveIntel(o));
    bidRow.append(intelBtn);
    const detBtn = el('button', null); detBtn.innerHTML = svg('doc') + 'Topic detail';
    detBtn.title = 'Full topic readout (objective, description, Phase I, keywords, ITAR)';
    detBtn.addEventListener('click', () => topicDetail(o));
    bidRow.append(detBtn);
    const ingBtn = el('button', null); ingBtn.innerHTML = svg('doc') + 'Ingest RFP';
    ingBtn.title = 'Paste/drop the real solicitation text — grounds every AI feature on the actual requirement';
    ingBtn.addEventListener('click', () => ingestRFP(o));
    bidRow.append(ingBtn);
    const workupBtn = el('button', 'mv'); workupBtn.innerHTML = svg('target') + 'Full workup';
    workupBtn.title = 'Agentic chain: deep research → grounded draft → red-team critique';
    workupBtn.addEventListener('click', () => fullWorkup(o));
    bidRow.append(workupBtn);
    const docxBtn = el('button', null); docxBtn.innerHTML = svg('doc') + 'Export .docx';
    docxBtn.title = 'Download the drafted volume as a Word document (with a compliance matrix) — run a draft first';
    docxBtn.addEventListener('click', () => exportDocx(o));
    bidRow.append(docxBtn);
    const compBtn = el('button', null); compBtn.innerHTML = svg('shield') + 'Compliance matrix';
    compBtn.title = 'Extract every shall/must/required statement from the solicitation';
    compBtn.addEventListener('click', () => complianceMatrix(o));
    bidRow.append(compBtn);
    qa.append(rowLabel('Bid'), bidRow);
    const trow = el('div', 'qarow');
    TQUICK.forEach((q) => { const b = el('button', null, q.label); b.addEventListener('click', () => sendAssist(q.a)); trow.append(b); });
    qa.append(rowLabel('Cross the valley'), trow);
    // Conversion engine: turn "has capability" into "wins the contract".
    const winRow = el('div', 'qarow');
    const wpBtn = el('button', 'mv'); wpBtn.innerHTML = svg('target') + 'Win plan';
    wpBtn.title = 'A dated capture-to-award plan: Q&A window, sponsor engagement, proof points, teaming, submit — grounded in the doctrine + your weakest wall';
    wpBtn.addEventListener('click', () => winPlan(o));
    winRow.append(wpBtn);
    const vcBtn = el('button', 'mv'); vcBtn.innerHTML = svg('shield') + 'Verify compliance';
    vcBtn.title = 'Closed-loop gate: maps every shall/must to the drafted section that answers it, flags the gaps that get a proposal eliminated';
    vcBtn.addEventListener('click', () => verifyCompliance(o));
    winRow.append(vcBtn);
    const rmBtn = el('button', null); rmBtn.innerHTML = svg('spark') + 'Close the gaps';
    rmBtn.title = 'Regenerate ready-to-paste content for every uncovered requirement so the volume becomes submittable';
    rmBtn.addEventListener('click', () => remediate(o));
    winRow.append(rmBtn);
    qa.append(rowLabel('Win & submit'), winRow);
    const mv = el('div', 'qarow');
    ['drafting', 'submitted', 'won', 'pilot', 'transition', 'pom', 'program'].forEach((st) => {
      const b = el('button', 'mv', '→ ' + st); b.addEventListener('click', () => moveStage(o, st)); mv.append(b);
    });
    qa.append(rowLabel('Move stage'), mv);
  }
  renderThread();
  $('#overlay').style.display = 'block';
  $('#assist').classList.add('open');
}

function rowLabel(t) { return el('div', 'qalabel', t); }

// the four-walls transition-readiness scorecard + lifetime value, edited inline.
function scorecard(o) {
  const p = STATE[o.id] || {};
  const walls = p.walls || {};
  const box = el('div', 'scorecard');
  const r = readiness(walls);
  const head = el('div', 'sc-head');
  head.innerHTML = `<span>Transition readiness</span><b class="${r.score >= 75 ? 'ok' : r.score >= 40 ? 'warn' : 'bad'}">${r.score}/100</b>`;
  box.append(head);
  if (r.score < 100) box.append(el('div', 'sc-weak', 'weakest wall → ' + r.weakest));
  WALLS.forEach((wkey) => {
    const w = el('div', 'wall');
    w.append(el('span', 'wname', WALL_LABEL[wkey]));
    const sel = el('select');
    ['', 'gap', 'partial', 'ready'].forEach((s) => sel.appendChild(new Option(s || '—', s)));
    sel.value = walls[wkey] || '';
    sel.addEventListener('change', () => {
      const nw = { ...(STATE[o.id]?.walls || {}) }; nw[wkey] = sel.value;
      saveState(o.id, { walls: nw }, { title: o.title, agency: o.agency, url: o.url });
    });
    w.append(sel); box.append(w);
  });
  const val = el('div', 'wall');
  val.append(el('span', 'wname', 'Value $K'));
  const vi = el('input'); vi.type = 'number'; vi.placeholder = 'e.g. 1800'; vi.value = p.value || '';
  let t; vi.addEventListener('input', () => { clearTimeout(t); t = setTimeout(() => saveState(o.id, { value: parseInt(vi.value) || 0 }, { title: o.title, agency: o.agency, url: o.url }), 600); });
  val.append(vi); box.append(val);
  if (ASSIST.enabled) {
    const aa = el('button', 'aabtn'); aa.innerHTML = svg('spark') + 'Auto-assess — Claude fills value + the four walls';
    aa.addEventListener('click', async () => {
      aa.textContent = 'assessing… (runs on your subscription)'; aa.disabled = true;
      const r = await fetch('/api/assess', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id: o.id }) }).then((x) => x.json()).catch(() => ({ error: 'failed' }));
      await reloadState();
      openAssist(o);
      if (r && r.error) { const d = el('div', 'msg err'); d.textContent = r.error; $('#thread').append(d); }
    });
    box.append(aa);
  }
  return box;
}

async function reloadState() { STATE = await fetch('/api/state').then((r) => r.json()).catch(() => STATE); }

function readiness(w) {
  const v = (x) => x === 'ready' ? 100 : x === 'partial' ? 50 : 0;
  let sum = 0, weak = 'Money', low = 101;
  WALLS.forEach((k) => { const s = v(w[k]); sum += s; if (s < low) { low = s; weak = WALL_LABEL[k]; } });
  return { score: Math.round(sum / 4), weakest: weak };
}
function closeAssist() { if (VOICE.listening) micStop(false); ttsCancel(); WAVE.mode = 'idle'; $('#assist').classList.remove('open'); $('#overlay').style.display = 'none'; CUR_OPP = null; }

// Export the current pursuit's Claude conversation as a timestamped markdown log.
function exportLog() {
  if (!CUR_OPP) return;
  const o = CUR_OPP, h = convo(o.id);
  if (!h.length) { toast('No conversation to export yet'); return; }
  const st = STATE[o.id] || {};
  const lines = [
    `# Mission log — ${o.title}`, '',
    `- **Agency:** ${o.agency || '—'}`,
    `- **Stage:** ${st.stage || 'watch'}`,
    o.url ? `- **Source:** ${o.url}` : '',
    o.matched_asset ? `- **Matched capability:** ${o.matched_asset}` : '',
    `- **Exported:** ${new Date().toISOString()}`, '', '---', '',
  ].filter(Boolean);
  h.forEach((m) => { lines.push(m.role === 'user' ? `**You:** ${m.content}` : `**Claude:**\n\n${m.content}`, ''); });
  const blob = new Blob([lines.join('\n')], { type: 'text/markdown' });
  const a = document.createElement('a');
  const slug = (o.title || 'pursuit').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 48);
  a.href = URL.createObjectURL(blob); a.download = `realizer-log-${slug}.md`;
  document.body.append(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(a.href), 4000);
  snd.apply(); toast('Mission log exported → ' + a.download);
}

function renderThread() {
  const t = $('#thread'); t.textContent = '';
  if (!ASSIST.enabled) {
    t.innerHTML = `<div class="disabled-note">Claude isn't connected. Easiest: install + log in to <b>Claude Code</b> — the workspace will use your <b>Max subscription</b> (no per-token cost):<br><br><code>npm i -g @anthropic-ai/claude-code</code><br><code>claude login</code><br><code>go run . workspace</code><br><br>Or set <b>ANTHROPIC_API_KEY</b> for the pay-per-token API. Everything stays on this machine.</div>`;
    return;
  }
  convo(CUR_OPP.id).forEach((m) => {
    const d = el('div', 'msg ' + (m.role === 'user' ? 'u' : 'a'));
    if (m.role === 'user') d.textContent = '› ' + m.content;
    else d.innerHTML = mdChat(m.content);
    t.append(d);
  });
  t.scrollTop = t.scrollHeight;
}

async function moveStage(o, stage) {
  await saveState(o.id, { stage }, { title: o.title, agency: o.agency, url: o.url });
  const d = el('div', 'msg a'); d.textContent = `Moved to ${stage}.`; $('#thread').append(d);
  $('#thread').scrollTop = $('#thread').scrollHeight;
}

async function sendAssist(action) {
  if (!ASSIST.enabled || !CUR_OPP) return;
  const input = $('#assist-input');
  const message = action ? '' : input.value.trim();
  if (!action && !message) return;
  const id = CUR_OPP.id;
  const hist = convo(id);
  const userLabel = action ? ([...QUICK, ...TQUICK].find((q) => q.a === action)?.label || action) : message;
  hist.push({ role: 'user', content: userLabel });
  saveConvo(id, hist);
  input.value = '';
  renderThread();

  const ans = el('div', 'msg a streaming'); ans.textContent = '◢ incoming transmission — decrypting…'; $('#thread').append(ans); $('#thread').scrollTop = 1e9;
  snd.send(); $('#assist').classList.add('thinking');
  ttsCancel(); WAVE.mode = 'streaming';
  let acc = '';
  try {
    const resp = await fetch('/api/assist', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ opp_id: id, action: action || '', message,
        history: convo(id).slice(0, -1).map((m) => ({ role: m.role, content: m.content })) }),
    });
    const reader = resp.body.getReader();
    const dec = new TextDecoder();
    let buf = '';
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      const parts = buf.split('\n\n'); buf = parts.pop();
      for (const p of parts) {
        const line = p.replace(/^data:\s*/, '').trim();
        if (!line) continue;
        let ev; try { ev = JSON.parse(line); } catch { continue; }
        if (ev.error) { ans.className = 'msg err'; ans.textContent = ev.error; snd.err(); }
        else if (ev.t) { acc += ev.t; ans.innerHTML = mdChat(acc); ans.classList.add('streaming'); corePulse(); waveKick(); ttsFeed(acc); $('#thread').scrollTop = 1e9; }
      }
    }
  } catch (e) { ans.className = 'msg err'; ans.textContent = 'stream failed: ' + e.message; snd.err(); }
  ans.classList.remove('streaming'); $('#assist').classList.remove('thinking');
  WAVE.mode = 'idle';
  if (acc) { snd.recv(); ttsFlush(acc); const h = convo(id); h.push({ role: 'assistant', content: acc }); saveConvo(id, h); }
  const dirs = [...acc.matchAll(/\[\[do:([^\]]+)\]\]/g)].map((m) => m[1]);
  if (dirs.length && CUR_OPP) renderDirectives(CUR_OPP, dirs);
}

// Confirmable action chips Claude proposed (tool use).
function dirLabel(d) {
  const p = d.split(':');
  if (p[0] === 'stage') return 'Move stage → ' + p[1];
  if (p[0] === 'wall') return 'Set ' + p[1] + ' → ' + p[2];
  if (p[0] === 'value') return 'Set value → $' + p[1] + 'K';
  if (p[0] === 'decision') return 'Record decision: ' + p[1];
  if (p[0] === 'draft') return 'Draft the volume → files';
  return d;
}
function applyDirective(o, d) {
  const p = d.split(':'), ex = { title: o.title, agency: o.agency, url: o.url };
  if (p[0] === 'stage') { saveState(o.id, { stage: p[1] }, ex); toast('Stage → ' + p[1]); }
  else if (p[0] === 'wall') { const w = { ...(STATE[o.id]?.walls || {}) }; w[p[1]] = p[2]; saveState(o.id, { walls: w }, ex); toast(p[1] + ' → ' + p[2]); }
  else if (p[0] === 'value') { saveState(o.id, { value: parseInt(p[1]) || 0 }, ex); toast('Value → $' + p[1] + 'K'); }
  else if (p[0] === 'decision') { saveState(o.id, { decision: p[1] }, ex); toast('Decision: ' + p[1]); }
  else if (p[0] === 'draft') { draftVolume(o); }
}
function renderDirectives(o, dirs) {
  const t = $('#thread'); const wrap = el('div', 'dirs');
  const lbl = el('div', 'dirlbl'); lbl.textContent = 'Claude proposes:'; wrap.append(lbl);
  dirs.forEach((d) => {
    const chip = el('button', 'dirchip'); chip.innerHTML = svg('spark') + dirLabel(d);
    chip.addEventListener('click', () => { applyDirective(o, d); snd.apply(); chip.disabled = true; chip.classList.add('done'); chip.innerHTML = svg('shield') + 'Applied'; });
    wrap.append(chip);
  });
  t.append(wrap); t.scrollTop = 1e9;
}

// ingestRFP lets Jesse paste/drop the real solicitation text for a pursuit; it's
// stored locally and grounds every AI feature (assist/draft/assess/day-read).
async function ingestRFP(o) {
  const t = $('#thread');
  let cur = { chars: 0, name: '' };
  try { cur = await fetch('/api/ingest?id=' + encodeURIComponent(o.id)).then((r) => r.json()); } catch { }
  const wrap = el('div', 'ingest');
  const have = cur.chars ? ` <b>Ingested: ${cur.chars.toLocaleString()} chars${cur.name ? ' (' + escapeHtml(cur.name) + ')' : ''}</b>` : '';
  wrap.innerHTML = `<div class="inglbl">${svg('doc')} Ground Claude on the real solicitation</div>
    <div class="inghint">Paste the RFP / sources-sought / topic text, or drop a .txt file. Stored locally; grounds assist, draft, assessment, and day-read for this pursuit.${have}</div>
    <textarea class="ingta" placeholder="Paste solicitation text here…"></textarea>
    <div class="ingbtns"><button class="ingsave">Save &amp; ground</button><button class="ingclear">Clear</button><span class="ingstat"></span></div>`;
  t.append(wrap); t.scrollTop = 1e9;
  const ta = wrap.querySelector('.ingta'), stat = wrap.querySelector('.ingstat');
  wrap.addEventListener('dragover', (e) => { e.preventDefault(); wrap.classList.add('drop'); });
  wrap.addEventListener('dragleave', () => wrap.classList.remove('drop'));
  wrap.addEventListener('drop', async (e) => {
    e.preventDefault(); wrap.classList.remove('drop');
    const f = e.dataTransfer.files && e.dataTransfer.files[0];
    if (f) { try { ta.value = await f.text(); ta.dataset.name = f.name; stat.textContent = 'loaded ' + f.name; } catch { stat.textContent = 'could not read file'; } }
  });
  const save = async (text) => {
    stat.textContent = 'saving…';
    const r = await fetch('/api/ingest', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ opp_id: o.id, text, name: ta.dataset.name || '' }) }).then((x) => x.json()).catch(() => ({}));
    stat.textContent = r.chars ? `grounded · ${Number(r.chars).toLocaleString()} chars` : 'cleared';
    snd.apply(); toast(r.chars ? 'RFP ingested — Claude now grounds on it' : 'RFP cleared');
  };
  wrap.querySelector('.ingsave').addEventListener('click', () => save(ta.value.trim()));
  wrap.querySelector('.ingclear').addEventListener('click', () => { ta.value = ''; save(''); });
}

// exportDocx downloads the drafted volume as a Word document (+ compliance matrix).
async function exportDocx(o) {
  // probe first so a missing draft gives a clean message instead of a broken download
  try {
    const head = await fetch('/api/export?id=' + encodeURIComponent(o.id), { method: 'HEAD' });
    if (head.status === 404) { toast('No draft yet — run “Draft volume → files” or “Full workup” first'); snd.err && snd.err(); return; }
  } catch { }
  const a = document.createElement('a');
  a.href = '/api/export?id=' + encodeURIComponent(o.id) + '&compliance=1';
  a.download = ''; document.body.append(a); a.click(); a.remove();
  snd.apply(); toast('Exporting .docx (with compliance matrix)…');
}

// complianceMatrix extracts every binding requirement from the solicitation and
// renders it in the thread (grounds on the ingested RFP if present).
async function complianceMatrix(o) {
  const t = $('#thread');
  const head = el('div', 'msg u'); head.textContent = '› Compliance matrix'; t.append(head);
  const out = el('div', 'msg a'); out.textContent = 'Extracting shall/must/required statements…'; t.append(out); t.scrollTop = 1e9;
  try {
    const r = await fetch('/api/compliance?id=' + encodeURIComponent(o.id)).then((x) => x.json());
    if (!r.has_detail) { out.innerHTML = '<b>No solicitation text to scan.</b> Use <b>Ingest RFP</b> to paste the real solicitation, or <b>Topic detail</b> to fetch it, then re-run.'; return; }
    if (!r.count) { out.innerHTML = '<b>No binding requirements found</b> in the available text (no shall/must/required statements).'; return; }
    const items = r.requirements.map((rq, i) => `<li><b>REQ-${String(i + 1).padStart(2, '0')}</b> — ${escapeHtml(rq)}</li>`).join('');
    out.innerHTML = `<h4>${r.count} binding requirement${r.count === 1 ? '' : 's'} extracted</h4><p>Map each to the section that answers it; the .docx export appends this as a fillable matrix.</p><ul class="complist">${items}</ul>`;
    snd.recv && snd.recv();
  } catch (e) { out.className = 'msg err'; out.textContent = 'compliance scan failed: ' + e.message; }
  t.scrollTop = 1e9;
}

// draftVolume streams a full submittable volume to files, showing per-section
// progress in the thread, then the output folder.
async function draftVolume(o) {
  const t = $('#thread');
  const head = el('div', 'msg u'); head.textContent = 'Drafting the volume to files…'; t.append(head);
  const prog = el('div', 'msg a'); prog.textContent = 'Starting…'; t.append(prog); t.scrollTop = 1e9;
  let lines = [];
  try {
    const resp = await fetch('/api/draft', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ opp_id: o.id }),
    });
    const reader = resp.body.getReader();
    const dec = new TextDecoder();
    let buf = '';
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      const parts = buf.split('\n\n'); buf = parts.pop();
      for (const p of parts) {
        const line = p.replace(/^data:\s*/, '').trim();
        if (!line) continue;
        let ev; try { ev = JSON.parse(line); } catch { continue; }
        if (ev.error) { prog.className = 'msg err'; prog.textContent = ev.error; }
        else if (ev.t) { lines.push(ev.t); prog.textContent = lines.slice(-14).join('\n'); t.scrollTop = 1e9; }
        else if (ev.dir) { const d = el('div', 'msg a'); d.innerHTML = `<b>Volume written.</b> Files are in:<br><code>${ev.dir}</code><br>Open <code>volume.md</code> for the combined draft, or the numbered section files to edit.`; const xb = el('button', 'dirchip'); xb.innerHTML = svg('doc') + 'Export .docx'; xb.addEventListener('click', () => exportDocx(o)); d.append(document.createElement('br'), xb); t.append(d); t.scrollTop = 1e9; toast('Volume drafted → files'); }
      }
    }
  } catch (e) { prog.className = 'msg err'; prog.textContent = 'draft failed: ' + e.message; }
}

// Agentic chain: deep research → grounded draft → red-team critique, streamed.
async function fullWorkup(o) {
  const t = $('#thread');
  const head = el('div', 'msg u'); head.textContent = '› Full workup (research → draft → critique)'; t.append(head);
  const prog = el('div', 'msg a'); prog.textContent = 'Starting agentic workup…'; t.append(prog); t.scrollTop = 1e9;
  const lines = [];
  try {
    const resp = await fetch('/api/workup', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ opp_id: o.id }) });
    const reader = resp.body.getReader(); const dec = new TextDecoder(); let buf = '';
    for (;;) {
      const { value, done } = await reader.read(); if (done) break;
      buf += dec.decode(value, { stream: true }); const parts = buf.split('\n\n'); buf = parts.pop();
      for (const p of parts) {
        const line = p.replace(/^data:\s*/, '').trim(); if (!line) continue;
        let ev; try { ev = JSON.parse(line); } catch { continue; }
        if (ev.error) { prog.className = 'msg err'; prog.textContent = ev.error; }
        else if (ev.t) { lines.push(ev.t); prog.textContent = lines.slice(-16).join('\n'); t.scrollTop = 1e9; }
        else if (ev.dir) { const d = el('div', 'msg a'); d.innerHTML = `<b>Workup complete.</b> Research + volume + reviewer notes in:<br><code>${ev.dir}</code><br>Open <code>00-research.md</code>, <code>volume.md</code>, and <code>00-reviewer-notes.md</code>.`; t.append(d); t.scrollTop = 1e9; toast('Full workup complete → files'); }
      }
    }
  } catch (e) { prog.className = 'msg err'; prog.textContent = 'workup failed: ' + e.message; }
}

// streamInto runs an SSE endpoint, rendering markdown deltas live into one bubble
// and noting the saved file when done. Shared by the conversion-engine actions.
async function streamInto(url, o, headLabel, waitMsg, savedLabel) {
  const t = $('#thread');
  const head = el('div', 'msg u'); head.textContent = '› ' + headLabel; t.append(head);
  const out = el('div', 'msg a streaming'); out.textContent = waitMsg; t.append(out); t.scrollTop = 1e9;
  snd.send(); WAVE.mode = 'streaming'; let acc = '', started = false;
  try {
    const resp = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ opp_id: o.id }) });
    const reader = resp.body.getReader(); const dec = new TextDecoder(); let buf = '';
    for (;;) {
      const { value, done } = await reader.read(); if (done) break;
      buf += dec.decode(value, { stream: true }); const parts = buf.split('\n\n'); buf = parts.pop();
      for (const p of parts) {
        const line = p.replace(/^data:\s*/, '').trim(); if (!line) continue;
        let ev; try { ev = JSON.parse(line); } catch { continue; }
        if (ev.error) { out.className = 'msg err'; out.textContent = ev.error; snd.err(); }
        else if (ev.t) { if (!started) { started = true; out.textContent = ''; } acc += ev.t; out.innerHTML = mdChat(acc); out.classList.add('streaming'); corePulse(); waveKick(); $('#thread').scrollTop = 1e9; }
        else if (ev.saved) { const d = el('div', 'msg a'); d.innerHTML = `<b>${savedLabel}</b> Saved to:<br><code>${escapeHtml(ev.saved)}</code>`; t.append(d); toast(savedLabel); }
      }
    }
  } catch (e) { out.className = 'msg err'; out.textContent = 'failed: ' + e.message; snd.err(); }
  out.classList.remove('streaming'); WAVE.mode = 'idle'; if (acc) snd.recv();
  t.scrollTop = 1e9;
}

// Win plan: a dated capture-to-award sequence (the conversion engine).
function winPlan(o) { streamInto('/api/winplan', o, 'Win plan (capture → award)', 'Building your dated capture-to-award plan…', 'Win plan ready →'); }

// Closed-loop compliance gate: maps every shall/must to the drafted section.
function verifyCompliance(o) { streamInto('/api/verify-compliance', o, 'Compliance gate (draft vs. solicitation)', 'Checking the draft against every binding requirement…', 'Compliance report ready →'); }

// Close the gaps: regenerate ready-to-paste content for every uncovered requirement.
function remediate(o) { streamInto('/api/remediate', o, 'Close the gaps (make it submittable)', 'Writing drop-in content for every uncovered requirement…', 'Compliance fixes ready →'); }

// Full topic readout (objective/description/Phase I/keywords/ITAR), cached.
async function topicDetail(o) {
  const t = $('#thread');
  const head = el('div', 'msg u'); head.textContent = '› Topic detail'; t.append(head);
  const m = el('div', 'msg a'); m.textContent = 'Loading topic readout…'; t.append(m); t.scrollTop = 1e9;
  try {
    const r = await fetch('/api/detail?id=' + encodeURIComponent(o.id)).then((x) => x.json());
    let head2 = `<b>${escapeHtml(r.title || o.title)}</b><br><span style="color:var(--dim);font-size:11px;font-family:var(--mono)">${[r.agency, r.type, r.setaside, r.closes ? 'closes ' + r.closes : ''].filter(Boolean).map(escapeHtml).join(' · ')}</span>`;
    if (!r.detail) { m.innerHTML = head2 + '<br><br><span style="color:var(--dim)">No extended topic text available for this source. ' + (r.url ? `<a href="${r.url}" target="_blank" rel="noopener">Open source ↗</a>` : '') + '</span>'; return; }
    // bold the LABEL: prefixes the detail uses
    const body = escapeHtml(r.detail).replace(/(^|\n\n)([A-Z][A-Z &/]+):/g, '$1<b style="color:var(--brand)">$2</b>');
    m.innerHTML = head2 + '<br><br>' + body.replace(/\n/g, '<br>');
    t.scrollTop = 1e9;
  } catch (e) { m.className = 'msg err'; m.textContent = 'detail failed: ' + e.message; }
}

// Competitive field — recent DoD SBIR/STTR awards in the opp's space (SBIR.gov).
async function competitiveIntel(o) {
  const t = $('#thread');
  const head = el('div', 'msg u'); head.textContent = '› Competitive field'; t.append(head);
  const m = el('div', 'msg a'); m.textContent = 'Querying SBIR.gov award history…'; t.append(m); t.scrollTop = 1e9;
  try {
    const r = await fetch('/api/awards?id=' + encodeURIComponent(o.id)).then((x) => x.json());
    if (!r.awards || !r.awards.length) {
      m.textContent = r.ok ? `No recent DoD SBIR/STTR awards found for “${r.keyword}”.` : 'SBIR.gov is rate-limiting right now — try again shortly (results cache for 7 days once fetched).';
      return;
    }
    let html = `<b>Recent DoD awards · “${escapeHtml(r.keyword)}”</b><br><span style="color:var(--dim);font-size:12px">${r.awards.length} incumbents to differentiate from:</span><br>`;
    r.awards.slice(0, 10).forEach((a) => {
      const bits = [a.branch, a.phase, a.year || '', a.amount ? '$' + (a.amount >= 1e6 ? (a.amount / 1e6).toFixed(1) + 'M' : Math.round(a.amount / 1000) + 'K') : ''].filter(Boolean).join(' · ');
      html += `<div style="margin-top:6px"><b style="color:var(--ink)">${escapeHtml(a.firm || '—')}</b><br><span style="color:var(--dim);font-size:11px;font-family:var(--mono)">${escapeHtml(bits)}</span></div>`;
    });
    html += `<br><span style="color:var(--faint);font-size:11px">Claude has this context — ask “how do I beat these incumbents?”</span>`;
    m.innerHTML = html; t.scrollTop = 1e9;
  } catch (e) { m.className = 'msg err'; m.textContent = 'intel failed: ' + e.message; }
}

async function load() {
  [OPPS, STATE] = await Promise.all([
    fetch('/api/opportunities').then((r) => r.json()).catch(() => []),
    fetch('/api/state').then((r) => r.json()).catch(() => ({})),
  ]);
  const now = OPPS.filter((o) => o.act_now && !done(o.id)).length;
  $('#stat').textContent = `${OPPS.length} scored · ${now} act-now · ${Object.keys(STATE).length} pursuits`;
  const sb = $('#statusbar');
  if (sb) {
    const team = OPPS.filter((o) => o.teaming_only).length;
    const be = ASSIST.backend === 'subscription' ? 'MAX SUB' : ASSIST.backend === 'api' ? 'API' : 'OFFLINE';
    sb.innerHTML = `<span class="sdot"></span><span><b>REALIZER</b> SECURE · LOCAL</span>` +
      `<span class="ss">DSIP <b>LIVE</b></span>` +
      `<span class="ss">SCORED <b>${OPPS.length}</b></span>` +
      `<span class="ss">ACT-NOW <b>${now}</b></span>` +
      `<span class="ss">TEAMING <b>${team}</b></span>` +
      `<span class="ss">PURSUITS <b>${Object.keys(STATE).length}</b></span>` +
      `<span class="grow"></span>` +
      `<span class="ss snd" id="sndtoggle" title="toggle UI sound">SND <b>${SOUND_ON ? 'ON' : 'OFF'}</b></span>` +
      `<span class="ss snd" id="spktoggle" title="Claude speaks replies aloud (on-device)">SPEAK <b>${VOICE.speak ? 'ON' : 'OFF'}</b></span>` +
      `<span class="ss" id="clock"></span>` +
      `<span class="ss">CLAUDE <b>${be}</b></span>`;
    tickClock();
    sb.querySelectorAll('span:not(#clock):not(#sndtoggle):not(#spktoggle) b').forEach((b) => scrambleText(b, b.textContent, 520));
  }
}

const VIEWS = [['today', 'Today'], ['now', 'Act now'], ['teaming', 'Teaming'], ['pipeline', 'Pipeline'], ['profit', 'Profit'], ['all', 'All'], ['playbook', 'Playbook']];
function switchView(v) {
  if (v === VIEW) return;
  VIEW = v; snd.tab(); glitchBurst();
  if (document.startViewTransition && !matchMedia('(prefers-reduced-motion: reduce)').matches) {
    document.startViewTransition(() => { setActive(); render(); });
  } else { setActive(); render(); }
}

// Command palette: fuzzy-jump to views, run actions, open any opportunity.
function initPalette() {
  const pal = document.getElementById('palette'); if (!pal) return;
  document.getElementById('help')?.addEventListener('click', (e) => { if (e.target.id === 'help') e.currentTarget.classList.remove('open'); });
  const input = pal.querySelector('input'), res = pal.querySelector('.res');
  let items = [], sel = 0;
  const isOpen = () => pal.classList.contains('open');
  const close = () => { pal.classList.remove('open'); };
  const open = () => { pal.classList.add('open'); input.value = ''; build(''); input.focus(); };
  const ic = (n) => `<span class="pic">${svg(n)}</span>`;
  function build(q) {
    q = q.toLowerCase().trim();
    const out = [];
    VIEWS.forEach(([v, l]) => { if (!q || l.toLowerCase().includes(q)) out.push({ cat: 'View', icon: 'arrow', label: l, tag: 'go', run: () => switchView(v) }); });
    [['Refresh data', 'radar', () => $('#refresh').click()], ['Toggle UI sound', 'spark', () => document.querySelector('#sndtoggle')?.click()]]
      .forEach(([l, i, fn]) => { if (!q || l.toLowerCase().includes(q)) out.push({ cat: 'Action', icon: i, label: l, tag: 'run', run: fn }); });
    const opps = (q ? OPPS.filter((o) => (o.title + ' ' + o.agency).toLowerCase().includes(q)) : OPPS).slice(0, q ? 9 : 6);
    opps.forEach((o) => out.push({ cat: 'Opportunity', icon: 'target', label: o.title, tag: o.matched_asset || o.source, run: () => openAssist(o) }));
    items = out; sel = 0; render();
  }
  function render() {
    let html = '', cat = '';
    items.forEach((it, i) => {
      if (it.cat !== cat) { cat = it.cat; html += `<div class="pcat">${cat}</div>`; }
      html += `<div class="pi${i === sel ? ' sel' : ''}" data-i="${i}">${ic(it.icon)}<span class="pl">${escapeHtml(it.label)}</span><span class="tg">${escapeHtml(it.tag)}</span></div>`;
    });
    res.innerHTML = html || `<div class="pcat">No matches</div>`;
    res.querySelectorAll('.pi').forEach((e) => {
      e.addEventListener('mousemove', () => { sel = +e.dataset.i; markSel(); });
      e.addEventListener('click', () => run());
    });
  }
  function markSel() { res.querySelectorAll('.pi').forEach((e, i) => e.classList.toggle('sel', i === sel)); }
  function run() { const it = items[sel]; close(); if (it) setTimeout(it.run, 60); }
  input.addEventListener('input', () => build(input.value));
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); sel = Math.min(items.length - 1, sel + 1); markSel(); scrollSel(); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); sel = Math.max(0, sel - 1); markSel(); scrollSel(); }
    else if (e.key === 'Enter') { e.preventDefault(); run(); }
    else if (e.key === 'Escape') { close(); }
  });
  function scrollSel() { const e = res.querySelector('.pi.sel'); if (e) e.scrollIntoView({ block: 'nearest' }); }
  pal.addEventListener('click', (e) => { if (e.target === pal) close(); });
  // global hotkeys
  addEventListener('keydown', (e) => {
    const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement?.tagName || '');
    if ((e.key === 'k' && (e.metaKey || e.ctrlKey)) || (e.key === '/' && !typing && !isOpen())) { e.preventDefault(); open(); return; }
    const help = document.getElementById('help');
    if (e.key === 'Escape') { if (isOpen()) { close(); return; } if (help && help.classList.contains('open')) { help.classList.remove('open'); return; } closeAssist(); return; }
    if (e.key === '?' && !typing) { e.preventDefault(); if (help) help.classList.toggle('open'); return; }
    if (typing || isOpen() || e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key >= '1' && e.key <= '7') { switchView(VIEWS[+e.key - 1][0]); }
    else if (e.key === 'r') { $('#refresh').click(); }
  });
  PALETTE_OPEN = open;
}
let PALETTE_OPEN = null;

function done(id) { const p = STATE[id]; return p && ['won', 'lost', 'pass', 'submitted'].includes(p.stage); }
function setActive() { document.querySelectorAll('.tab').forEach((t) => t.classList.toggle('active', t.dataset.view === VIEW)); moveIndicator(); }

function celebrate(stage) {
  const f = document.getElementById('winflash');
  if (f) { f.classList.remove('go'); void f.offsetWidth; f.classList.add('go'); }
  if (SOUND_ON) { blip(523, .12, 'sine', .05, 784); setTimeout(() => blip(784, .14, 'sine', .045, 1047), 110); setTimeout(() => blip(1047, .22, 'sine', .04), 230); }
  toast(stage === 'program' ? 'Program of record — revenue realized.' : 'Pursuit advanced to ' + stage + '.');
}

async function saveState(id, patch, extra = {}) {
  const cur = STATE[id] || {};
  if (patch && patch.stage && patch.stage !== cur.stage && (patch.stage === 'won' || patch.stage === 'program')) celebrate(patch.stage);
  const next = { ...cur, ...patch, ...extra };
  if (!next.stage && !next.decision && !next.notes) { delete STATE[id]; }
  else STATE[id] = next;
  await fetch('/api/state', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id, ...next }) });
  $('#stat') && load().then(() => { if (VIEW !== 'all') render(); });
}

function daysLabel(o) {
  if (o.days_left == null || o.days_left < -10000) return '';
  if (o.days_left < 0) return `<span class="exp">closed ${-o.days_left}d ago</span>`;
  if (o.days_left === -1) return 'rolling';
  const c = o.days_left <= 30 ? 'soon' : '';
  return `<span class="${c}">closes in ${o.days_left}d</span>`;
}

function controls(id) {
  const p = STATE[id] || {};
  const wrap = el('div', 'ctl');
  const stage = el('select');
  stage.appendChild(new Option('— stage —', ''));
  STAGES.forEach((s) => stage.appendChild(new Option(s, s)));
  stage.value = p.stage || '';
  const opp = OPPS.find((o) => o.id === id);
  stage.addEventListener('change', () => saveState(id, { stage: stage.value },
    opp ? { title: opp.title, agency: opp.agency, url: opp.url } : {}));
  const dec = el('select');
  ['', 'bid', 'no-bid'].forEach((d) => dec.appendChild(new Option(d || '— decision —', d)));
  dec.value = p.decision || '';
  dec.addEventListener('change', () => saveState(id, { decision: dec.value }));
  const notes = el('textarea'); notes.placeholder = 'notes…'; notes.value = p.notes || '';
  let t; notes.addEventListener('input', () => { clearTimeout(t); t = setTimeout(() => saveState(id, { notes: notes.value }), 600); });
  wrap.append(stage, dec, notes);
  return wrap;
}

function oppCard(o, now) {
  const card = el('div', 'card' + (now ? ' now' : ''));
  const top = el('div', 'ctop');
  const left = el('div');
  const title = el('div', 'ctitle');
  if (o.url) { const a = el('a', null, o.title); a.href = o.url; a.target = '_blank'; a.rel = 'noopener'; title.append(a); }
  else title.textContent = o.title;
  const meta = el('div', 'meta');
  meta.innerHTML = srcChip(o.source) + [o.type, o.agency, daysLabel(o)].filter(Boolean).join(' · ');
  left.append(title, meta);
  const sc = el('div', 'score'); sc.innerHTML = `${o.score}<small>/100</small>`;
  top.append(left, sc);
  const bars = el('div', 'bars');
  const trl = o.matched_asset_trl ? ' ' + trlShort(o.matched_asset_trl) : '';
  bars.innerHTML =
    (o.hardware_excluded ? `<span class="bar hw">${svg('chip')}hardware — excluded</span>` : '') +
    (o.teaming_only ? `<span class="bar team">${svg('link')}teaming — you: software+design · partner builds</span>` : '') +
    (o.usv_prime ? `<span class="bar usv">${svg('wave')}USV — partner builds+funds, you prime</span>` : '') +
    (o.clearance_edge ? `<span class="bar clr">${svg('shield')}clearance edge</span>` : '') +
    (o.allied_edge ? `<span class="bar ally">${svg('globe')}AUKUS / allied</span>` : '') +
    `<span class="bar ${o.matched_asset ? 'asset' : ''}">fit <b>${o.capability}</b>${o.matched_asset ? ' · ' + o.matched_asset + trl : ''}</span>` +
    `<span class="bar">elig <b>${o.eligibility}</b></span>` +
    `<span class="bar">runway <b>${o.runway}</b></span>` +
    `<span class="bar">value <b>${o.value}</b></span>`;
  // score composition meter (capability / eligibility / runway / value → total)
  const meter = el('div', 'meter');
  meter.innerHTML = `<i class="mc" style="width:${o.capability}%"></i><i class="me" style="width:${o.eligibility}%"></i><i class="mr" style="width:${o.runway}%"></i><i class="mv" style="width:${o.value}%"></i>`;
  const mlbl = el('div', 'meterlbl');
  mlbl.innerHTML = `<span>capability · eligibility · runway · value</span><span>${o.score}/100</span>`;
  const row = el('div', 'ctl');
  const realize = el('button', 'realize magnetic'); realize.innerHTML = svg('spark') + 'Realize with Claude';
  realize.addEventListener('click', () => openAssist(o));
  row.append(realize);
  card.append(top, bars, meter, mlbl, controls(o.id), row);
  card.append(el('span', 'ticks'));
  return card;
}

function render() {
  document.querySelectorAll('.view').forEach((v) => v.hidden = true);
  if (VIEW === 'today') renderToday();
  else if (VIEW === 'now') renderNow();
  else if (VIEW === 'teaming') renderTeaming();
  else if (VIEW === 'pipeline') renderPipeline();
  else if (VIEW === 'profit') renderProfit();
  else if (VIEW === 'playbook') renderPlaybook();
  else renderAll();
  requestAnimationFrame(staggerIn);
}

async function renderProfit() {
  const v = $('#view-profit'); v.hidden = false; v.textContent = '';
  v.append(el('h2', null, 'Pipeline → profit'));
  v.append(el('p', 'sub', 'Each pursuit carries a best-case program-of-record value ceiling. Expected value = ceiling × the cumulative probability of that stage actually reaching a funded program of record — not the odds of clearing the next gate. The SBIR→PoR funnel is brutal, so a drafting/submitted bid is risk-adjusted to ~1–2%. Edit a pursuit’s ceiling in its Claude panel.'));
  const d = await fetch('/api/profit').then((r) => r.json()).catch(() => null);
  if (!d || !d.stages || !d.stages.length) { v.append(el('p', 'empty', 'No valued pursuits yet. Open a pursuit → set its estimated value.')); return; }
  const head = el('div', 'card');
  head.innerHTML = `<div class="ctop"><div><div class="ctitle">Expected revenue — risk-adjusted to program of record</div><div class="meta">best-case ceiling $${(d.total_value).toLocaleString()}K across ${d.stages.reduce((a, s) => a + s.count, 0)} pursuits</div></div><div class="score">$${(d.expected_value).toLocaleString()}<small>K expected</small></div></div>`;
  v.append(head);
  const grid = el('div', 'grid'); v.append(grid);
  const maxW = Math.max(...d.stages.map((s) => s.weighted), 1);
  d.stages.forEach((s) => {
    const c = el('div', 'card');
    const pct = Math.max(3, Math.round((s.weighted / maxW) * 100));
    c.innerHTML = `<div class="ctop"><div><div class="ctitle">${s.stage}</div><div class="meta">${s.count} pursuit${s.count === 1 ? '' : 's'} · $${s.value.toLocaleString()}K ceiling · ${(s.prob * 100).toFixed(1)}% reach PoR</div></div><div class="score">$${s.weighted.toLocaleString()}<small>K expected</small></div></div><div style="margin-top:8px;height:6px;border-radius:4px;background:linear-gradient(to right,var(--brand) ${pct}%,var(--panel2) ${pct}%)"></div>`;
    grid.append(c);
  });
}

async function renderPlaybook() {
  const v = $('#view-playbook'); v.hidden = false; v.textContent = '';
  const md = await fetch('/api/playbook').then((r) => r.text()).catch(() => '');
  const pre = el('div', 'playbook'); pre.innerHTML = mdLite(md);
  v.append(pre);
}

// minimal markdown → HTML (headings, bold, lists) for the playbook view
function mdLite(md) {
  const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;');
  return esc(md).split('\n').map((l) => {
    if (/^### /.test(l)) return '<h3>' + l.slice(4) + '</h3>';
    if (/^## /.test(l)) return '<h2>' + l.slice(3) + '</h2>';
    if (/^# /.test(l)) return '<h1>' + l.slice(2) + '</h1>';
    if (/^- /.test(l)) return '<li>' + bold(l.slice(2)) + '</li>';
    if (/^\d+\. /.test(l)) return '<li>' + bold(l.replace(/^\d+\.\s/, '')) + '</li>';
    if (l.trim() === '') return '<br>';
    return '<p>' + bold(l) + '</p>';
  }).join('');
  function bold(s) { return s.replace(/\*\*(.+?)\*\*/g, '<b>$1</b>').replace(/`(.+?)`/g, '<code>$1</code>'); }
}

// open a brief card: deep-link into the Claude assist panel for the live opp;
// for a pursuit with no live opp (e.g. a seeded volume), jump to the pipeline.
function openById(id) {
  const o = OPPS.find((x) => x.id === id);
  if (o) { openAssist(o); return; }
  VIEW = 'pipeline'; setActive(); render();
}

function tcard(it) {
  const c = el('div', 'tcard l-' + it.kind + (it.urgent ? ' l-urgent' : ''));
  const tt = el('div', 'tt', it.title);
  const td = el('div', 'td');
  const chips = [];
  if (it.kind === 'move') {
    const pct = it.score || 0;
    const col = pct >= 75 ? 'var(--ok)' : pct >= 40 ? 'var(--warn)' : 'var(--bad)';
    chips.push(`<span class="ready"><i style="width:${pct}%;background:${col}"></i></span>`);
    chips.push(`<span class="chip">${pct}/100 ready</span>`);
    if (it.weakest) chips.push(`<span class="chip soon">weak: ${it.weakest}</span>`);
  } else {
    if (it.days != null && it.days >= 0) {
      const cls = it.days <= 7 ? 'urgent' : it.days <= 30 ? 'soon' : '';
      chips.push(`<span class="chip ${cls}">${it.days === 0 ? 'today' : 'in ' + it.days + 'd'}</span>`);
    }
    if (it.score) chips.push(`<span class="chip fit">fit ${it.score}</span>`);
    if (it.asset) chips.push(`<span class="chip asset">${it.asset}</span>`);
  }
  td.innerHTML = chips.join(' ');
  c.append(tt, td);
  if (it.detail) c.append(el('div', 'det', it.detail));
  c.append(el('span', 'ticks'));
  c.addEventListener('click', () => openById(it.id));
  return c;
}

function tsection(parent, cls, iconName, label, items, emptyMsg) {
  const h = el('div', 'section-h ' + cls);
  h.innerHTML = `<span class="ic">${svg(iconName)}</span><h3>${label}</h3><span class="ln"></span><span class="cnt">${items.length}</span>`;
  parent.append(h);
  const g = el('div', 'tgrid');
  if (!items.length) g.append(el('div', 'tempty', emptyMsg));
  else items.forEach((it) => g.append(tcard(it)));
  parent.append(g);
}

async function renderToday() {
  const v = $('#view-today'); v.hidden = false; v.textContent = '';
  BRIEF = await fetch('/api/brief').then((r) => r.json()).catch(() => null);
  const b = BRIEF || { deadlines: [], qa: [], new: [], moves: [], ev: 0, total_value: 0, pursuits: 0, act_now: 0, new_count: 0 };
  const today = new Date().toLocaleDateString(undefined, { weekday: 'long', month: 'long', day: 'numeric' });

  // hero — one-line "what to do today"
  const hero = el('div', 'hero');
  const urgent = b.deadlines.find((d) => d.urgent);
  const dleft = (n) => n === 0 ? 'closes today' : n === 1 ? 'closes in 1 day' : `closes in ${n} days`;
  const lead = urgent
    ? `${urgent.title} ${dleft(urgent.days)} — make the call today.`
    : b.deadlines.length
      ? `Nearest deadline: ${b.deadlines[0].title} — ${dleft(b.deadlines[0].days)}.`
      : b.new_count
        ? `${b.new_count} new high-fit opportunit${b.new_count === 1 ? 'y' : 'ies'} surfaced. Triage them.`
        : 'No deadlines this month — push a pursuit one wall forward.';
  hero.innerHTML = `<span class="cb tl"></span><span class="cb tr"></span><span class="cb bl"></span><span class="cb br"></span>` +
    `<div class="date">Today · ${today}</div><div class="lead">${escapeHtml(lead)}</div>` +
    `<div class="leadsub">Your private bid autopilot — deadlines, sanctioned Q&A windows, fresh fits, and the next move on every pursuit.</div>` +
    `<div class="wave2"></div><div class="wave"></div>`;
  if (!HERO_DECODED) { HERO_DECODED = true; const ld = hero.querySelector('.lead'); if (ld) decrypt(ld, lead, 950); }

  // bento: hero (dominant) + two feature stat tiles + a base row of three
  const mkStat = (cls, n, l) => { const d = el('div', 'stat ' + cls); d.innerHTML = `<div class="n">${n}</div><div class="l">${l}</div>`; return d; };
  const bento = el('div', 'bento');
  const row = el('div', 'statrow');
  row.append(
    mkStat('', b.pursuits || 0, 'Active pursuits'),
    mkStat('now', b.act_now || 0, 'Act-now'),
    mkStat('new', b.new_count || 0, 'New high-fit'),
  );
  bento.append(
    hero,
    mkStat('ev feat fa', '$' + (b.ev || 0).toLocaleString() + 'K', 'Expected (risk-adj. to PoR)'),
    mkStat('feat fb', '$' + (b.total_value || 0).toLocaleString() + 'K', 'Best-case ceiling'),
    row,
  );
  v.append(bento);
  animateCounts(bento);
  // proactive AI read of the day (on-demand)
  if (ASSIST.enabled) {
    const dr = el('div', 'dayread');
    const cta = el('button', 'drcta'); cta.innerHTML = svg('spark') + "Claude’s read on today";
    const body = el('div', 'drbody'); body.hidden = true;
    cta.addEventListener('click', () => dayRead(cta, body));
    // portfolio-level strategist: reason across the whole pipeline (which to pursue)
    const sca = el('button', 'drcta strat'); sca.innerHTML = svg('target') + 'Strategize pipeline';
    const sbody = el('div', 'drbody'); sbody.hidden = true;
    sca.addEventListener('click', () => strategize(sca, sbody));
    const row = el('div', 'drrow'); row.append(cta, sca);
    dr.append(row, body, sbody); v.append(dr);
  }
  v.append(el('p', 'sub', 'Expected value = each pursuit’s program-of-record ceiling × its cumulative probability of actually reaching a funded program (the SBIR→PoR funnel is brutal — early stages are <2%). Ceilings are editable best-case estimates; set them per pursuit in its Claude panel.'));

  tsection(v, 'deadline', 'clock', 'Deadlines (≤30d)', b.deadlines, 'No tracked deadlines in the next 30 days.');
  tsection(v, 'qa', 'chat', 'Q&A windows — sanctioned channel', b.qa, 'No open topic Q&A windows right now.');
  tsection(v, 'new', 'spark', 'New high-fit opportunities', b.new, 'Nothing new since your last brief.');
  tsection(v, 'team', 'link', 'Teaming plays — you provide the software brain', b.teaming || [], 'No teaming plays surfaced yet (autonomous vehicles / payloads where you sub to a prime).');
  tsection(v, 'move', 'arrow', 'Next move on each pursuit', b.moves, 'No live pursuits — add one from Act now.');

  // push hint + calendar subscribe
  const pr = el('div', 'pushrow');
  pr.innerHTML = `<span class="hint">Get this pushed to your phone each morning: <code>engine workspace brief --push</code> (set <code>NTFY_TOPIC</code>), scheduled daily via Task Scheduler. Or run the strategist headless: <code>engine workspace autopilot --push</code>.</span>`;
  const cal = el('a', 'calsub'); cal.href = '/api/calendar.ics'; cal.setAttribute('download', 'realizer-deadlines.ics');
  cal.innerHTML = svg('clock') + 'Subscribe to deadlines (.ics)';
  cal.title = 'Download/subscribe — every pursuit + act-now + strong-fit close date, with a 7-day reminder, in your calendar app';
  pr.append(cal);
  v.append(pr);
}

function escapeHtml(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); }

// colored source tag so SBIR vs contract/OTA vs program reads at a glance
const SRC_LABEL = { dsip: 'SBIR', sam: 'CONTRACT', grants: 'GRANT/BAA', pipeline: 'PIPELINE', programs: 'PROGRAM' };
function srcChip(src) { return `<span class="src ${src || ''}">${SRC_LABEL[src] || (src || '').toUpperCase()}</span>`; }

// lightweight markdown for streamed Claude replies (headings, bold, italic, code, lists)
function mdChat(md) {
  let s = escapeHtml(String(md).replace(/\[\[do:[^\]]+\]\]/g, '')); // hide action directives from prose
  s = s.replace(/```([\s\S]*?)```/g, (_, c) => `<pre>${c.trim()}</pre>`);
  s = s.replace(/`([^`]+)`/g, '<code>$1</code>');
  s = s.replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>').replace(/(^|[^*])\*([^*]+)\*/g, '$1<em>$2</em>');
  const lines = s.split('\n'); let out = '', inList = false;
  for (let l of lines) {
    if (/^### /.test(l)) { if (inList) { out += '</ul>'; inList = false; } out += '<h4>' + l.slice(4) + '</h4>'; }
    else if (/^## /.test(l)) { if (inList) { out += '</ul>'; inList = false; } out += '<h4>' + l.slice(3) + '</h4>'; }
    else if (/^\s*[-*]\s+/.test(l)) { if (!inList) { out += '<ul>'; inList = true; } out += '<li>' + l.replace(/^\s*[-*]\s+/, '') + '</li>'; }
    else if (/^\s*\d+\.\s+/.test(l)) { if (!inList) { out += '<ul>'; inList = true; } out += '<li>' + l.replace(/^\s*\d+\.\s+/, '') + '</li>'; }
    else { if (inList) { out += '</ul>'; inList = false; } out += l.trim() ? '<p>' + l + '</p>' : ''; }
  }
  if (inList) out += '</ul>';
  return out;
}
function trlShort(s) { const m = String(s).match(/TRL\s*\d+/i); return m ? m[0].toUpperCase().replace(/\s+/, '') : ''; }

async function dayRead(cta, body) {
  cta.disabled = true; cta.innerHTML = svg('radar') + 'Reading the board…';
  body.hidden = false; body.innerHTML = '<span class="drwait">analyzing pipeline…</span>';
  let acc = '';
  try {
    const resp = await fetch('/api/dayread', { method: 'POST' });
    const reader = resp.body.getReader(); const dec = new TextDecoder(); let buf = '';
    for (;;) {
      const { value, done } = await reader.read(); if (done) break;
      buf += dec.decode(value, { stream: true }); const parts = buf.split('\n\n'); buf = parts.pop();
      for (const p of parts) {
        const line = p.replace(/^data:\s*/, '').trim(); if (!line) continue;
        let ev; try { ev = JSON.parse(line); } catch { continue; }
        if (ev.error) { body.innerHTML = `<span class="drwait">${escapeHtml(ev.error)}</span>`; }
        else if (ev.t) { acc += ev.t; body.innerHTML = mdChat(acc); }
      }
    }
  } catch (e) { body.innerHTML = `<span class="drwait">read failed: ${escapeHtml(e.message)}</span>`; }
  cta.disabled = false; cta.innerHTML = svg('spark') + 'Refresh read';
}

// Portfolio strategist: the ranked pipeline (win-prob meters) + Claude's
// cross-pipeline call streamed below it.
async function strategize(cta, body) {
  cta.disabled = true; cta.innerHTML = svg('radar') + 'Reasoning across the pipeline…';
  body.hidden = false; body.innerHTML = '<div class="stratrows"></div><div class="stratread"><span class="drwait">weighing win-probability × value across every pursuit…</span></div>';
  const rowsEl = body.querySelector('.stratrows'), readEl = body.querySelector('.stratread');
  let acc = '', narrating = false;
  try {
    const resp = await fetch('/api/strategize', { method: 'POST' });
    const reader = resp.body.getReader(); const dec = new TextDecoder(); let buf = '';
    for (;;) {
      const { value, done } = await reader.read(); if (done) break;
      buf += dec.decode(value, { stream: true }); const parts = buf.split('\n\n'); buf = parts.pop();
      for (const p of parts) {
        const line = p.replace(/^data:\s*/, '').trim(); if (!line) continue;
        let ev; try { ev = JSON.parse(line); } catch { continue; }
        if (ev.rows) { rowsEl.innerHTML = stratTable(ev.rows); snd.recv && snd.recv(); }
        else if (ev.error) { readEl.innerHTML = `<span class="drwait">${escapeHtml(ev.error)}</span>`; }
        else if (ev.t) { if (!narrating) { narrating = true; readEl.innerHTML = ''; } acc += ev.t; readEl.innerHTML = mdChat(acc); corePulse(); }
      }
    }
  } catch (e) { readEl.innerHTML = `<span class="drwait">strategize failed: ${escapeHtml(e.message)}</span>`; }
  cta.disabled = false; cta.innerHTML = svg('target') + 'Re-strategize';
}

// Ranked pipeline table with win-probability bars.
function stratTable(rows) {
  if (!rows || !rows.length) return '<p class="empty">No active pursuits yet — open Claude on an opportunity and move it into the pipeline.</p>';
  const head = '<div class="strow sthead"><span>Pursuit</span><span>Win</span><span>EV</span><span>Closes</span></div>';
  const body = rows.map((r) => {
    const wp = Math.max(0, Math.min(100, r.win_prob || 0));
    const tone = wp >= 60 ? 'ok' : wp >= 25 ? 'warn' : 'bad';
    const dl = r.days_left >= 0 ? (r.days_left === 0 ? 'today' : r.days_left + 'd') : '—';
    const ev = r.ev > 0 ? '$' + r.ev + 'K' : '—';
    const asset = r.asset ? `<span class="stasset">${escapeHtml(r.asset)}</span>` : '';
    const rdy = r.ready && r.ready !== '—' ? `<span class="rdy ${r.ready === 'GO' ? 'go' : r.ready === 'FIX' ? 'fix' : 'nogo'}" title="${escapeHtml(r.ready_why || '')}">${r.ready}</span> ` : '';
    return `<div class="strow">
      <span class="sttitle"><b>${rdy}${escapeHtml(r.title)}</b><small>${escapeHtml(r.stage)} · weakest: ${escapeHtml(r.weakest || '—')} ${asset}</small></span>
      <span class="stwin ${tone}"><i style="width:${wp}%"></i><em>${wp}%</em></span>
      <span class="stev">${ev}</span>
      <span class="stdl ${r.days_left >= 0 && r.days_left <= 7 ? 'urgent' : ''}">${dl}</span>
    </div>`;
  }).join('');
  return head + body;
}

function renderTeaming() {
  const v = $('#view-teaming'); v.hidden = false; v.textContent = '';
  v.append(el('h2', null, 'Teaming plays — you provide the software brain'));
  v.append(el('p', 'sub', 'Hardware you do not fabricate yourself (payloads, autonomous vehicles incl. UUV/UAV/UGV) where you lead software + design. Your Australian partner can build and fund the hardware as subcontractor (mind ITAR/EAR + SBIR foreign-sub limits) - open one with Claude to structure the teaming compliantly. USV topics where the partner builds+funds and you prime appear in Act now.'));
  const team = OPPS.filter((o) => o.teaming_only);
  const usv = OPPS.filter((o) => o.usv_prime);
  if (usv.length) v.append(el('p', 'sub', `${usv.length} USV / surface-vessel topic${usv.length === 1 ? '' : 's'} you can PRIME — see Act now / All.`));
  if (!team.length) { v.append(el('p', 'empty', 'No teaming plays surfaced right now. Grounding more of your portfolio will surface more autonomy/perception teaming fits.')); return; }
  const grid = el('div', 'grid');
  team.sort((a, b) => b.score - a.score).forEach((o) => grid.append(oppCard(o, false)));
  v.append(grid);
}

function renderNow() {
  const v = $('#view-now'); v.hidden = false; v.textContent = '';
  v.append(el('h2', null, 'Act now — pursue this week'));
  const sub = el('p', 'sub', 'High capability-fit, eligible, closing within 30 days, not yet decided. Ranked by fit score.');
  v.append(sub);
  const list = OPPS.filter((o) => o.act_now && !done(o.id));
  if (!list.length) { v.append(el('p', 'empty', 'Nothing urgent matches your capabilities right now. Check All opportunities.')); return; }
  const grid = el('div', 'grid');
  list.forEach((o) => grid.append(oppCard(o, true)));
  v.append(grid);
}

function renderPipeline() {
  const v = $('#view-pipeline'); v.hidden = false; v.textContent = '';
  const head = el('div', 'phead');
  head.append(el('h2', null, 'Pipeline — your pursuits'));
  if (ASSIST.enabled) {
    const aa = el('button', 'act'); aa.innerHTML = svg('spark') + 'Auto-assess all pursuits';
    aa.title = 'Claude fills $ value + the four transition walls for every pursuit (runs on your subscription)';
    aa.addEventListener('click', async () => {
      aa.textContent = 'assessing all… (~30–60s, on your subscription)'; aa.disabled = true;
      const r = await fetch('/api/assess-all', { method: 'POST' }).then((x) => x.json()).catch(() => null);
      await load(); render();
      if (r) { const n = $('#stat'); if (n) n.textContent = `auto-assessed ${r.assessed}/${r.total}` + (r.failed ? ` (${r.failed} failed)` : ''); }
    });
    head.append(aa);
  }
  v.append(head);
  const board = el('div', 'kanban');
  const byId = Object.fromEntries(OPPS.map((o) => [o.id, o]));
  COLS.forEach((col) => {
    const c = el('div', 'col');
    const items = Object.entries(STATE).filter(([, p]) => col.match.includes(p.stage));
    const h = el('h3'); h.innerHTML = `${col.label} <span>${items.length}</span>`; c.append(h);
    if (!items.length) c.append(el('div', 'empty', 'empty'));
    items.forEach(([id, p]) => {
      const o = byId[id];
      const kc = el('div', 'kc');
      kc.draggable = true;
      kc.addEventListener('dragstart', (e) => { e.dataTransfer.setData('text/plain', id); e.dataTransfer.effectAllowed = 'move'; kc.classList.add('drag'); });
      kc.addEventListener('dragend', () => kc.classList.remove('drag'));
      const t = el('div', 't'); t.textContent = (o ? o.title : p.title) || id;
      const m = el('div', 'm');
      const bits = [o ? o.agency : p.agency, o ? daysLabel(o) : '', p.decision].filter(Boolean);
      m.innerHTML = bits.join(' · ');
      kc.append(t, m);
      if (p.notes) { const n = el('div', 'm'); n.textContent = p.notes; kc.append(n); }
      kc.append(stageMover(id, p));
      kc.append(el('span', 'ticks'));
      c.append(kc);
    });
    // drop target → move pursuit into this column's stage
    c.addEventListener('dragover', (e) => { e.preventDefault(); e.dataTransfer.dropEffect = 'move'; c.classList.add('dragover'); });
    c.addEventListener('dragleave', (e) => { if (!c.contains(e.relatedTarget)) c.classList.remove('dragover'); });
    c.addEventListener('drop', (e) => {
      e.preventDefault(); c.classList.remove('dragover');
      const id = e.dataTransfer.getData('text/plain');
      if (id && STATE[id] && STATE[id].stage !== col.drop) { snd.tab(); saveState(id, { stage: col.drop }); if (col.drop !== 'won' && col.drop !== 'program') toast('Moved to ' + col.drop); }
    });
    board.append(c);
  });
  v.append(board);
}

function stageMover(id, p) {
  const sel = el('select');
  STAGES.forEach((s) => sel.appendChild(new Option(s, s)));
  sel.value = p.stage;
  sel.style.marginTop = '6px';
  sel.addEventListener('change', () => saveState(id, { stage: sel.value }));
  return sel;
}

function renderAll() {
  const v = $('#view-all'); v.hidden = false; v.textContent = '';
  v.append(el('h2', null, `All opportunities (${OPPS.length})`));
  const f = el('input', 'filter'); f.placeholder = 'Filter title / agency / source / type…';
  v.append(f);
  const hwn = OPPS.filter((o) => o.hardware_excluded).length;
  const toggle = el('label', 'hwtoggle');
  toggle.innerHTML = `<input type="checkbox" id="showhw"> show hardware-build topics excluded by your software-only profile (${hwn})`;
  v.append(toggle);
  const grid = el('div', 'grid');
  v.append(grid);
  const draw = () => {
    const q = f.value.trim().toLowerCase();
    const showHw = $('#showhw').checked;
    grid.textContent = '';
    OPPS.filter((o) => (showHw || !o.hardware_excluded) &&
      (!q || (o.title + o.agency + o.source + o.type).toLowerCase().includes(q)))
      .slice(0, 300).forEach((o) => grid.append(oppCard(o, o.act_now)));
  };
  f.addEventListener('input', draw);
  $('#showhw').addEventListener('change', draw);
  draw();
}

boot();
