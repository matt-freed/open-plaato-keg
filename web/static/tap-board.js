'use strict';

// Pointer handling for the Tap Setup board: dragging a tap card from one slot
// to another, where a slot is a scale's place on the board or the tray of
// taps on no scale.
//
// Pointer events rather than HTML5 drag and drop, which does not work on touch
// screens, as in tile-gestures.js. A mouse drags as soon as it moves. A finger
// presses and holds, then moves to drag; moving before the hold scrolls the
// page. A card that is pressed and released without dragging is still a link,
// so it opens the tap's editor.

const BOARD_DRAG_THRESHOLD = 6;
const BOARD_LONG_PRESS_MS = 350;

// attachTapBoard wires up the cards inside root. Cards are .tap-card elements
// with a data-tap-id; slots are elements with a data-slot, holding the scale's
// keg id or "" for the tray.
//   onDrop(tapId, kegId) after a card is dropped on a slot other than its
//                        own; kegId is null for the tray
// It returns an object whose active property is true while a drag runs, when
// the page must not rebuild the board under the pointer.
function attachTapBoard({ root, onDrop }) {
  let press = null;
  let drag = null;
  const state = { get active() { return !!drag; } };

  root.addEventListener('pointerdown', e => {
    if (e.button !== 0 || drag) return;
    const card = e.target.closest('.tap-card');
    if (!card) return;
    press = { card, id: e.pointerId, type: e.pointerType, x0: e.clientX, y0: e.clientY, x: e.clientX, y: e.clientY };
    if (e.pointerType === 'touch') press.timer = setTimeout(hold, BOARD_LONG_PRESS_MS, press);
  });

  // hold readies a finger's press to drag. Distance is measured afresh from
  // where the finger now rests.
  function hold(p) {
    if (p !== press) return;
    p.held = true;
    p.x0 = p.x;
    p.y0 = p.y;
    navigator.vibrate?.(10);
    p.card.classList.add('is-held');
  }

  window.addEventListener('pointermove', e => {
    if (drag) {
      if (e.pointerId === drag.id) moveDrag(e.clientX, e.clientY);
      return;
    }
    if (!press || e.pointerId !== press.id) return;
    press.x = e.clientX;
    press.y = e.clientY;
    if (Math.hypot(press.x - press.x0, press.y - press.y0) < BOARD_DRAG_THRESHOLD) return;
    if (press.type === 'touch' && !press.held) { clearPress(); return; } // the user is scrolling
    startDrag(press);
  });

  window.addEventListener('pointerup', e => {
    if (drag && e.pointerId === drag.id) endDrag(true);
    clearPress();
  });
  window.addEventListener('pointercancel', e => {
    if (drag && e.pointerId === drag.id) endDrag(false);
    clearPress();
  });
  window.addEventListener('keydown', e => {
    if (e.key === 'Escape' && drag) endDrag(false);
  });
  // Once a finger has held, stop the browser scrolling with it.
  document.addEventListener('touchmove', e => {
    if (drag || press?.held) e.preventDefault();
  }, { passive: false });
  // The cards are links, which the browser would otherwise drag itself, and
  // a held finger would otherwise open the browser's link menu.
  root.addEventListener('dragstart', e => {
    if (e.target.closest?.('.tap-card')) e.preventDefault();
  });
  root.addEventListener('contextmenu', e => {
    if (press?.type === 'touch' || drag) e.preventDefault();
  });

  function clearPress() {
    if (press?.timer) clearTimeout(press.timer);
    press?.card.classList.remove('is-held');
    press = null;
  }

  function startDrag(p) {
    const { card } = p;
    clearPress();
    const rect = card.getBoundingClientRect();

    // A copy follows the pointer while the card itself stays in its slot,
    // faded, to show where it came from.
    const ghost = card.cloneNode(true);
    ghost.classList.add('tap-card-ghost');
    ghost.removeAttribute('href');
    ghost.style.width = `${rect.width}px`;
    ghost.style.left = `${rect.left}px`;
    ghost.style.top = `${rect.top}px`;
    document.body.append(ghost);
    card.classList.add('is-dragging');
    document.body.classList.add('is-board-dragging');

    drag = {
      card, ghost, id: p.id,
      from: card.closest('[data-slot]'),
      dx: p.x - rect.left, dy: p.y - rect.top,
      over: null,
      x: p.x, y: p.y, scrollSpeed: 0, scrolling: false,
    };
    moveDrag(p.x, p.y);
  }

  function moveDrag(x, y) {
    drag.x = x;
    drag.y = y;
    drag.ghost.style.left = `${x - drag.dx}px`;
    drag.ghost.style.top = `${y - drag.dy}px`;

    // Scroll when the pointer nears the top or bottom edge, as tile-gestures.js
    // does, so a phone can reach a scale that is off screen.
    const edge = 70;
    drag.scrollSpeed = y < edge ? -(edge - y) / 4 : y > innerHeight - edge ? (y - innerHeight + edge) / 4 : 0;
    if (drag.scrollSpeed && !drag.scrolling) {
      drag.scrolling = true;
      requestAnimationFrame(autoScroll);
    }

    highlight(x, y);
  }

  function autoScroll() {
    if (!drag || !drag.scrollSpeed) { if (drag) drag.scrolling = false; return; }
    window.scrollBy(0, drag.scrollSpeed);
    // The pointer stays put while the page moves under it.
    highlight(drag.x, drag.y);
    requestAnimationFrame(autoScroll);
  }

  // highlight marks the slot under the pointer as the one a drop lands in.
  function highlight(x, y) {
    const slot = document.elementFromPoint(x, y)?.closest('[data-slot]');
    const over = slot && root.contains(slot) ? slot : null;
    if (over === drag.over) return;
    drag.over?.classList.remove('is-over');
    over?.classList.add('is-over');
    drag.over = over;
  }

  function endDrag(drop) {
    const { card, ghost, from, over } = drag;
    drag = null;
    ghost.remove();
    card.classList.remove('is-dragging');
    over?.classList.remove('is-over');
    document.body.classList.remove('is-board-dragging');

    // The click that ends a mouse drag would otherwise follow the link. It
    // arrives straight after pointerup, if at all, so the guard comes off on
    // the next task rather than waiting for a click a touch drag never sends.
    window.addEventListener('click', swallowClick, true);
    setTimeout(() => window.removeEventListener('click', swallowClick, true), 0);

    if (drop && over && over !== from) onDrop(card.dataset.tapId, over.dataset.slot || null);
  }

  function swallowClick(e) {
    e.preventDefault();
    e.stopPropagation();
  }

  return state;
}
