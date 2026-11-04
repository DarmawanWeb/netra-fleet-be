// The NETRA License Manager UI: first-run setup, login, then the app shell with a hash router.

import { $, $$, ApiError, busy, dialog, field, get, html, icon, on, onUnauthorized, post, showError, toast } from './lib.js';
import { dashboardPage } from './pages/dashboard.js';
import { licenseDetailPage, licenseListPage } from './pages/licenses.js';
import { issuePage } from './pages/issue.js';
import { clientDetailPage, clientListPage } from './pages/clients.js';
import { verifyPage } from './pages/verify.js';
import { keyPage } from './pages/key.js';
import { operatorsPage } from './pages/operators.js';
import { auditPage } from './pages/audit.js';

const app = {
  user: null,
  meta: null,
  key: null,
  version: '',
  keyListeners: new Set(),
};

const NAV = [
  { title: 'Overview', items: [{ id: 'dashboard', label: 'Dashboard', icon: 'dashboard' }] },
  {
    title: 'Licenses',
    items: [
      { id: 'issue', label: 'Issue license', icon: 'issue' },
      { id: 'licenses', label: 'Licenses', icon: 'license' },
      { id: 'verify', label: 'Verify', icon: 'verify' },
    ],
  },
  { title: 'Clients', items: [{ id: 'clients', label: 'Clients', icon: 'clients' }] },
  {
    title: 'Administration',
    items: [
      { id: 'key', label: 'Signing key', icon: 'key' },
      { id: 'operators', label: 'Operators', icon: 'users' },
      { id: 'audit', label: 'Audit log', icon: 'audit' },
    ],
  },
];

const ROUTES = [
  [/^\/(?:dashboard)?$/, 'dashboard', dashboardPage],
  [/^\/licenses$/, 'licenses', licenseListPage],
  [/^\/licenses\/([\w-]+)$/, 'licenses', licenseDetailPage],
  [/^\/issue$/, 'issue', issuePage],
  [/^\/clients$/, 'clients', clientListPage],
  [/^\/clients\/([\w-]+)$/, 'clients', clientDetailPage],
  [/^\/verify$/, 'verify', verifyPage],
  [/^\/key$/, 'key', keyPage],
  [/^\/operators$/, 'operators', operatorsPage],
  [/^\/audit$/, 'audit', auditPage],
];

const root = $('#app');

// Theme

const themeIcon = () => icon(document.documentElement.dataset.theme === 'light' ? 'moon' : 'sun');
function toggleTheme() {
  const next = document.documentElement.dataset.theme === 'light' ? 'dark' : 'light';
  document.documentElement.dataset.theme = next;
  try {
    localStorage.setItem('netra-lm.theme', next);
  } catch {
    /* private mode: not remembered */
  }
  $$('[data-theme-toggle]').forEach((b) => {
    b.innerHTML = String(themeIcon());
    b.title = next === 'light' ? 'Dark theme' : 'Light theme';
  });
}
const themeButton = () =>
  html`<button class="btn icon" data-theme-toggle title="${document.documentElement.dataset.theme === 'light' ? 'Dark theme' : 'Light theme'}" aria-label="Switch theme">${themeIcon()}</button>`;
on(document, 'click', '[data-theme-toggle]', toggleTheme);

// First run and login

function authScreen({ intro, fields, submit, note }) {
  root.innerHTML = String(html`
    <div class="auth">
      <div class="auth-corner">${themeButton()}</div>
      <div class="auth-card">
        <img class="logo" src="favicon.svg" alt="">
        <div class="auth-title"><div class="name">NETRA</div><div class="sub">License Manager</div></div>
        ${intro ? html`<p class="auth-intro">${intro}</p>` : ''}
        <form id="auth-form" novalidate>
          ${fields}
          <div class="form-error"></div>
          <button class="btn primary lg block" type="submit">${submit}</button>
        </form>
        <div class="auth-note">${note}</div>
      </div>
    </div>`);
  return $('#auth-form');
}

function showSetup() {
  const form = authScreen({
    intro: 'First start: create the first operator. Operators can add others later.',
    fields: html`${field({ name: 'username', label: 'Username', auto: 'username', placeholder: 'e.g. budi' })}
      ${field({ name: 'password', label: 'Password', type: 'password', auto: 'new-password', hint: 'At least 10 characters.' })}`,
    submit: 'Create operator',
    note: html`EZRA Robotics internal tool · ${app.version}`,
  });
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const data = Object.fromEntries(new FormData(form));
    try {
      await busy($('button[type=submit]', form), async () => {
        const res = await post('/setup', data);
        app.user = res.user;
      });
      await startApp();
    } catch (err) {
      showError(form, err);
    }
  });
}

function showLogin(expired = false) {
  stopApp();
  const form = authScreen({
    intro: expired ? 'Your session ended. Sign in again.' : '',
    fields: html`${field({ name: 'username', label: 'Username', auto: 'username' })}
      ${field({ name: 'password', label: 'Password', type: 'password', auto: 'current-password' })}`,
    submit: 'Sign in',
    note: html`EZRA Robotics internal tool. Licenses signed here are accepted by NETRA servers · ${app.version}`,
  });
  $('input', form).focus();
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const data = Object.fromEntries(new FormData(form));
    try {
      await busy($('button[type=submit]', form), async () => {
        const res = await post('/login', data);
        app.user = res.user;
      });
      await startApp();
    } catch (err) {
      showError(form, err);
    }
  });
}

// Signing key status

function keyPill() {
  const k = app.key;
  const pill = $('#keypill');
  if (!pill || !k) return;
  let led = 'warn';
  let label = 'Key locked';
  let sub = 'Unlock';
  if (!k.exists) {
    led = 'danger';
    label = 'No signing key';
    sub = 'Set up';
  } else if (k.unlocked) {
    led = 'ok';
    label = 'Key unlocked';
    const min = Math.max(0, Math.round((new Date(k.locks_at) - Date.now()) / 60000));
    sub = `locks in ${min} min`;
  }
  pill.innerHTML = String(html`<span class="led ${led}"></span>${label}<span class="sub">${sub}</span>`);
}

async function refreshKey() {
  try {
    app.key = await get('/key');
  } catch {
    return app.key;
  }
  keyChanged();
  return app.key;
}

function keyChanged() {
  keyPill();
  app.keyListeners.forEach((fn) => fn(app.key));
}

/** Asks for the passphrase when the key is locked; resolves true once it is unlocked. */
function ensureUnlocked() {
  return new Promise((resolve) => {
    const k = app.key;
    if (k && k.unlocked) return resolve(true);
    if (!k || !k.exists) {
      toast('Generate or import the signing key first.', 'warn');
      location.hash = '#/key';
      return resolve(false);
    }
    let ok = false;
    const d = dialog({
      title: 'Unlock the signing key',
      body: html`<form id="unlock-form" novalidate>
        <p class="muted small">The key is decrypted into this process only. It locks itself after
        ${k.idle_minutes} minutes without use, and when the manager stops.</p>
        <dl class="kv tight"><dt>Fingerprint</dt><dd class="mono">${k.fingerprint}</dd></dl>
        <div class="field-gap"></div>
        ${field({ name: 'passphrase', label: 'Passphrase', type: 'password', auto: 'current-password' })}
        <div class="form-error"></div>
      </form>`,
      foot: html`<button class="btn" data-close>Cancel</button>
        <button class="btn primary" type="submit" form="unlock-form">${icon('unlock', 16)} Unlock</button>`,
      onClose: () => resolve(ok),
    });
    const form = $('#unlock-form', d.root);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      try {
        await busy($('button[type=submit]', d.root), async () => {
          app.key = await post('/key/unlock', { passphrase: form.passphrase.value });
        });
        ok = true;
        keyChanged();
        toast('Signing key unlocked.');
        d.close();
      } catch (err) {
        showError(form, err);
      }
    });
  });
}

function keyMenu() {
  const k = app.key;
  if (!k || !k.exists) {
    location.hash = '#/key';
    return;
  }
  if (!k.unlocked) {
    ensureUnlocked();
    return;
  }
  const d = dialog({
    title: 'Signing key',
    body: html`<div class="keycard ok"><div class="glyph">${icon('unlock', 24)}</div><div class="grow">
        <h3>Unlocked</h3><p>Locks itself at ${new Date(k.locks_at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}
        unless it signs something before.</p></div></div>
      <dl class="kv tight"><dt>Fingerprint</dt><dd class="mono">${k.fingerprint}</dd></dl>`,
    foot: html`<a class="btn" href="#/key" data-close>Key details</a>
      <button class="btn danger" data-lock>${icon('lock', 16)} Lock now</button>`,
  });
  on(d.root, 'click', '[data-lock]', async () => {
    app.key = await post('/key/lock');
    keyChanged();
    toast('Signing key locked.');
    d.close();
  });
}

// Shell

let timers = [];
let cleanup = null;
let seq = 0;

function stopApp() {
  timers.forEach(clearInterval);
  timers = [];
  window.removeEventListener('hashchange', route);
  if (cleanup) cleanup();
  cleanup = null;
}

async function startApp() {
  stopApp();
  app.meta = await get('/meta');
  await refreshKey();
  renderShell();
  window.addEventListener('hashchange', route);
  timers.push(setInterval(refreshKey, 30000));
  await route();
  refreshCounts();
}

/** The number of licenses needing attention, on the Licenses menu entry. */
async function refreshCounts() {
  try {
    const d = await get('/dashboard');
    const n = d.counts.expiring + d.counts.grace;
    const el = $('[data-nav="licenses"] .count');
    if (el) {
      el.textContent = String(n);
      el.classList.toggle('hidden', n === 0);
      el.title = `${n} license${n === 1 ? '' : 's'} expiring or in grace`;
    }
  } catch {
    /* the counter is a convenience */
  }
}

function renderShell() {
  const u = app.user;
  root.innerHTML = String(html`
    <div class="shell">
      <aside class="sidebar"><div class="sidebar-inner">
        <div class="brand"><img src="favicon.svg" alt=""><div><div class="brand-name">NETRA</div><div class="brand-sub">License Manager</div></div></div>
        <nav class="nav">
          ${NAV.map(
            (s) => html`<div class="nav-title">${s.title}</div>
              ${s.items.map(
                (i) =>
                  html`<a href="#/${i.id}" data-nav="${i.id}">${icon(i.icon)}<span>${i.label}</span>${
                    i.id === 'licenses' ? html`<span class="count hidden"></span>` : ''
                  }</a>`,
              )}`,
          )}
        </nav>
        <div class="vendor-tag"><b>Vendor only</b>Never install this on a client server. It signs every NETRA license.</div>
        <div class="me">
          <div class="grow"><div class="me-name">${u.username}</div><div class="me-role">Operator</div></div>
          <button class="btn icon" data-logout title="Sign out" aria-label="Sign out">${icon('logout')}</button>
        </div>
      </div></aside>
      <div class="main">
        <header class="topbar">
          <div class="crumbs" id="crumbs"></div>
          <div class="topbar-right">
            <button class="keypill" id="keypill" title="Signing key"></button>
            ${themeButton()}
          </div>
        </header>
        <main class="content" id="content"></main>
      </div>
    </div>`);
  keyPill();
  $('#keypill').addEventListener('click', keyMenu);
  $('[data-logout]').addEventListener('click', async () => {
    await post('/logout').catch(() => null);
    app.user = null;
    history.replaceState(null, '', location.pathname);
    showLogin();
  });
}

function setActive(id) {
  $$('[data-nav]').forEach((a) => a.classList.toggle('active', a.dataset.nav === id));
}

function setTitle(...parts) {
  const crumbs = $('#crumbs');
  if (!crumbs) return;
  crumbs.innerHTML = String(
    html`${parts.map((p, i) => (i === parts.length - 1 ? html`<b>${p}</b>` : html`<span>${p}</span><span class="dim">/</span>`))}`,
  );
  document.title = `${parts[parts.length - 1]} · NETRA License Manager`;
}

async function route() {
  const hash = location.hash.replace(/^#/, '') || '/dashboard';
  const [path, qs] = hash.split('?');
  const match = ROUTES.find(([re]) => re.test(path));
  if (!match) {
    location.hash = '#/dashboard';
    return;
  }
  const [re, nav, page] = match;
  setActive(nav);
  if (cleanup) cleanup();
  cleanup = null;
  // A fresh element per page, so the handlers a page delegates go away with it.
  const content = $('#content');
  const view = document.createElement('div');
  const mine = ++seq;
  content.replaceChildren(view);
  window.scrollTo(0, 0);
  const ctx = {
    root: view,
    params: path.match(re).slice(1),
    query: new URLSearchParams(qs || ''),
    app,
    setTitle,
    refreshKey,
    refreshCounts,
    ensureUnlocked,
    current: () => mine === seq,
    onKey: (fn) => {
      app.keyListeners.add(fn);
      return () => app.keyListeners.delete(fn);
    },
  };
  try {
    const done = await page(ctx);
    if (mine === seq && typeof done === 'function') cleanup = done;
  } catch (err) {
    if (mine !== seq || (err instanceof ApiError && err.status === 401)) return;
    view.innerHTML = String(html`<div class="callout danger">${icon('alert')}<div class="grow"><b>This page could not load.</b>
      <p>${err.message}</p></div></div>`);
  }
}

// Boot

onUnauthorized(() => {
  if (app.user) {
    app.user = null;
    showLogin(true);
  }
});

(async () => {
  try {
    const s = await get('/session');
    app.version = s.version;
    if (s.setup) return showSetup();
    if (!s.user) return showLogin();
    app.user = s.user;
    await startApp();
  } catch (err) {
    root.innerHTML = String(html`<div class="auth"><div class="auth-card"><div class="callout danger">${icon('alert')}
      <div class="grow"><b>The license manager does not answer.</b><p>${err.message}</p></div></div></div></div>`);
  }
})();
