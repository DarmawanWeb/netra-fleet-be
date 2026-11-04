// Applies the remembered theme (or the system's) before the page paints.
(function () {
  var theme;
  try {
    theme = localStorage.getItem('netra-lm.theme');
  } catch (e) {
    theme = null;
  }
  if (theme !== 'light' && theme !== 'dark') {
    theme = window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  }
  document.documentElement.setAttribute('data-theme', theme);
})();
