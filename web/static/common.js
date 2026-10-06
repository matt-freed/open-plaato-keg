'use strict';

// Helpers every page shares. Pages load this before their own script, so
// these names are taken: a page must not declare its own esc, kegName,
// kegNameWithBeer, api or showToast.

// esc makes a value safe to put in HTML. null and undefined become empty
// rather than the text "null".
function esc(v) {
  return String(v ?? '').replace(/[&<>"']/g, c =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
}

// kegName is what the UI calls a keg scale: its label, or "Keg Scale n" by
// its place in the display order that /api/kegs returns.
function kegName(keg, index) {
  return keg.label?.trim() || `Keg Scale ${index + 1}`;
}

// kegNameWithBeer puts the beer on the scale's tap first, as History and its
// editor name a scale: "Hazy Daze (Basement)". A scale on no tap, or a tap
// with no beer named, is just the scale.
function kegNameWithBeer(keg, index, tap) {
  const name = kegName(keg, index);
  const beer = tap?.name?.trim();
  return beer ? `${beer} (${name})` : name;
}

// api sends a request and returns the parsed JSON response, or null for an
// empty one. A body is sent as JSON. A failed request throws an Error whose
// message is the server's explanation when it gave one, otherwise the status.
//
// Reads that need a response header, such as X-Total-Count, use fetch.
async function api(method, path, body) {
  const init = { method };
  if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body);
  }
  const res = await fetch(path, init);
  const data = await res.json().catch(() => null);
  if (!res.ok) throw new Error(data?.detail || data?.error || `HTTP ${res.status}`);
  return data;
}

// showToast shows a short message in the page's #toast element. type is a
// notification style: success, danger, warning or info. A newer message
// replaces an older one and gets its own full three seconds.
function showToast(message, type = 'info') {
  const t = document.getElementById('toast');
  if (!t) return;
  t.textContent = message;
  t.className = `notification toast is-${type}`;
  clearTimeout(t.hideTimer);
  t.hideTimer = setTimeout(() => t.classList.add('is-hidden'), 3000);
}
