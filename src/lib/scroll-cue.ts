/** Which ends of a horizontal strip are hiding content. */
export interface ScrollCue {
  before: boolean;
  after: boolean;
}

/**
 * Read from an element's own scroll geometry. A pixel of slack absorbs the
 * sub-pixel rounding that leaves `scrollWidth` one past `clientWidth` on a
 * strip that fits.
 */
export function scrollCue(scrollLeft: number, scrollWidth: number, clientWidth: number): ScrollCue {
  const hidden = scrollWidth - clientWidth;
  if (hidden <= 1) return { before: false, after: false };
  return { before: scrollLeft > 1, after: scrollLeft < hidden - 1 };
}
