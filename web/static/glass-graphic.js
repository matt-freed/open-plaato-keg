'use strict';

// The beer glass drawn in place of a tap's picture, on the tap list and in
// Tap Setup and Dashboard Setup.
//
// Each glass is drawn in a 120 × 160 box, the 3:4 of the picture panel: a
// faint glass bowl holding beer up to a foam head, with a stem and foot or a
// handle where the glass has one. The beer uses the keg graphic's classes and
// the --accent colour, so the tap list's rules for clear drinks and empty
// kegs apply to it unchanged. A clear drink gets no head.

// GLASSES are the styles Tap Setup offers, in its order. Each has the inside
// of its bowl (bowl), the y of its rim (rim) and how deep its head is (foam),
// plus any solid parts outside the bowl: a stem and foot (stem) or a handle
// (handle), and any marks in the glass drawn over the beer (facets). The keys
// match store.GlassStyles.
const GLASSES = [
  { key: 'pint', label: 'Pint', rim: 34, foam: 14,
    bowl: 'M30 34 H90 L82 146 Q81 150 77 150 H43 Q39 150 38 146 Z',
    stem: '<path d="M38 146 Q39 154 44 154 H76 Q81 154 82 146 Z"/>' },
  { key: 'nonic', label: 'Nonic', rim: 18, foam: 14,
    bowl: `M35 18 H85 C85 28 89 34 89 42 C89 50 84 54 83 62 L78 146 Q77 150 73 150 H47
           Q43 150 42 146 L37 62 C36 54 31 50 31 42 C31 34 35 28 35 18 Z`,
    stem: '<path d="M42 146 Q43 154 48 154 H72 Q77 154 78 146 Z"/>' },
  // A shaker that bulges below the rim and turns in toward a slightly
  // flared lip, as in the glass made for Boston lager.
  { key: 'boston', label: 'Boston', rim: 30, foam: 13,
    bowl: `M32 30 C34 36 32 40 29 48 C25 60 27 76 33 96 L40 146 Q41 150 45 150 H75
           Q79 150 80 146 L87 96 C93 76 95 60 91 48 C88 40 86 36 88 30 Z`,
    stem: '<path d="M40 146 Q41 154 46 154 H74 Q79 154 80 146 Z"/>' },
  { key: 'tulip', label: 'Tulip', rim: 14, foam: 13,
    bowl: 'M40 14 C36 30 28 46 32 66 C36 84 48 94 60 94 C72 94 84 84 88 66 C92 46 84 30 80 14 Z',
    stem: '<rect x="56.5" y="93" width="7" height="48"/><ellipse cx="60" cy="146" rx="24" ry="6"/>' },
  { key: 'snifter', label: 'Snifter', rim: 32, foam: 11,
    bowl: 'M44 32 C28 44 24 66 30 84 C36 100 48 108 60 108 C72 108 84 100 90 84 C96 66 92 44 76 32 Z',
    stem: '<rect x="56.5" y="107" width="7" height="33"/><ellipse cx="60" cy="146" rx="26" ry="6"/>' },
  { key: 'pilsner', label: 'Pilsner', rim: 8, foam: 15,
    bowl: 'M38 8 H82 L71 136 Q70 140 66 140 H54 Q50 140 49 136 Z',
    stem: '<path d="M49 136 L46 148 Q45 154 51 154 H69 Q75 154 74 148 L71 136 Z"/>' },
  { key: 'weizen', label: 'Weizen', rim: 8, foam: 16,
    bowl: `M40 8 C36 28 44 46 49 66 C54 88 47 112 48 134 Q48 140 54 140 H66 Q72 140 72 134
           C73 112 66 88 71 66 C76 46 84 28 80 8 Z`,
    stem: '<path d="M48 136 L45 148 Q44 154 50 154 H70 Q76 154 75 148 L72 136 Z"/>' },
  { key: 'goblet', label: 'Goblet', rim: 28, foam: 11,
    bowl: 'M26 28 H94 C94 62 82 90 60 90 C38 90 26 62 26 28 Z',
    stem: '<path d="M54 89 H66 L64 138 H56 Z"/><ellipse cx="60" cy="144" rx="28" ry="7"/>' },
  { key: 'mug', label: 'Mug', rim: 22, foam: 15,
    bowl: 'M22 22 H80 V140 Q80 148 72 148 H30 Q22 148 22 140 Z',
    stem: `<path d="M20 140 V150 Q20 156 28 156 H74 Q82 156 82 150 V140 Q82 148 72 148 H30 Q20 148 20 140 Z"/>`,
    handle: 'M80 46 H92 Q104 46 104 58 V104 Q104 116 92 116 H80' },
  // The German seidel: a squat, slightly barrel-shaped mug with rows of
  // dimples below a plain band at the rim, and a heavy handle.
  { key: 'dimpled', label: 'Dimpled', rim: 34, foam: 13,
    bowl: 'M20 34 C17 64 17 112 21 140 Q22 148 31 148 H71 Q80 148 81 140 C85 112 85 64 82 34 Z',
    stem: '<path d="M19 140 V150 Q19 156 28 156 H74 Q83 156 83 150 V140 Q81 148 71 148 H31 Q21 148 19 140 Z"/>',
    handle: 'M83 54 H93 Q106 54 106 68 V104 Q106 118 93 118 H83',
    facets: '<g fill="#fff" fill-opacity="0.07" stroke="#fff" stroke-opacity="0.28" stroke-width="1.5"><ellipse cx="29" cy="60" rx="6.5" ry="8"/><ellipse cx="45" cy="60" rx="6.5" ry="8"/><ellipse cx="61" cy="60" rx="6.5" ry="8"/><ellipse cx="77" cy="60" rx="6.5" ry="8"/><ellipse cx="21" cy="79" rx="6.5" ry="8"/><ellipse cx="37" cy="79" rx="6.5" ry="8"/><ellipse cx="53" cy="79" rx="6.5" ry="8"/><ellipse cx="69" cy="79" rx="6.5" ry="8"/><ellipse cx="85" cy="79" rx="6.5" ry="8"/><ellipse cx="29" cy="98" rx="6.5" ry="8"/><ellipse cx="45" cy="98" rx="6.5" ry="8"/><ellipse cx="61" cy="98" rx="6.5" ry="8"/><ellipse cx="77" cy="98" rx="6.5" ry="8"/><ellipse cx="21" cy="117" rx="6.5" ry="8"/><ellipse cx="37" cy="117" rx="6.5" ry="8"/><ellipse cx="53" cy="117" rx="6.5" ry="8"/><ellipse cx="69" cy="117" rx="6.5" ry="8"/><ellipse cx="85" cy="117" rx="6.5" ry="8"/><ellipse cx="29" cy="136" rx="6.5" ry="8"/><ellipse cx="45" cy="136" rx="6.5" ry="8"/><ellipse cx="61" cy="136" rx="6.5" ry="8"/><ellipse cx="77" cy="136" rx="6.5" ry="8"/></g>' },
  { key: 'flute', label: 'Flute', rim: 8, foam: 10,
    bowl: 'M47 8 H73 C76 40 75 70 69 92 C67 99 64 102 60 102 C56 102 53 99 51 92 C45 70 44 40 47 8 Z',
    stem: '<rect x="58" y="101" width="4" height="42"/><ellipse cx="60" cy="147" rx="20" ry="5"/>' },
];
const GLASS_BY_KEY = Object.fromEntries(GLASSES.map(g => [g.key, g]));

let glassSeq = 0;

// glassSvg draws one glass, the pint for '' or a style it does not know.
function glassSvg(key) {
  const g = GLASS_BY_KEY[key] || GLASS_BY_KEY.pint;
  const n = ++glassSeq;
  const beerTop = g.rim + g.foam;
  return `
<svg class="glass-svg" viewBox="0 0 120 160" aria-hidden="true">
  <defs>
    <clipPath id="gb${n}"><path d="${g.bowl}"/></clipPath>
    <linearGradient id="gl${n}" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" style="stop-color: var(--accent)" stop-opacity="0.95"/>
      <stop offset="1" style="stop-color: var(--accent)" stop-opacity="0.75"/>
    </linearGradient>
    <linearGradient id="gs${n}" x1="0" x2="1">
      <stop offset="0"    stop-color="#fff" stop-opacity="0"/>
      <stop offset="0.16" stop-color="#fff" stop-opacity="0.28"/>
      <stop offset="0.28" stop-color="#fff" stop-opacity="0.04"/>
      <stop offset="0.82" stop-color="#000" stop-opacity="0"/>
      <stop offset="1"    stop-color="#000" stop-opacity="0.3"/>
    </linearGradient>
  </defs>
  ${g.handle ? `<path d="${g.handle}" fill="none" stroke="#c9d1de" stroke-opacity="0.45" stroke-width="9"/>` : ''}
  <g fill="#c9d1de" fill-opacity="0.45">${g.stem || ''}</g>
  <path d="${g.bowl}" fill="#fff" fill-opacity="0.07"/>
  <g clip-path="url(#gb${n})">
    <rect class="liquid-fill" x="0" y="${beerTop}" width="120" height="${160 - beerTop}" fill="url(#gl${n})"/>
    <rect class="liquid-surface" x="0" y="${beerTop}" width="120" height="2" fill="#fff" fill-opacity="0.35"/>
    <rect class="glass-foam" x="0" y="${g.rim - 4}" width="120" height="${g.foam + 4}" fill="#f4ede0"/>
    ${g.facets || ''}
    <path d="${g.bowl}" fill="url(#gs${n})"/>
  </g>
  <path d="${g.bowl}" fill="none" stroke="#dfe5ee" stroke-opacity="0.6" stroke-width="2.5"/>
</svg>`;
}
