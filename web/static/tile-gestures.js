'use strict';

// Pointer handling shared by the tap list and the Kegs page: dragging tiles to
// reorder them, and opening the tile menu from tile-menu.js.
//
// Pointer events rather than HTML5 drag and drop, which does not work on touch
// screens. A mouse drags as soon as it moves and opens the menu with a
// right-click; Shift+right-click still gives the browser's own menu. A finger
// presses and holds to open the menu, then moves to drag, as on a phone's home
// screen. Moving before the hold scrolls the page.

const DRAG_THRESHOLD = 6;
const LONG_PRESS_MS = 350;

// attachTileGestures wires up the tiles inside root. Only tiles in grid can be
// dragged, and only within it; every tile in root has the menu.
//   idOf(tile)     the tile's id, as the saved order holds it
//   menuFor(tile)  the { tapId, kegId } its menu links to, or null for none
//   onReorder(ids) the grid's ids after a drop that changed their order
//   onEnd()        after every drag, for renders held back while it ran
// It returns an object whose active property is true while a drag runs, when
// the pages must not rebuild the grid under the pointer.
function attachTileGestures({ root, grid, idOf, menuFor, onReorder, onEnd }) {
  let press = null;
  let drag = null;

  root.addEventListener('pointerdown', e => {
    if (e.button !== 0 || drag) return;
    const tile = e.target.closest('.tile');
    if (!tile) return;
    const draggable = tile.parentElement === grid;
    // A mouse can only drag, so it has nothing to do on any other tile.
    if (e.pointerType !== 'touch' && !draggable) return;
    press = {
      tile, draggable, id: e.pointerId, type: e.pointerType,
      x0: e.clientX, y0: e.clientY, x: e.clientX, y: e.clientY,
    };
    if (e.pointerType === 'touch') press.timer = setTimeout(hold, LONG_PRESS_MS, press);
  });

  // hold opens the menu once a finger has stayed put long enough. The press
  // stays live, so moving from here drags; distance is measured afresh from
  // where the finger now rests.
  function hold(p) {
    if (p !== press) return;
    p.held = true;
    p.x0 = p.x;
    p.y0 = p.y;
    navigator.vibrate?.(10);
    const ids = menuFor(p.tile);
    if (ids) showTileMenu(p.x, p.y, ids, { touch: true });
  }

  window.addEventListener('pointermove', e => {
    if (drag) {
      if (e.pointerId === drag.id) moveDrag(e.clientX, e.clientY);
      return;
    }
    if (!press || e.pointerId !== press.id) return;
    press.x = e.clientX;
    press.y = e.clientY;
    if (Math.hypot(press.x - press.x0, press.y - press.y0) < DRAG_THRESHOLD) return;
    if (press.type === 'touch' && !press.held) { clearPress(); return; } // the user is scrolling
    // A held tile that cannot be dragged keeps its menu open.
    if (!press.draggable) return;
    closeTileMenu();
    startDrag(press);
  });

  // Lifting a finger after the hold leaves the menu open to choose from.
  window.addEventListener('pointerup', e => {
    if (drag && e.pointerId === drag.id) endDrag(true);
    clearPress();
  });
  window.addEventListener('pointercancel', e => {
    if (drag && e.pointerId === drag.id) endDrag(true);
    clearPress();
  });
  window.addEventListener('keydown', e => {
    if (e.key === 'Escape' && drag) endDrag(false);
  });
  // Once a finger has held, stop the browser scrolling with it, so it can
  // drag or rest on the menu.
  document.addEventListener('touchmove', e => {
    if (drag || press?.held) e.preventDefault();
  }, { passive: false });

  root.addEventListener('contextmenu', e => {
    // A touch press and hold opens the menu itself, and the browser's own
    // event for it arrives later, mid-press.
    if (press || drag) { e.preventDefault(); return; }
    if (e.shiftKey) return;
    const tile = e.target.closest('.tile');
    const ids = tile && menuFor(tile);
    if (!ids) return;
    e.preventDefault();
    showTileMenu(e.clientX, e.clientY, ids);
  });

  function clearPress() {
    if (press?.timer) clearTimeout(press.timer);
    press = null;
  }

  function startDrag(p) {
    const { tile } = p;
    clearPress();
    const rect = tile.getBoundingClientRect();

    const placeholder = document.createElement('div');
    placeholder.className = 'tile-placeholder';
    tile.before(placeholder);

    drag = {
      tile, placeholder, id: p.id,
      x0: p.x0, y0: p.y0,
      startTiles: [...grid.children].filter(el => el !== placeholder),
      // The page's own inline style, such as the beer colour, to put back.
      style: tile.style.cssText,
      lastX: p.x, lastY: p.y,
      scrollSpeed: 0,
    };

    tile.getAnimations().forEach(a => a.cancel());
    tile.classList.add('dragging');
    document.body.classList.add('is-dragging');
    Object.assign(tile.style, {
      position: 'fixed',
      left: rect.left + 'px',
      top: rect.top + 'px',
      width: rect.width + 'px',
      height: rect.height + 'px',
    });
    moveDrag(p.x, p.y);
  }

  function moveDrag(x, y) {
    drag.lastX = x;
    drag.lastY = y;
    drag.tile.style.transform =
      `translate(${x - drag.x0}px, ${y - drag.y0}px) rotate(1.2deg) scale(1.03)`;

    // Scroll when the pointer nears the top or bottom edge.
    const edge = 70;
    drag.scrollSpeed = y < edge ? -(edge - y) / 4 : y > innerHeight - edge ? (y - innerHeight + edge) / 4 : 0;
    if (drag.scrollSpeed && !drag.scrolling) {
      drag.scrolling = true;
      requestAnimationFrame(autoScroll);
    }

    reposition(x, y);
  }

  function autoScroll() {
    if (!drag || !drag.scrollSpeed) { if (drag) drag.scrolling = false; return; }
    window.scrollBy(0, drag.scrollSpeed);
    reposition(drag.lastX, drag.lastY);
    requestAnimationFrame(autoScroll);
  }

  // reposition moves the placeholder to the slot under the pointer. It hit
  // tests against layout boxes rather than on-screen ones, so tiles that are
  // still sliding into place cannot be hit twice and make the order flicker.
  // Only the grid's tiles are tested, so nothing can be dropped outside it.
  function reposition(x, y) {
    const g = grid.getBoundingClientRect();
    const tiles = [...grid.querySelectorAll('.tile:not(.dragging)')];
    const over = tiles.find(t => {
      const left = g.left + t.offsetLeft, top = g.top + t.offsetTop;
      return x >= left && x < left + t.offsetWidth && y >= top && y < top + t.offsetHeight;
    });
    if (!over) return;

    const items = [...grid.children];
    const from = items.indexOf(drag.placeholder);
    const to = items.indexOf(over);
    animateLayout(() => {
      if (to > from) over.after(drag.placeholder);
      else over.before(drag.placeholder);
    });
  }

  // animateLayout runs a DOM change and slides every tile from where it was
  // to where it ended up.
  function animateLayout(change) {
    const tiles = [...grid.querySelectorAll('.tile:not(.dragging)')];
    const before = new Map(tiles.map(t => [t, t.getBoundingClientRect()]));
    change();
    for (const t of tiles) {
      t.getAnimations().forEach(a => a.cancel());
      const a = before.get(t), b = t.getBoundingClientRect();
      const dx = a.left - b.left, dy = a.top - b.top;
      if (dx || dy) {
        t.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }],
          { duration: 220, easing: 'cubic-bezier(0.2, 0.8, 0.2, 1)' });
      }
    }
  }

  function endDrag(commit) {
    const { tile, placeholder, startTiles } = drag;

    if (!commit) {
      // Put the placeholder back where the tile started.
      animateLayout(() => {
        const rest = startTiles.filter(t => t !== tile);
        grid.insertBefore(placeholder, rest[startTiles.indexOf(tile)] ?? null);
      });
    }

    // Settle the lifted tile into its slot.
    const from = tile.getBoundingClientRect();
    placeholder.replaceWith(tile);
    tile.classList.remove('dragging');
    document.body.classList.remove('is-dragging');
    tile.style.cssText = drag.style;
    const to = tile.getBoundingClientRect();
    tile.animate(
      [{ transform: `translate(${from.left - to.left}px, ${from.top - to.top}px) rotate(1.2deg) scale(1.03)` },
       { transform: 'none' }],
      { duration: 240, easing: 'cubic-bezier(0.2, 0.8, 0.2, 1)' });

    drag = null;

    const tiles = [...grid.children];
    if (commit && tiles.some((t, i) => t !== startTiles[i])) onReorder(tiles.map(idOf));
    onEnd?.();
  }

  return { get active() { return drag !== null; } };
}
