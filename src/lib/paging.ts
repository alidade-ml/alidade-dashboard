/** Windowing for the experiments table.
 *
 * The whole list is already in the browser, so paging here is display only:
 * filters, sort and the summary counts still see every row and stay correct.
 */
export const PAGE_SIZE = 50;

export interface PageSlice {
  page: number;
  pageCount: number;
  pageStart: number;
}

/** Resolve a requested page against a list that may have changed size.
 *
 * The request is clamped rather than trusted: a URL can name any page, and the
 * 3s poll can shrink the list under a reader sitting on the last one. */
export function pageSlice(total: number, requested: number | undefined): PageSlice {
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const page = Math.min(Math.max(1, Math.floor(requested ?? 1) || 1), pageCount);
  return { page, pageCount, pageStart: (page - 1) * PAGE_SIZE };
}

/** Page numbers around the current one, with the first and last always
 *  reachable. Long runs collapse to a gap so the control stays one line. */
export function pageWindow(current: number, total: number): (number | "gap")[] {
  if (total <= 7) return Array.from({ length: total }, (_, i) => i + 1);
  const out: (number | "gap")[] = [1];
  const start = Math.max(2, current - 1);
  const end = Math.min(total - 1, current + 1);
  if (start > 2) out.push("gap");
  for (let p = start; p <= end; p++) out.push(p);
  if (end < total - 1) out.push("gap");
  out.push(total);
  return out;
}
