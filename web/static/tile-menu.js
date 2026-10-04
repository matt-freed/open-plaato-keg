'use strict';

// The right-click menu on tap list and Kegs page tiles, linking to the keg's
// history and to the tap's and keg's settings. Nothing on the tile shows it is
// there: it is a shortcut for whoever runs the screens, not part of the
// display. Shift+right-click still opens the browser's own menu.

let tileMenu = null;

// tileMenuWanted says whether a contextmenu event should open the tile menu,
// returning the tile it was on. A press and hold that has started a drag has
// already cancelled the event.
function tileMenuWanted(e) {
  if (e.defaultPrevented || e.shiftKey) return null;
  return e.target.closest('.tile');
}

// showTileMenu opens the menu at the pointer of a contextmenu event. Either id
// may be null, which disables the items that need it.
function showTileMenu(e, { tapId, kegId }) {
  e.preventDefault();
  closeTileMenu();

  const enc = encodeURIComponent;
  const items = [
    ['View history', kegId && `/history.html#keg=${enc(kegId)}`],
    ['Edit tap', tapId && `/taplist-setup.html#tap=${enc(tapId)}`],
    ['Edit keg', kegId && `/keg-setup.html#keg=${enc(kegId)}`],
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

  // Opened at the pointer, but kept inside the window.
  const pad = 8;
  const x = Math.min(e.clientX, innerWidth - menu.offsetWidth - pad);
  const y = Math.min(e.clientY, innerHeight - menu.offsetHeight - pad);
  menu.style.left = `${Math.max(pad, x)}px`;
  menu.style.top = `${Math.max(pad, y)}px`;
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
