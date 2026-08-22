/**
 * Examples tab — what a model actually produces.
 *
 * Every sample carries four coordinates: sample set, input, model, step. There
 * is no single correct nesting, because the question changes: "how do my two
 * models differ on this prompt" wants input above model, and "what does this
 * model make" wants the reverse. So the view is data, not layout — see
 * `SampleGroupingPanel`.
 *
 * Two rules keep the page from disappearing under its own structure. Both
 * exist because the first pass rendered 44 section headers for 17 samples:
 *
 *  1. **A level with one value is not a level.** It collapses into the parent
 *     heading as a breadcrumb crumb, so `denoise › unet-sm-v3` is one heading
 *     rather than two nested boxes.
 *  2. **A level that is really ordering is not a level either.** When every
 *     cell would hold a single row, `step` says nothing a position in a list
 *     does not already say, so it is dropped rather than labelled.
 *
 * Grouping is genuinely recursive rather than a switch over known orders —
 * anything else silently supports only the orders someone anticipated.
 *
 * Not wired to the API: the hub has never served an Aim artifact, so images
 * are canvas-drawn from a seed. The grouping, which is the part under test,
 * runs on data shaped exactly like the producer writes.
 */
import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { SampleGroupingPanel } from "@/components/sample-grouping-panel";
import {
  ALL_DIMENSIONS,
  DIMENSION_LABELS,
  VIEWS,
  cardLabelDims,
  cellOwnsInput,
  dimensionValue,
  distinct,
  planView,
  type Crumb,
  type GroupDimension,
  viewFor,
  type PlannedBlock,
  type PlannedSection,
  type SampleView,
} from "@/lib/sample-grouping";
import { FixtureControls } from "@/components/fixture-controls";
import { MODEL_COLORS, MODEL_HASHES, type SampleRow } from "@/lib/sample-fixtures";
import { DEFAULT_SHAPE, generateSamples, type FixtureShape } from "@/lib/sample-generator";
import { cn } from "@/lib/utils";

// ------------------------------------------------------------------ grouping

// ------------------------------------------------------------------ visuals

function mulberry32(a: number) {
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Deterministic stand-in for a generated image. Seeded so a sample looks the
 *  same across regroupings — pictures that changed on reshuffle would make the
 *  grouping impossible to follow. */
function SampleTile({ seed, noisy, size = 104 }: { seed: number; noisy?: boolean; size?: number }) {
  const ref = useRef<HTMLCanvasElement | null>(null);

  useEffect(() => {
    const cv = ref.current;
    if (!cv) return;
    const ctx = cv.getContext("2d");
    if (!ctx) return;
    const w = cv.width;
    const h = cv.height;
    const rnd = mulberry32(seed);
    const hueA = Math.floor(rnd() * 360);
    const hueB = (hueA + 40 + Math.floor(rnd() * 80)) % 360;

    const g = ctx.createLinearGradient(0, 0, w, h);
    g.addColorStop(0, `hsl(${hueA} 55% 62%)`);
    g.addColorStop(1, `hsl(${hueB} 50% 38%)`);
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, w, h);

    for (let i = 0; i < 12; i++) {
      ctx.beginPath();
      ctx.globalAlpha = 0.16 + rnd() * 0.2;
      ctx.fillStyle = `hsl(${Math.floor(rnd() * 360)} 60% ${30 + rnd() * 50}%)`;
      ctx.ellipse(
        rnd() * w,
        rnd() * h,
        rnd() * w * 0.4 + 5,
        rnd() * h * 0.3 + 5,
        rnd() * Math.PI,
        0,
        Math.PI * 2,
      );
      ctx.fill();
    }
    ctx.globalAlpha = 1;

    if (noisy) {
      const img = ctx.getImageData(0, 0, w, h);
      const d = img.data;
      for (let p = 0; p < d.length; p += 4) {
        const n = (rnd() - 0.5) * 190;
        d[p] += n;
        d[p + 1] += n;
        d[p + 2] += n;
      }
      ctx.putImageData(img, 0, 0);
    }
  }, [seed, noisy]);

  return (
    <canvas
      ref={ref}
      width={size}
      height={size}
      className="block w-full rounded border border-border"
      // Intrinsic size stays fixed so the drawing is crisp; the CSS size
      // scales with the column. A fixed px width overflowed its cell as soon
      // as a third run was added.
      style={{ maxWidth: size, aspectRatio: "1 / 1", height: "auto" }}
    />
  );
}

/**
 * One image cell.
 *
 * `src` is a real sample served by the hub; `seed` is the workbench's
 * canvas drawing. They are alternatives, not a fallback chain — a real
 * image that fails to load must show as broken rather than quietly
 * substituting generated art, because a plausible wrong picture is worse
 * than a visibly missing one.
 */
function SampleImage({
  src,
  seed,
  noisy,
  size = 96,
  alt,
}: {
  src?: string;
  seed?: number;
  noisy?: boolean;
  size?: number;
  alt: string;
}) {
  if (src) {
    return (
      <img
        src={src}
        alt={alt}
        // Native lazy loading: a grid can hold dozens of images and each
        // is its own request to Aim. Off-screen cells cost nothing until
        // they are scrolled to.
        loading="lazy"
        decoding="async"
        className="block w-full rounded border border-border bg-muted object-contain"
        style={{ maxWidth: size, aspectRatio: "1 / 1", height: "auto" }}
      />
    );
  }
  if (seed === undefined) return null;
  return <SampleTile seed={seed} noisy={noisy} size={size} />;
}

/** An image input is either a real URL or a workbench seed. Both call
 *  sites used to test the seed alone, which silently hid every real
 *  image the moment the tab stopped being fixture-driven. */
function hasInputImage(row: SampleRow): boolean {
  return row.inputUrl !== undefined || row.inputSeed !== undefined;
}

function hasOutputImage(row: SampleRow): boolean {
  return row.outputUrl !== undefined || row.outputSeed !== undefined;
}

function ModelSwatch({ model }: { model: string }) {
  return (
    <span
      className="inline-block h-2 w-2 shrink-0 rounded-sm"
      style={{ background: MODEL_COLORS[model] ?? "var(--muted-foreground)" }}
    />
  );
}

/** One sample's output. Labels are passed in already filtered — a card never
 *  decides for itself what is worth repeating. */
function SampleCard({
  row,
  labels,
  showInput,
}: {
  row: SampleRow;
  labels: string[];
  /** False when a heading or column header already shows this input. */
  showInput: boolean;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      {/* A text input the card owns reads above the output, as the question
          the output answers — not appended underneath as another chip. */}
      {showInput && !hasInputImage(row) && row.input && (
        <p className="border-l-2 border-border pl-2 text-xs leading-snug text-muted-foreground">
          {row.input}
        </p>
      )}
      <div className="flex items-start gap-2">
        {/* An image input needs saying so. Two tiles side by side with no
            labels leaves the reader guessing which one the model produced,
            which is the one thing this tab exists to show. */}
        {showInput && hasInputImage(row) && (
          <span className="flex min-w-0 flex-1 flex-col gap-1">
            <span className="font-mono text-[9px] uppercase tracking-wider text-muted-foreground">
              in
            </span>
            <SampleImage
              src={row.inputUrl}
              seed={row.inputSeed}
              noisy
              size={96}
              alt={`input at step ${row.step}`}
            />
          </span>
        )}
        {row.output !== undefined ? (
          <p className="whitespace-pre-wrap break-words text-sm leading-snug text-foreground">
            {row.output}
          </p>
        ) : (
          hasOutputImage(row) && (
            <span className="flex min-w-0 flex-1 flex-col gap-1">
              {showInput && hasInputImage(row) && (
                <span className="font-mono text-[9px] uppercase tracking-wider text-muted-foreground">
                  out
                </span>
              )}
              <SampleImage
                src={row.outputUrl}
                seed={row.outputSeed}
                size={96}
                alt={`output at step ${row.step}`}
              />
            </span>
          )
        )}
      </div>
      {labels.length > 0 && (
        <div className="flex flex-wrap gap-x-2 gap-y-0.5 text-[10px] text-muted-foreground">
          {labels.map((l) => (
            <span key={l}>{l}</span>
          ))}
        </div>
      )}
    </div>
  );
}

/** Crumb chain: "faces" or "denoise › unet-sm-v3". Only the last crumb is
 *  emphasised — the earlier ones are context already established above. */
function Heading({
  crumbs,
  depth,
  rows,
}: {
  crumbs: Crumb[];
  depth: number;
  /** Rows under this heading, used only to find the input image belonging to
   *  an `input` crumb. The input is a property of the axis, not of each cell:
   *  drawing it per column repeated one identical noisy frame three times
   *  across a denoise comparison. */
  rows?: SampleRow[];
}) {
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5">
      {crumbs.map((c, i) => {
        const last = i === crumbs.length - 1;
        const thumb =
          c.dim === "input" ? rows?.find((r) => dimensionValue(r, "input") === c.value) : undefined;
        return (
          <span key={`${c.dim}:${c.value}`} className="flex items-center gap-1.5">
            {i > 0 && <span className="text-muted-foreground/60">›</span>}
            {c.dim === "model" && <ModelSwatch model={c.value} />}
            {thumb && hasInputImage(thumb) && (
              <SampleImage src={thumb.inputUrl} seed={thumb.inputSeed} noisy size={40} alt="" />
            )}
            <span
              className={cn(
                "truncate",
                last
                  ? depth === 0
                    ? "text-sm font-semibold text-foreground"
                    : "text-xs font-medium text-foreground"
                  : "text-xs text-muted-foreground",
              )}
              title={c.value}
            >
              {c.dim === "step" ? `step ${c.value}` : c.value}
            </span>
          </span>
        );
      })}
    </span>
  );
}

// ------------------------------------------------------------------ render

/** One value covering an entire section — "everything below came from this
 *  model". Rendered apart from the crumb path so it cannot be misread as a
 *  grouping level that outranks the levels below it. */
function ConstantColumn({
  dim,
  value,
  rows,
}: {
  dim: GroupDimension;
  value: string;
  /** Needed to find the image behind an `input` value. This was an empty
   *  array — a placeholder that shipped — so a set with a single input folded
   *  the input into this chip and then drew nothing, and the denoise frame
   *  vanished from the page while the plan believed it was on the axis. */
  rows: SampleRow[];
}) {
  return (
    <span className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
      <span className="font-mono text-[9px] uppercase tracking-wider">{DIMENSION_LABELS[dim]}</span>
      {dim === "model" && <ModelSwatch model={value} />}
      {dim === "input" && <InputThumb value={value} rows={rows} />}
      <span className="text-foreground">{value}</span>
    </span>
  );
}

/** The image behind an `input` value, when there is one. */
function InputThumb({
  value,
  rows,
  size = 40,
}: {
  value: string;
  rows: SampleRow[];
  size?: number;
}) {
  const row = rows.find((r) => dimensionValue(r, "input") === value);
  if (!row || !hasInputImage(row)) return null;
  return <SampleImage src={row.inputUrl} seed={row.inputSeed} noisy size={size} alt="" />;
}

function ColumnHeader({
  dim,
  value,
  rows,
}: {
  dim: GroupDimension;
  value: string;
  rows: SampleRow[];
}) {
  return (
    <div className="flex items-center gap-1.5 border-b border-border pb-1 text-xs font-medium text-foreground">
      {dim === "model" && <ModelSwatch model={value} />}
      {dim === "input" && <InputThumb value={value} rows={rows} />}
      <span className="truncate" title={value}>
        {dim === "step" ? `step ${value}` : value}
      </span>
    </div>
  );
}

/** One aligned matrix. Columns come from the plan, which computed them once
 *  across the whole block — this renders, it does not decide. */
function BlockView({ block, allRows }: { block: PlannedBlock; allRows: SampleRow[] }) {
  const hasText = allRows.some((r) => r.output !== undefined);
  const minColumn = hasText ? 200 : 132;
  const labelled = block.rows.some((r) => r.crumbs.length > 0);
  const template = labelled
    ? `minmax(140px, 0.6fr) repeat(${block.columns.length}, minmax(${minColumn}px, 1fr))`
    : `repeat(${block.columns.length}, minmax(${minColumn}px, 1fr))`;

  return (
    <div className="overflow-x-auto pb-1">
      <div className="grid gap-x-4 gap-y-3" style={{ gridTemplateColumns: template }}>
        {block.columnDim !== null && (
          <>
            {labelled && <div />}
            {block.columns.map((col) => (
              <ColumnHeader
                key={`h-${col}`}
                dim={block.columnDim as GroupDimension}
                value={col as string}
                rows={allRows}
              />
            ))}
          </>
        )}

        {block.rows.map((row, ri) => (
          <Fragment key={`r-${ri}-${row.crumbs.map((c) => c.value).join("/")}`}>
            {labelled && (
              <div className="flex items-center border-t border-dashed border-border pt-2">
                <Heading crumbs={row.crumbs} depth={1} rows={row.rows} />
              </div>
            )}
            {row.cells.map((cell) => (
              <div
                key={`c-${ri}-${cell.column}`}
                className={cn(
                  "flex min-w-0 flex-col gap-2.5",
                  labelled && "border-t border-dashed border-border pt-2",
                )}
              >
                {cell.cards.length === 0 ? (
                  <p className="text-xs italic text-muted-foreground/60">not sampled</p>
                ) : (
                  cell.cards.map((card, i) => (
                    <SampleCard
                      key={`${card.row.sampleSet}-${card.row.model}-${card.row.step}-${i}`}
                      row={card.row}
                      showInput={card.showInput}
                      labels={card.labelDims.map((d) =>
                        d === "step" ? `step ${card.row.step}` : dimensionValue(card.row, d),
                      )}
                    />
                  ))
                )}
              </div>
            ))}
          </Fragment>
        ))}
      </div>
    </div>
  );
}

function SectionView({ section, depth }: { section: PlannedSection; depth: number }) {
  const chips = section.constants.map((c) => (
    <ConstantColumn key={c.dim} dim={c.dim} value={c.value} rows={section.rows} />
  ));

  const body = (
    <>
      {section.block && <BlockView block={section.block} allRows={section.rows} />}
      {section.children?.map((child, i) => (
        <SectionView
          key={`${i}-${child.crumbs.map((c) => c.value).join("/")}`}
          section={child}
          depth={depth + 1}
        />
      ))}
    </>
  );

  if (depth === 0) {
    return (
      <section className="overflow-hidden rounded-lg border border-border bg-card">
        <header className="flex items-baseline gap-2 border-b border-border bg-surface-elevated px-3 py-2">
          <Heading crumbs={section.crumbs} depth={0} rows={section.rows} />
          <span className="ml-auto flex shrink-0 items-baseline gap-3">
            {chips}
            <span className="text-[11px] text-muted-foreground">{section.rows.length} samples</span>
          </span>
        </header>
        <div className="flex flex-col gap-4 p-3">{body}</div>
      </section>
    );
  }

  // Sub-levels are a labelled band, not another box. Nested cards were the
  // main source of the "too broken up" feel.
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-baseline gap-2 border-b border-dashed border-border pb-1">
        <Heading crumbs={section.crumbs} depth={depth} rows={section.rows} />
        {chips.length > 0 && (
          <span className="ml-auto flex shrink-0 items-baseline gap-3">{chips}</span>
        )}
      </div>
      <div className={cn("flex flex-col gap-4", depth > 1 && "pl-3")}>{body}</div>
    </div>
  );
}

// ------------------------------------------------------------------ legend

/** Model legend with visibility toggles.
 *
 *  Deliberately mirrors `RunsPanel`'s contract (`hiddenRuns: Set<string>` plus
 *  a setter) rather than inventing one, so folding this into that shared panel
 *  later is a prop swap. It is not reused directly here because RunsPanel
 *  takes twelve props of comparison-modal and version-chip state that this
 *  route has no business faking. */

// ------------------------------------------------------------------ tab

interface SamplesViewProps {
  /** Every row to render. The caller decides where they came from. */
  rows: SampleRow[];
  /** Extra sidebar content — the workbench's fixture dials. Absent on
   *  the real tab, which has nothing to dial. */
  controls?: ReactNode;
  /** Rendered instead of the grid when there is nothing to show. */
  empty?: ReactNode;
  /** Shown above the grid while the first fetch is in flight. */
  loading?: boolean;
  error?: string | null;
  /** Chrome-free: the real tab already sits inside the experiment page's
   *  heading and padding. */
  bare?: boolean;
}

/**
 * The renderer. Knows nothing about where rows come from — that split is
 * the reason the read-path rework did not touch this file's layout logic
 * at all.
 */
export function SamplesView({
  rows: allRows,
  controls,
  empty,
  loading,
  error,
  bare,
}: SamplesViewProps) {
  const [view, setView] = useState<SampleView>(() => viewFor(VIEWS[0]));

  // Filtering by model happens upstream now: the caller passes only the
  // runs the shared panel has left visible.
  const rows = allRows;

  // Every layout decision happens here, in one testable place. The components
  // below render the result and decide nothing — see sample-grouping.test.ts,
  // which sweeps all 96 views this panel can produce.
  const plan = useMemo(() => planView(rows, view), [rows, view]);

  const { order, fold } = view;
  const summary = [
    fold > 0
      ? order
          .slice(0, fold)
          .map((d) => DIMENSION_LABELS[d].toLowerCase())
          .join(" \u203a ")
      : null,
    fold < order.length ? `${DIMENSION_LABELS[order[fold]].toLowerCase()} across` : null,
  ]
    .filter(Boolean)
    .join(", ");

  let main: ReactNode;
  if (error) {
    main = (
      <div className="rounded-lg border border-dashed border-destructive/40 bg-card p-12 text-center text-sm text-destructive">
        {error}
      </div>
    );
  } else if (loading && allRows.length === 0) {
    main = (
      <div className="rounded-lg border border-dashed border-border bg-card p-12 text-center text-sm text-muted-foreground">
        Loading samples\u2026
      </div>
    );
  } else if (allRows.length === 0) {
    main = empty ?? (
      <div className="rounded-lg border border-dashed border-border bg-card p-12 text-center text-sm text-muted-foreground">
        No samples logged for these runs.
      </div>
    );
  } else {
    main = (
      <div className="flex flex-col gap-3">
        {plan.map((section, i) => (
          <SectionView
            key={`${i}-${section.crumbs.map((c) => c.value).join("/")}`}
            section={section}
            depth={0}
          />
        ))}
      </div>
    );
  }

  return (
    <div
      className={cn(
        "flex w-full flex-col gap-4 lg:flex-row",
        bare ? "" : "mx-auto max-w-[1600px] px-6 py-5",
      )}
    >
      <div className="flex min-w-0 flex-1 flex-col gap-3">
        {!bare && (
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <h1 className="text-base font-semibold text-foreground">Examples</h1>
            <p className="text-xs text-muted-foreground">
              {rows.length} samples &middot; {summary || "ungrouped"}
            </p>
          </div>
        )}
        {bare && allRows.length > 0 && (
          <p className="text-xs text-muted-foreground">
            {rows.length} samples &middot; {summary || "ungrouped"}
          </p>
        )}
        {main}
      </div>

      {/* Sticky: the view picker and legend are used *while* reading the
          samples, and they were scrolling out of reach on a long page. Offset
          clears AppShell's own sticky header; the column scrolls internally
          if the dials make it taller than the viewport. */}
      <div className="flex w-full shrink-0 flex-col gap-3 lg:sticky lg:top-16 lg:max-h-[calc(100vh-5rem)] lg:w-72 lg:overflow-y-auto">
        <SampleGroupingPanel view={view} onChange={setView} />
        {controls}
        {/* No model legend here. The shared RunsPanel beside this column is
            already the legend — same colour dots, same toggle — and two
            controls doing overlapping jobs at different granularity on one
            page is worse than one doing it at the wrong granularity. Hiding
            a run there hides it here, as on Training and Eval. */}
      </div>
    </div>
  );
}

/** The development workbench: generated rows plus the dials that stress them. */
export function SamplesTab() {
  const [shape, setShape] = useState<FixtureShape>(DEFAULT_SHAPE);
  const allRows = useMemo(() => generateSamples(shape), [shape]);
  return (
    <SamplesView
      rows={allRows}
      controls={<FixtureControls shape={shape} onChange={setShape} total={allRows.length} />}
    />
  );
}
