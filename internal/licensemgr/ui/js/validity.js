// The validity control of the issue form and the renew dialog: a number of days, an end date or
// no expiry, with the resulting dates spelled out.

import { $, $$, dateInput, endOfDay, html, icon, longDate, on } from './lib.js';

const DAY = 86400000;

/** The expiry a validity gives for a license issued now; null for none, undefined when invalid. */
export function expiresOf(v) {
  if (v.mode === 'none') return null;
  if (v.mode === 'days') {
    const n = Number(v.days);
    return Number.isInteger(n) && n > 0 ? new Date(Date.now() + n * DAY) : undefined;
  }
  if (!v.until) return undefined;
  const [y, m, d] = v.until.split('-').map(Number);
  const t = new Date(y, m - 1, d, 23, 59, 59);
  return t > new Date() ? t : undefined;
}

/** The validity as the API takes it. */
export const validityBody = (v) =>
  v.mode === 'days'
    ? { mode: 'days', days: Number(v.days) }
    : v.mode === 'until'
      ? { mode: 'until', until: v.until ? endOfDay(v.until) : '' }
      : { mode: 'none' };

export const validityHTML = (v, maxDays) => html`
  <div class="field" data-field="validity.mode">
    <div class="seg" data-validity-mode>
      <button type="button" data-mode="days" class="${v.mode === 'days' ? 'on' : ''}">Days</button>
      <button type="button" data-mode="until" class="${v.mode === 'until' ? 'on' : ''}">End date</button>
      <button type="button" data-mode="none" class="${v.mode === 'none' ? 'on' : ''}">No expiry</button>
    </div>
    <div class="msg"></div>
  </div>
  <div class="form-grid validity-inputs">
    <label class="field ${v.mode === 'days' ? '' : 'hidden'}" data-field="validity.days" data-for="days">
      <span class="label">Days of validity</span>
      <div class="input-group"><input class="input" name="days" type="number" min="1" max="${maxDays}" value="${v.days}"><span class="addon">days</span></div>
      <div class="hint">Counted from the moment it is signed. 365 is a year.</div>
      <div class="msg"></div>
    </label>
    <label class="field ${v.mode === 'until' ? '' : 'hidden'}" data-field="validity.until" data-for="until">
      <span class="label">Valid until (end of the day)</span>
      <input class="input" name="until" type="date" value="${v.until}" min="${dateInput(Date.now() + DAY)}">
      <div class="msg"></div>
    </label>
    <div class="${v.mode === 'none' ? '' : 'hidden'} full" data-for="none">
      <div class="callout warn">${icon('alert')}<div class="grow"><b>A perpetual license.</b>
        <p>It never needs renewing; use it only for a perpetual purchase.</p></div></div>
    </div>
  </div>
  <div class="hint" data-validity-summary></div>`;

/** Wires the control in root to state (mode, days, until); onChange runs after each change. */
export function bindValidity(root, state, graceDays, onChange = () => {}) {
  const summary = () => {
    const el = $('[data-validity-summary]', root);
    const exp = expiresOf(state);
    if (!el) return;
    if (exp === null) el.textContent = 'No expiry: the modules stay on for good.';
    else if (exp === undefined) el.textContent = '';
    else {
      const grace = new Date(exp.getTime() + graceDays * DAY);
      el.textContent = `Valid until ${longDate(exp.toISOString())}; the modules keep working until ${longDate(grace.toISOString())} (${graceDays} days of grace).`;
    }
  };
  on(root, 'click', '[data-mode]', (e, b) => {
    state.mode = b.dataset.mode;
    $$('[data-mode]', root).forEach((x) => x.classList.toggle('on', x === b));
    $$('[data-for]', root).forEach((x) => x.classList.toggle('hidden', x.dataset.for !== state.mode));
    if (state.mode === 'until' && !state.until) {
      state.until = dateInput(Date.now() + Number(state.days || 365) * DAY);
      $('input[name=until]', root).value = state.until;
    }
    summary();
    onChange();
  });
  on(root, 'input', 'input[name=days], input[name=until]', (e, input) => {
    state[input.name] = input.value;
    input.closest('.field').classList.remove('invalid');
    summary();
    onChange();
  });
  summary();
}
