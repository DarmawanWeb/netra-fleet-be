import {
  $,
  $$,
  EDITIONS,
  EDITION_ORDER,
  EDITION_TEXT,
  MAPS,
  MAP_TEXT,
  MODULES,
  TIERS,
  TIER_ORDER,
  TIER_TEXT,
  busy,
  clearErrors,
  dataSize,
  dialog,
  field,
  fieldError,
  get,
  html,
  icon,
  isZero,
  longDate,
  on,
  ordered,
  paintSwatches,
  post,
  showError,
  textareaField,
  toast,
} from '../lib.js';
import { accents, paint, preview } from '../brand.js';
import { bindValidity, expiresOf, validityBody, validityHTML } from '../validity.js';
import { keyCallouts } from './dashboard.js';

const DAY = 86400000;
const HEX = /^#[0-9a-f]{6}$/i;
const hex = (rgb) => '#' + rgb.map((v) => v.toString(16).padStart(2, '0')).join('');

/** Reads a logo file into a data URL, refusing what NETRA would not serve. */
async function readLogo(file, max) {
  if (file.size > max) {
    throw fieldError('branding.logo', `The logo is ${Math.ceil(file.size / 1024)} KB; at most ${max / 1024} KB.`);
  }
  const buf = new Uint8Array(await file.arrayBuffer());
  const png = buf.length > 8 && buf[0] === 0x89 && buf[1] === 0x50 && buf[2] === 0x4e && buf[3] === 0x47;
  const svg = !png && new TextDecoder().decode(buf.subarray(0, 4096)).toLowerCase().includes('<svg');
  if (!png && !svg) throw fieldError('branding.logo', 'The logo must be an SVG or PNG file.');
  let bin = '';
  for (let i = 0; i < buf.length; i += 0x8000) bin += String.fromCharCode(...buf.subarray(i, i + 0x8000));
  return `data:${png ? 'image/png' : 'image/svg+xml'};base64,${btoa(bin)}`;
}

const choice = (name, value, title, text, checked, tag = '') => html`
  <label class="choice"><input type="radio" name="${name}" value="${value}" ${checked ? 'checked' : ''}>
    <div class="choice-body"><div class="choice-title">${title}${tag}</div><div class="choice-text">${text}</div></div></label>`;

export async function issuePage(ctx) {
  const { root, query, setTitle, app, current } = ctx;
  const meta = app.meta;
  const fromId = query.get('from');
  const [clients, from] = await Promise.all([get('/clients'), fromId ? get(`/licenses/${fromId}`) : null]);
  if (!current()) return;
  setTitle('Licenses', from ? 'Change license' : 'Issue license');

  const preset = (edition, tier) => [...new Set([...(meta.editions[edition] || []), ...(meta.tiers[tier] || [])])].sort();
  const mapDefault = (edition) => meta.edition_maps[edition] || 'floorplan';
  const st = {
    client: query.get('client') || '',
    deployment: query.get('deployment') || '',
    edition: 'mall',
    tier: 'pro',
    add: new Set(),
    remove: new Set(),
    map: '',
    robots: 0,
    validity: { mode: 'days', days: 365, until: '' },
    brand: { name: '', custom: false, color: '#0e7490', color_dark: '#22d3ee', linked: false, logo: '' },
    supersedes: '',
  };
  if (from) {
    const l = from.license;
    const p = preset(l.edition, l.tier);
    const fs = new Set(l.features || []);
    Object.assign(st, {
      client: from.client_id,
      deployment: from.deployment_id || '',
      edition: l.edition,
      tier: l.tier,
      add: new Set([...fs].filter((f) => !p.includes(f))),
      remove: new Set(p.filter((f) => !fs.has(f))),
      map: l.map === mapDefault(l.edition) ? '' : l.map,
      robots: l.max_robots,
      supersedes: from.id,
    });
    if (isZero(l.expires_at)) st.validity.mode = 'none';
    else st.validity.days = Math.max(1, Math.round((new Date(l.expires_at) - new Date(l.issued_at)) / DAY));
    const b = l.branding || {};
    st.brand = {
      name: b.name || '',
      custom: !!b.color,
      color: b.color || st.brand.color,
      color_dark: b.color_dark || b.color || st.brand.color_dark,
      linked: !!b.color && b.color === b.color_dark,
      logo: b.logo || '',
    };
  }

  const modules = () => {
    const p = preset(st.edition, st.tier);
    return meta.catalog.map((m) => m.id).filter((id) => (p.includes(id) && !st.remove.has(id)) || st.add.has(id));
  };
  const client = () => clients.find((c) => c.id === st.client);
  const deployment = () => (client() ? client().deployments.find((d) => d.id === st.deployment) : undefined);
  const branding = () => ({
    name: st.brand.name.trim(),
    color: st.brand.custom ? st.brand.color.toLowerCase() : '',
    color_dark: st.brand.custom ? (st.brand.linked ? st.brand.color : st.brand.color_dark).toLowerCase() : '',
    logo: st.brand.logo,
  });

  root.innerHTML = String(html`
    <div class="page-head">
      <div><div class="eyebrow">Licenses</div><h1>${from ? 'Change a license' : 'Issue a license'}</h1>
        <p>Choose the terms, check the preview, sign. A signed license never changes: a change is a new license that replaces it.</p></div>
    </div>
    ${
      from
        ? html`<div class="callout">${icon('edit')}<div class="grow"><b>Changing <span class="mono">${from.id}</span> of ${from.client_name}.</b>
          <p>The form starts from its terms. The new license replaces it; to only extend the dates, use Renew on the license page.</p></div>
          <a class="btn sm" href="#/licenses/${from.id}">Back to the license</a></div>`
        : ''
    }
    ${keyCallouts(app.key)}
    <form id="issue-form" class="grid split-wide" novalidate>
      <div class="stack">
        <section class="panel">
          <div class="panel-head"><h2><span class="step">1</span> Client and deployment</h2>
            <button type="button" class="btn sm" data-new-client>${icon('plus', 14)} New client</button></div>
          <div class="panel-body">
            <div class="form-grid">
              <label class="field" data-field="client_id"><span class="label">Client</span>
                <select class="select" name="client"></select><div class="msg"></div></label>
              <label class="field" data-field="deployment_id"><span class="label">Deployment<span class="opt">binds the license to its server</span></span>
                <select class="select" name="deployment"></select><div class="msg"></div></label>
            </div>
            <div id="binding"></div>
          </div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2><span class="step">2</span> Edition and tier</h2></div>
          <div class="panel-body">
            <div class="label">Edition: the product line</div>
            <div class="choices" data-field="edition">
              ${ordered(Object.keys(meta.editions), EDITION_ORDER).map((e) => choice('edition', e, EDITIONS[e] || e, EDITION_TEXT[e] || '', e === st.edition))}
            </div>
            <div class="field-gap"></div>
            <div class="label">Tier: the premium modules</div>
            <div class="choices" data-field="tier">
              ${ordered(Object.keys(meta.tiers), TIER_ORDER).map((t) => choice('tier', t, TIERS[t] || t, TIER_TEXT[t] || '', t === st.tier))}
            </div>
          </div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2><span class="step">3</span> Modules</h2>
            <div class="actions"><span class="muted small" id="mod-summary"></span>
            <button type="button" class="linkish small" data-reset-modules>Reset to the preset</button></div></div>
          <div class="panel-body" data-field="features"><div class="modules" id="modules"></div><div class="msg"></div>
            <div class="hint">The core (robots, live map, commands, routes, patrols, schedules, floor plans, users, patrol reports) works without any module.</div></div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2><span class="step">4</span> Map and robots</h2></div>
          <div class="panel-body">
            <div class="label">Map mode: fixed by the license</div>
            <div class="choices" data-field="map" id="maps"></div>
            <div class="field-gap"></div>
            <div class="form-grid">
              <label class="field" data-field="max_robots"><span class="label">Robot limit</span>
                <div class="input-group"><input class="input" name="robots" type="number" min="0" max="${meta.max_robots}" value="${st.robots}"><span class="addon">robots</span></div>
                <div class="hint">0 for no limit. Robots already registered keep working under a lower limit.</div><div class="msg"></div></label>
            </div>
          </div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2><span class="step">5</span> Validity</h2></div>
          <div class="panel-body" id="validity">${validityHTML(st.validity, meta.max_days)}</div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2><span class="step">6</span> Branding</h2><span class="muted small">name, colours and logo in the dashboard and reports</span></div>
          <div class="panel-body">
            <div class="form-grid">
              ${field({ name: 'brand_name', label: 'Application name', value: st.brand.name, placeholder: 'NETRA', hint: 'In the sidebar, on the login page and in reports. Empty: NETRA.', attrs: 'maxlength="40"' })}
              <div class="field"><span class="label">Colour</span>
                <label class="check"><input type="checkbox" name="custom" ${st.brand.custom ? 'checked' : ''}> The client's colour instead of NETRA cyan</label></div>
              <div class="field ${st.brand.custom ? '' : 'hidden'}" data-field="branding.color" data-colors>
                <span class="label">Light theme</span>
                <div class="color-row"><input type="color" name="color_pick" value="${st.brand.color}" aria-label="Light theme colour">
                  <input class="input" name="color" value="${st.brand.color}" maxlength="7" spellcheck="false"></div><div class="msg"></div></div>
              <div class="field ${st.brand.custom ? '' : 'hidden'}" data-field="branding.color_dark" data-colors>
                <span class="label">Dark theme <label class="check small"><input type="checkbox" name="linked" ${st.brand.linked ? 'checked' : ''}> Same as light</label></span>
                <div class="color-row"><input type="color" name="color_dark_pick" value="${st.brand.color_dark}" aria-label="Dark theme colour">
                  <input class="input" name="color_dark" value="${st.brand.color_dark}" maxlength="7" spellcheck="false"></div><div class="msg"></div></div>
              <div class="full hint ${st.brand.custom ? '' : 'hidden'}" data-colors>The dashboard mixes each colour towards black or white until it reads at 4.5:1 on that theme. The preview shows the result.</div>
              <div class="field full" data-field="branding.logo"><span class="label">Logo<span class="opt">SVG or PNG, up to ${meta.max_logo / 1024} KB, embedded in the license</span></span>
                <div class="drop" id="drop"><div class="thumb" id="thumb"></div><div class="grow" id="logo-info"></div>
                  <input type="file" id="logo-file" class="hidden" accept=".svg,.png,image/svg+xml,image/png">
                  <button type="button" class="btn sm" data-pick-logo>${icon('upload', 14)} Choose file</button>
                  <button type="button" class="btn sm danger" data-remove-logo>Remove</button></div>
                <div class="msg"></div></div>
            </div>
          </div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2><span class="step">7</span> Note</h2></div>
          <div class="panel-body">${textareaField({ name: 'note', label: 'Internal note', opt: 'kept here, not in the license', placeholder: 'e.g. PO 17/2026, contract until November 2027', rows: 2 })}</div>
        </section>
      </div>
      <div class="stack sticky">
        <section class="panel">
          <div class="panel-head"><h2>${icon('key', 15)} To be signed</h2><span class="muted small">product netra</span></div>
          <div class="panel-body">
            <ul class="summary" id="summary"></ul>
            <div class="form-error"></div>
            <div class="field-gap"></div>
            <button class="btn primary lg block" type="submit" id="sign">${icon('key', 16)} Sign and issue</button>
            <div class="hint" id="sign-hint"></div>
          </div>
        </section>
        <section class="panel">
          <div class="panel-head"><h2>${icon('pin', 15)} Branding preview</h2><span class="muted small">as clients will see it</span></div>
          <div class="panel-body" id="preview">${preview(branding())}</div>
        </section>
      </div>
    </form>`);

  const form = $('#issue-form', root);

  const drawClients = () => {
    form.client.innerHTML = String(
      html`<option value="">Choose a client…</option>${clients.map((c) => html`<option value="${c.id}" ${c.id === st.client ? 'selected' : ''}>${c.name}</option>`)}`,
    );
    const c = client();
    if (!c) st.deployment = '';
    form.deployment.innerHTML = String(
      html`<option value="">No deployment: any server</option>${(c ? c.deployments : []).map(
        (d) => html`<option value="${d.id}" ${d.id === st.deployment ? 'selected' : ''}>${d.name}${d.installation ? ` · ${d.installation}` : ' · no installation ID yet'}</option>`,
      )}`,
    );
    form.deployment.disabled = !c;
    const d = deployment();
    $('#binding', root).innerHTML = String(
      d && d.installation
        ? html`<div class="binding bound">${icon('shield', 16)}<div>Bound to <b class="mono">${d.installation}</b>: the license works on that server only.</div></div>`
        : html`<div class="binding unbound">${icon('alert', 16)}<div>${
            d ? html`${d.name} has no installation ID yet, so the license will work on any server.` : 'Not bound: the license will work on any server.'
          } Bind client licenses to their deployment; leave only EZRA's own cloud deployments unbound.</div></div>`,
    );
  };

  const drawModules = () => {
    const p = preset(st.edition, st.tier);
    const on_ = new Set(modules());
    $('#modules', root).innerHTML = String(
      html`${meta.catalog.map((m) => {
        const inPreset = p.includes(m.id);
        const tag = st.add.has(m.id)
          ? html`<span class="tag info">added</span>`
          : st.remove.has(m.id)
            ? html`<span class="tag warn">removed</span>`
            : inPreset
              ? html`<span class="tag">preset</span>`
              : '';
        return html`<label class="module ${on_.has(m.id) ? 'on' : ''} ${st.remove.has(m.id) ? 'removed' : ''}">
          <input type="checkbox" value="${m.id}" ${on_.has(m.id) ? 'checked' : ''}>
          <div><div class="module-name">${MODULES[m.id] || m.id} ${tag}</div><div class="module-id mono">${m.id}</div>
            <div class="module-text">${m.description}</div></div></label>`;
      })}`,
    );
    const delta = [st.add.size ? `+${st.add.size}` : '', st.remove.size ? `−${st.remove.size}` : ''].filter(Boolean).join(' ');
    $('#mod-summary', root).textContent = `${on_.size} module${on_.size === 1 ? '' : 's'}${delta ? ` · preset ${delta}` : ' · the preset'}`;
    $('[data-reset-modules]', root).classList.toggle('hidden', !st.add.size && !st.remove.size);
  };

  const drawMaps = () => {
    const def = mapDefault(st.edition);
    const current_ = st.map || def;
    $('#maps', root).innerHTML = String(
      html`${meta.map_modes.map((m) =>
        choice('map', m, MAPS[m] || m, MAP_TEXT[m] || '', m === current_, m === def ? html`<span class="tag">edition default</span>` : ''),
      )}`,
    );
  };

  const drawLogo = () => {
    const logo = st.brand.logo;
    $('#thumb', root).innerHTML = logo ? String(html`<img src="${logo}" alt="">`) : String(icon('upload', 20));
    $('#logo-info', root).innerHTML = String(
      logo
        ? html`<b>${logo.startsWith('data:image/png') ? 'PNG' : 'SVG'}</b> · ${dataSize(logo)}, embedded in the license`
        : html`No logo: the dashboard shows the EZRA Robotics mark.<br>Drop a file here or choose one.`,
    );
    $('[data-remove-logo]', root).classList.toggle('hidden', !logo);
  };

  const drawSummary = () => {
    const c = client();
    const d = deployment();
    const mods = modules();
    const exp = expiresOf(st.validity);
    const b = branding();
    const a = accents(b);
    const item = (k, v) => html`<li><span>${k}</span><span>${v}</span></li>`;
    $('#summary', root).innerHTML = String(html`
      ${item('Client', c ? c.name : html`<span class="dim">not chosen</span>`)}
      ${item('Server', d && d.installation ? html`<span class="mono">${d.installation}</span>` : html`<span class="warn-text">any (unbound)</span>`)}
      ${item('Edition', `${EDITIONS[st.edition]} · ${TIERS[st.tier]}`)}
      ${item('Modules', mods.length ? mods.map((m) => MODULES[m] || m).join(', ') : 'core only')}
      ${item('Map', MAPS[st.map || mapDefault(st.edition)])}
      ${item('Robots', Number(st.robots) > 0 ? `up to ${Number(st.robots)}` : 'no limit')}
      ${item('Valid until', exp === null ? 'no expiry' : exp ? longDate(exp.toISOString()) : html`<span class="dim">—</span>`)}
      ${item('Branding', html`${b.name || 'NETRA'} <i class="swatch" data-swatch="${hex(a.light.rgb)}"></i> <i class="swatch" data-swatch="${hex(a.dark.rgb)}"></i>${b.logo ? ' · logo' : ''}`)}
      ${st.supersedes ? item('Replaces', html`<span class="mono">${st.supersedes}</span>`) : ''}`);
    paintSwatches($('#summary', root));
    const k = app.key;
    const sign = $('#sign', root);
    sign.disabled = !k || !k.exists;
    $('#sign-hint', root).innerHTML = String(
      !k || !k.exists
        ? html`No signing key yet: <a href="#/key">set it up</a>.`
        : k.unlocked
          ? html`With the key <span class="mono">${k.fingerprint}</span>.`
          : html`The key is locked: you will be asked for its passphrase.`,
    );
  };

  const drawPreview = () => {
    const el = $('#preview', root);
    el.innerHTML = String(preview(branding()));
    paint(el, branding());
  };

  const redraw = () => {
    drawSummary();
    drawPreview();
  };

  drawClients();
  drawModules();
  drawMaps();
  drawLogo();
  redraw();
  bindValidity($('#validity', root), st.validity, meta.grace_days, drawSummary);

  on(form, 'change', 'select[name=client]', () => {
    st.client = form.client.value;
    st.deployment = '';
    const c = client();
    if (c && c.deployments.length === 1) st.deployment = c.deployments[0].id;
    drawClients();
    drawSummary();
  });
  on(form, 'change', 'select[name=deployment]', () => {
    st.deployment = form.deployment.value;
    drawClients();
    drawSummary();
  });
  on(form, 'change', 'input[name=edition], input[name=tier]', (e, input) => {
    st[input.name] = input.value;
    const p = preset(st.edition, st.tier);
    st.add = new Set([...st.add].filter((f) => !p.includes(f)));
    st.remove = new Set([...st.remove].filter((f) => p.includes(f)));
    if (input.name === 'edition') st.map = '';
    drawModules();
    drawMaps();
    drawSummary();
  });
  on(form, 'change', '#modules input', (e, input) => {
    const p = preset(st.edition, st.tier);
    const id = input.value;
    st.add.delete(id);
    st.remove.delete(id);
    if (input.checked && !p.includes(id)) st.add.add(id);
    if (!input.checked && p.includes(id)) st.remove.add(id);
    drawModules();
    drawSummary();
  });
  on(form, 'click', '[data-reset-modules]', () => {
    st.add.clear();
    st.remove.clear();
    drawModules();
    drawSummary();
  });
  on(form, 'change', 'input[name=map]', (e, input) => {
    st.map = input.value === mapDefault(st.edition) ? '' : input.value;
    drawSummary();
  });
  on(form, 'input', 'input[name=robots]', (e, input) => {
    st.robots = input.value;
    drawSummary();
  });
  on(form, 'input', 'input[name=brand_name]', (e, input) => {
    st.brand.name = input.value;
    redraw();
  });
  on(form, 'change', 'input[name=custom]', (e, input) => {
    st.brand.custom = input.checked;
    $$('[data-colors]', form).forEach((el) => el.classList.toggle('hidden', !input.checked));
    redraw();
  });
  const syncDark = () => {
    const linked = form.linked.checked;
    form.color_dark.disabled = linked;
    form.color_dark_pick.disabled = linked;
    if (linked) {
      form.color_dark.value = st.brand.color;
      form.color_dark_pick.value = st.brand.color;
    }
  };
  on(form, 'change', 'input[name=linked]', () => {
    st.brand.linked = form.linked.checked;
    syncDark();
    redraw();
  });
  on(form, 'input', 'input[name=color_pick], input[name=color_dark_pick]', (e, input) => {
    const key = input.name === 'color_pick' ? 'color' : 'color_dark';
    st.brand[key] = input.value;
    form[key].value = input.value;
    input.closest('.field').classList.remove('invalid');
    syncDark();
    redraw();
  });
  on(form, 'input', 'input[name=color], input[name=color_dark]', (e, input) => {
    const v = input.value.trim();
    st.brand[input.name] = v;
    if (HEX.test(v)) {
      form[`${input.name}_pick`].value = v.toLowerCase();
      input.closest('.field').classList.remove('invalid');
      syncDark();
      redraw();
    }
  });
  syncDark();

  const takeLogo = async (file) => {
    const box = $('[data-field="branding.logo"]', form);
    box.classList.remove('invalid');
    try {
      st.brand.logo = await readLogo(file, meta.max_logo);
      drawLogo();
      redraw();
    } catch (err) {
      showError(form, err);
    }
  };
  on(form, 'click', '[data-pick-logo]', () => $('#logo-file', form).click());
  on(form, 'change', '#logo-file', (e, input) => {
    if (input.files[0]) takeLogo(input.files[0]);
    input.value = '';
  });
  on(form, 'click', '[data-remove-logo]', () => {
    st.brand.logo = '';
    drawLogo();
    redraw();
  });
  const drop = $('#drop', form);
  drop.addEventListener('dragover', (e) => {
    e.preventDefault();
    drop.classList.add('over');
  });
  drop.addEventListener('dragleave', () => drop.classList.remove('over'));
  drop.addEventListener('drop', (e) => {
    e.preventDefault();
    drop.classList.remove('over');
    if (e.dataTransfer.files[0]) takeLogo(e.dataTransfer.files[0]);
  });

  on(form, 'click', '[data-new-client]', () =>
    newClientDialog((c) => {
      clients.push(c);
      clients.sort((a, b) => a.name.localeCompare(b.name));
      st.client = c.id;
      st.deployment = '';
      drawClients();
      drawSummary();
    }),
  );

  const stopKey = ctx.onKey(drawSummary);

  const check = () => {
    if (!st.client) throw fieldError('client_id', 'Choose the client.');
    const n = Number(st.robots);
    if (!Number.isInteger(n) || n < 0 || n > meta.max_robots) throw fieldError('max_robots', `A whole number from 0 to ${meta.max_robots}.`);
    if (st.validity.mode === 'days') {
      const d = Number(st.validity.days);
      if (!Number.isInteger(d) || d < 1 || d > meta.max_days) throw fieldError('validity.days', `From 1 to ${meta.max_days} days.`);
    }
    if (st.validity.mode === 'until' && expiresOf(st.validity) === undefined) throw fieldError('validity.until', 'Choose a date after today.');
    if (st.brand.custom) {
      if (!HEX.test(st.brand.color)) throw fieldError('branding.color', 'A colour like #0f766e.');
      if (!st.brand.linked && !HEX.test(st.brand.color_dark)) throw fieldError('branding.color_dark', 'A colour like #2dd4bf.');
    }
  };

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    clearErrors(form);
    try {
      check();
    } catch (err) {
      showError(form, err);
      return;
    }
    const body = {
      client_id: st.client,
      deployment_id: st.deployment,
      edition: st.edition,
      tier: st.tier,
      features: modules(),
      map: st.map || mapDefault(st.edition),
      max_robots: Number(st.robots),
      validity: validityBody(st.validity),
      branding: branding(),
      note: form.note.value,
      supersedes: st.supersedes,
    };
    const send = () => post('/licenses', body);
    try {
      let res;
      await busy($('#sign', root), async () => {
        try {
          res = await send();
        } catch (err) {
          if (err.code !== 'key_locked' || !(await ctx.ensureUnlocked())) throw err;
          res = await send();
        }
      });
      toast(from ? 'The new license is signed and replaces the old one.' : 'License signed.');
      ctx.refreshKey();
      ctx.refreshCounts();
      location.hash = `#/licenses/${res.id}`;
    } catch (err) {
      if (err.code === 'no_key') ctx.refreshKey();
      showError(form, err);
    }
  });

  return stopKey;
}

/** Adds a client from the issue form. */
export function newClientDialog(done) {
  const d = dialog({
    title: 'New client',
    body: html`<form id="client-form" novalidate>
      ${field({ name: 'name', label: 'Name', placeholder: 'e.g. PLN UPT Bekasi', hint: 'Signed into its licenses and shown in Settings › License.' })}
      ${field({ name: 'contact', label: 'Contact', opt: 'optional', placeholder: 'name, e-mail, phone' })}
      ${textareaField({ name: 'notes', label: 'Notes', opt: 'optional', rows: 2 })}
      <div class="form-error"></div></form>`,
    foot: html`<button class="btn" data-close>Cancel</button><button class="btn primary" type="submit" form="client-form">Add client</button>`,
  });
  const form = $('#client-form', d.root);
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      const c = await busy($('button[type=submit]', d.root), () => post('/clients', Object.fromEntries(new FormData(form))));
      d.close();
      toast(`${c.name} added.`);
      done(c);
    } catch (err) {
      showError(form, err);
    }
  });
}
