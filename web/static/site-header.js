'use strict';

// <site-header heading="Keg Scales"></site-header> renders the header bar every
// page shares: the page title, then the navigation with the Configure menu.
// Load this script in <head>, without defer, so the element is defined before
// the parser reaches it and the bar never renders empty.
//
// The span beside the title shows supporting text, such as the tap list's
// beer count; set it through the element's `count` property.
//
// A `fullscreen` attribute adds a button after the navigation that puts the
// whole page into the browser's full screen and takes it out again. Browsers
// only allow that from a click, so a page cannot enter full screen by itself,
// and it ends when the page reloads. Where the browser has no full screen for
// pages, such as Safari on an iPhone, the button is left out.
//
// On a page whose <html> carries the `kiosk` class, site-header.css hides the
// navigation and leaves the title, the count and the full-screen button.
(() => {
  const LINKS = [
    ['/taplist.html', 'Tap List'],
    ['/kegs.html', 'Keg Scales'],
    // All Pours and Edit history are reached from History, so they keep it marked.
    ['/history.html', 'History', ['/pours.html', '/history-edit.html']],
  ];
  const CONFIGURE = [
    ['/taplist-setup.html', 'Tap Setup'],
    ['/keg-setup.html', 'Keg Scale Setup'],
    ['/dashboard-setup.html', 'Dashboard Setup'],
    ['/system.html', 'System'],
  ];

  function link([href, label, also = []]) {
    const a = document.createElement('a');
    a.href = href;
    a.textContent = label;
    if (location.pathname === href || also.includes(location.pathname)) a.setAttribute('aria-current', 'page');
    return a;
  }

  // Expand and collapse arrows, drawn in the text colour.
  const ENTER_ICON = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/></svg>';
  const EXIT_ICON = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 4v5H4M15 4v5h5M9 20v-5H4M15 20v-5h5"/></svg>';

  // Older Safari, on iPads among others, only has the webkit-prefixed names.
  const fullscreenElement = () => document.fullscreenElement || document.webkitFullscreenElement;

  // fullscreenButton returns the full-screen toggle, or null where the
  // browser cannot show a page full screen.
  function fullscreenButton() {
    if (!document.fullscreenEnabled && !document.webkitFullscreenEnabled) return null;

    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'site-fullscreen';

    // Follows the browser's state rather than the clicks, so leaving with
    // Esc or a swipe also turns the icon back.
    const sync = () => {
      const on = Boolean(fullscreenElement());
      button.innerHTML = on ? EXIT_ICON : ENTER_ICON;
      button.title = on ? 'Exit full screen' : 'Full screen';
      button.setAttribute('aria-label', button.title);
    };
    document.addEventListener('fullscreenchange', sync);
    document.addEventListener('webkitfullscreenchange', sync);
    sync();

    button.addEventListener('click', () => {
      const el = document.documentElement;
      const call = fullscreenElement()
        ? (document.exitFullscreen || document.webkitExitFullscreen).call(document)
        : (el.requestFullscreen || el.webkitRequestFullscreen).call(el);
      // The prefixed calls return nothing; the standard ones reject when the
      // browser refuses, which leaves the page as it was.
      Promise.resolve(call).catch(() => {});
    });
    return button;
  }

  class SiteHeader extends HTMLElement {
    connectedCallback() {
      if (this.bar) return;

      this.bar = document.createElement('header');
      this.bar.className = 'site-header';
      this.bar.innerHTML = `
        <div class="site-header-title">
          <h1></h1>
          <span class="site-header-count"></span>
        </div>
        <div class="site-header-actions">
          <nav class="site-nav">
            <div class="site-menu">
              <button type="button" aria-haspopup="true" aria-expanded="false">Configure ▾</button>
              <div class="site-menu-list"></div>
            </div>
          </nav>
        </div>`;
      this.bar.querySelector('h1').textContent = this.getAttribute('heading') || document.title;

      const nav = this.bar.querySelector('.site-nav');
      const menu = this.bar.querySelector('.site-menu');
      nav.prepend(...LINKS.map(link));
      const items = CONFIGURE.map(link);
      menu.querySelector('.site-menu-list').prepend(...items);

      const toggle = menu.querySelector('button');
      toggle.classList.toggle('is-current', items.some(a => a.hasAttribute('aria-current')));
      toggle.addEventListener('click', e => {
        e.stopPropagation();
        toggle.setAttribute('aria-expanded', String(menu.classList.toggle('is-open')));
      });
      document.addEventListener('click', () => {
        menu.classList.remove('is-open');
        toggle.setAttribute('aria-expanded', 'false');
      });

      if (this.hasAttribute('fullscreen')) {
        const button = fullscreenButton();
        if (button) this.bar.querySelector('.site-header-actions').append(button);
      }

      this.replaceChildren(this.bar);
    }

    set count(text) {
      this.bar.querySelector('.site-header-count').textContent = text || '';
    }
  }

  customElements.define('site-header', SiteHeader);
})();
