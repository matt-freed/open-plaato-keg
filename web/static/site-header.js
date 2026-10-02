'use strict';

// <site-header heading="Kegs"></site-header> renders the header bar every
// page shares: the page title, then the navigation with the Configure menu.
// Load this script in <head>, without defer, so the element is defined before
// the parser reaches it and the bar never renders empty.
//
// The span beside the title shows supporting text, such as the tap list's
// beer count; set it through the element's `count` property.
(() => {
  const LINKS = [
    ['/taplist.html', 'Tap List'],
    ['/index.html', 'Kegs'],
    ['/history.html', 'History'],
  ];
  const CONFIGURE = [
    ['/taplist-setup.html', 'Tap Setup'],
    ['/setup.html', 'Scale Setup'],
    ['/dashboard-setup.html', 'Dashboard Setup'],
  ];

  function link([href, label]) {
    const a = document.createElement('a');
    a.href = href;
    a.textContent = label;
    if (location.pathname === href) a.setAttribute('aria-current', 'page');
    return a;
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
        <nav class="site-nav">
          <div class="site-menu">
            <button type="button" aria-haspopup="true" aria-expanded="false">Configure ▾</button>
            <div class="site-menu-list"><p class="site-version"></p></div>
          </div>
        </nav>`;
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

      this.replaceChildren(this.bar);

      fetch('/api/alive')
        .then(r => r.json())
        .then(d => {
          if (d.version) this.bar.querySelector('.site-version').textContent = 'v' + d.version;
        })
        .catch(() => {});
    }

    set count(text) {
      this.bar.querySelector('.site-header-count').textContent = text || '';
    }
  }

  customElements.define('site-header', SiteHeader);
})();
