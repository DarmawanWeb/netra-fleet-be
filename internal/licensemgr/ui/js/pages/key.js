import { $, busy, copyText, dateTime, field, fieldError, html, icon, on, post, rerender, showError, toast } from '../lib.js';

const passphraseFields = (minimum) => html`
  ${field({ name: 'passphrase', label: 'Passphrase', type: 'password', auto: 'new-password', hint: `At least ${minimum} characters. A few unrelated words are easy to type and hard to guess.` })}
  ${field({ name: 'confirm', label: 'Passphrase again', type: 'password', auto: 'new-password' })}`;

function setupHTML(meta) {
  return html`
    <div class="callout">${icon('info')}<div class="grow"><b>One key signs every license.</b>
      <p>Its public half goes into netra-api (<code>internal/license/keys/</code>); only builds carrying it accept the licenses it signs.
      If a key already signs licenses for clients, import that one instead of making a new one.</p></div></div>
    <div class="grid cols-2">
      <section class="panel">
        <div class="panel-head"><h2>${icon('key', 15)} Generate a new key</h2></div>
        <form class="panel-body" id="gen-form" novalidate>
          <p class="muted small">A new Ed25519 key pair, made here. The private key is only ever written encrypted with your passphrase.</p>
          ${passphraseFields(meta.min_passphrase)}
          <div class="form-error"></div><div class="field-gap"></div>
          <button class="btn primary" type="submit">${icon('key', 16)} Generate the key</button>
        </form>
      </section>
      <section class="panel">
        <div class="panel-head"><h2>${icon('upload', 15)} Import an existing key</h2></div>
        <form class="panel-body" id="import-form" novalidate>
          <p class="muted small">The <code>.key</code> file of <code>netra-license keygen</code> (one line of base64). Keep the original offline as the master copy.</p>
          <label class="field" data-field="key"><span class="label">Private key<button type="button" class="linkish small" data-open-key>Open a .key file</button></span>
            <textarea class="textarea mono" name="key" rows="3" spellcheck="false" autocomplete="off" placeholder="base64 of the 64-byte Ed25519 key"></textarea><div class="msg"></div></label>
          <input type="file" id="key-file" class="hidden" accept=".key,text/plain">
          <div class="field-gap"></div>
          ${passphraseFields(meta.min_passphrase)}
          <div class="form-error"></div><div class="field-gap"></div>
          <button class="btn primary" type="submit">${icon('upload', 16)} Import the key</button>
        </form>
      </section>
    </div>`;
}

function keyHTML(k, meta) {
  return html`
    <div class="grid split">
      <div class="stack">
        <section class="panel">
          <div class="keycard ${k.unlocked ? 'ok' : 'warn'}">
            <div class="glyph">${icon(k.unlocked ? 'unlock' : 'lock', 26)}</div>
            <div class="grow"><h3>${k.unlocked ? 'Unlocked' : 'Locked'}</h3>
              <p>${
                k.unlocked
                  ? html`Decrypted in this process. It locks itself at ${new Date(k.locks_at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}
                    after ${k.idle_minutes} minutes without signing, and when the manager stops.`
                  : 'Encrypted on disk. Unlock it to sign; it stays in memory only, never on disk or in the browser.'
              }</p></div>
            ${k.unlocked ? html`<button class="btn danger" data-lock>${icon('lock', 16)} Lock now</button>` : html`<button class="btn primary" data-unlock>${icon('unlock', 16)} Unlock</button>`}
          </div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2>${icon('shield', 15)} Public key</h2>
            <div class="actions"><button class="btn sm" data-copy-pub>${icon('copy', 14)} Copy</button>
            <a class="btn sm" href="/api/key/public" download="${k.file_name}">${icon('download', 14)} ${k.file_name}</a></div></div>
          <div class="panel-body">
            <div class="pubkey"><code>${k.public_key}</code></div>
            <div class="field-gap"></div>
            <dl class="kv tight"><dt>Fingerprint</dt><dd class="fingerprint">${k.fingerprint}</dd></dl>
            <div class="field-gap"></div>
            ${
              k.trusted
                ? html`<div class="callout ok">${icon('check')}<div class="grow"><b>Built into this build of netra-api.</b>
                  <p>Servers running this version accept the licenses signed here.</p></div></div>`
                : html`<div class="callout warn">${icon('alert')}<div class="grow"><b>Not built into this build of netra-api.</b>
                  <p>${k.trusted_keys ? `It trusts ${k.trusted_keys} other key${k.trusted_keys === 1 ? '' : 's'}.` : 'It trusts no key at all.'}
                  netra-api builds without this public key refuse every license signed here.${k.trust_error ? ` (${k.trust_error})` : ''}</p></div></div>`
            }
            <ol class="steps">
              <li>Save it as <code>internal/license/keys/${k.file_name}</code> in netra-fleet-be (only the <code>.pub</code>).</li>
              <li>Commit it and release netra-api; the images built from then on accept these licenses.</li>
              <li>Keep the old <code>.pub</code> until every client has a license signed by this key.</li>
            </ol>
          </div>
        </section>
      </div>
      <div class="stack">
        <section class="panel">
          <div class="panel-head"><h2>${icon('key', 15)} Key file</h2></div>
          <div class="panel-body"><dl class="kv tight">
            <dt>File</dt><dd><code>key.enc</code> in the data folder</dd>
            <dt>Origin</dt><dd>${k.source === 'imported' ? 'Imported from a .key file' : 'Generated here'}</dd>
            <dt>Since</dt><dd>${dateTime(k.created_at)}</dd>
            <dt>Encryption</dt><dd>AES-256-GCM, key from PBKDF2-SHA256</dd>
            <dt>Iterations</dt><dd class="mono">${k.iterations.toLocaleString('en')}</dd>
          </dl></div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2>${icon('lock', 15)} Change the passphrase</h2></div>
          <form class="panel-body" id="pass-form" novalidate>
            ${field({ name: 'current', label: 'Current passphrase', type: 'password', auto: 'current-password' })}
            <div class="field-gap"></div>
            ${field({ name: 'new', label: 'New passphrase', type: 'password', auto: 'new-password', hint: `At least ${meta.min_passphrase} characters.` })}
            <div class="field-gap"></div>
            ${field({ name: 'confirm', label: 'New passphrase again', type: 'password', auto: 'new-password' })}
            <div class="form-error"></div><div class="field-gap"></div>
            <button class="btn" type="submit">Change the passphrase</button>
          </form>
        </section>
        <div class="callout">${icon('info')}<div class="grow"><b>Backups</b>
          <p>Copy the whole data folder. Without the passphrase <code>key.enc</code> is useless, so keep the passphrase apart from the backups.</p></div></div>
      </div>
    </div>`;
}

export async function keyPage(ctx) {
  const { root, setTitle, app, refreshKey } = ctx;
  setTitle('Administration', 'Signing key');
  const k = await refreshKey();
  const meta = app.meta;
  root.innerHTML = String(html`
    <div class="page-head"><div><h1>Signing key</h1>
      <p>The Ed25519 key that signs every license, kept in <code>key.enc</code> encrypted with a passphrase.</p></div></div>
    ${k.exists ? keyHTML(k, meta) : setupHTML(meta)}`);
  const again = () => rerender(ctx, keyPage);

  const confirmed = (form, name = 'passphrase') => {
    if (form[name].value.length < meta.min_passphrase) throw fieldError(name, `At least ${meta.min_passphrase} characters.`);
    if (form[name].value !== form.confirm.value) throw fieldError('confirm', 'The two passphrases differ.');
  };
  const submit = (id, fn) => {
    const form = $(id, root);
    if (!form) return;
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      try {
        await busy($('button[type=submit]', form), () => fn(form));
      } catch (err) {
        showError(form, err);
      }
    });
  };
  submit('#gen-form', async (form) => {
    confirmed(form);
    await post('/key/generate', { passphrase: form.passphrase.value });
    toast('Signing key generated and unlocked.');
    await refreshKey();
    again();
  });
  submit('#import-form', async (form) => {
    if (!form.key.value.trim()) throw fieldError('key', 'Paste the private key or open the .key file.');
    confirmed(form);
    await post('/key/import', { key: form.key.value, passphrase: form.passphrase.value });
    toast('Signing key imported and unlocked.');
    await refreshKey();
    again();
  });
  submit('#pass-form', async (form) => {
    confirmed(form, 'new');
    await post('/key/passphrase', { current: form.current.value, new: form.new.value });
    form.reset();
    toast('Passphrase changed.');
  });
  on(root, 'click', '[data-open-key]', () => $('#key-file', root).click());
  on(root, 'change', '#key-file', async (e, input) => {
    if (input.files[0]) $('#import-form', root).key.value = (await input.files[0].text()).trim();
    input.value = '';
  });
  on(root, 'click', '[data-copy-pub]', async () => {
    await copyText(k.public_key);
    toast('Public key copied.');
  });
  on(root, 'click', '[data-lock]', async () => {
    await post('/key/lock');
    await refreshKey();
    toast('Signing key locked.');
    again();
  });
  on(root, 'click', '[data-unlock]', async () => {
    if (await ctx.ensureUnlocked()) again();
  });
}
