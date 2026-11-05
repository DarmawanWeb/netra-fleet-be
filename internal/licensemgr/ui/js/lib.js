// Shared helpers: escaped HTML templates, the API client, formatting, icons, dialogs and toasts.

const ESCAPES = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
export const esc = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ESCAPES[c]);

class Safe {
  constructor(s) {
    this.s = s;
  }
  toString() {
    return this.s;
  }
}

/** Marks trusted markup; html`` escapes everything else. */
export const raw = (s) => new Safe(String(s));

const part = (v) => {
  if (v instanceof Safe) return v.s;
  if (Array.isArray(v)) return v.map(part).join('');
  if (v === null || v === undefined || v === false || v === true) return '';
  return esc(v);
};

export const html = (strings, ...values) =>
  new Safe(strings.reduce((out, s, i) => out + s + (i < values.length ? part(values[i]) : ''), ''));

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

/** Delegated event handler on root for elements matching sel. */
export function on(root, event, sel, fn) {
  root.addEventListener(event, (e) => {
    const el = e.target.closest(sel);
    if (el && root.contains(el)) fn(e, el);
  });
}

// API

export class ApiError extends Error {
  constructor(status, body) {
    super((body && body.error) || `The manager answered ${status}`);
    this.status = status;
    this.field = body && body.field;
    this.code = body && body.code;
  }
}

let unauthorized = () => {};
export const onUnauthorized = (fn) => {
  unauthorized = fn;
};

export async function api(method, path, body) {
  const init = { method, credentials: 'same-origin', headers: { 'X-Netra-Manager': '1', Accept: 'application/json' } };
  if (body !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(body);
  }
  let res;
  try {
    res = await fetch('/api' + path, init);
  } catch {
    throw new ApiError(0, { error: 'The license manager does not answer. Is it still running?' });
  }
  if (res.status === 204) return null;
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    if (res.status === 401 && path !== '/login' && path !== '/setup') unauthorized();
    throw new ApiError(res.status, data);
  }
  return data;
}
export const get = (path) => api('GET', path);
export const post = (path, body = {}) => api('POST', path, body);
export const put = (path, body = {}) => api('PUT', path, body);
export const del = (path) => api('DELETE', path);

// Labels (as the NETRA dashboard names them)

export const MODULES = {
  anomaly: 'Anomaly detection',
  crowd: 'Crowd analytics',
  substation: 'Substation inspection',
  'reports.export': 'Report exports',
  'alerts.telegram': 'Telegram alerts',
  audit: 'Audit log',
  'ai.final': 'Server AI verification',
  integration: 'Integration API',
  multisite: 'Multi-site sync',
};
export const EDITIONS = { mall: 'Mall', substation: 'Substation', custom: 'Custom' };
export const EDITION_TEXT = {
  mall: 'Shopping malls and buildings: anomaly queue and crowd analytics.',
  substation: 'Substations: assets, thermal ΔT and partial discharge inspection.',
  custom: 'Core only; pick the modules one by one.',
};
export const TIERS = { basic: 'Basic', pro: 'Pro', enterprise: 'Enterprise' };
export const TIER_TEXT = {
  basic: 'The edition’s modules only.',
  pro: 'Adds report exports, Telegram alerts and the audit log.',
  enterprise: 'Pro plus server AI, the integration API and multi-site.',
};
export const MAPS = { floorplan: 'Floor plan', pgm: 'Robot map', gps: 'GPS map' };
export const MAP_TEXT = {
  floorplan: 'Indoor floor plan (SVG, PNG, JPG, PDF) calibrated onto the robot map.',
  pgm: 'The robot’s own occupancy grid (map.yaml + map.pgm).',
  gps: 'Outdoor basemap in latitude and longitude.',
};
export const moduleName = (id) => MODULES[id] || id;

/** keys in the given order, then any others. */
export const ordered = (keys, order) => [...order.filter((k) => keys.includes(k)), ...keys.filter((k) => !order.includes(k))];
export const EDITION_ORDER = ['mall', 'substation', 'custom'];
export const TIER_ORDER = ['basic', 'pro', 'enterprise'];

/** The modules of a license in the catalog's display order. */
export const catalogOrder = (features, catalog) => ordered(features || [], catalog.map((m) => m.id));

/** The size of a data URL's content, e.g. "461 bytes" or "12 KB". */
export function dataSize(url) {
  const n = Math.floor(((url.length - url.indexOf(',') - 1) * 3) / 4);
  if (n < 1024) return `${n} bytes`;
  return n < 10240 ? `${(n / 1024).toFixed(1)} KB` : `${Math.round(n / 1024)} KB`;
}
export const editionTier = (l) => `${EDITIONS[l.edition] || l.edition} · ${TIERS[l.tier] || l.tier}`;

// Dates

const fmtD = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
const fmtDT = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' });
const fmtLong = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'long', year: 'numeric' });

export const isZero = (t) => !t || t.startsWith('0001-01-01');
export const date = (t) => (isZero(t) ? '—' : fmtD.format(new Date(t)));
export const dateTime = (t) => (isZero(t) ? '—' : fmtDT.format(new Date(t)));
export const longDate = (t) => (isZero(t) ? '—' : fmtLong.format(new Date(t)));
const DAY = 86400000;
export const daysUntil = (t) => Math.ceil((new Date(t) - Date.now()) / DAY);

/** "in 12 days", "3 days ago", "today". */
export function relDays(t) {
  if (isZero(t)) return '';
  const ms = new Date(t) - Date.now();
  const d = Math.round(Math.abs(ms) / DAY);
  if (d === 0) return ms >= 0 ? 'today' : 'earlier today';
  const unit = d === 1 ? 'day' : 'days';
  return ms >= 0 ? `in ${d} ${unit}` : `${d} ${unit} ago`;
}

/** "just now", "5 min ago", "3 h ago", or the date. */
export function ago(t) {
  const s = (Date.now() - new Date(t)) / 1000;
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  if (s < 7 * 86400) return `${Math.floor(s / 86400)} d ago`;
  return date(t);
}

/** A date input's value (local) for a timestamp. */
export function dateInput(t) {
  const d = new Date(t);
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** The end of a local day (YYYY-MM-DD) as RFC 3339 with the browser's offset. */
export function endOfDay(day) {
  const [y, m, d] = day.split('-').map(Number);
  const t = new Date(y, m - 1, d, 23, 59, 59);
  const off = -t.getTimezoneOffset();
  const pad = (n) => String(Math.floor(Math.abs(n))).padStart(2, '0');
  const sign = off >= 0 ? '+' : '-';
  return `${day}T23:59:59${sign}${pad(off / 60)}:${pad(off % 60)}`;
}

// Status of a license

export function statusOf(v) {
  if (v.superseded_by) return { tone: 'muted', label: 'Replaced' };
  if (v.status === 'valid' && v.expiring) return { tone: 'warn', label: 'Expiring' };
  if (v.status === 'valid') return { tone: 'ok', label: 'Valid' };
  if (v.status === 'grace') return { tone: 'warn', label: 'In grace' };
  return { tone: 'danger', label: 'Expired' };
}

export const badge = (tone, label, led = true) => html`<span class="badge ${tone}">${led ? raw('<span class="led"></span>') : ''}${label}</span>`;
export const statusBadge = (v) => {
  const s = statusOf(v);
  return badge(s.tone, s.label);
};

/** Expiry with its distance under it, for a table cell. */
export function expiry(l) {
  if (isZero(l.expires_at)) return html`<span class="muted">No expiry</span>`;
  return html`${date(l.expires_at)}<div class="cell-sub">${relDays(l.expires_at)}</div>`;
}

// Icons (the NETRA dashboard's line style)

const ICONS = {
  dashboard: 'M4 4h7v9H4zM13 4h7v5h-7zM13 11h7v9h-7zM4 15h7v5H4z',
  license: 'M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8l-5-5Zm0 0v5h5M9 13h6m-6 4h4',
  issue: 'M12 5v14M5 12h14',
  verify: 'M12 3 5 6v5c0 4.5 3 8.4 7 10 4-1.6 7-5.5 7-10V6l-7-3Zm-3.5 9 2.5 2.5 5-5',
  clients: 'M3 21V7l6-4 6 4v14M15 21V11h6v10M3 21h18M7 9h4m-4 4h4m-4 4h4',
  key: 'M15 7a3 3 0 1 1-6 0 3 3 0 0 1 6 0Zm-3 3v11m0-4h3m-3-3h2',
  users: 'M16 19v-1a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v1m7-9a3 3 0 1 0 0-6 3 3 0 0 0 0 6Zm13 9v-1a4 4 0 0 0-3-3.9M16 4.1a3 3 0 0 1 0 5.8',
  audit:
    'M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2M9 5a2 2 0 0 0 2 2h2a2 2 0 0 0 2-2M9 5a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2m-6 9 2 2 4-4',
  sun: 'M12 4V2m0 20v-2m8-8h2M2 12h2m13.7-5.7 1.4-1.4M4.9 19.1l1.4-1.4m0-11.4L4.9 4.9m14.2 14.2-1.4-1.4M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10Z',
  moon: 'M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z',
  logout: 'M15 4h4v16h-4M10 8l-4 4 4 4m-4-4h11',
  lock: 'M6 11h12v10H6zM8 11V8a4 4 0 0 1 8 0v3m-4 4v2',
  unlock: 'M6 11h12v10H6zM8 11V8a4 4 0 0 1 7.5-2m-3.5 9v2',
  download: 'M12 4v11m-5-5 5 5 5-5M5 20h14',
  copy: 'M9 9h10v12H9zM5 15V3h10',
  check: 'm5 12 5 5 9-10',
  x: 'M6 6l12 12M18 6 6 18',
  alert: 'M12 3 2 21h20L12 3Zm0 6v5m0 3v.5',
  info: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Zm0-10v6m0-9v.5',
  refresh: 'M20 11a8 8 0 0 0-14.6-4.5L4 8m0-5v5h5m-5 5a8 8 0 0 0 14.6 4.5L20 16m0 5v-5h-5',
  edit: 'M4 20h4L19 9l-4-4L4 16v4Zm9-13 4 4',
  trash: 'M4 7h16M10 11v6m4-6v6M6 7l1 13h10l1-13M9 7V4h6v3',
  server: 'M4 4h16v6H4zM4 14h16v6H4zM8 7h.01M8 17h.01',
  link: 'M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1m2 3.7a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1',
  plus: 'M12 5v14M5 12h14',
  arrow: 'M5 12h14m-6-6 6 6-6 6',
  file: 'M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8l-5-5Zm0 0v5h5',
  mail: 'M3 6h18v12H3zm0 0 9 7 9-7',
  shield: 'M12 3 5 6v5c0 4.5 3 8.4 7 10 4-1.6 7-5.5 7-10V6l-7-3Z',
  clock: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Zm0-13v5l3 2',
  pin: 'M12 21s-7-6.2-7-11a7 7 0 0 1 14 0c0 4.8-7 11-7 11Zm0-8a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z',
  layers: 'm12 3 9 5-9 5-9-5 9-5Zm-9 9 9 5 9-5M3 16l9 5 9-5',
  upload: 'M12 16V5m-5 5 5-5 5 5M5 20h14',
};

export const icon = (name, size = 18) =>
  raw(
    `<svg viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="${ICONS[name] || ICONS.info}"/></svg>`,
  );

// Toasts, dialogs, clipboard

export function toast(message, tone = 'ok') {
  const box = $('#toasts');
  while (box.children.length >= 3) box.firstElementChild.remove();
  const el = document.createElement('div');
  el.className = `toast ${tone}`;
  el.textContent = message;
  box.append(el);
  setTimeout(() => el.classList.add('out'), 3600);
  setTimeout(() => el.remove(), 4000);
}

/** Opens a dialog; returns {root, close}. onClose runs once when it closes. */
export function dialog({ title, body, foot, wide = false, onClose }) {
  const overlay = document.createElement('div');
  overlay.className = 'overlay';
  overlay.innerHTML = String(html`<div class="dialog ${wide ? 'wide' : ''}" role="dialog" aria-modal="true" aria-label="${title}">
    <div class="dialog-head"><h3>${title}</h3><button class="btn icon" data-close aria-label="Close">${icon('x')}</button></div>
    <div class="dialog-body">${body}</div>
    ${foot ? html`<div class="dialog-foot">${foot}</div>` : ''}
  </div>`);
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    overlay.remove();
    document.removeEventListener('keydown', onKey);
    if (onClose) onClose();
  };
  const onKey = (e) => {
    if (e.key === 'Escape') close();
  };
  overlay.addEventListener('mousedown', (e) => {
    if (e.target === overlay) close();
  });
  on(overlay, 'click', '[data-close]', close);
  document.addEventListener('keydown', onKey);
  document.body.append(overlay);
  const first = $('input:not([type=hidden]), textarea, select', overlay);
  if (first) first.focus();
  return { root: overlay, close };
}

/** A yes/no question; resolves true on yes. */
export function confirmDialog({ title, text, yes = 'Confirm', danger = false }) {
  return new Promise((resolve) => {
    let answer = false;
    const d = dialog({
      title,
      body: html`<p class="muted">${text}</p>`,
      foot: html`<button class="btn" data-close>Cancel</button><button class="btn ${danger ? 'danger' : 'primary'}" data-yes>${yes}</button>`,
      onClose: () => resolve(answer),
    });
    on(d.root, 'click', '[data-yes]', () => {
      answer = true;
      d.close();
    });
  });
}

export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.className = 'hidden-copy';
    document.body.append(ta);
    ta.select();
    document.execCommand('copy');
    ta.remove();
  }
}

// Forms

/** Clears the error marks of a form. */
export function clearErrors(form) {
  $$('.invalid', form).forEach((el) => el.classList.remove('invalid'));
  const box = $('.form-error', form);
  if (box) box.classList.remove('show');
}

/** Shows an API error on its field (data-field) or in the form's .form-error box. */
export function showError(form, err) {
  clearErrors(form);
  const field = err && err.field ? $(`[data-field="${CSS.escape(err.field)}"]`, form) : null;
  if (field) {
    field.classList.add('invalid');
    const msg = $('.msg', field);
    if (msg) msg.textContent = err.message;
    const input = $('input, select, textarea', field);
    if (input) input.focus();
    field.scrollIntoView({ block: 'center', behavior: 'smooth' });
    return;
  }
  const box = $('.form-error', form);
  if (box) {
    box.textContent = err.message;
    box.classList.add('show');
  } else {
    toast(err.message, 'danger');
  }
}

/** A field marked invalid with message, from the browser side. */
export function fieldError(field, message) {
  const e = new Error(message);
  e.field = field;
  return e;
}

/** Runs fn with the button marked busy. */
export async function busy(button, fn) {
  if (!button) return fn();
  button.classList.add('busy');
  button.disabled = true;
  try {
    return await fn();
  } finally {
    button.classList.remove('busy');
    button.disabled = false;
  }
}

export const field = ({ name, label, value = '', type = 'text', hint, opt, placeholder, mono, auto, attrs = '' }) => html`
  <label class="field" data-field="${name}">
    <span class="label">${label}${opt ? html`<span class="opt">${opt}</span>` : ''}</span>
    <input class="input ${mono ? 'mono' : ''}" name="${name}" type="${type}" value="${value}" placeholder="${placeholder || ''}" autocomplete="${auto || 'off'}" ${raw(attrs)}>
    ${hint ? html`<div class="hint">${hint}</div>` : ''}
    <div class="msg"></div>
  </label>`;

export const textareaField = ({ name, label, value = '', hint, opt, placeholder, mono, rows = 3 }) => html`
  <label class="field" data-field="${name}">
    <span class="label">${label}${opt ? html`<span class="opt">${opt}</span>` : ''}</span>
    <textarea class="textarea ${mono ? 'mono' : ''}" name="${name}" rows="${rows}" placeholder="${placeholder || ''}">${value}</textarea>
    ${hint ? html`<div class="hint">${hint}</div>` : ''}
    <div class="msg"></div>
  </label>`;

/** Paints elements with data-swatch="#rrggbb" (inline styles are not allowed by the CSP). */
export function paintSwatches(root) {
  $$('[data-swatch]', root).forEach((el) => {
    const c = el.dataset.swatch;
    if (/^#[0-9a-f]{6}$/i.test(c)) el.style.background = c;
  });
}

/** Pretty JSON with the logo shortened, keys, strings and numbers marked for colour. */
export function prettyJSON(value) {
  const text = JSON.stringify(
    value,
    (k, v) => (k === 'logo' && typeof v === 'string' && v.length > 72 ? `${v.slice(0, 64)}… (${Math.round((v.length * 3) / 4 / 1024)} KB)` : v),
    2,
  );
  return raw(
    esc(text).replace(/(&quot;(?:\\.|[^&\\]|&(?!quot;))*?&quot;)(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?)/g, (m, str, colon, lit, num) => {
      if (str) return colon ? `<span class="k">${str}</span>${colon}` : `<span class="s">${str}</span>`;
      if (lit) return `<span class="b">${lit}</span>`;
      return `<span class="n">${num}</span>`;
    }),
  );
}

export const empty = (iconName, text, action = '') => html`<div class="empty">${icon(iconName, 30)}<div>${text}</div>${action}</div>`;

/** Renders page again in a fresh element, so the handlers delegated by the old render go away. */
export function rerender(ctx, page) {
  const fresh = document.createElement('div');
  ctx.root.replaceWith(fresh);
  return page({ ...ctx, root: fresh });
}
