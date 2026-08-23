import { useEffect, useState } from "react";

import { api } from "@/lib/api";

/**
 * Counts the eval and sample manifests for a set of models.
 *
 * The manifests are hoisted out of the tabs because the tab strip has to know
 * whether to offer a tab before anyone opens it — which is exactly what the
 * tabs' lazy mount was avoiding. Only the manifests move: one search each. The
 * expensive parts, metric series and image blobs, stay inside the tabs and are
 * still paid for only when opened.
 *
 * `null` means not yet known. Callers treat that as present; see visibleTabs.
 */
export function useTabPresence(runHashes: string[]): {
  evalEntries: number | null;
  sampleEntries: number | null;
} {
  const [counts, setCounts] = useState<{
    evalEntries: number | null;
    sampleEntries: number | null;
  }>({ evalEntries: null, sampleEntries: null });

  // Stable dependency: a new array with the same hashes must not refetch.
  const key = runHashes.join(",");

  useEffect(() => {
    if (runHashes.length === 0) {
      setCounts({ evalEntries: 0, sampleEntries: 0 });
      return;
    }
    const ctrl = new AbortController();
    let cancelled = false;

    async function countAcross(
      fetchOne: (hash: string, signal: AbortSignal) => Promise<{ length: number }>,
    ): Promise<number | null> {
      const per = await Promise.all(
        runHashes.map(async (hash) => {
          try {
            return (await fetchOne(hash, ctrl.signal)).length;
          } catch {
            // One unreachable model must not claim the whole tab is empty.
            return null;
          }
        }),
      );
      if (per.some((n) => n === null)) return null;
      return per.reduce((a: number, b) => a + (b ?? 0), 0);
    }

    Promise.all([countAcross((h, s) => api.evals(h, s)), countAcross((h, s) => api.samples(h, s))])
      .then(([evalEntries, sampleEntries]) => {
        if (cancelled) return;
        setCounts({ evalEntries, sampleEntries });
      })
      .catch(() => {
        if (cancelled) return;
        setCounts({ evalEntries: null, sampleEntries: null });
      });

    return () => {
      cancelled = true;
      ctrl.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  return counts;
}
