// Applies the theme before first paint: a stored choice wins, else the
// system preference, else dark. Loaded as a plain blocking script from
// index.html; kept external because the bff serves the bundle under a
// CSP with script-src 'self', which never runs inline scripts.
;(function () {
  var stored = localStorage.getItem('theme')
  var dark =
    stored !== null
      ? stored === 'dark'
      : !window.matchMedia('(prefers-color-scheme: light)').matches
  document.documentElement.classList.toggle('dark', dark)
})()
