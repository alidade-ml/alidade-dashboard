// Astrolabe API types — mirror the Go backend contract exactly.

export type ExperimentState =
  | "PENDING"
  | "ACQUIRING"
  | "SETUP"
  | "RUNNING"
  | "HEALING"
  | "SUMMARIZING"
  | "COMPLETED"
  | "FAILED";

export type ExperimentOutcome = "success" | "failure" | "timeout" | "stopped" | null;

export interface Experiment {
  name: string;
  state: ExperimentState;
  gpu_type: string;
  started_at: string | null;
  /** Pre-formatted by the Go API (e.g. "4h 12m"), like ExperimentDetail and Run.
      Was declared as seconds, which nothing ever sent. */
  duration: string;
  outcome: ExperimentOutcome;
  /** Total number of runs across every version (versions × runs-per-version). */
  run_count: number;
  /**
   * Number of versions of this experiment (each = one re-submit). Optional —
   * older API responses may not include it; the dashboard falls back to
   * counting versions from the runs payload when it's missing.
   */
  version_count?: number;
  // Optional, may be present in future API versions; falls back gracefully
  repo?: string | null;
  state_history?: { state: ExperimentState; at: string }[];
  /**
   * URL to the experiment's Linear writeup. When present, the dashboard's
   * "Linear doc" link points here. When missing, the dashboard falls back to
   * a Linear search URL — that's a soft landing, not a guarantee the doc
   * exists.
   */
  linear_doc_url?: string | null;
  /**
   * Submitter identity (OS username from astrolabe.user / ExperimentRecord.
   * submitted_by). Empty string for legacy experiments that pre-date v1.2.1;
   * the home-page Submitter filter buckets those under "unknown".
   */
  submitted_by?: string;
}

/** Shape of /api/experiments/{name} — one experiment's header metadata.
 *
 * Deliberately narrower than Experiment: no run_count, because the endpoint
 * answers from the state DB alone and keeps working when Aim is down. */
export interface ExperimentDetail {
  name: string;
  state: ExperimentState;
  gpu_type: string;
  started_at: string;
  duration: string;
  outcome: string;
  repo?: string;
  linear_doc_url?: string;
  version_count: number;
  state_history?: { state: ExperimentState; at: string }[];
  submitted_by?: string;
}

export interface RunMetricRef {
  name: string;
  context?: string | null;
}

export interface Run {
  hash: string;
  name: string;
  experiment: string;
  /**
   * Which version of the experiment this run belongs to ("v1", "v2", …).
   * One submit = one version = one or more runs (e.g. "BERT" + "LatentBERT"
   * are two runs of the same version of an "architecture-comparison"
   * experiment). Optional for backward compatibility — when missing, the
   * dashboard treats the run as version "v1".
   */
  version?: string;
  /** Unix timestamp (seconds, float) when the run was created. */
  creation_time: number;
  /** Unix timestamp (seconds, float) when the run ended; 0 if active. */
  end_time: number | null;
  active: boolean;
  /** Pre-formatted duration string from the Go API (e.g. "5m 12s", "2h 15m"). */
  duration: string;
  metrics: RunMetricRef[];
  final_loss: number | null;
  /**
   * Submitter identity for this run. Used by the stats table to show
   * "by alice" when comparing across users. Empty for legacy runs.
   */
  submitted_by?: string;
  /**
   * astrolabe.kind. Absent or "training" means a model this experiment
   * trained; anything else is a model that arrived some other way (an
   * imported checkpoint, say). Use isTrainingRun rather than comparing
   * this directly, so an unfamiliar kind is never treated as training.
   */
  kind?: string;
  /**
   * True when the experiment evaluated this model rather than producing
   * it — the model may live in a different experiment entirely, which
   * `experiment` then names.
   */
  evaluated?: boolean;
}

/**
 * Whether a run should appear in training views (the loss chart, the
 * run count, the training stats table).
 *
 * Deliberately a whitelist. Listing the kinds to exclude means every
 * kind added later is silently treated as training — which is how
 * imported models ended up counted as training runs.
 */
export function isTrainingRun(run: Run): boolean {
  return !run.kind || run.kind === "training";
}

/**
 * Resolution shape for a single --include argument, returned from
 * /api/experiments/{name}/includes. The Go API resolves each include
 * against four shapes: hash → experiment name → run name → unknown.
 *
 * - "hash":       single Aim run hash matched directly
 * - "experiment": Aim experiment name matched (multi-run)
 * - "run-name":   Aim run.name matched somewhere in the corpus;
 *                 resolves to the SINGLE most recent matching run
 *                 (researchers wanting wider scope use the experiment
 *                 name or a specific hash)
 * - "unknown":    no match; runs is empty. Frontend renders the
 *                 include as a struck-out chip rather than silently
 *                 dropping it
 */
export type IncludeType = "hash" | "experiment" | "run-name" | "unknown";

export interface IncludeGroup {
  name: string;
  type: IncludeType;
  /** Aim run hashes — empty for type="unknown". */
  runs: string[];
}

export interface IncludesResponse {
  includes: IncludeGroup[];
}

export interface MetricSeries {
  name: string;
  steps: number[];
  values: number[];
  /** Elapsed seconds at each step, index-aligned with `steps`. Absent when the
   *  run logged no wall_time at all; null at a step the series does not cover. */
  wall_times?: (number | null)[];
}

/** One row in the eval-discovery manifest — one eval Aim run per
 *  (model_run, task_set) pair, surfaced by /api/runs/{hash}/evals. */
export interface EvalManifestEntry {
  aim_run_hash: string;
  task_set: string;
  /** Unix seconds (float), matches Aim's serialization. */
  creation_time: number;
}

/** One sample batch: one log_samples call. Payloads are fetched
 *  separately — the manifest is cheap and the payloads are not. */
export interface SampleManifestEntry {
  aim_run_hash: string;
  sample_set: string;
  model_run_hash: string;
  creation_time: number;
}

/** One step of a batch, joined by step on the server.
 *
 *  Absent and empty are different facts, so these are optional rather
 *  than defaulted to "": a set logged without inputs has no input_text
 *  at all, while a model that returned the empty string has one and it
 *  is "". Test with `"input_text" in pair`, never with truthiness. */
export interface SamplePair {
  step: number;
  input_text?: string;
  /** Stable hub URL for an image input — same address every fetch. */
  input_url?: string;
  output_text?: string;
  /** Stable hub URL for an image output — same address every fetch. */
  output_url?: string;
}

/** `kind` describes the OUTPUT, not the input. A prompt-to-image batch
 *  is kind "image" with input_text set. Read the per-pair fields. */
export interface SampleBatch {
  aim_run_hash: string;
  sample_set: string;
  kind: "text" | "image";
  pairs: SamplePair[];
}

/** Shape of /api/runs/{hash}/info — same as the existing Aim REST
 *  response. We only extract metric names from props for the eval
 *  table-vs-trace dispatch; the full payload is opaque otherwise. */
export interface RunInfo {
  params: Record<string, unknown>;
  traces: {
    metric: Array<{
      name: string;
      context: Record<string, unknown>;
      last_value: number;
    }>;
  };
}

export interface ColorsResponse {
  palette: string[];
}

export interface HealthResponse {
  status: string;
}

// --- Cost API -------------------------------------------------------------
//
// The cost page renders from a single API call so the frontend never shows
// a partially-consistent state (e.g., total + chart loaded but breakdown
// still spinning). The window + group_by query params drive every panel.
//
// All money values are integer cents to dodge JS float weirdness at the
// boundary; the frontend formats with the standard 2-decimal locale.

export type CostWindowLabel = "7d" | "30d" | "90d" | "all" | "custom";

export type CostGroupByDimension = "submitter" | "repo" | "gpu_type" | "outcome" | "backend";

export interface CostWindow {
  start: string; // ISO-8601
  end: string; // ISO-8601
  label: CostWindowLabel;
  /** Bucket size for the time-series chart — backend chooses based on window. */
  bucket: "daily" | "weekly" | "monthly";
}

export interface CostTimeBucket {
  /** Bucket start, ISO-8601 date. */
  start: string;
  total_cents: number;
  /**
   * Cents broken out by the chart's stacking dimension (GPU type by default).
   * Backend mirrors whatever stacking the frontend requested via the
   * ``stack`` query param; falls back to gpu_type if omitted.
   */
  by_dimension: Record<string, number>;
}

export interface CostBreakdownRow {
  /** Grouping key (submitter username, repo URL, gpu_type, outcome name). */
  key: string;
  /** Submits = versions = compute acquisitions; NOT Aim run count. */
  submits: number;
  /** Hours billed across all submits in this group. */
  hours: number;
  cents: number;
  /** Percent of the window's total cost, [0..100]. */
  pct: number;
}

/**
 * One experiment's place in the window. The cost page renders a multilevel
 * table: experiment name appears once (rowspan), versions listed under it.
 * Click a version row → that experiment's detail page (which stays cost-
 * free by design).
 */
export interface CostExperimentEntry {
  name: string;
  /** Hours summed across versions (running ones excluded — they have null hours). */
  total_hours: number;
  total_cents: number;
  versions: CostVersionEntry[];
}

export interface CostVersionEntry {
  version: string; // "v1", "v2", ...
  gpu_type: string;
  state: ExperimentState;
  outcome: ExperimentOutcome;
  /**
   * Null when the run is still in flight — the table renders a tilde +
   * "[running]" pill in that case, using estimated_cents instead.
   */
  hours: number | null;
  /** Final cents for terminal runs; null for in-flight (use estimated_cents). */
  cents: number | null;
  /** Pre-submit estimate (budget_hours × rate). Populated for every row. */
  estimated_cents: number;
}

export interface CostResponse {
  window: CostWindow;
  /** Sum across all experiments in the window. */
  total_cents: number;
  /** Same metric for the prior matching window — drives the "↑ 17%" delta.
   *  Backend-provided because the frontend doesn't have prior-window data
   *  in the same response; computing it would require a second API call. */
  prior_total_cents: number;
  // Note: failed_cents is intentionally NOT in the response. The header's
  // "of which $X on failed runs" line is derived on the frontend by
  // summing experiments[*].versions[*].cents where outcome ∈
  // {failed, stopped, timeout}. v1 doesn't paginate experiments so the
  // derivation is exhaustive. If pagination ever lands, lift the
  // computation to the backend then.
  time_series: CostTimeBucket[];
  breakdown: {
    dimension: CostGroupByDimension;
    rows: CostBreakdownRow[];
  };
  experiments: CostExperimentEntry[];
}
