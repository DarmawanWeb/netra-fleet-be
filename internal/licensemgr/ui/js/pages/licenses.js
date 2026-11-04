import {
  $,
  EDITIONS,
  EDITION_ORDER,
  MAPS,
  MODULES,
  TIERS,
  busy,
  catalogOrder,
  copyText,
  dataSize,
  date,
  dateInput,
  dateTime,
  dialog,
  editionTier,
  empty,
  expiry,
  get,
  html,
  icon,
  isZero,
  longDate,
  moduleName,
  on,
  ordered,
  paintSwatches,
  post,
  prettyJSON,
  relDays,
  showError,
  statusBadge,
  textareaField,
  toast,
} from '../lib.js';
import { paint, preview } from '../brand.js';
import { bindValidity, validityBody, validityHTML } from '../validity.js';

const DAY = 86400000;
const KINDS = { issue: 'New license', renew: 'Renewal', change: 'Change' };

// List

const STATUSES = [
  ['', 'All'],
  ['valid', 'Valid'],
  ['expiring', 'Expiring ≤ 30 d'],
  ['grace', 'In grace'],
  ['expired', 'Expired'],
];

const inStatus = (v, s) =>
  !s || (s === 'valid' && v.status === 'valid') || (s === 'expiring' && v.expiring) || ((s === 'grace' || s === 'expired') && v.status === s);

const binding = (l) =>
  l.installation ? html`<span class="id">${l.installation}</span>` : html`<span class="tag warn" title="Works on any server">Unbound</span>`;

/** "5 modules · 2 robots · GPS map". */
const terms = (l) => {
  const n = (l.features || []).length;
  const robots = l.max_robots ? `${l.max_robots} robot${l.max_robots === 1 ? '' : 's'}` : 'no robot limit';
  return `${n} module${n === 1 ? '' : 's'} · ${robots} · ${MAPS[l.map] || l.map}`;
};

export function licenseRows(list, { client = true } = {}) {
  return html`<div class="table-wrap"><table><thead><tr>
      ${client ? html`<th>Client</th>` : html`<th>Deployment</th>`}<th>License</th><th>Installation</th><th>Terms</th>
      <th class="wide-only">Issued</th><th>Expires</th><th>Status</th></tr></thead><tbody>
    ${list.map(
      (v) => html`<tr class="row-link ${v.superseded_by ? 'replaced' : ''}" data-href="#/licenses/${v.id}">
        ${client ? html`<td><div class="cell-main">${v.client_name}</div><div class="cell-sub">${v.deployment || 'No deployment'}</div></td>` : html`<td class="nowrap"><div class="cell-main">${v.deployment || 'No deployment'}</div></td>`}
        <td><div class="id">${v.id}</div><div class="cell-sub">${KINDS[v.kind] || v.kind}</div></td>
        <td>${binding(v.license)}</td>
        <td class="nowrap"><div>${editionTier(v.license)}</div><div class="cell-sub">${terms(v.license)}</div></td>
        <td class="nowrap wide-only">${date(v.license.issued_at)}<div class="cell-sub">by ${v.issued_by}</div></td>
        <td class="nowrap">${expiry(v.license)}</td>
        <td>${statusBadge(v)}</td></tr>`,
    )}</tbody></table></div>`;
}

export function bindRows(root) {
  on(root, 'click', 'tr[data-href]', (e, tr) => {
    if (!e.target.closest('a, button')) location.hash = tr.dataset.href;
  });
}

export async function licenseListPage({ root, setTitle, query, current }) {
  setTitle('Licenses');
  const [all, clients] = await Promise.all([get('/licenses'), get('/clients')]);
  if (!current()) return;
  const f = {
    status: query.get('status') || '',
    client: query.get('client') || '',
    edition: query.get('edition') || '',
    current: query.get('current') !== '0',
  };
  root.innerHTML = String(html`
    <div class="page-head"><div><h1>Licenses</h1><p>Every license this manager has signed. A renewal or a change replaces the license before it.</p></div>
      <div class="actions"><a class="btn primary" href="#/issue">${icon('plus', 16)} Issue license</a></div></div>
    <section class="panel">
      <div class="filters">
        <div class="seg" id="status-seg"></div>
        <select class="select" id="f-client"><option value="">All clients</option>
          ${clients.map((c) => html`<option value="${c.id}" ${c.id === f.client ? 'selected' : ''}>${c.name}</option>`)}</select>
        <select class="select" id="f-edition"><option value="">All editions</option>
          ${ordered(Object.keys(EDITIONS), EDITION_ORDER).map((k) => html`<option value="${k}" ${k === f.edition ? 'selected' : ''}>${EDITIONS[k]}</option>`)}</select>
        <label class="check"><input type="checkbox" id="f-current" ${f.current ? 'checked' : ''}> Hide replaced</label>
        <span class="spacer"></span><span class="muted small" id="f-count"></span>
      </div>
      <div id="rows"></div>
    </section>`);
  const draw = () => {
    const base = all.filter(
      (v) => (!f.client || v.client_id === f.client) && (!f.edition || v.license.edition === f.edition) && (!f.current || !v.superseded_by),
    );
    $('#status-seg', root).innerHTML = String(
      html`${STATUSES.map(
        ([s, label]) => html`<button type="button" data-status="${s}" class="${s === f.status ? 'on' : ''}">${label} <span class="n">${base.filter((v) => inStatus(v, s)).length}</span></button>`,
      )}`,
    );
    const rows = base.filter((v) => inStatus(v, f.status));
    $('#f-count', root).textContent = `${rows.length} license${rows.length === 1 ? '' : 's'}`;
    $('#rows', root).innerHTML = String(
      rows.length
        ? licenseRows(rows)
        : all.length
          ? empty('license', 'No license matches these filters.')
          : empty('license', 'No license issued yet.', html`<a class="btn primary" href="#/issue">${icon('plus', 16)} Issue the first license</a>`),
    );
    const q = new URLSearchParams();
    if (f.status) q.set('status', f.status);
    if (f.client) q.set('client', f.client);
    if (f.edition) q.set('edition', f.edition);
    if (!f.current) q.set('current', '0');
    history.replaceState(null, '', `#/licenses${q.toString() ? '?' + q : ''}`);
  };
  on(root, 'click', '[data-status]', (e, b) => {
    f.status = b.dataset.status;
    draw();
  });
  on(root, 'change', '#f-client, #f-edition, #f-current', () => {
    f.client = $('#f-client', root).value;
    f.edition = $('#f-edition', root).value;
    f.current = $('#f-current', root).checked;
    draw();
  });
  bindRows(root);
  draw();
}

// Detail

/** The message to send with the license: what it includes and where the client installs it. */
export function deliveryNote(v, withText, meta) {
  const l = v.license;
  const site = v.deployment ? `${v.deployment} (${v.client_name})` : v.client_name;
  const mods = catalogOrder(l.features, meta.catalog).map(moduleName);
  const pad = (s) => s + ': ';
  const lines = [
    `Subject: NETRA license for ${site}`,
    '',
    'Hello,',
    '',
    `Attached is the NETRA license for ${site}: ${v.file_name}.`,
    '',
    `${pad('License ID')}${l.id}`,
    `${pad('Edition')}${EDITIONS[l.edition] || l.edition}, ${TIERS[l.tier] || l.tier} tier`,
    `${pad('Modules')}${mods.length ? mods.join(', ') : 'core only'}`,
    `${pad('Map')}${MAPS[l.map] || l.map}`,
    `${pad('Robots')}${l.max_robots ? `up to ${l.max_robots}` : 'no limit'}`,
    isZero(l.expires_at)
      ? `${pad('Valid')}no expiry`
      : `${pad('Valid until')}${longDate(l.expires_at)} (the modules keep working ${meta.grace_days} more days)`,
    l.installation ? `${pad('Server')}${l.installation} only` : `${pad('Server')}any (not bound to an installation)`,
    '',
    'How to install it (no internet connection is needed):',
    '',
    '1. In the NETRA dashboard: sign in as an administrator, open Settings › License,',
    `   choose the file ${v.file_name} (or paste the license text) and press Install.`,
    '   It applies at once, without a restart. On a new server, the setup wizard asks',
    '   for the license while it creates the first administrator.',
    '',
    '2. Or as a file on the server: copy it to license/netra.lic in the NETRA folder',
    '   and restart the API: docker compose up -d api',
    '',
    'Settings › License then shows the license, its modules and its expiry.',
  ];
  if (l.installation) {
    lines.push(
      '',
      `The license works only on the server with the installation ID ${l.installation}`,
      '(shown in the setup wizard and in Settings › License). A reinstalled server with a',
      'new database gets a new ID: send it to us and we issue a new license.',
    );
  }
  if (withText) lines.push('', 'License text (one line, paste it whole):', '', v.text);
  lines.push('', 'Regards,', 'EZRA Robotics');
  return lines.join('\n');
}

const kvRow = (label, value) => html`<dt>${label}</dt><dd>${value}</dd>`;

function statusText(v) {
  const l = v.license;
  if (v.superseded_by) return html`Replaced by <a class="mono" href="#/licenses/${v.superseded_by}">${v.superseded_by}</a>`;
  if (isZero(l.expires_at)) return 'Valid, without expiry';
  if (v.status === 'valid') return html`Valid until ${longDate(l.expires_at)} (${relDays(l.expires_at)})`;
  if (v.status === 'grace') return html`Expired ${relDays(l.expires_at)}; modules on until ${longDate(v.grace_ends)}`;
  return html`Expired ${relDays(l.expires_at)}; the server runs the core only`;
}

export async function licenseDetailPage(ctx) {
  const { root, params, query, setTitle, app, current } = ctx;
  const v = await get(`/licenses/${params[0]}`);
  if (!current()) return;
  const l = v.license;
  const meta = app.meta;
  setTitle('Licenses', v.client_name);
  const features = new Set(l.features || []);
  const b = l.branding || {};
  const withText = v.text.length <= 4000;
  root.innerHTML = String(html`
    <div class="page-head">
      <div><div class="eyebrow">${KINDS[v.kind] || 'License'}</div>
        <h1>${v.client_name}${v.deployment ? html` <span class="muted">· ${v.deployment}</span>` : ''}</h1>
        <p class="head-line">${statusBadge(v)}<span class="mono">${v.id}</span><span>${editionTier(l)}</span><span>issued ${dateTime(l.issued_at)} by ${v.issued_by}</span></p></div>
      <div class="actions">
        <a class="btn" href="/api/licenses/${v.id}/file" download="${v.file_name}">${icon('download', 16)} Download .lic</a>
        <button class="btn" data-copy="text">${icon('copy', 16)} Copy text</button>
        <a class="btn" href="#/issue?from=${v.id}">${icon('edit', 16)} Change</a>
        <button class="btn primary" data-renew>${icon('refresh', 16)} Renew</button>
      </div>
    </div>
    ${
      v.superseded_by
        ? html`<div class="callout">${icon('info')}<div class="grow"><b>A newer license replaces this one.</b>
          <p>NETRA keeps the newest valid license it is given; this one is history.</p></div>
          <a class="btn sm" href="#/licenses/${v.superseded_by}">Open ${v.superseded_by}</a></div>`
        : ''
    }
    ${
      !l.installation && !v.superseded_by
        ? html`<div class="callout warn">${icon('alert')}<div class="grow"><b>Not bound to an installation.</b>
          <p>This license works on any server. Bind client licenses to the installation ID of their deployment; leave only EZRA's own cloud deployments unbound.</p></div></div>`
        : ''
    }
    <div class="grid split-wide">
      <div class="stack">
        <section class="panel">
          <div class="panel-head"><h2>${icon('license', 15)} License</h2><span class="muted small">${KINDS[v.kind] || ''}</span></div>
          <div class="panel-body"><dl class="kv">
            ${kvRow('Status', statusText(v))}
            ${kvRow('Client', html`<a href="#/clients/${v.client_id}">${v.client_name}</a>${v.client_name !== l.client ? html` <span class="dim">(signed as ${l.client})</span>` : ''}`)}
            ${kvRow('Deployment', v.deployment || html`<span class="muted">None</span>`)}
            ${kvRow('Installation', l.installation ? html`<span class="mono">${l.installation}</span> <span class="dim">· works on that server only</span>` : html`<span class="warn-text">Any server</span>`)}
            ${kvRow('Edition', html`${EDITIONS[l.edition] || l.edition} <span class="dim">·</span> ${TIERS[l.tier] || l.tier} tier`)}
            ${kvRow('Map mode', MAPS[l.map] || l.map)}
            ${kvRow('Robot limit', l.max_robots ? `${l.max_robots} robots` : 'No limit')}
            ${kvRow('Issued', html`${dateTime(l.issued_at)} <span class="dim">by ${v.issued_by}</span>`)}
            ${kvRow('Expires', isZero(l.expires_at) ? 'Never' : html`${dateTime(l.expires_at)} <span class="dim">· grace until ${date(v.grace_ends)}</span>`)}
            ${kvRow('Signing key', html`<span class="mono">${v.key}</span>`)}
            ${v.supersedes ? kvRow('Replaces', html`<a class="mono" href="#/licenses/${v.supersedes}">${v.supersedes}</a>`) : ''}
            ${v.note ? kvRow('Note', v.note) : ''}
          </dl></div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2>${icon('layers', 15)} Modules</h2><span class="muted small">${features.size} of ${meta.catalog.length} · core always on</span></div>
          <div class="panel-body"><ul class="mod-list">
            ${meta.catalog.map(
              (m) => html`<li class="${features.has(m.id) ? 'on' : 'off'}"><span><b class="${features.has(m.id) ? '' : 'dim'}">${MODULES[m.id] || m.id}</b>
                <span class="dim small"> · ${m.description}</span></span>
                <span class="state">${features.has(m.id) ? html`${icon('check', 14)} Included` : 'Not included'}</span></li>`,
            )}
          </ul></div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2>${icon('mail', 15)} Delivery note</h2>
            <div class="actions"><label class="check small"><input type="checkbox" id="with-text" ${withText ? 'checked' : ''}> Include the license text</label>
            <button class="btn sm" data-copy="note">${icon('copy', 14)} Copy</button></div></div>
          <div class="panel-body">
            <div class="textblock prose note" id="note"></div>
            <div class="hint">Send it with the .lic file attached. Mail programs may wrap a pasted license text; the file arrives intact.</div>
          </div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2>${icon('file', 15)} License text</h2><span class="muted small">${v.text.length.toLocaleString('en')} characters</span></div>
          <div class="panel-body"><textarea class="textblock" rows="5" readonly>${v.text}</textarea></div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2>${icon('info', 15)} Signed payload</h2><span class="muted small">what netra-api reads</span></div>
          <pre class="code">${prettyJSON(l)}</pre>
        </section>
      </div>
      <div class="stack">
        <section class="panel">
          <div class="panel-head"><h2>${icon('pin', 15)} Branding</h2></div>
          <div class="panel-body">
            <dl class="kv tight">
              ${kvRow('Name', b.name || html`<span class="muted">NETRA (default)</span>`)}
              ${kvRow('Colours', b.color ? html`<span class="color-row"><i class="swatch" data-swatch="${b.color}"></i><span class="mono">${b.color}</span><i class="swatch" data-swatch="${b.color_dark}"></i><span class="mono">${b.color_dark}</span></span>` : html`<span class="muted">NETRA cyan (default)</span>`)}
              ${kvRow('Logo', b.logo ? html`<span class="color-row"><span class="logo-chip"><img src="${b.logo}" alt=""></span><span class="muted">${b.logo.startsWith('data:image/png') ? 'PNG' : 'SVG'}, ${dataSize(b.logo)}</span></span>` : html`<span class="muted">EZRA Robotics (default)</span>`)}
            </dl>
            <div class="field-gap"></div>
            <div id="brand-preview">${preview(b)}</div>
          </div>
        </section>
      </div>
    </div>`);
  paintSwatches(root);
  paint($('#brand-preview', root), b);
  const note = $('#note', root);
  const writeNote = () => {
    note.textContent = deliveryNote(v, $('#with-text', root).checked, meta);
  };
  writeNote();
  on(root, 'change', '#with-text', writeNote);
  on(root, 'click', '[data-copy]', async (e, el) => {
    await copyText(el.dataset.copy === 'note' ? note.textContent : v.text);
    toast(el.dataset.copy === 'note' ? 'Delivery note copied.' : 'License text copied.');
  });
  on(root, 'click', '[data-renew]', () => renewDialog(v, ctx));
  if (query.get('renew') === '1' && !v.superseded_by) {
    history.replaceState(null, '', `#/licenses/${v.id}`);
    renewDialog(v, ctx);
  }
}

/** Renew: the same terms with new dates. The default keeps the length of the license, counted
 * from its expiry when it has not expired yet, so renewing early loses no days. */
function renewDialog(v, ctx) {
  const l = v.license;
  const meta = ctx.app.meta;
  const perpetual = isZero(l.expires_at);
  const period = perpetual ? 365 : Math.max(1, Math.round((new Date(l.expires_at) - new Date(l.issued_at)) / DAY));
  const from = perpetual ? Date.now() : Math.max(Date.now(), new Date(l.expires_at).getTime());
  const state = { mode: perpetual ? 'none' : 'until', days: period, until: dateInput(from + period * DAY) };
  // A renewal binds to the deployment's installation ID as it is now.
  const bind = v.deployment_id ? v.deployment_installation || '' : l.installation || '';
  const d = dialog({
    title: `Renew ${v.client_name}${v.deployment ? ' · ' + v.deployment : ''}`,
    wide: true,
    body: html`<form id="renew-form" novalidate>
      <p class="muted small">The same edition, modules, map, robot limit and branding, with new dates. The new license replaces
      <span class="mono">${v.id}</span>; the client installs it like the first one.</p>
      ${v.superseded_by ? html`<div class="callout warn">${icon('alert')}<div class="grow">This license was already replaced by <span class="mono">${v.superseded_by}</span>.</div></div>` : ''}
      <dl class="kv tight">
        <dt>Now</dt><dd>${isZero(l.expires_at) ? 'No expiry' : html`Expires ${longDate(l.expires_at)} (${relDays(l.expires_at)})`}</dd>
        <dt>Installation</dt><dd>${bind ? html`<span class="mono">${bind}</span>` : 'Any server (not bound)'}</dd>
      </dl>
      ${
        bind !== (l.installation || '')
          ? html`<div class="callout warn">${icon('alert')}<div class="grow">The installation ID of ${v.deployment} changed since this license
            (<span class="mono">${l.installation || 'none'}</span>): the renewal is ${bind ? html`bound to <span class="mono">${bind}</span>` : 'not bound'}.</div></div>`
          : ''
      }
      <div class="field-gap"></div>
      ${validityHTML(state, meta.max_days)}
      <div class="field-gap"></div>
      ${textareaField({ name: 'note', label: 'Note', opt: 'kept here, not in the license', placeholder: 'e.g. invoice or purchase order', rows: 2 })}
      <div class="form-error"></div>
    </form>`,
    foot: html`<button class="btn" data-close>Cancel</button><button class="btn primary" type="submit" form="renew-form">${icon('refresh', 16)} Sign renewal</button>`,
  });
  const form = $('#renew-form', d.root);
  bindValidity(form, state, meta.grace_days);
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const body = { validity: validityBody(state), note: form.note.value };
    const send = () => post(`/licenses/${v.id}/renew`, body);
    try {
      let res;
      await busy($('button[type=submit]', d.root), async () => {
        try {
          res = await send();
        } catch (err) {
          if (err.code !== 'key_locked' || !(await ctx.ensureUnlocked())) throw err;
          res = await send();
        }
      });
      d.close();
      toast('Renewal signed.');
      ctx.refreshKey();
      ctx.refreshCounts();
      location.hash = `#/licenses/${res.id}`;
    } catch (err) {
      showError(form, err);
    }
  });
}
