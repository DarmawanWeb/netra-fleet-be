import { date, editionTier, empty, expiry, get, html, icon, on, relDays, statusBadge } from '../lib.js';
import { feed } from './audit.js';

const stat = (label, value, hint, tone = '', href = '') =>
  href
    ? html`<a class="stat ${tone}" href="${href}"><div class="stat-label">${label}</div><div class="stat-value">${value}</div><div class="stat-hint">${hint}</div></a>`
    : html`<div class="stat ${tone}"><div class="stat-label">${label}</div><div class="stat-value">${value}</div><div class="stat-hint">${hint}</div></div>`;

/** Callouts for a missing key or a key this build of netra-api does not trust. */
export function keyCallouts(k) {
  if (!k.exists) {
    return html`<div class="callout warn">${icon('key')}<div class="grow"><b>No signing key yet.</b>
      <p>Generate a key or import the one from <code>netra-license keygen</code> before issuing licenses.</p></div>
      <a class="btn sm" href="#/key">Set up the key</a></div>`;
  }
  if (!k.trusted) {
    return html`<div class="callout warn">${icon('alert')}<div class="grow"><b>This build of NETRA does not trust the manager's key.</b>
      <p>netra-api accepts only licenses signed by a key built into it. Add the public key as
      <code>internal/license/keys/${k.file_name}</code> and release netra-api; until then its licenses are refused
      (${k.trusted_keys} key${k.trusted_keys === 1 ? '' : 's'} built into this build).</p></div>
      <a class="btn sm" href="#/key">Public key</a></div>`;
  }
  return '';
}

export async function dashboardPage({ root, setTitle, current }) {
  setTitle('Dashboard');
  const [d, licenses] = await Promise.all([get('/dashboard'), get('/licenses')]);
  if (!current()) return;
  const c = d.counts;
  const k = d.key;
  const recent = licenses.slice(0, 6);
  root.innerHTML = String(html`
    <div class="page-head">
      <div><div class="eyebrow">EZRA Robotics · NETRA licensing</div><h1>Dashboard</h1>
        <p>What this manager has issued, what needs renewing, and the latest activity.</p></div>
      <div class="actions"><a class="btn" href="#/verify">${icon('verify', 16)} Verify a license</a>
        <a class="btn primary" href="#/issue">${icon('plus', 16)} Issue license</a></div>
    </div>
    ${keyCallouts(k)}
    <div class="stats">
      ${stat('Clients', c.clients, `${c.deployments} deployment${c.deployments === 1 ? '' : 's'}`, 'info', '#/clients')}
      ${stat('Issued', c.licenses, `${c.current} current`, '', '#/licenses?current=0')}
      ${stat('Valid', c.valid, c.unbound ? `${c.unbound} not bound to a server` : 'all bound to a server', 'ok', '#/licenses?status=valid')}
      ${stat('Expiring soon', c.expiring, 'within 30 days', c.expiring ? 'warn' : '', '#/licenses?status=expiring')}
      ${stat('In grace', c.grace, 'modules still on', c.grace ? 'warn' : '', '#/licenses?status=grace')}
      ${stat('Expired', c.expired, 'core only', c.expired ? 'danger' : '', '#/licenses?status=expired')}
    </div>
    <div class="grid dash">
      <div class="stack">
        <section class="panel">
          <div class="panel-head"><h2>${icon('clock', 15)} Needs attention</h2><span class="muted small">expiring within 30 days, or in grace</span></div>
          ${
            d.attention.length
              ? html`<div class="table-wrap"><table><thead><tr><th>Client</th><th>License</th><th>Expires</th><th>Status</th><th></th></tr></thead><tbody>
              ${d.attention.map(
                (v) => html`<tr class="row-link" data-href="#/licenses/${v.id}">
                  <td><div class="cell-main">${v.client_name}</div><div class="cell-sub">${v.deployment || 'No deployment'}</div></td>
                  <td class="nowrap"><div>${editionTier(v.license)}</div><div class="id">${v.id}</div></td>
                  <td class="nowrap">${date(v.license.expires_at)}<div class="cell-sub">${relDays(v.license.expires_at)}</div></td>
                  <td>${statusBadge(v)}</td>
                  <td class="num"><a class="btn sm" href="#/licenses/${v.id}?renew=1">${icon('refresh', 14)} Renew</a></td></tr>`,
              )}</tbody></table></div>`
              : empty('check', 'Nothing expires within 30 days.')
          }
        </section>
        <section class="panel">
          <div class="panel-head"><h2>${icon('license', 15)} Recently issued</h2><a class="small" href="#/licenses">All licenses</a></div>
          ${
            recent.length
              ? html`<div class="table-wrap"><table><thead><tr><th>Client</th><th>Edition</th><th>Issued</th><th>Expires</th><th>Status</th></tr></thead><tbody>
              ${recent.map(
                (v) => html`<tr class="row-link ${v.superseded_by ? 'replaced' : ''}" data-href="#/licenses/${v.id}">
                  <td><div class="cell-main">${v.client_name}</div><div class="cell-sub">${v.deployment || 'No deployment'}</div></td>
                  <td class="nowrap">${editionTier(v.license)}</td>
                  <td class="nowrap">${date(v.license.issued_at)}<div class="cell-sub">by ${v.issued_by}</div></td>
                  <td class="nowrap">${expiry(v.license)}</td>
                  <td>${statusBadge(v)}</td></tr>`,
              )}</tbody></table></div>`
              : empty('license', 'No license issued yet.', html`<a class="btn primary" href="#/issue">${icon('plus', 16)} Issue the first license</a>`)
          }
        </section>
      </div>
      <div class="stack side">
        <section class="panel">
          <div class="panel-head"><h2>${icon('key', 15)} Signing key</h2><a class="small" href="#/key">Details</a></div>
          ${
            k.exists
              ? html`<div class="keycard ${k.unlocked ? 'ok' : 'warn'}"><div class="glyph">${icon(k.unlocked ? 'unlock' : 'lock', 24)}</div>
                <div class="grow"><h3>${k.unlocked ? 'Unlocked' : 'Locked'}</h3>
                <p class="fingerprint">${k.fingerprint}</p>
                <p>${k.trusted ? html`<span class="ok-text">Built into this build of netra-api</span>` : html`<span class="warn-text">Not built into this build</span>`}</p></div></div>`
              : empty('key', 'No key yet.', html`<a class="btn" href="#/key">Set up the key</a>`)
          }
        </section>
        <section class="panel">
          <div class="panel-head"><h2>${icon('audit', 15)} Recent activity</h2><a class="small" href="#/audit">Audit log</a></div>
          ${feed(d.recent)}
        </section>
      </div>
    </div>`);
  on(root, 'click', 'tr[data-href]', (e, tr) => {
    if (!e.target.closest('a, button')) location.hash = tr.dataset.href;
  });
}
