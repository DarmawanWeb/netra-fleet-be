import { $, EDITIONS, MAPS, TIERS, busy, catalogOrder, html, icon, isZero, longDate, moduleName, on, post, prettyJSON, relDays, showError } from '../lib.js';

const check = (state, title, sub = '') =>
  html`<li class="${state}">${icon(state === 'ok' ? 'check' : state === 'bad' ? 'x' : state === 'warn' ? 'alert' : 'info', 16)}
    <div><div>${title}</div>${sub ? html`<div class="sub">${sub}</div>` : ''}</div></li>`;

function verdict(r) {
  if (r.error) return ['danger', 'x', 'Not a NETRA license', r.error];
  const m = r.manager.ok;
  const b = r.built_in.ok;
  if (m && b) return ['ok', 'check', 'Genuine, and accepted by this build of netra-api', 'Signed by the manager’s key, which this build trusts.'];
  if (m) return ['warn', 'alert', 'Signed by this manager, refused by this build', 'The manager’s key is not built into this build of netra-api.'];
  if (b) return ['ok', 'check', 'Signed by a key built into netra-api', 'Not by the manager’s current key: an older or another EZRA key.'];
  return ['danger', 'x', 'Not signed by a trusted key', 'The payload was changed after signing, or an unknown key signed it. netra-api refuses it.'];
}

// Checks of what the payload says: neutral when no trusted key signed it (netra-api refuses it anyway).
const claim = (signed, state) => (signed ? state : 'na');

function statusLine(r, signed) {
  const l = r.license;
  const c = (state, title, sub) => check(claim(signed, state), title, sub);
  if (isZero(l.expires_at)) return c('ok', 'Valid, without expiry');
  if (r.status === 'valid') {
    return c(r.expiring ? 'warn' : 'ok', `Valid until ${longDate(l.expires_at)}`, relDays(l.expires_at));
  }
  if (r.status === 'grace') return c('warn', `Expired ${relDays(l.expires_at)}, in its grace period`, `Modules on until ${longDate(r.grace_ends)}`);
  return c('bad', `Expired on ${longDate(l.expires_at)}`, 'netra-api runs the core only');
}

function installLine(r, signed) {
  const bound = r.license.installation;
  const x = r.installation;
  const c = (state, title, sub) => check(claim(signed, state), title, sub);
  if (x) {
    if (!bound) return c('warn', `Not bound: it works on ${x.id} and on any other server`);
    return x.match
      ? c('ok', html`Bound to <span class="mono">${bound}</span>, the server you named`)
      : c('bad', html`Bound to <span class="mono">${bound}</span>, not <span class="mono">${x.id}</span>`, 'That server refuses it.');
  }
  return bound
    ? c('ok', html`Bound to the installation <span class="mono">${bound}</span>`)
    : c('warn', 'Not bound to an installation: it works on any server');
}

function resultHTML(r, meta) {
  const [tone, ico, title, text] = verdict(r);
  if (r.error) {
    return html`<div class="verdict ${tone}"><div class="big">${icon(ico, 24)}</div><div><h3>${title}</h3><p>${text}</p></div></div>
      ${r.warning ? html`<ul class="checks">${check('warn', r.warning)}</ul>` : ''}`;
  }
  const l = r.license;
  return html`<div class="verdict ${tone}"><div class="big">${icon(ico, 24)}</div><div><h3>${title}</h3><p>${text}</p></div></div>
    <ul class="checks">
      ${r.warning ? check('warn', 'The text is wrapped', r.warning) : ''}
      ${
        r.manager.ok
          ? check('ok', 'The manager’s key signed it')
          : check('bad', 'Not signed by the manager’s key', r.manager.error === 'license signature is not from a trusted key' ? '' : r.manager.error)
      }
      ${
        r.built_in.ok
          ? check('ok', `Accepted by ${r.built_in.keys === 1 ? 'the key' : `one of the ${r.built_in.keys} keys`} built into this build`)
          : r.built_in.keys
            ? check('bad', `None of the ${r.built_in.keys} key${r.built_in.keys === 1 ? '' : 's'} built into this build signed it`)
            : check('na', 'This build of netra-api has no key built in', 'Release builds carry internal/license/keys/*.pub')
      }
      ${statusLine(r, r.manager.ok || r.built_in.ok)}
      ${installLine(r, r.manager.ok || r.built_in.ok)}
      ${
        !r.record
          ? check('na', 'Not in this manager’s records')
          : r.record.same_text
            ? check('ok', html`Issued by this manager: <a class="mono" href="#/licenses/${r.record.id}">${r.record.id}</a>`)
            : check(
                'warn',
                html`The ID <a class="mono" href="#/licenses/${r.record.id}">${r.record.id}</a> was issued here, with another text`,
                'Compare the two: this one is not what the manager signed.',
              )
      }
    </ul>
    <div class="panel-body"><dl class="kv tight">
      <dt>Client</dt><dd>${l.client}</dd>
      <dt>Edition</dt><dd>${EDITIONS[l.edition] || l.edition} · ${TIERS[l.tier] || l.tier}</dd>
      <dt>Modules</dt><dd>${(l.features || []).length ? html`<div class="chips">${catalogOrder(l.features, meta.catalog).map((f) => html`<span class="chip">${moduleName(f)}</span>`)}</div>` : 'core only'}</dd>
      <dt>Map</dt><dd>${MAPS[l.map] || l.map || 'edition default'}</dd>
      <dt>Robots</dt><dd>${l.max_robots ? `up to ${l.max_robots}` : 'no limit'}</dd>
      <dt>Issued</dt><dd>${longDate(l.issued_at)}</dd>
    </dl></div>`;
}

export async function verifyPage({ root, setTitle, app }) {
  setTitle('Licenses', 'Verify');
  root.innerHTML = String(html`
    <div class="page-head"><div><h1>Verify a license</h1>
      <p>Paste any NETRA license, from this manager or not. It is decoded and checked the way netra-api checks it.</p></div></div>
    <div class="grid cols-2">
      <section class="panel">
        <div class="panel-head"><h2>${icon('file', 15)} License text</h2>
          <div class="actions"><input type="file" id="lic-file" class="hidden" accept=".lic,text/plain">
          <button class="btn sm" data-open>${icon('upload', 14)} Open a .lic file</button></div></div>
        <form class="panel-body" id="verify-form" novalidate>
          <label class="field" data-field="text"><textarea class="textblock" name="text" rows="9" placeholder="NETRA-LIC-1.eyJwcm9kdWN0Ijoi…" spellcheck="false"></textarea><div class="msg"></div></label>
          <div class="field-gap"></div>
          <label class="field" data-field="installation"><span class="label">Installation ID<span class="opt">optional: check the binding against a server</span></span>
            <input class="input mono" name="installation" placeholder="NETRA-XXXX-XXXX-XXXX" autocomplete="off"><div class="msg"></div></label>
          <div class="form-error"></div>
          <div class="field-gap"></div>
          <button class="btn primary" type="submit">${icon('verify', 16)} Verify</button>
        </form>
      </section>
      <section class="panel" id="result">
        <div class="panel-head"><h2>${icon('shield', 15)} Result</h2></div>
        <div class="empty">${icon('verify', 30)}<div>Paste a license and press Verify.</div></div>
      </section>
    </div>
    <section class="panel hidden" id="payload">
      <div class="panel-head"><h2>${icon('info', 15)} Decoded payload</h2><span class="muted small">shown even when no key signed it</span></div>
      <pre class="code" id="payload-json"></pre>
    </section>`);
  const form = $('#verify-form', root);
  on(root, 'click', '[data-open]', () => $('#lic-file', root).click());
  on(root, 'change', '#lic-file', async (e, input) => {
    if (!input.files[0]) return;
    form.text.value = (await input.files[0].text()).trim();
    input.value = '';
    form.requestSubmit();
  });
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (!form.text.value.trim()) {
      showError(form, Object.assign(new Error('Paste a license text first.'), { field: 'text' }));
      return;
    }
    try {
      const r = await busy($('button[type=submit]', form), () =>
        post('/verify', { text: form.text.value, installation: form.installation.value }),
      );
      $('#result', root).innerHTML = String(html`<div class="panel-head"><h2>${icon('shield', 15)} Result</h2></div>${resultHTML(r, app.meta)}`);
      const payload = $('#payload', root);
      payload.classList.toggle('hidden', !r.license);
      if (r.license) $('#payload-json', root).innerHTML = String(prettyJSON(r.license));
    } catch (err) {
      showError(form, err);
    }
  });
}
