import { $, ago, busy, confirmDialog, date, del, field, fieldError, get, html, icon, on, post, rerender, showError, toast } from '../lib.js';

export async function operatorsPage(ctx) {
  const { root, setTitle, app, current } = ctx;
  setTitle('Administration', 'Operators');
  const ops = await get('/operators');
  if (!current()) return;
  const me = app.user.username;
  const min = app.meta.min_password;
  root.innerHTML = String(html`
    <div class="page-head"><div><h1>Operators</h1>
      <p>The people who can issue licenses here. Every operator can do everything, so keep the list short.</p></div></div>
    <div class="grid split">
      <section class="panel">
        <div class="panel-head"><h2>${icon('users', 15)} Operators</h2><span class="muted small">${ops.length}</span></div>
        <div class="table-wrap"><table><thead><tr><th>Username</th><th>Added</th><th>Last sign-in</th><th></th></tr></thead><tbody>
          ${ops.map(
            (o) => html`<tr><td><span class="cell-main">${o.username}</span> ${o.username === me ? html`<span class="tag info">you</span>` : ''}</td>
              <td class="nowrap">${date(o.created_at)}<div class="cell-sub">${o.created_by ? `by ${o.created_by}` : 'first operator'}</div></td>
              <td class="nowrap">${o.last_login ? ago(o.last_login) : html`<span class="dim">never</span>`}</td>
              <td class="num">${o.username === me ? '' : html`<button class="btn sm danger" data-remove="${o.username}">${icon('trash', 14)} Remove</button>`}</td></tr>`,
          )}
        </tbody></table></div>
      </section>
      <div class="stack">
        <section class="panel">
          <div class="panel-head"><h2>${icon('plus', 15)} Add an operator</h2></div>
          <form class="panel-body" id="add-form" novalidate>
            ${field({ name: 'username', label: 'Username', placeholder: 'e.g. dewi', hint: '3 to 32 characters: a-z, 0-9, . _ -' })}
            <div class="field-gap"></div>
            ${field({ name: 'password', label: 'Password', type: 'password', auto: 'new-password', hint: `At least ${min} characters. Tell it to them in person.` })}
            <div class="form-error"></div><div class="field-gap"></div>
            <button class="btn primary" type="submit">Add operator</button>
          </form>
        </section>
        <section class="panel">
          <div class="panel-head"><h2>${icon('key', 15)} Your password</h2></div>
          <form class="panel-body" id="pw-form" novalidate>
            ${field({ name: 'current', label: 'Current password', type: 'password', auto: 'current-password' })}
            <div class="field-gap"></div>
            ${field({ name: 'new', label: 'New password', type: 'password', auto: 'new-password', hint: 'Your other sessions end.' })}
            <div class="field-gap"></div>
            ${field({ name: 'confirm', label: 'New password again', type: 'password', auto: 'new-password' })}
            <div class="form-error"></div><div class="field-gap"></div>
            <button class="btn" type="submit">Change password</button>
          </form>
        </section>
      </div>
    </div>`);
  const again = () => rerender(ctx, operatorsPage);
  const add = $('#add-form', root);
  add.addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      await busy($('button[type=submit]', add), () => post('/operators', Object.fromEntries(new FormData(add))));
      toast(`${add.username.value} can sign in now.`);
      again();
    } catch (err) {
      showError(add, err);
    }
  });
  const pw = $('#pw-form', root);
  pw.addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      if (pw.new.value !== pw.confirm.value) throw fieldError('confirm', 'The two passwords differ.');
      await busy($('button[type=submit]', pw), () => post('/password', { current: pw.current.value, new: pw.new.value }));
      pw.reset();
      toast('Password changed.');
    } catch (err) {
      showError(pw, err);
    }
  });
  on(root, 'click', '[data-remove]', async (e, b) => {
    const name = b.dataset.remove;
    if (!(await confirmDialog({ title: `Remove ${name}?`, text: 'Their sessions end at once.', yes: 'Remove', danger: true }))) return;
    try {
      await del(`/operators/${encodeURIComponent(name)}`);
      toast(`${name} removed.`);
      again();
    } catch (err) {
      toast(err.message, 'danger');
    }
  });
}
