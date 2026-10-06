'use strict';

// The menu on tap list and Kegs page tiles, linking to the keg's history and
// to the tap's and keg's settings. tile-gestures.js opens it on a right-click
// or a touch press and hold. Nothing on the tile shows it is there: it is a
// shortcut for whoever runs the screens, not part of the display.

let tileMenu = null;

// showTileMenu opens the menu at a point in the window. Either id may be
// null, which disables the items that need it. A menu opened by touch sits
// clear of the finger, above it when there is room.
function showTileMenu(x, y, { tapId, kegId }, { touch = false } = {}) {
  closeTileMenu();

  const enc = encodeURIComponent;
  // The setup pages link back to the screen the menu was opened on.
  const from = { '/taplist.html': '?from=taplist', '/kegs.html': '?from=kegs' }[location.pathname] ?? '';
  const items = [
    ['View history', kegId && `/history.html#keg=${enc(kegId)}`],
    ['Edit tap', tapId && `/taplist-setup.html${from}#tap=${enc(tapId)}`],
    ['Edit keg scale', kegId && `/keg-setup.html${from}#keg=${enc(kegId)}`],
  ];

  const menu = document.createElement('div');
  menu.className = 'tile-menu';
  menu.setAttribute('role', 'menu');
  for (const [label, href] of items) {
    // Real links, so a middle or Cmd-click opens the page in a new tab.
    const item = document.createElement(href ? 'a' : 'span');
    item.className = 'tile-menu-item';
    item.setAttribute('role', 'menuitem');
    item.textContent = label;
    if (href) item.href = href;
    else item.setAttribute('aria-disabled', 'true');
    menu.append(item);
  }
  document.body.append(menu);

  // Kept inside the window.
  const pad = 8, w = menu.offsetWidth, h = menu.offsetHeight;
  let left = x, top = y;
  if (touch) {
    left = x - w / 2;
    top = y - h - 24;
    if (top < pad) top = y + 32;
  }
  menu.style.left = `${Math.max(pad, Math.min(left, innerWidth - w - pad))}px`;
  menu.style.top = `${Math.max(pad, Math.min(top, innerHeight - h - pad))}px`;
  tileMenu = menu;
}

function closeTileMenu() {
  tileMenu?.remove();
  tileMenu = null;
}

// Any press outside the menu closes it, including the right-click that opens
// the next one. A choice closes it too, so it is gone if Back returns here.
window.addEventListener('pointerdown', e => {
  if (tileMenu && !tileMenu.contains(e.target)) closeTileMenu();
}, true);
window.addEventListener('click', e => {
  if (tileMenu?.contains(e.target) && e.target.closest('a')) closeTileMenu();
});
window.addEventListener('keydown', e => {
  if (!tileMenu) return;
  if (e.key === 'Escape') { closeTileMenu(); return; }
  if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
  e.preventDefault();
  const links = [...tileMenu.querySelectorAll('a')];
  if (!links.length) return;
  const i = links.indexOf(document.activeElement);
  const step = e.key === 'ArrowDown' ? 1 : -1;
  links[i < 0 ? (step > 0 ? 0 : links.length - 1) : (i + step + links.length) % links.length].focus();
});
window.addEventListener('scroll', closeTileMenu, true);
window.addEventListener('resize', closeTileMenu);
window.addEventListener('blur', closeTileMenu);
window.addEventListener('pagehide', closeTileMenu);
