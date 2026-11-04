// The branding preview. The contrast logic is the NETRA dashboard's (src/lib/branding.tsx): the
// licensed colour replaces the accent of each theme, mixed towards black or white until it reaches
// 4.5:1 against that theme's surface, so what this preview shows is what clients will see.

import { $$, html } from './lib.js';

const parseHex = (c) => {
  if (!c || !/^#[0-9a-f]{6}$/i.test(c)) return undefined;
  const n = parseInt(c.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
};

const channel = (v) => {
  const c = v / 255;
  return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
};
const luminance = ([r, g, b]) => 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);

export const contrast = (a, b) => {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
};

/** Mixes c towards `toward` until it reaches `min` contrast against `bg`. */
export function readable(c, bg, toward, min = 4.5) {
  const mix = (v, to, t) => Math.round(v + (to - v) * t);
  for (let t = 0; t <= 1.0001; t += 0.05) {
    const mixed = [mix(c[0], toward[0], t), mix(c[1], toward[1], t), mix(c[2], toward[2], t)];
    if (contrast(mixed, bg) >= min) return mixed;
  }
  return toward;
}

// The surfaces the accent is drawn on (ink-900 of each theme) and NETRA's own accents.
const DARK_SURFACE = [10, 16, 28];
const LIGHT_SURFACE = [255, 255, 255];
const NETRA_DARK = [34, 211, 238];
const NETRA_LIGHT = [8, 145, 178];

const hex = (rgb) => '#' + rgb.map((v) => v.toString(16).padStart(2, '0')).join('');

/** The accent of each theme for a branding: the colour as drawn, the one asked, the contrast. */
export function accents(b) {
  const light = parseHex(b.color);
  const dark = parseHex(b.color_dark) || light;
  if (!light || !dark) {
    return {
      custom: false,
      light: { rgb: NETRA_LIGHT, asked: NETRA_LIGHT, ratio: contrast(NETRA_LIGHT, LIGHT_SURFACE) },
      dark: { rgb: NETRA_DARK, asked: NETRA_DARK, ratio: contrast(NETRA_DARK, DARK_SURFACE) },
    };
  }
  const l = readable(light, LIGHT_SURFACE, [0, 0, 0]);
  const d = readable(dark, DARK_SURFACE, [255, 255, 255]);
  return {
    custom: true,
    light: { rgb: l, asked: light, ratio: contrast(l, LIGHT_SURFACE) },
    dark: { rgb: d, asked: dark, ratio: contrast(d, DARK_SURFACE) },
  };
}

const logoMark = (b, size) =>
  b.logo && /^data:image\/(svg\+xml|png);base64,/.test(b.logo)
    ? html`<img src="${b.logo}" alt="">`
    : html`<span class="mock-ezra ${size}">EZRA<span>ROBOTICS</span></span>`;

const login = (b, theme) => html`
  <div class="mock ${theme}" data-mock="${theme}">
    <div class="mock-login"><div class="mock-card">
      <div class="mock-logo">${logoMark(b, '')}</div>
      <div class="mock-name">${b.name || 'NETRA'}</div>
      <div class="mock-tag">PATROL COMMAND CENTER</div>
      <div class="mock-field focus">Username<i></i></div>
      <div class="mock-field">Password<i></i></div>
      <div class="mock-btn">Sign in</div>
    </div></div>
  </div>`;

const app = (b, theme) => html`
  <div class="mock ${theme}" data-mock="${theme}">
    <div class="mock-app">
      <div class="mock-side">
        <div class="mock-side-name">${b.name || 'NETRA'}</div>
        <div class="mock-side-tag">PATROL COMMAND CENTER</div>
        <div class="mock-nav-title">OPERATIONS</div>
        <div class="mock-nav on">Dashboard</div>
        <div class="mock-nav">Operations map</div>
        <div class="mock-nav">Patrols</div>
        <div class="mock-nav">Schedules</div>
        <div class="mock-nav-title">DATA</div>
        <div class="mock-nav">Routes</div>
        <div class="mock-nav">Reports</div>
      </div>
      <div class="mock-main">
        <div class="mock-head">${logoMark(b, 'small')}<span class="mock-live">Live</span></div>
        <div class="mock-body">
          <div class="mock-tile"><span>ROBOTS ONLINE</span><b>3</b></div>
          <div class="mock-tile"><span>PATROLS TODAY</span><b>12</b></div>
          <div class="mock-map"></div>
        </div>
      </div>
    </div>
  </div>`;

/** The preview: the login page and the dashboard, dark and light. */
export const preview = (b) => html`
  <div class="preview-grid">
    <div><div class="preview-label"><span>Login · dark</span></div>${login(b, 'dark')}</div>
    <div><div class="preview-label"><span>Login · light</span></div>${login(b, 'light')}</div>
    <div><div class="preview-label"><span>Dashboard · dark</span></div>${app(b, 'dark')}</div>
    <div><div class="preview-label"><span>Dashboard · light</span></div>${app(b, 'light')}</div>
  </div>
  <div class="contrast" data-contrast></div>`;

/** Applies the adjusted accents to the mocks of root and writes the contrast line. */
export function paint(root, b) {
  const a = accents(b);
  $$('[data-mock]', root).forEach((el) => el.style.setProperty('--m-accent', a[el.dataset.mock].rgb.join(' ')));
  const line = root.querySelector('[data-contrast]');
  if (!line) return;
  const describe = (theme, label) => {
    const x = a[theme];
    const changed = a.custom && hex(x.rgb) !== hex(x.asked);
    return html`<span><i class="swatch" data-swatch="${hex(x.rgb)}"></i>${label} ${hex(x.rgb)} · ${x.ratio.toFixed(1)}:1${
      changed ? html` <span class="dim">(adjusted from ${hex(x.asked)})</span>` : ''
    }</span>`;
  };
  line.innerHTML = String(
    a.custom
      ? html`${describe('light', 'Light')}${describe('dark', 'Dark')}`
      : html`${describe('light', 'NETRA cyan, light')}${describe('dark', 'dark')}`,
  );
  line.querySelectorAll('[data-swatch]').forEach((el) => (el.style.background = el.dataset.swatch));
}

