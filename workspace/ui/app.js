// Private bid workspace UI. Reads /api/opportunities (scored) + /api/state, lets
// Jesse triage and track pursuits. State changes POST to /api/state (local file).

let OPPS = [];
let STATE = {};
let VIEW = 'now';

const $ = (s, r = document) => r.querySelector(s);
const el = (t, c, txt) => { const e = document.createElement(t); if (c) e.className = c; if (txt != null) e.textContent = txt; return e; };

const STAGES = ['watching', 'qualifying', 'drafting', 'submitted', 'won', 'lost', 'pass'];
const COLS = [
  { key: 'watching', label: 'Watching', match: ['watching'] },
  { key: 'qualifying', label: 'Qualifying', match: ['qualifying'] },
  { key: 'drafting', label: 'Drafting', match: ['drafting'] },
  { key: 'submitted', label: 'Submitted', match: ['submitted'] },
  { key: 'decided', label: 'Decided', match: ['won', 'lost', 'pass'] },
];

async function boot() {
  await load();
  document.querySelectorAll('.tab').forEach((t) =>
    t.addEventListener('click', () => { VIEW = t.dataset.view; setActive(); render(); }));
  $('#refresh').addEventListener('click', async (e) => {
    e.target.textContent = '…'; await fetch('/api/refresh', { method: 'POST' }); await load(); render(); e.target.textContent = '↻ Refresh';
  });
  render();
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
  card.append(top, bars, controls(o.id));
  return card;
}

function render() {
  document.querySelectorAll('.view').forEach((v) => v.hidden = true);
  if (VIEW === 'now') renderNow();
  else if (VIEW === 'pipeline') renderPipeline();
  else renderAll();
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
