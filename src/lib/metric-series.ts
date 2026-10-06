/**
 * Placing metric points on an axis, and merging many runs onto one dataset.
 *
 * Pulled out of `metric-chart` so the axis rule is testable without a DOM: it
 * is the only part of the chart that can silently draw a point in the wrong
 * place, and it did.
 */
import type { XAxisMode } from "../hooks/chart-zoom-context.ts";
import type { MetricSeries } from "./types.ts";

export interface SeriesPoint {
  x: number;
  /** run-hash-keyed values */
  [runHash: string]: number | undefined;
}

/**
 * The x coordinate for point `i`, or null when it cannot be placed on this
 * axis and must be dropped.
 *
 * A run with no `wall_times` at all falls back to step number, so it still
 * charts in wall-time mode — a researcher reading "step 50" instead of "5m" is
 * at least not wrong. A run that HAS the series but no reading at this step is
 * a different fact, and the same fallback there would mix step counts into an
 * axis of seconds.
 */
export function pointX(series: MetricSeries, i: number, xMode: XAxisMode): number | null {
  const step = series.steps[i];
  if (xMode === "step") return step;
  if (!series.wall_times) return step;
  return series.wall_times[i] ?? null;
}

/** Merge each run's series into one dataset keyed by x, dropping points with
 *  no value and points that cannot be placed on the requested axis. */
export function mergeSeriesPoints(
  runHashes: string[],
  seriesByRun: Record<string, MetricSeries | undefined>,
  xMode: XAxisMode,
): SeriesPoint[] {
  const map = new Map<number, SeriesPoint>();
  for (const hash of runHashes) {
    const series = seriesByRun[hash];
    if (!series) continue;
    for (let i = 0; i < series.steps.length; i++) {
      const value = series.values[i];
      if (value == null || !isFinite(value)) continue;
      const x = pointX(series, i, xMode);
      if (x == null || !isFinite(x)) continue;
      const existing = map.get(x) ?? { x };
      existing[hash] = value;
      map.set(x, existing);
    }
  }
  return Array.from(map.values()).sort((a, b) => a.x - b.x);
}

export interface ChartRunRef {
  hash: string;
  name: string;
  visible: boolean;
}

export interface ChartHeader {
  /** "3 runs", or "1 of 3 runs" when some selected runs draw nothing. */
  count: string;
  /** Names the selected runs with nothing on this chart, or null when none are known. */
  absent: string | null;
}

/**
 * What a chart's header says about the runs selected for it.
 *
 * A run counts once it has a point on `data`, so a run with no series for this
 * metric, or none placeable on the current axis, is not counted. A run whose
 * fetch has not answered is neither drawn nor named: it may still arrive.
 */
export function chartHeader(
  metricName: string,
  runs: ChartRunRef[],
  data: SeriesPoint[],
  loaded: Record<string, unknown>,
  failed: Record<string, unknown>,
): ChartHeader {
  const selected = runs.filter((r) => r.visible);
  const drawn = selected.filter((r) => data.some((p) => typeof p[r.hash] === "number"));
  const undrawn = selected.filter((r) => !drawn.includes(r));
  // A failed first fetch stays in `failed` after a later poll answers, so an answer wins.
  const noData = undrawn.filter((r) => r.hash in loaded);
  const unloaded = undrawn.filter((r) => r.hash in failed && !(r.hash in loaded));

  const noun = selected.length === 1 ? "run" : "runs";
  const count =
    drawn.length === selected.length
      ? `${selected.length} ${noun}`
      : `${drawn.length} of ${selected.length} ${noun}`;

  const names = (rs: ChartRunRef[]) => rs.map((r) => r.name).join(", ");
  const parts = [
    noData.length > 0 &&
      `${names(noData)} ${noData.length === 1 ? "has" : "have"} no ${metricName}`,
    unloaded.length > 0 && `${names(unloaded)} could not be loaded`,
  ].filter((p): p is string => !!p);

  return { count, absent: parts.length > 0 ? parts.join(" · ") : null };
}
