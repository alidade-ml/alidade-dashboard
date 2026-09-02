import { DEFAULT_PRESET, PRESETS, type PresetId } from "./palette-recipe.ts";

export const PALETTE_STORAGE_KEY = "alidade-palette";

/** Where build-favicon.py writes one file per preset. */
export const faviconHref = (id: PresetId) => `/favicons/${id}.svg`;

/**
 * localStorage is user-writable and outlives releases, so a stored value may be
 * absent, empty, garbage, or the name of a preset that has since been renamed.
 * All four resolve to the default rather than throwing or stamping something the
 * stylesheet has no block for — which would silently render the bare :root.
 */
export function resolvePalette(stored: string | null | undefined): PresetId {
  return PRESETS.some((p) => p.id === stored) ? (stored as PresetId) : DEFAULT_PRESET;
}

/**
 * What a `storage` event from another tab should change the palette to, or null
 * to ignore it.
 *
 * Split out of the provider so the decision is testable without a DOM: the
 * provider keeps only the listener wiring, which has nothing to get wrong.
 */
export function paletteFromStorageEvent(
  key: string | null,
  newValue: string | null,
): PresetId | null {
  return key === PALETTE_STORAGE_KEY ? resolvePalette(newValue) : null;
}
