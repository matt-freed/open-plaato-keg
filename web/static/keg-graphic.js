'use strict';

// The keg graphic shared by the tap list and the Kegs page.
//
// A side view of a 5 gallon corny keg: rubber top with two hand holes,
// a steel body and a rubber foot ring. The beer is a rectangle clipped to
// the body and slid down as the keg empties. The body is left plain, since
// any horizontal line on it would read as a level mark.
const BODY_TOP = 50, BODY_BOTTOM = 198, BODY_H = BODY_BOTTOM - BODY_TOP;
let kegSeq = 0;

function kegSvg() {
  const n = ++kegSeq;
  return `
<svg viewBox="0 0 120 214" aria-hidden="true">
  <defs>
    <clipPath id="kb${n}"><rect x="19" y="${BODY_TOP}" width="82" height="${BODY_H}" rx="9"/></clipPath>
    <linearGradient id="ks${n}" x1="0" x2="1">
      <stop offset="0"    stop-color="#fff" stop-opacity="0"/>
      <stop offset="0.18" stop-color="#fff" stop-opacity="0.18"/>
      <stop offset="0.3"  stop-color="#fff" stop-opacity="0.03"/>
      <stop offset="0.8"  stop-color="#000" stop-opacity="0"/>
      <stop offset="1"    stop-color="#000" stop-opacity="0.35"/>
    </linearGradient>
    <linearGradient id="kl${n}" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" style="stop-color: var(--accent)" stop-opacity="0.95"/>
      <stop offset="1" style="stop-color: var(--accent)" stop-opacity="0.72"/>
    </linearGradient>
  </defs>

  <!-- steel shell with rounded shoulders -->
  <rect x="16" y="${BODY_TOP - 8}" width="88" height="${BODY_H + 10}" rx="13" fill="#434957"/>
  <!-- The empty headspace is a mid grey rather than near-black, so a black
       stout still reads as liquid against it. -->
  <rect x="19" y="${BODY_TOP}" width="82" height="${BODY_H}" rx="9" fill="#2a2e38"/>

  <g clip-path="url(#kb${n})">
    <g class="liquid" style="transform: translateY(${BODY_H}px)">
      <rect class="liquid-fill" x="19" y="${BODY_TOP}" width="82" height="${BODY_H}" fill="url(#kl${n})"/>
      <rect class="liquid-surface" x="19" y="${BODY_TOP}" width="82" height="3" fill="#fff" fill-opacity="0.55"/>
    </g>
    <rect x="19" y="${BODY_TOP}" width="82" height="${BODY_H}" fill="url(#ks${n})"/>
  </g>

  <!-- posts between the handles -->
  <rect x="55" y="22" width="10" height="16" rx="2" fill="#5a6070"/>
  <rect x="53" y="18" width="14" height="6" rx="2" fill="#6b7282"/>

  <!-- rubber top ring: two hand grips with a gap for the lid -->
  <path fill="#2b2e37" fill-rule="evenodd" d="
    M20 50 V16 Q20 6 30 6 H44 Q52 6 52 14 V30 H68 V14 Q68 6 76 6 H90 Q100 6 100 16 V50 Z
    M31 13 H42 Q46 13 46 17 V21 Q46 25 42 25 H31 Q26 25 26 21 V17 Q26 13 31 13 Z
    M78 13 H89 Q94 13 94 17 V21 Q94 25 89 25 H78 Q74 25 74 21 V17 Q74 13 78 13 Z"/>

  <!-- rubber foot -->
  <rect x="13" y="${BODY_BOTTOM - 2}" width="94" height="16" rx="6" fill="#2b2e37"/>
</svg>`;
}

// kegLevelTransform is the transform that slides a graphic's .liquid group
// down to show pct (0–100) full.
function kegLevelTransform(pct) {
  return `translateY(${BODY_H * (1 - (pct ?? 0) / 100)}px)`;
}
