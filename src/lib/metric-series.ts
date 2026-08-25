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
