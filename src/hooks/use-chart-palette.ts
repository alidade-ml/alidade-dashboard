import { useEffect, useState } from "react";

import { api, DEFAULT_CHART_PALETTE } from "@/lib/api";

/**
 * The categorical trace palette the server is serving.
 *
 * One mechanism for every chart. The experiment page fetched it and the cost page
 * read the bundled fallback directly, so on any NUC whose colors.json differed
 * from the shipped default — an edited config, or a wheel and a frontend at
 * different versions — the two pages coloured the same submitter differently.
 *
 * Falls back to the bundled list when the API is unreachable, which is also what
 * keeps offline preview coherent.
 */
export function useChartPalette(): string[] {
  const [palette, setPalette] = useState<string[]>(DEFAULT_CHART_PALETTE);

  useEffect(() => {
    const ctrl = new AbortController();
    api
      .colors(ctrl.signal)
      .then((c) => {
        if (c.palette && c.palette.length > 0) setPalette(c.palette);
      })
      .catch(() => {
        /* keep the bundled list */
      });
    return () => ctrl.abort();
  }, []);

  return palette;
}
