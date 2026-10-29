'use strict';

// ---------- helpers ----------
const SVGNS = 'http://www.w3.org/2000/svg';
const h = (tag, props, ...kids) => {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') e.className = v;
    else if (k === 'text') e.textContent = v;
    else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
    else e.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat()) if (kid != null && kid !== false) e.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  return e;
};
const s = (tag, attrs, ...kids) => {
  const e = document.createElementNS(SVGNS, tag);
  for (const [k, v] of Object.entries(attrs || {})) e.setAttribute(k, v);
  for (const kid of kids) e.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  return e;
};
const $ = (q) => document.querySelector(q);
const fmt = (n) => Number(n).toLocaleString('en-US');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const reduceMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;
const store = {
  get(k) { try { return localStorage.getItem(k); } catch { return null; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch { /* private mode */ } },
};

const state = { me: null, account: null, house: null, games: null, game: 'roulette', bucket: store.get('casino.bucket') || 'Main', bet: 'red', busy: false, plays: [] };

async function api(path, opts = {}) {
  const init = { method: opts.method || 'GET', headers: {}, credentials: 'same-origin' };
  if (opts.body !== undefined) { init.body = JSON.stringify(opts.body); init.headers['Content-Type'] = 'application/json'; }
  if (init.method !== 'GET') init.headers['X-CSRF-Token'] = state.me ? state.me.csrf : '';
  const res = await fetch(path, init);
  if (res.status === 401) { location.href = '/login'; throw new Error('signed out'); }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((data.error && data.error.message) || 'Request failed');
  return data;
}

function toast(msg) {
  const t = h('div', { class: 'toast', role: 'status', text: msg });
  $('#toasts').append(t);
  setTimeout(() => t.remove(), 5000);
}

// ---------- wallet, stake and limits ----------
const bucketOf = () => state.account.buckets.find((b) => b.name === state.bucket) || state.account.buckets[0];

function renderWallet(pendingStake = 0) {
  const b = bucketOf();
  $('#wallet').replaceChildren(h('small', { text: b.name }), `${fmt(b.balance - pendingStake)} CC`);
  const sel = $('#bucket');
  sel.replaceChildren(...state.account.buckets.map((x) => h('option', { value: x.name, selected: x.name === b.name }, `${x.name} · ${fmt(x.balance)} CC`)));
  $('#house').textContent = state.house ? `House bank: ${fmt(state.house.balance)} CC` : '';
  renderLimits();
}

// The best outcome of the current bet, as a multiple of the stake.
function multiplier() {
  if (state.game === 'slots') return 50;
  if (state.game === 'lucky') return 2;
  const b = state.bet;
  if (['red', 'black', 'odd', 'even', 'low', 'high'].includes(b)) return 2;
  if (['1-12', '13-24', '25-36'].includes(b)) return 3;
  return 36;
}

function maxStake() {
  const house = state.house ? Math.floor(state.house.balance / (multiplier() - 1)) : 0;
  return Math.max(0, Math.min(bucketOf().balance, house));
}

function renderLimits() {
  const houseMax = state.house ? Math.floor(state.house.balance / (multiplier() - 1)) : 0;
  const limit = houseMax < bucketOf().balance ? `The house covers bets up to ${fmt(houseMax)} CC on this one.` : '';
  $('#limits').textContent = `Pays up to ${multiplier()}× your stake. ${limit}`;
}

function setStake(n) {
  $('#stake').value = Math.max(1, Math.floor(n) || 1);
}

function renderChips() {
  const stake = () => Number($('#stake').value) || 0;
  const chip = (label, f) => h('button', { class: 'chip', type: 'button', onclick: () => setStake(f()) }, label);
  $('#chips').replaceChildren(
    ...[1, 5, 10, 25, 100].map((n) => chip(String(n), () => n)),
    chip('½', () => stake() / 2), chip('2×', () => stake() * 2), chip('Max', maxStake));
}

// ---------- roulette ----------
const WHEEL = [0, 32, 15, 19, 4, 21, 2, 25, 17, 34, 6, 27, 13, 36, 11, 30, 8, 23, 10, 5, 24, 16, 33, 1, 20, 14, 31, 9, 22, 18, 29, 7, 28, 12, 35, 3, 26];
const SEG = 360 / WHEEL.length;
const roulette = { wheelRot: 0, ballRot: 0, el: null };
const colorOf = (n) => (n === 0 ? 'green' : state.games.redPockets.includes(n) ? 'red' : 'black');

function polar(r, deg) {
  const a = (deg - 90) * Math.PI / 180;
  return `${(150 + r * Math.cos(a)).toFixed(2)} ${(150 + r * Math.sin(a)).toFixed(2)}`;
}

function wheelSVG() {
  const wheel = s('g', { class: 'wheel' });
  const fills = { red: '#c62b36', black: '#1b1b1b', green: '#138a4f' };
  WHEEL.forEach((n, i) => {
    const a0 = i * SEG - SEG / 2, a1 = a0 + SEG;
    wheel.append(s('path', { d: `M${polar(136, a0)} A136 136 0 0 1 ${polar(136, a1)} L${polar(96, a1)} A96 96 0 0 0 ${polar(96, a0)}Z`, fill: fills[colorOf(n)], stroke: '#d4af5a', 'stroke-width': '0.6' }));
    wheel.append(s('text', { x: 150, y: 150 - 121, transform: `rotate(${i * SEG} 150 150)`, 'text-anchor': 'middle', 'dominant-baseline': 'middle', fill: '#f2e8cf', 'font-size': '11', 'font-weight': '700' }, n));
  });
  wheel.append(s('circle', { cx: 150, cy: 150, r: 96, fill: '#0b3b29', stroke: '#d4af5a', 'stroke-width': '2' }));
  for (let i = 0; i < 8; i++) wheel.append(s('line', { x1: 150, y1: 150, x2: polar(70, i * 45).split(' ')[0], y2: polar(70, i * 45).split(' ')[1], stroke: '#d4af5a', 'stroke-width': '3', 'stroke-linecap': 'round' }));
  wheel.append(s('circle', { cx: 150, cy: 150, r: 26, fill: '#d4af5a', stroke: '#8a6a22', 'stroke-width': '3' }));
  wheel.append(s('circle', { cx: 150, cy: 150, r: 8, fill: '#f0d488' }));
  const ball = s('g', { class: 'ball-track' }, s('circle', { cx: 150, cy: 150 - 126, r: 6.5, fill: '#fff', stroke: '#bbb', 'stroke-width': '1' }));
  const svg = s('svg', { viewBox: '0 0 300 300', role: 'img', 'aria-label': 'Roulette wheel' },
    s('circle', { cx: 150, cy: 150, r: 149, fill: '#3b2410' }),
    s('circle', { cx: 150, cy: 150, r: 141, fill: '#24160a', stroke: '#d4af5a', 'stroke-width': '1.5' }),
    wheel, ball);
  wheel.style.transform = `rotate(${roulette.wheelRot}deg)`;
  ball.style.transform = `rotate(${roulette.ballRot}deg)`;
  return { svg, wheel, ball };
}

function renderRoulette() {
  const w = wheelSVG();
  roulette.el = w;
  const landed = h('div', { class: 'landed', id: 'landed' });
  $('#stage').replaceChildren(h('div', { class: 'wheel-wrap' }, h('div', { class: 'pointer', 'aria-hidden': 'true' }), w.svg, landed));

  const cell = (bet, label, cls, extra) => {
    const b = h('button', { class: `cell ${cls || ''}`, type: 'button', 'aria-pressed': String(state.bet === bet), 'data-bet': bet, onclick: () => pickBet(bet) }, label);
    if (extra) for (const [k, v] of Object.entries(extra)) b.style.setProperty(k, v);
    return b;
  };
  const nums = [cell('0', '0', 'zero')];
  for (let n = 1; n <= 36; n++) nums.push(cell(String(n), n, colorOf(n), { '--c': String(Math.floor((n - 1) / 3)), '--r': String(3 - ((n - 1) % 3)) }));
  $('#betpanel').replaceChildren(
    h('span', { class: 'lbl', text: 'Your bet' }),
    h('div', { class: 'board' },
      h('div', { class: 'outside' }, [['red', 'Red', 'red'], ['black', 'Black', 'black'], ['odd', 'Odd'], ['even', 'Even'], ['low', '1–18'], ['high', '19–36'], ['1-12', '1st 12'], ['13-24', '2nd 12'], ['25-36', '3rd 12']].map(([b, l, c]) => cell(b, l, c))),
      h('div', { class: 'numbers' }, nums)),
    h('div', { class: 'betsel', id: 'betsel' }));
  pickBet(state.bet);
}

function pickBet(bet) {
  if (state.busy) return;
  state.bet = bet;
  document.querySelectorAll('.cell').forEach((c) => c.setAttribute('aria-pressed', String(c.dataset.bet === bet)));
  const m = multiplier();
  $('#betsel').replaceChildren('On ', h('b', { text: /^\d+$/.test(bet) ? `number ${bet}` : bet }), ` · pays ${m}× (${m - 1} to 1)`);
  renderLimits();
}

async function animateRoulette(draw) {
  const i = WHEEL.indexOf(draw.pocket);
  const target = -i * SEG;
  const r = roulette;
  r.wheelRot += 360 * 5 + ((((target - r.wheelRot) % 360) + 360) % 360);
  r.ballRot -= 360 * 3 + (((r.ballRot % 360) + 360) % 360);
  $('#landed').replaceChildren();
  r.el.wheel.style.transform = `rotate(${r.wheelRot}deg)`;
  r.el.ball.style.transform = `rotate(${r.ballRot}deg)`;
  await sleep(reduceMotion ? 450 : 4700);
  $('#landed').replaceChildren(h('span', { class: draw.color, text: draw.pocket }));
}

// ---------- slots ----------
const slots = { pos: [0, 7, 13], strips: [] };
const REPS = 10;

function renderSlots() {
  const strip = state.games.slotStrip;
  slots.strips = [0, 1, 2].map((r) => {
    const el = h('div', { class: 'strip' });
    for (let k = 0; k < REPS; k++) for (const sym of strip) el.append(h('div', { class: 'sym', text: sym }));
    return el;
  });
  const reels = slots.strips.map((st) => h('div', { class: 'reel' }, st));
  $('#stage').replaceChildren(h('div', { class: 'machine' },
    h('div', { class: 'marquee', text: '★ Lucky Sevens ★' }),
    h('div', { class: 'reels' }, reels, h('div', { class: 'payline', 'aria-hidden': 'true' }))));
  slots.strips.forEach((st, r) => placeReel(r, slots.pos[r], false));
  const pays = state.games.slotPays;
  $('#betpanel').replaceChildren(
    h('span', { class: 'lbl', text: 'Pays (three in a row)' }),
    h('div', { class: 'paytable' },
      Object.entries(pays).sort((a, b) => b[1] - a[1]).map(([sym, m]) => h('div', {}, `${sym}${sym}${sym} `, h('b', { text: `${m}×` }))),
      h('div', {}, '🍒🍒 any ', h('b', { text: '2×' }))));
}

// placeReel shows cell idx of a reel's strip on the payline.
function placeReel(r, idx, animate, ms) {
  const st = slots.strips[r];
  const cellH = st.firstChild.offsetHeight || 72;
  st.style.transition = animate ? `transform ${ms}ms cubic-bezier(.15, .75, .25, 1.04)` : 'none';
  st.style.transform = `translateY(${-(idx - 1) * cellH}px)`;
}

async function animateSlots(draw) {
  const n = state.games.slotStrip.length;
  document.querySelectorAll('.reel').forEach((r) => r.classList.remove('hit'));
  let longest = 0;
  draw.stops.forEach((stop, r) => {
    placeReel(r, n + (slots.pos[r] % n), false); // same symbol, one lap in, so there's room to spin
    slots.strips[r].getBoundingClientRect();
    const ms = reduceMotion ? 300 : 1300 + r * 450;
    longest = Math.max(longest, ms);
    slots.pos[r] = (6 + r) * n + stop;
    placeReel(r, slots.pos[r], true, ms);
  });
  await sleep(longest + 80);
  const [a, b, c] = draw.symbols;
  if (a === b && b === c) document.querySelectorAll('.reel').forEach((r) => r.classList.add('hit'));
}

// ---------- lucky dip ----------
function renderLucky() {
  $('#stage').replaceChildren(h('div', { class: 'dip' },
    h('div', { class: 'mult', id: 'mult', text: '1.00×' }),
    h('div', { class: 'sub', id: 'dipsub', text: 'Get back anywhere from nothing to double your stake.' }),
    h('div', { class: 'meter' }, h('i', { id: 'meter' })),
    h('div', { class: 'ticks' }, h('span', { text: '0×' }), h('span', { text: '1×' }), h('span', { text: '2×' }))));
  $('#betpanel').replaceChildren(h('p', { class: 'fine', text: 'A 5% chance of bust, otherwise a return drawn evenly from 0 to 2× your stake.' }));
}

async function animateLucky(draw, stake, payout) {
  const mult = $('#mult'), meter = $('#meter');
  mult.classList.remove('bust');
  meter.style.transition = 'none'; meter.style.width = '0'; meter.getBoundingClientRect(); meter.style.transition = '';
  const end = performance.now() + (reduceMotion ? 300 : 1500);
  meter.style.width = `${(payout / (2 * stake)) * 100}%`;
  while (performance.now() < end) { mult.textContent = `${(Math.random() * 2).toFixed(2)}×`; await sleep(55); }
  if (draw.gate < 5) { mult.textContent = 'BUST'; mult.classList.add('bust'); $('#dipsub').textContent = 'The 5% bust. Better luck next dip.'; }
  else { mult.textContent = `${(payout / stake).toFixed(2)}×`; $('#dipsub').textContent = `${fmt(payout)} CC back on ${fmt(stake)} CC`; }
  await sleep(250);
}

// ---------- playing ----------
const GAMES = {
  roulette: { render: renderRoulette, verb: 'Spin', animate: (d) => animateRoulette(d) },
  slots: { render: renderSlots, verb: 'Pull', animate: (d) => animateSlots(d) },
  lucky: { render: renderLucky, verb: 'Dip', animate: (d, st, p) => animateLucky(d, st, p) },
};

function selectGame(name) {
  if (state.busy || !GAMES[name]) return;
  state.game = name;
  document.querySelectorAll('.tabs button').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.game === name)));
  $('#play').textContent = GAMES[name].verb;
  $('#result').className = 'result'; $('#result').replaceChildren();
  GAMES[name].render();
  renderLimits();
  if (location.hash !== '#' + name) history.replaceState(null, '', '#' + name);
}

async function play() {
  if (state.busy) return;
  const stake = Number($('#stake').value);
  if (!Number.isInteger(stake) || stake < 1) return toast('Bet a whole number of coins, 1 or more.');
  if (stake > bucketOf().balance) return toast(`${bucketOf().name} has ${fmt(bucketOf().balance)} CC. Lower the stake or pick another bucket.`);
  state.busy = true;
  const btn = $('#play'); btn.disabled = true;
  const result = $('#result'); result.className = 'result'; result.replaceChildren();
  const old = $('#stage .stage-result'); if (old) old.remove();
  const game = state.game;
  // On a phone the button sits below the game: bring the game into view to watch it.
  const top = $('#stage').getBoundingClientRect().top;
  if (top < 0 || top > innerHeight * 0.4) $('#stage').scrollIntoView({ behavior: reduceMotion ? 'auto' : 'smooth', block: 'start' });
  try {
    const res = await api('/api/play', { method: 'POST', body: { game, bet: game === 'roulette' ? state.bet : '', stake, bucket: bucketOf().name, key: crypto.randomUUID() } });
    renderWallet(stake); // the stake leaves the wallet while the game plays out
    if (res.draw) await GAMES[game].animate(res.draw, res.stake, res.payout);
    state.account = res.account; state.house = res.house;
    renderWallet();
    showResult(res);
  } catch (x) {
    if (x.message !== 'signed out') toast(x.message);
  } finally {
    state.busy = false; btn.disabled = false;
  }
}

function showResult(res) {
  const net = res.payout - res.stake;
  const result = $('#result');
  result.className = 'result ' + (net > 0 ? 'win' : net < 0 ? 'lose' : 'even');
  const headline = net > 0 ? `You win ${fmt(net)} CC!` : net < 0 ? (res.payout > 0 ? `${fmt(res.payout)} CC back` : 'No luck this time') : 'Your stake back';
  const detail = `${fmt(res.payout)} CC back on ${fmt(res.stake)} CC · ${res.memo}`;
  result.replaceChildren(h('div', {}, headline, h('small', { text: detail })));
  $('#stage').append(h('div', { class: 'stage-result ' + (net > 0 ? 'win' : net < 0 ? 'lose' : 'even'), text: `${net >= 0 ? '+' : '−'}${fmt(Math.abs(net))} CC` }));
  state.plays.unshift(res);
  state.plays = state.plays.slice(0, 15);
  $('#history').replaceChildren(...state.plays.map((p) => {
    const n = p.payout - p.stake;
    return h('div', { class: 'hrow' }, h('span', { class: 'm', text: p.memo }),
      h('span', { class: 'n ' + (n > 0 ? 'win' : n < 0 ? 'lose' : ''), text: `${n >= 0 ? '+' : '−'}${fmt(Math.abs(n))} CC` }));
  }));
}

// ---------- boot ----------
function userMenu() {
  const btn = $('#userbtn');
  btn.textContent = state.me.login;
  let menu = null;
  const close = () => { menu && menu.remove(); menu = null; };
  btn.addEventListener('click', (e) => {
    e.stopPropagation();
    if (menu) return close();
    menu = h('div', { class: 'menu', role: 'menu' },
      h('a', { role: 'menuitem', href: state.me.bankUrl, target: '_blank', rel: 'noopener' }, 'Open Bailey Bank'),
      h('button', { role: 'menuitem', onclick: async () => { await api('/logout', { method: 'POST' }).catch(() => {}); location.href = '/login'; } }, 'Sign out'));
    btn.append(menu);
  });
  document.addEventListener('click', close);
}

async function refresh() {
  if (state.busy) return;
  const me = await api('/api/me');
  state.account = me.account; state.house = me.house;
  renderWallet();
}

async function boot() {
  state.me = await api('/api/me');
  Object.assign(state, { account: state.me.account, house: state.me.house, games: state.me.games });
  if (!state.account.buckets.some((b) => b.name === state.bucket)) state.bucket = 'Main';
  $('#banklink').href = state.me.bankUrl;
  userMenu(); renderChips(); renderWallet();
  $('#bucket').addEventListener('change', (e) => { state.bucket = e.target.value; store.set('casino.bucket', state.bucket); renderWallet(); });
  $('#stake').addEventListener('keydown', (e) => { if (e.key === 'Enter') play(); });
  $('#play').addEventListener('click', play);
  document.querySelectorAll('.tabs button').forEach((b) => b.addEventListener('click', () => selectGame(b.dataset.game)));
  document.addEventListener('visibilitychange', () => { if (!document.hidden) refresh().catch(() => {}); });
  selectGame(location.hash.slice(1) in GAMES ? location.hash.slice(1) : 'roulette');
}

boot().catch((e) => { if (e.message !== 'signed out') $('#stage').replaceChildren(h('div', { class: 'alert', text: 'Could not load the casino: ' + e.message })); });
