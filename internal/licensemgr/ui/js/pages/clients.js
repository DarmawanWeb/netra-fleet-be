import {
  $,
  busy,
  confirmDialog,
  copyText,
  date,
  del,
  dialog,
  empty,
  field,
  get,
  html,
  icon,
  on,
  post,
  put,
  relDays,
  rerender,
  showError,
  statusBadge,
  textareaField,
  toast,
} from '../lib.js';
import { bindRows, licenseRows } from './licenses.js';
import { newClientDialog } from './issue.js';

export async function clientListPage({ root, setTitle, current }) {
  setTitle('Clients');
  const clients = await get('/clients');
  if (!current()) return;
  root.innerHTML = String(html`
    <div class="page-head"><div><h1>Clients</h1>
      <p>Companies that buy NETRA, and their deployments: one NETRA server each, known by its installation ID.</p></div>
      <div class="actions"><button class="btn primary" data-new>${icon('plus', 16)} New client</button></div></div>
    <section class="panel">
      ${
        clients.length
          ? html`<div class="table-wrap"><table><thead><tr><th>Client</th><th>Contact</th><th class="num">Deployments</th>
            <th class="num">Licenses</th><th class="num">In force</th><th>Next expiry</th><th>Added</th></tr></thead><tbody>
            ${clients.map(
              (c) => html`<tr class="row-link" data-href="#/clients/${c.id}">
                <td><div class="cell-main">${c.name}</div>${c.notes ? html`<div class="cell-sub">${c.notes.split('\n')[0]}</div>` : ''}</td>
                <td>${c.contact || html`<span class="dim">—</span>`}</td>
                <td class="num">${c.deployments.length}</td>
                <td class="num">${c.licenses}</td>
                <td class="num">${c.active ? html`<span class="ok-text">${c.active}</span>` : html`<span class="dim">0</span>`}</td>
                <td class="nowrap">${c.next_expiry ? html`<span class="${new Date(c.next_expiry) < Date.now() ? 'warn-text' : ''}">${date(c.next_expiry)}</span><div class="cell-sub">${relDays(c.next_expiry)}</div>` : html`<span class="dim">—</span>`}</td>
                <td class="nowrap">${date(c.created_at)}</td></tr>`,
            )}</tbody></table></div>`
          : empty('clients', 'No client yet. Add one, then its deployments with their installation IDs.', html`<button class="btn primary" data-new>${icon('plus', 16)} New client</button>`)
      }
    </section>`);
  on(root, 'click', 'tr[data-href]', (e, tr) => (location.hash = tr.dataset.href));
  on(root, 'click', '[data-new]', () => newClientDialog((c) => (location.hash = `#/clients/${c.id}`)));
}

function clientDialog(c, done) {
  const d = dialog({
    title: 'Edit client',
    body: html`<form id="client-edit" novalidate>
      ${field({ name: 'name', label: 'Name', value: c.name, hint: 'New licenses carry the new name; issued ones keep theirs.' })}
      ${field({ name: 'contact', label: 'Contact', opt: 'optional', value: c.contact || '' })}
      ${textareaField({ name: 'notes', label: 'Notes', opt: 'optional', value: c.notes || '', rows: 3 })}
      <div class="form-error"></div></form>`,
    foot: html`<button class="btn" data-close>Cancel</button><button class="btn primary" type="submit" form="client-edit">Save</button>`,
  });
  const form = $('#client-edit', d.root);
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      await busy($('button[type=submit]', d.root), () => put(`/clients/${c.id}`, Object.fromEntries(new FormData(form))));
      d.close();
      toast('Client saved.');
      done();
    } catch (err) {
      showError(form, err);
    }
  });
}

function deploymentDialog(c, dep, done) {
  const d = dialog({
    title: dep ? `Edit ${dep.name}` : `New deployment of ${c.name}`,
    body: html`<form id="dep-form" novalidate>
      ${field({ name: 'name', label: 'Name', value: dep ? dep.name : '', placeholder: 'e.g. GI Cawang, Mall Ciputra Tower B', hint: 'The site, as the client calls it.' })}
      ${field({
        name: 'installation',
        label: 'Installation ID',
        opt: 'from the setup wizard or Settings › License',
        value: dep ? dep.installation || '' : '',
        placeholder: 'NETRA-XXXX-XXXX-XXXX',
        mono: true,
        hint: 'Licenses issued for this deployment work on that server only. Case and look-alike letters (O for 0, I and L for 1) do not matter. Empty until the client sends it.',
      })}
      ${textareaField({ name: 'notes', label: 'Notes', opt: 'optional', value: dep ? dep.notes || '' : '', rows: 2 })}
      ${
        dep && dep.installation
          ? html`<div class="callout warn">${icon('alert')}<div class="grow">Changing the ID does not move issued licenses: they stay bound to
            <span class="mono">${dep.installation}</span>. Renew or change the license afterwards.</div></div>`
          : ''
      }
      <div class="form-error"></div></form>`,
    foot: html`<button class="btn" data-close>Cancel</button><button class="btn primary" type="submit" form="dep-form">${dep ? 'Save' : 'Add deployment'}</button>`,
  });
  const form = $('#dep-form', d.root);
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const body = Object.fromEntries(new FormData(form));
    try {
      await busy($('button[type=submit]', d.root), () =>
        dep ? put(`/clients/${c.id}/deployments/${dep.id}`, body) : post(`/clients/${c.id}/deployments`, body),
      );
      d.close();
      toast(dep ? 'Deployment saved.' : 'Deployment added.');
      done();
    } catch (err) {
      showError(form, err);
    }
  });
}

export async function clientDetailPage(ctx) {
  const { root, params, setTitle, current } = ctx;
  const c = await get(`/clients/${params[0]}`);
  if (!current()) return;
  setTitle('Clients', c.name);
  const issued = c.issued || [];
  const reload = () => rerender(ctx, clientDetailPage);
  root.innerHTML = String(html`
    <div class="page-head">
      <div><div class="eyebrow">Client</div><h1>${c.name}</h1>
        <p>${c.contact || 'No contact'} · added ${date(c.created_at)}</p></div>
      <div class="actions">
        <button class="btn" data-edit>${icon('edit', 16)} Edit</button>
        ${issued.length ? '' : html`<button class="btn danger" data-delete>${icon('trash', 16)} Remove</button>`}
        <a class="btn primary" href="#/issue?client=${c.id}">${icon('plus', 16)} Issue license</a>
      </div>
    </div>
    ${c.notes ? html`<div class="callout">${icon('info')}<div class="grow textblock-plain">${c.notes}</div></div>` : ''}
    <section class="panel">
      <div class="panel-head"><h2>${icon('server', 15)} Deployments</h2>
        <button class="btn sm" data-add-dep>${icon('plus', 14)} Add deployment</button></div>
      ${
        c.deployments.length
          ? html`<div class="table-wrap"><table><thead><tr><th>Deployment</th><th>Installation ID</th><th>Current license</th><th>Expires</th><th></th></tr></thead><tbody>
            ${c.deployments.map(
              (d) => html`<tr>
                <td><div class="cell-main">${d.name}</div>${d.notes ? html`<div class="cell-sub">${d.notes}</div>` : ''}</td>
                <td>${
                  d.installation
                    ? html`<span class="color-row"><span class="mono nowrap">${d.installation}</span>
                      <button class="btn icon" data-copy="${d.installation}" title="Copy" aria-label="Copy the installation ID">${icon('copy', 15)}</button></span>`
                    : html`<span class="tag warn">Not known yet</span>`
                }</td>
                <td class="nowrap">${d.license ? html`${statusBadge(d.license)} <a class="id" href="#/licenses/${d.license.id}">${d.license.id}</a>` : html`<span class="dim">None</span>`}</td>
                <td class="nowrap">${
                  d.license
                    ? d.license.license.expires_at && !d.license.license.expires_at.startsWith('0001')
                      ? html`${date(d.license.license.expires_at)}<div class="cell-sub">${relDays(d.license.license.expires_at)}</div>`
                      : 'Never'
                    : ''
                }</td>
                <td class="num nowrap">
                  <a class="btn sm" href="#/issue?client=${c.id}&deployment=${d.id}">${icon('plus', 14)} Issue</a>
                  <button class="btn icon" data-edit-dep="${d.id}" title="Edit" aria-label="Edit ${d.name}">${icon('edit', 15)}</button>
                  <button class="btn icon" data-delete-dep="${d.id}" title="Remove" aria-label="Remove ${d.name}">${icon('trash', 15)}</button>
                </td></tr>`,
            )}</tbody></table></div>`
          : empty('server', 'No deployment yet. Add one when the client sends the installation ID of its server.')
      }
    </section>
    <section class="panel">
      <div class="panel-head"><h2>${icon('license', 15)} Licenses</h2><span class="muted small">${issued.length} issued · ${c.active} in force</span></div>
      ${issued.length ? licenseRows(issued, { client: false }) : empty('license', 'No license issued for this client yet.')}
    </section>`);
  bindRows(root);
  on(root, 'click', '[data-edit]', () => clientDialog(c, reload));
  on(root, 'click', '[data-add-dep]', () => deploymentDialog(c, null, reload));
  on(root, 'click', '[data-edit-dep]', (e, b) =>
    deploymentDialog(
      c,
      c.deployments.find((d) => d.id === b.dataset.editDep),
      reload,
    ),
  );
  on(root, 'click', '[data-copy]', async (e, b) => {
    await copyText(b.dataset.copy);
    toast('Installation ID copied.');
  });
  on(root, 'click', '[data-delete-dep]', async (e, b) => {
    const d = c.deployments.find((x) => x.id === b.dataset.deleteDep);
    const yes = await confirmDialog({
      title: `Remove ${d.name}?`,
      text: 'A deployment with licenses stays: they are the record of what was sold.',
      yes: 'Remove',
      danger: true,
    });
    if (!yes) return;
    try {
      await del(`/clients/${c.id}/deployments/${d.id}`);
      toast('Deployment removed.');
      reload();
    } catch (err) {
      toast(err.message, 'danger');
    }
  });
  on(root, 'click', '[data-delete]', async () => {
    const yes = await confirmDialog({ title: `Remove ${c.name}?`, text: 'The client and its deployments go away.', yes: 'Remove', danger: true });
    if (!yes) return;
    try {
      await del(`/clients/${c.id}`);
      toast('Client removed.');
      location.hash = '#/clients';
    } catch (err) {
      toast(err.message, 'danger');
    }
  });
}
