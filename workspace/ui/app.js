// Private bid workspace UI. Reads /api/opportunities (scored) + /api/state, lets
// Jesse triage and track pursuits. State changes POST to /api/state (local file).

let OPPS = [];
let STATE = {};
let VIEW = 'now';

const $ = (s, r = document) => r.querySelector(s);
const el = (t, c, txt) => { const e = document.createElement(t); if (c) e.className = c; if (txt != null) e.textContent = txt; return e; };

const STAGES = ['watching', 'qualifying', 'drafting', 'submitted', 'won', 'pilot', 'transition', 'pom', 'program', 'lost', 'pass'];
const COLS = [
  { key: 'discovery', label: 'Discovery', match: ['watching', 'qualifying'] },
  { key: 'bid', label: 'Bid', match: ['drafting', 'submitted'] },
  { key: 'pilot', label: 'Award · Pilot', match: ['won', 'pilot'] },
  { key: 'transition', label: 'Transition · POM', match: ['transition', 'pom'] },
  { key: 'program', label: 'Program of Record', match: ['program'] },
  { key: 'closed', label: 'Closed', match: ['lost', 'pass'] },
];
const WALLS = ['money', 'requirements', 'contracts', 'incentives'];
const WALL_LABEL = { money: 'Money', requirements: 'Requirements', contracts: 'Contracts', incentives: 'Incentives' };

let ASSIST = { enabled: false, model: '' };
let CUR_OPP = null;

async function boot() {
  await load();
  ASSIST = await fetch('/api/assist-status').then((r) => r.json()).catch(() => ({ enabled: false }));
  document.querySelectorAll('.tab').forEach((t) =>
    t.addEventListener('click', () => { VIEW = t.dataset.view; setActive(); render(); }));
  $('#refresh').addEventListener('click', async (e) => {
    e.target.textContent = '…'; await fetch('/api/refresh', { method: 'POST' }); await load(); render(); e.target.textContent = '↻ Refresh';
  });
  $('#assist-close').addEventListener('click', closeAssist);
  $('#overlay').addEventListener('click', closeAssist);
  $('#assist-send').addEventListener('click', () => sendAssist());
  $('#assist-input').addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sendAssist(); } });
  render();
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
  { a: 'transition', label: 'Structure for transition' },
  { a: 'sponsor', label: 'Who owns the money' },
  { a: 'outreach', label: '✉ Outreach + draft message' },
  { a: 'pom', label: 'POM readiness' },
  { a: 'pmadopt', label: 'PM adoption pitch' },
  { a: 'nextstep', label: '★ Next best action' },
];

function openAssist(o) {
  CUR_OPP = o;
  const via = ASSIST.backend === 'subscription' ? ' · via Max subscription' : ASSIST.backend === 'api' ? ' · via API key' : '';
  $('.ah .who').textContent = '◎ CLAUDE — BID STRATEGIST' + via;
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
    qa.append(rowLabel('Bid'), bidRow);
    const trow = el('div', 'qarow');
    TQUICK.forEach((q) => { const b = el('button', null, q.label); b.addEventListener('click', () => sendAssist(q.a)); trow.append(b); });
    qa.append(rowLabel('Cross the valley'), trow);
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
  return box;
}

function readiness(w) {
  const v = (x) => x === 'ready' ? 100 : x === 'partial' ? 50 : 0;
  let sum = 0, weak = 'Money', low = 101;
  WALLS.forEach((k) => { const s = v(w[k]); sum += s; if (s < low) { low = s; weak = WALL_LABEL[k]; } });
  return { score: Math.round(sum / 4), weakest: weak };
}
function closeAssist() { $('#assist').classList.remove('open'); $('#overlay').style.display = 'none'; CUR_OPP = null; }

function renderThread() {
  const t = $('#thread'); t.textContent = '';
  if (!ASSIST.enabled) {
    t.innerHTML = `<div class="disabled-note">Claude isn't connected. Easiest: install + log in to <b>Claude Code</b> — the workspace will use your <b>Max subscription</b> (no per-token cost):<br><br><code>npm i -g @anthropic-ai/claude-code</code><br><code>claude login</code><br><code>go run . workspace</code><br><br>Or set <b>ANTHROPIC_API_KEY</b> for the pay-per-token API. Everything stays on this machine.</div>`;
    return;
  }
  convo(CUR_OPP.id).forEach((m) => {
    const d = el('div', 'msg ' + (m.role === 'user' ? 'u' : 'a'));
    d.textContent = (m.role === 'user' ? '› ' : '') + m.content;
    t.append(d);
  });
  t.scrollTop = t.scrollHeight;
}

async function moveStage(o, stage) {
  await saveState(o.id, { stage }, { title: o.title, agency: o.agency, url: o.url });
  const d = el('div', 'msg a'); d.textContent = `✓ Moved to ${stage}.`; $('#thread').append(d);
  $('#thread').scrollTop = $('#thread').scrollHeight;
}

async function sendAssist(action) {
  if (!ASSIST.enabled || !CUR_OPP) return;
  const input = $('#assist-input');
  const message = action ? '' : input.value.trim();
  if (!action && !message) return;
  const id = CUR_OPP.id;
  const hist = convo(id);
  const userLabel = action ? QUICK.find((q) => q.a === action)?.label || action : message;
  hist.push({ role: 'user', content: userLabel });
  saveConvo(id, hist);
  input.value = '';
  renderThread();

  const ans = el('div', 'msg a'); ans.textContent = '…'; $('#thread').append(ans); $('#thread').scrollTop = 1e9;
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
        if (ev.error) { ans.className = 'msg err'; ans.textContent = ev.error; }
        else if (ev.t) { acc += ev.t; ans.textContent = acc; $('#thread').scrollTop = 1e9; }
      }
    }
  } catch (e) { ans.className = 'msg err'; ans.textContent = 'stream failed: ' + e.message; }
  if (acc) { const h = convo(id); h.push({ role: 'assistant', content: acc }); saveConvo(id, h); }
}

async function load() {
  [OPPS, STATE] = await Promise.all([
    fetch('/api/opportunities').then((r) => r.json()).catch(() => []),
    fetch('/api/state').then((r) => r.json()).catch(() => ({})),
  ]);
  const now = OPPS.filter((o) => o.act_now && !done(o.id)).length;
  $('#stat').textContent = `${OPPS.length} scored · ${now} act-now · ${Object.keys(STATE).length} pursuits`;
}

function done(id) { const p = STATE[id]; return p && ['won', 'lost', 'pass', 'submitted'].includes(p.stage); }
function setActive() { document.querySelectorAll('.tab').forEach((t) => t.classList.toggle('active', t.dataset.view === VIEW)); }

async function saveState(id, patch, extra = {}) {
  const cur = STATE[id] || {};
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
  meta.innerHTML = [o.source, o.type, o.agency, daysLabel(o)].filter(Boolean).join(' · ');
  left.append(title, meta);
  const sc = el('div', 'score'); sc.innerHTML = `${o.score}<small>/100</small>`;
  top.append(left, sc);
  const bars = el('div', 'bars');
  bars.innerHTML =
    `<span class="bar ${o.matched_asset ? 'asset' : ''}">fit <b>${o.capability}</b>${o.matched_asset ? ' · ' + o.matched_asset : ''}</span>` +
    `<span class="bar">elig <b>${o.eligibility}</b></span>` +
    `<span class="bar">runway <b>${o.runway}</b></span>` +
    `<span class="bar">value <b>${o.value}</b></span>`;
  const row = el('div', 'ctl');
  const realize = el('button', 'realize', '◎ Realize with Claude');
  realize.addEventListener('click', () => openAssist(o));
  row.append(realize);
  card.append(top, bars, controls(o.id), row);
  return card;
}

function render() {
  document.querySelectorAll('.view').forEach((v) => v.hidden = true);
  if (VIEW === 'now') renderNow();
  else if (VIEW === 'pipeline') renderPipeline();
  else if (VIEW === 'profit') renderProfit();
  else if (VIEW === 'playbook') renderPlaybook();
  else renderAll();
}

async function renderProfit() {
  const v = $('#view-profit'); v.hidden = false; v.textContent = '';
  v.append(el('h2', null, 'Pipeline → profit'));
  v.append(el('p', 'sub', 'Estimated lifetime value weighted by lifecycle conversion probability. Set a $ value per pursuit in its Claude panel.'));
  const d = await fetch('/api/profit').then((r) => r.json()).catch(() => null);
  if (!d || !d.stages || !d.stages.length) { v.append(el('p', 'empty', 'No valued pursuits yet. Open a pursuit → set its estimated value.')); return; }
  const head = el('div', 'card');
  head.innerHTML = `<div class="ctop"><div><div class="ctitle">Expected revenue (probability-weighted)</div><div class="meta">total pipeline $${(d.total_value).toLocaleString()}K across ${d.stages.reduce((a, s) => a + s.count, 0)} pursuits</div></div><div class="score">$${(d.expected_value).toLocaleString()}<small>K EV</small></div></div>`;
  v.append(head);
  const grid = el('div', 'grid'); v.append(grid);
  const maxW = Math.max(...d.stages.map((s) => s.weighted), 1);
  d.stages.forEach((s) => {
    const c = el('div', 'card');
    const pct = Math.max(3, Math.round((s.weighted / maxW) * 100));
    c.innerHTML = `<div class="ctop"><div><div class="ctitle">${s.stage}</div><div class="meta">${s.count} pursuit${s.count === 1 ? '' : 's'} · $${s.value.toLocaleString()}K value · ${(s.prob * 100).toFixed(0)}% convert</div></div><div class="score">$${s.weighted.toLocaleString()}<small>K EV</small></div></div><div style="margin-top:8px;height:6px;border-radius:4px;background:linear-gradient(to right,var(--brand) ${pct}%,var(--panel2) ${pct}%)"></div>`;
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
  v.append(el('h2', null, 'Pipeline — your pursuits'));
  const board = el('div', 'kanban');
  const byId = Object.fromEntries(OPPS.map((o) => [o.id, o]));
  COLS.forEach((col) => {
    const c = el('div', 'col');
    const items = Object.entries(STATE).filter(([, p]) => col.match.includes(p.stage));
    const h = el('h3'); h.innerHTML = `${col.label} <span>${items.length}</span>`; c.append(h);
    if (!items.length) c.append(el('div', 'empty', '—'));
    items.forEach(([id, p]) => {
      const o = byId[id];
      const kc = el('div', 'kc');
      const t = el('div', 't'); t.textContent = (o ? o.title : p.title) || id;
      const m = el('div', 'm');
      const bits = [o ? o.agency : p.agency, o ? daysLabel(o) : '', p.decision].filter(Boolean);
      m.innerHTML = bits.join(' · ');
      kc.append(t, m);
      if (p.notes) { const n = el('div', 'm'); n.textContent = p.notes; kc.append(n); }
      kc.append(stageMover(id, p));
      c.append(kc);
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
  const grid = el('div', 'grid');
  v.append(grid);
  const draw = () => {
    const q = f.value.trim().toLowerCase();
    grid.textContent = '';
    OPPS.filter((o) => !q || (o.title + o.agency + o.source + o.type).toLowerCase().includes(q))
      .slice(0, 300).forEach((o) => grid.append(oppCard(o, o.act_now)));
  };
  f.addEventListener('input', draw); draw();
}

boot();
