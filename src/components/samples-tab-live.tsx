/**
 * Samples tab, over real data.
 *
 * Mirrors EvalTab's shape deliberately: same props, same run-scoping, same
 * respect for the shared RunsPanel toggle. A reader who knows one should
 * recognise the other.
 *
 * Fetch graph per model run in scope:
 *
 *   /api/runs/<model_hash>/samples        -> batches, newest per sample_set
 *   /api/samples/<batch_hash>?set=<set>   -> pairs, already joined by step
 *
 * Image pairs carry a uri, not bytes, so the grid lays out before any pixel
 * is fetched. Each <img> is its own request to the hub, which is deliberate:
 * the browser gets progressive rendering, per-image caching and free
 * cancellation on scroll, none of which a batched fetch would give back.
 */
import { useEffect, useMemo, useState } from "react";

import { SamplesView } from "@/components/samples-tab";
import { api } from "@/lib/api";
import { rowsFromBatch } from "@/lib/sample-rows";
import type { SampleRow } from "@/lib/sample-fixtures";

interface SamplesTabProps {
  /** All runs currently visible — same set as the Training tab's charts. */
  runs: Array<{ hash: string; name: string; experiment: string }>;
  /** Run hashes the user has toggled off via the shared RunsPanel. */
  hiddenRunHashes?: Set<string>;
}

interface FetchState {
  rows: SampleRow[];
  loading: boolean;
  error: string | null;
}

function useRealSamples(runs: SamplesTabProps["runs"]): FetchState {
  const [state, setState] = useState<FetchState>({ rows: [], loading: true, error: null });

  // Stable dependency: a new array with the same hashes must not refetch.
  const runsKey = runs.map((r) => r.hash).join(",");

  useEffect(() => {
    if (runs.length === 0) {
      setState({ rows: [], loading: false, error: null });
      return;
    }
    const ctrl = new AbortController();
    let cancelled = false;

    async function rowsForRun(run: SamplesTabProps["runs"][number]): Promise<SampleRow[]> {
      const manifest = await api.samples(run.hash, ctrl.signal);
      const batches = await Promise.all(
        manifest.map(async (entry) => {
          try {
            return await api.sampleBatch(entry.aim_run_hash, entry.sample_set, ctrl.signal);
          } catch {
            // One unreadable batch must not empty the tab. The rest of
            // the grid is still true, and a partial view is more useful
            // than an error page.
            return null;
          }
        }),
      );
      return batches
        .filter((b): b is NonNullable<typeof b> => b !== null)
        .flatMap((b) => rowsFromBatch(b, run.name, run.hash));
    }

    setState((prev) => ({ ...prev, loading: true, error: null }));
    Promise.all(runs.map(rowsForRun))
      .then((perRun) => {
        if (cancelled) return;
        setState({ rows: perRun.flat(), loading: false, error: null });
      })
      .catch((err: unknown) => {
        if (cancelled || ctrl.signal.aborted) return;
        setState({
          rows: [],
          loading: false,
          error: err instanceof Error ? err.message : "Could not load samples.",
        });
      });

    return () => {
      cancelled = true;
      ctrl.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runsKey]);

  return state;
}

/**
 * Adapter matching EvalTabFromAllRuns, so the experiment page passes the
 * same `allRuns` shape to both tabs and neither knows about the other's
 * run model.
 */
export function SamplesTabLive({
  allRuns,
  hiddenRunHashes,
}: {
  allRuns: Array<{ hash: string; name: string; experiment?: string }>;
  hiddenRunHashes?: Set<string>;
}) {
  const runs = useMemo(
    () =>
      allRuns.map((r) => ({
        hash: r.hash,
        name: r.name,
        experiment: r.experiment ?? "",
      })),
    [allRuns],
  );
  return <SamplesTabInner runs={runs} hiddenRunHashes={hiddenRunHashes} />;
}

function SamplesTabInner({ runs, hiddenRunHashes }: SamplesTabProps) {
  // Filter before fetching, not after: a hidden run's samples are requests
  // nobody asked for, and each image batch is several round trips.
  const visibleRuns = useMemo(
    () => (hiddenRunHashes ? runs.filter((r) => !hiddenRunHashes.has(r.hash)) : runs),
    [runs, hiddenRunHashes],
  );
  const { rows, loading, error } = useRealSamples(visibleRuns);

  return (
    <SamplesView
      rows={rows}
      loading={loading}
      error={error}
      bare
      empty={
        <div className="rounded-lg border border-dashed border-border bg-card p-12 text-center text-sm text-muted-foreground">
          <p>No samples logged for these runs.</p>
          <p className="mt-1 text-xs">
            Call <code className="font-mono">log_samples()</code> from your training or eval script
            to show what the model actually produced.
          </p>
        </div>
      }
    />
  );
}
