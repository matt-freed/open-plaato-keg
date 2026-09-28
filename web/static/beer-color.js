// Drink colours, shared by the tap list, the tap editor and the beverage
// library.
//
// A drink's colour is either an SRM, for beer and anything else the SRM scale
// describes (cider, kombucha, coffee), or one of a few named presets for
// drinks it cannot, such as sparkling water. The server stores at most one.
(function () {
  'use strict';

  // The widely used SRM 1-40 reference palette. Values between whole numbers
  // are interpolated; anything outside the table is clamped to its ends, since
  // beer darker than 40 SRM already looks black.
  const SRM_HEX = [
    '#FFE699', '#FFD878', '#FFCA5A', '#FFBF42', '#FBB123', '#F8A600', '#F39C00', '#EA8F00',
    '#E58500', '#DE7C00', '#D77200', '#CF6900', '#CB6200', '#C35900', '#BB5100', '#B54C00',
    '#B04500', '#A63E00', '#A13700', '#9B3200', '#952D00', '#8E2900', '#882300', '#821E00',
    '#7B1A00', '#771900', '#701400', '#6A0E00', '#660D00', '#5E0B00', '#5A0A02', '#600903',
    '#520907', '#4C0505', '#470606', '#440607', '#3F0708', '#3B0607', '#3A070B', '#36080A',
  ];

  // The keys must match store.ColorPresets. "clear" is drawn as a faint tint
  // rather than a solid fill; see isClear.
  const PRESETS = [
    { key: 'clear',  label: 'Clear',  hex: '#dcecf2' },
    { key: 'pink',   label: 'Pink',   hex: '#f29bb0' },
    { key: 'red',    label: 'Red',    hex: '#a3192b' },
    { key: 'purple', label: 'Purple', hex: '#6c2a73' },
    { key: 'green',  label: 'Green',  hex: '#7fb843' },
    { key: 'blue',   label: 'Blue',   hex: '#3f86d6' },
  ];
  const PRESET_BY_KEY = Object.fromEntries(PRESETS.map(p => [p.key, p]));

  const DEFAULT_COLOR = '#c9a849';

  const rgb = hex => [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16));
  const hex2 = n => Math.round(n).toString(16).padStart(2, '0');

  // srmToHex returns a #rrggbb colour, or null when srm is not a number.
  function srmToHex(srm) {
    const v = typeof srm === 'string' && srm.trim() !== '' ? Number(srm) : srm;
    if (typeof v !== 'number' || !Number.isFinite(v)) return null;
    const x = Math.min(Math.max(v, 1), SRM_HEX.length);
    const lo = Math.floor(x), hi = Math.ceil(x), f = x - lo;
    const a = rgb(SRM_HEX[lo - 1]), b = rgb(SRM_HEX[hi - 1]);
    return '#' + a.map((c, i) => hex2(c + (b[i] - c) * f)).join('');
  }

  // beerColor is the colour a tap or beverage is drawn in: its preset or SRM,
  // otherwise the colour picked by hand before either existed.
  function beerColor(drink) {
    return PRESET_BY_KEY[drink.color_preset]?.hex || srmToHex(drink.srm) || drink.color || DEFAULT_COLOR;
  }

  // isClear reports whether a drink should be drawn see-through.
  function isClear(drink) {
    return drink.color_preset === 'clear';
  }

  const SCALE_MAX = 40;
  const scalePos = v => (Math.min(Math.max(v, 1), SCALE_MAX) - 1) / (SCALE_MAX - 1) * 100;
  let pickerSeq = 0;

  // colorPicker fills root with the colour control: a switch between SRM and
  // the named presets, the SRM input with its swatch and clickable scale, and
  // the preset buttons. Only the active mode's value is reported, so switching
  // modes clears the other.
  function colorPicker(root) {
    const id = 'bc' + (++pickerSeq);
    root.classList.add('beer-color');
    root.innerHTML = `
      <div class="bc-modes" role="radiogroup" aria-label="Colour type">
        <button type="button" class="bc-mode" data-mode="srm" role="radio">SRM</button>
        <button type="button" class="bc-mode" data-mode="preset" role="radio">Other</button>
      </div>
      <div class="bc-panel" data-panel="srm">
        <div class="srm-row">
          <div class="control">
            <input class="input" type="number" id="${id}-srm" step="0.5" min="0" max="80"
              placeholder="e.g. 6" style="max-width:120px;" aria-label="SRM" />
          </div>
          <div class="srm-swatch"></div>
        </div>
        <div class="srm-scale" title="Click to pick an SRM"><div class="srm-marker"></div></div>
        <p class="help">Pilsner 2–4 · pale ale 5–10 · amber 11–18 · brown 19–25 · stout 30+.
          Also suits cider, kombucha and coffee.</p>
      </div>
      <div class="bc-panel" data-panel="preset">
        <div class="bc-presets">
          ${PRESETS.map(p => `
            <button type="button" class="bc-preset" data-key="${p.key}" aria-pressed="false">
              <span class="bc-dot${p.key === 'clear' ? ' is-clear' : ''}" style="--c:${p.hex}"></span>${p.label}
            </button>`).join('')}
        </div>
        <p class="help">For drinks the SRM scale cannot describe, such as sparkling water or rosé.</p>
      </div>`;

    const input = root.querySelector('input');
    const swatch = root.querySelector('.srm-swatch');
    const scale = root.querySelector('.srm-scale');
    const marker = root.querySelector('.srm-marker');
    let mode = 'srm';
    let preset = '';

    const stops = [];
    for (let v = 1; v <= SCALE_MAX; v++) stops.push(`${srmToHex(v)} ${scalePos(v).toFixed(1)}%`);
    scale.style.background = `linear-gradient(to right, ${stops.join(', ')})`;

    function render() {
      root.querySelectorAll('.bc-mode').forEach(b =>
        b.setAttribute('aria-checked', String(b.dataset.mode === mode)));
      root.querySelectorAll('.bc-panel').forEach(p => { p.hidden = p.dataset.panel !== mode; });
      root.querySelectorAll('.bc-preset').forEach(b =>
        b.setAttribute('aria-pressed', String(b.dataset.key === preset)));

      const hex = srmToHex(input.value);
      swatch.style.background = hex || '';
      swatch.title = hex ? `SRM ${input.value}` : 'No SRM set';
      marker.style.display = hex ? '' : 'none';
      if (hex) marker.style.left = scalePos(Number(input.value)) + '%';
    }

    root.querySelectorAll('.bc-mode').forEach(b => b.addEventListener('click', () => {
      mode = b.dataset.mode;
      render();
    }));
    root.querySelectorAll('.bc-preset').forEach(b => b.addEventListener('click', () => {
      preset = preset === b.dataset.key ? '' : b.dataset.key;
      render();
    }));
    scale.addEventListener('click', e => {
      const r = scale.getBoundingClientRect();
      const f = Math.min(Math.max((e.clientX - r.left) / r.width, 0), 1);
      input.value = Math.round((1 + f * (SCALE_MAX - 1)) * 2) / 2;
      render();
    });
    input.addEventListener('input', render);

    // set shows a drink's colour, opening whichever mode it uses.
    function set(drink) {
      preset = PRESET_BY_KEY[drink.color_preset] ? drink.color_preset : '';
      input.value = drink.srm ?? '';
      mode = preset ? 'preset' : 'srm';
      render();
    }

    // value returns the fields to save.
    function value() {
      return mode === 'preset'
        ? { srm: '', color_preset: preset }
        : { srm: input.value.trim(), color_preset: '' };
    }

    set({});
    return { set, value };
  }

  // hasColor reports whether a drink has an SRM or preset of its own, as
  // opposed to only the legacy hand-picked colour.
  function hasColor(drink) {
    return drink.srm != null || Boolean(PRESET_BY_KEY[drink.color_preset]);
  }

  window.srmToHex = srmToHex;
  window.beerColor = beerColor;
  window.isClear = isClear;
  window.hasColor = hasColor;
  window.colorPicker = colorPicker;
})();
