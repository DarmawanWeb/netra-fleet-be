import { $, ago, dateTime, empty, get, html, icon, on } from '../lib.js';

const link = (hash, text, mono = false) => html`<a href="${hash}" class="${mono ? 'mono' : ''}">${text}</a>`;

/** What an audit entry means: an icon, a tone and a sentence. */
export function describe(e) {
  const d = e.detail || {};
  const lic = () => link(`#/licenses/${e.target}`, e.target, true);
  const client = () => (e.target ? link(`#/clients/${e.target}`, d.name || e.target) : d.name);
  switch (e.action) {
    case 'license.issue':
      return { icon: 'license', tone: 'ok', text: html`Issued ${lic()} for ${d.client}` };
    case 'license.renew':
      return { icon: 'refresh', tone: 'ok', text: html`Renewed ${d.client}: ${lic()}` };
    case 'license.change':
      return { icon: 'edit', tone: 'ok', text: html`Changed the license of ${d.client}: ${lic()}` };
    case 'client.create':
      return { icon: 'clients', tone: 'info', text: html`Added the client ${client()}` };
    case 'client.update':
      return {
        icon: 'clients',
        tone: 'info',
        text: d.was ? html`Renamed the client ${d.was} to ${client()}` : html`Updated the client ${client()}`,
      };
    case 'client.delete':
      return { icon: 'trash', tone: 'warn', text: html`Removed the client ${d.name}` };
    case 'deployment.create':
      return {
        icon: 'server',
        tone: 'info',
        text: html`Added the deployment ${d.name} to ${d.client}${d.installation ? html` (<span class="mono">${d.installation}</span>)` : ''}`,
      };
    case 'deployment.update':
      return {
        icon: 'server',
        tone: 'info',
        text:
          'was' in d
            ? html`Changed the installation ID of ${d.name} (${d.client}) to <span class="mono">${d.installation || 'none'}</span>`
            : html`Updated the deployment ${d.name} of ${d.client}`,
      };
    case 'deployment.delete':
      return { icon: 'trash', tone: 'warn', text: html`Removed the deployment ${d.name} of ${d.client}` };
    case 'key.generate':
      return { icon: 'key', tone: 'ok', text: html`Generated the signing key <span class="mono">${e.target}</span>` };
    case 'key.import':
      return { icon: 'key', tone: 'ok', text: html`Imported the signing key <span class="mono">${e.target}</span>` };
    case 'key.unlock':
      return { icon: 'unlock', tone: 'info', text: 'Unlocked the signing key' };
    case 'key.unlock_failed':
      return { icon: 'alert', tone: 'danger', text: 'Wrong passphrase for the signing key' };
    case 'key.lock':
      return {
        icon: 'lock',
        tone: '',
        text: d.reason === 'idle' ? 'The signing key locked itself after 30 minutes without use' : 'Locked the signing key',
      };
    case 'key.passphrase':
      return { icon: 'key', tone: 'warn', text: 'Changed the passphrase of the signing key' };
    case 'setup':
      return { icon: 'users', tone: 'info', text: 'Created the first operator' };
    case 'login':
      return { icon: 'users', tone: '', text: 'Signed in' };
    case 'login_failed':
      return { icon: 'alert', tone: 'danger', text: 'Failed sign-in' };
    case 'logout':
      return { icon: 'logout', tone: '', text: 'Signed out' };
    case 'operator.create':
      return { icon: 'users', tone: 'info', text: html`Added the operator ${e.target}` };
    case 'operator.delete':
      return { icon: 'users', tone: 'warn', text: html`Removed the operator ${e.target}` };
    case 'password.change':
      return { icon: 'key', tone: '', text: 'Changed their password' };
    default:
      return { icon: 'info', tone: '', text: e.action };
  }
}

export const feed = (entries) =>
  entries.length
    ? html`<ul class="feed">${entries.map((e) => {
        const x = describe(e);
        return html`<li><span class="ico ${x.tone}">${icon(x.icon, 16)}</span>
          <div><div class="what">${x.text}</div><div class="who">${e.user === '-' ? 'the manager' : e.user}</div></div>
          <time datetime="${e.time}" title="${dateTime(e.time)}">${ago(e.time)}</time></li>`;
      })}</ul>`
    : empty('audit', 'No activity yet.');

const GROUPS = {
  '': 'All actions',
  license: 'Licenses',
  client: 'Clients',
  deployment: 'Deployments',
  key: 'Signing key',
  session: 'Sign-ins',
  operator: 'Operators',
};

const group = (action) => {
  if (['login', 'login_failed', 'logout', 'setup'].includes(action)) return 'session';
  if (action === 'password.change') return 'operator';
  return action.split('.')[0];
};

export async function auditPage({ root, setTitle }) {
  setTitle('Administration', 'Audit log');
  const entries = await get('/audit?limit=1000');
  root.innerHTML = String(html`
    <div class="page-head"><div><h1>Audit log</h1>
      <p>Every sign-in, key unlock, license issued or renewed and client change, with the operator and the time.
      Kept in <code>audit.log</code> in the data folder.</p></div></div>
    <section class="panel">
      <div class="filters">
        <select class="select" id="audit-group">${Object.entries(GROUPS).map(([v, l]) => html`<option value="${v}">${l}</option>`)}</select>
        <input class="input search" id="audit-search" placeholder="Search operator, client, license…">
        <span class="spacer"></span><span class="muted small" id="audit-count"></span>
      </div>
      <div class="table-wrap" id="audit-table"></div>
    </section>`);
  const draw = () => {
    const g = $('#audit-group', root).value;
    const q = $('#audit-search', root).value.trim().toLowerCase();
    const rows = entries.filter((e) => (!g || group(e.action) === g) && (!q || JSON.stringify(e).toLowerCase().includes(q)));
    $('#audit-count', root).textContent = `${rows.length} of ${entries.length} entries`;
    $('#audit-table', root).innerHTML = String(
      rows.length
        ? html`<table><thead><tr><th>Time</th><th>Operator</th><th>Action</th><th>Address</th></tr></thead><tbody>
          ${rows.map((e) => {
            const x = describe(e);
            return html`<tr><td class="nowrap"><span class="cell-main">${dateTime(e.time)}</span><div class="cell-sub">${ago(e.time)}</div></td>
              <td>${e.user === '-' ? html`<span class="muted">the manager</span>` : e.user}</td>
              <td><div class="color-row"><span class="ico-inline ${x.tone}">${icon(x.icon, 15)}</span><span>${x.text}</span></div>
                <div class="cell-sub mono">${e.action}</div></td>
              <td class="id">${e.ip || ''}</td></tr>`;
          })}</tbody></table>`
        : empty('audit', 'No entry matches.'),
    );
  };
  on(root, 'input', '#audit-search', draw);
  on(root, 'change', '#audit-group', draw);
  draw();
}
