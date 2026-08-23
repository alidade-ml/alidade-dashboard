/**
 * Every decision the Samples tab makes about where a sample is drawn.
 *
 * This file contains no JSX on purpose. The same bug — a coordinate rendered
 * twice, or not at all — surfaced three times in three different groupings,
 * each time because "has this already been shown?" was answered in a different
 * place with a different rule. The component now renders a plan produced here
 * rather than deciding as it walks, so the rules live in one testable place
 * and the sweep in `sample-grouping.test.ts` can assert them across every
 * ordering instead of waiting for someone to notice.
 */
import type { SampleRow } from "./sample-fixtures.ts";

export type GroupDimension = "sampleSet" | "input" | "model" | "step";

export const ALL_DIMENSIONS: GroupDimension[] = ["sampleSet", "input", "model", "step"];

/**
 * Identity dims name *which* sample you are looking at; `step` only says where
 * it sat in the batch — it is `enumerate(samples)` on the producer side.
 *
 * The distinction is load-bearing. Asking one question of every dim ("does
 * this tell two rows in the cell apart?") is right for an ordinal and wrong
 * for identity: with `input` as a card dim every cell held one row, so the
 * prompt "did not disambiguate" and vanished, leaving completions with no
 * question attached.
 */
export const IDENTITY_DIMS: ReadonlySet<GroupDimension> = new Set<GroupDimension>([
  "sampleSet",
  "input",
  "model",
]);

/** Reader-facing names for the coordinates. Lives here rather than in a
 *  component because the plan, the tests and the render all use them. */
export const DIMENSION_LABELS: Record<GroupDimension, string> = {
  sampleSet: "Sample set",
  input: "Input",
  model: "Model",
  step: "Step",
};

export interface SampleView {
  order: GroupDimension[];
  /** Count of dims above the fold. `order[fold]` is the column axis. */
  fold: number;
}

export function dimensionValue(row: SampleRow, dim: GroupDimension): string {
  switch (dim) {
    case "sampleSet":
      return row.sampleSet;
    case "input":
      return row.input ?? "(unconditional)";
    case "model":
      return row.model;
    case "step":
      return String(row.step);
  }
}

export function groupBy(rows: SampleRow[], dim: GroupDimension): Map<string, SampleRow[]> {
  const out = new Map<string, SampleRow[]>();
  for (const row of rows) {
    const key = dimensionValue(row, dim);
    const bucket = out.get(key);
    if (bucket) bucket.push(row);
    else out.set(key, [row]);
  }
  return out;
}

export function distinct(rows: SampleRow[], dim: GroupDimension): string[] {
  return [...groupBy(rows, dim).keys()];
}

export interface Crumb {
  dim: GroupDimension;
  value: string;
}

export interface Section {
  crumbs: Crumb[];
  rows: SampleRow[];
  children: Section[] | null;
}

/**
 * Nest `rows` by `dims`, skipping any dim that does not actually split the
 * rows it is given. A skipped dim still appears — as a crumb on the heading —
 * because its value is information; what it does not get is a box of its own.
 */
export function buildSections(rows: SampleRow[], dims: GroupDimension[]): Section[] {
  const crumbs: Crumb[] = [];
  let i = 0;

  while (i < dims.length) {
    const groups = groupBy(rows, dims[i]);
    if (groups.size <= 1) {
      const only = [...groups.keys()][0];
      if (only !== undefined) crumbs.push({ dim: dims[i], value: only });
      i += 1;
      continue;
    }

    return [...groups.entries()].map(([value, groupRows]) => {
      let children = buildSections(groupRows, dims.slice(i + 1));
      const ownCrumbs = [...crumbs, { dim: dims[i], value }];
      // A single child adds a box without adding a distinction: absorb it.
      while (children.length === 1) {
        ownCrumbs.push(...children[0].crumbs);
        const next = children[0].children;
        if (!next) {
          children = [];
          break;
        }
        children = next;
      }
      return {
        crumbs: ownCrumbs,
        rows: groupRows,
        children: children.length > 0 ? children : null,
      };
    });
  }

  return [{ crumbs, rows, children: null }];
}

/** Does this cell have to draw the input itself, or has something above
 *  already shown it? One answer to a question that was previously computed in
 *  three places and got a different answer in each. */
export function cellOwnsInput(
  established: ReadonlySet<GroupDimension>,
  columnDim: GroupDimension | null,
  rowCrumbs: Crumb[] = [],
): boolean {
  return (
    !established.has("input") && columnDim !== "input" && !rowCrumbs.some((c) => c.dim === "input")
  );
}

/** Which card dims earn a text label in this cell. `input` is excluded because
 *  the card renders it as content, not as a trailing label. */
export function cardLabelDims(
  cardDims: GroupDimension[],
  cell: SampleRow[],
  established: ReadonlySet<GroupDimension>,
): GroupDimension[] {
  return cardDims.filter(
    (d) =>
      d !== "input" &&
      !established.has(d) &&
      (IDENTITY_DIMS.has(d) || distinct(cell, d).length > 1),
  );
}

// ------------------------------------------------------- legal orderings

/**
 * Pairs that may not be inverted: `[a, b]` means a must stay above b.
 *
 * These are containment, not preference. An input belongs to a sample set —
 * it is one of the prompts *in* that batch — so a view nesting sets inside
 * inputs claims a prompt spans sets, which is not a thing. A step belongs to
 * a model: it is the index of that model's sample, so steps outside models
 * would group `step 0` across runs that share nothing but a counter.
 *
 * `sampleSet` deliberately does NOT precede `model`. A multi-modal model
 * producing several sample sets is exactly the case where model-outermost is
 * the view you want, and an earlier version of this file asserted the
 * opposite from three fixtures that happened not to contain one.
 *
 * Twenty-four orderings become six, and all six are stories someone would
 * actually tell.
 */
export const ORDER_CONSTRAINTS: ReadonlyArray<readonly [GroupDimension, GroupDimension]> = [
  ["sampleSet", "input"],
  ["model", "step"],
];

export function isLegalOrder(order: GroupDimension[]): boolean {
  return ORDER_CONSTRAINTS.every(([a, b]) => order.indexOf(a) < order.indexOf(b));
}

/** Why a move is refused, for the UI to explain rather than just resist. */
export function illegalReason(order: GroupDimension[]): string | null {
  for (const [a, b] of ORDER_CONSTRAINTS) {
    if (order.indexOf(a) > order.indexOf(b)) {
      return a === "sampleSet" ? "an input belongs to a sample set" : "a step belongs to a model";
    }
  }
  return null;
}

/** Indices `from` may legally be dropped on. */
export function legalMoveTargets(view: SampleView, from: number): number[] {
  const out: number[] = [];
  for (let to = 0; to < view.order.length; to++) {
    if (to !== from && isLegalOrder(moveDim(view, from, to).order)) out.push(to);
  }
  return out;
}

/** Only `input` and `model` are axes. `sampleSet` is a container and `step`
 *  is an ordinal; either one in the column slot wastes the horizontal axis on
 *  a dimension nobody scans across. */
export function columnDimIsAxis(view: SampleView): boolean {
  const col = view.fold < view.order.length ? view.order[view.fold] : null;
  return col === "input" || col === "model";
}

// ------------------------------------------------------- view manipulation

/** The fold never reaches the top. At fold 0 there are no sections at all:
 *  one unlabelled grid holding every sample set at once, columns cutting
 *  across payload kinds, and a blank heading where the set name should be. */
export const MIN_FOLD = 1;

export function clampFold(fold: number, dimCount: number): number {
  return Math.min(Math.max(fold, MIN_FOLD), dimCount);
}

/** Move a dim within the order. The fold is an index into that order, so it
 *  has to travel with the dims either side of it — otherwise dragging a dim
 *  across the fold silently changes which role a *different* dim plays. */
export function moveDim(view: SampleView, from: number, to: number): SampleView {
  const { order, fold } = view;
  if (from === to || from < 0 || to < 0 || from >= order.length || to >= order.length) {
    return view;
  }
  const next = [...order];
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  return { order: next, fold: clampFold(fold, next.length) };
}

/**
 * Move the fold to the next position where the column slot holds a real axis.
 *
 * Measured across all 24 legal views: folds 3 and 4 render 9-16 separate
 * matrices where folds 1 and 2 render 3, without exception in any ordering.
 * They are not a taste call, they are always fragmented — and what folds 1
 * and 2 vary between them is which of `input`/`model` sits in the columns.
 * So the fold, over its useful range, IS the transpose; the other positions
 * are the degenerate tail.
 *
 * Skipping rather than clamping means the arrows always do something: press
 * down and you land on the next view worth looking at, not on a wall.
 */
export function setFold(view: SampleView, fold: number): SampleView {
  const offered = legalFolds(view.order);
  if (offered.length === 0) return view;
  const direction = fold >= view.fold ? 1 : -1;
  const candidates = direction > 0 ? offered : [...offered].reverse();
  return {
    order: view.order,
    fold:
      candidates.find((f) => (direction > 0 ? f >= fold : f <= fold)) ??
      (direction > 0 ? offered[offered.length - 1] : offered[0]),
  };
}

/**
 * Fold positions worth offering for this order.
 *
 * Two rules, both from measurement rather than taste:
 *
 *  - the column slot must hold a real axis (`input` or `model`), because
 *    nobody scans across a container or an ordinal; and
 *  - the fold may not pass 2, because a view gets **one container level plus
 *    two axes** and no more. Past that you are nesting an axis as a section,
 *    which fragments by construction: measured across every legal ordering,
 *    folds 3 and 4 rendered 9-16 separate matrices where folds 1 and 2
 *    rendered 3.
 *
 * The second rule is not implied by the first. `sampleSet>model>step>input`
 * at fold 3 has `input` — a real axis — in its column slot and still splits
 * the page into ten blocks. The sweep caught that; the eye had not.
 */
/**
 * The fold is gone, fixed here at 2: one container level, then rows, then
 * columns.
 *
 * It was dropped by measurement, not taste. Its two positions looked like a
 * transpose, but the transpose comes from reordering the dims — what fold 1
 * actually did was collapse the second axis into the cell, and on the
 * fixtures that stacked 4 to 6 samples into one cell where fold 2 never
 * exceeded 2. A control whose other position is always worse is not a
 * control, and it duplicated something dragging already did.
 */
export const MAX_FOLD = 2;
export const FIXED_FOLD = 2;

/** The whole offered surface, named.
 *
 * Three views, because three is what survived inspection — every other
 * reachable layout either fragmented the page, crammed a cell, or drew a page
 * another view already drew.
 *
 * Not all three are always offered: see availableViews. */
export interface NamedView {
  id: string;
  name: string;
  /** What a row means, in the reader's terms. */
  hint: string;
  order: GroupDimension[];
}

export const VIEWS: NamedView[] = [
  {
    id: "side-by-side",
    name: "Comparison View",
    hint: "Same input, one column per model",
    order: ["sampleSet", "input", "model", "step"],
  },
  {
    id: "model-rows",
    name: "Model View",
    hint: "One model at a time, its inputs across",
    order: ["sampleSet", "model", "input", "step"],
  },
  {
    id: "model-first",
    name: "Catalog View",
    hint: "One model across every sample set at once",
    order: ["model", "sampleSet", "input", "step"],
  },
];

/**
 * The views worth offering for a given batch of rows.
 *
 * A picker entry that draws the page another entry already drew is not a
 * choice, and the reader has to click both to find that out. Which views are
 * distinct depends on the data, measured across the models-by-sets matrix:
 *
 *   - **Comparison View** puts models in columns, so it needs at least two
 *     models. With one it loses its column dimension entirely and renders as
 *     single-cell boxes stacked down the page — comparing one thing.
 *   - **Catalog View** differs from Model View only by merging sample sets,
 *     which needs at least two models AND at least two sets. Below that it
 *     draws an identical grid with the heading written backwards.
 *   - **Model View** always applies, and is the fallback when nothing else does.
 */
export function availableViews(rows: SampleRow[]): NamedView[] {
  const models = new Set<string>();
  const sets = new Set<string>();
  for (const r of rows) {
    models.add(r.model);
    sets.add(r.sampleSet);
  }
  return VIEWS.filter((v) => {
    if (v.id === "side-by-side") return models.size > 1;
    if (v.id === "model-first") return models.size > 1 && sets.size > 1;
    return true;
  });
}

export function viewFor(named: NamedView): SampleView {
  return { order: named.order, fold: FIXED_FOLD };
}

/** The named view whose order matches, if any. Dragging can land on an order
 *  that is legal but not one of the three; this is how the picker knows
 *  whether to highlight anything. */
export function namedViewFor(view: SampleView): NamedView | null {
  return VIEWS.find((v) => v.order.join() === view.order.join()) ?? null;
}

export function legalFolds(order: GroupDimension[]): number[] {
  const out: number[] = [];
  for (let f = MIN_FOLD; f <= Math.min(MAX_FOLD, order.length); f++) {
    if (columnDimIsAxis({ order, fold: f })) out.push(f);
  }
  return out;
}

/** An order is offerable when it puts a real axis in the column slot at the
 *  fixed fold. Three of the six legal orders do; the rest put `step` or
 *  `sampleSet` there, which nobody scans across. */
export function orderIsOfferable(order: GroupDimension[]): boolean {
  return isLegalOrder(order) && columnDimIsAxis({ order, fold: FIXED_FOLD });
}

// --------------------------------------------------------------- the plan

export interface PlannedCard {
  row: SampleRow;
  /** True when this card must draw the input itself. */
  showInput: boolean;
  /** Dims this card labels, already filtered. */
  labelDims: GroupDimension[];
}

export interface PlannedCell {
  column: string | null;
  cards: PlannedCard[];
}

export interface PlannedRow {
  crumbs: Crumb[];
  rows: SampleRow[];
  cells: PlannedCell[];
}

/** A block is one aligned matrix: columns computed once, from the union of
 *  every row in it. Deriving them per row is what let `restormer-ft` sit in a
 *  different horizontal position depending on which row you were reading. */
export interface PlannedBlock {
  columnDim: GroupDimension | null;
  columns: (string | null)[];
  rows: PlannedRow[];
}

export interface PlannedSection {
  crumbs: Crumb[];
  rows: SampleRow[];
  /** Dims constant across this whole section, surfaced beside the heading. */
  constants: Crumb[];
  /** Everything this heading (and its ancestors) has stated. */
  established: ReadonlySet<GroupDimension>;
  children: PlannedSection[] | null;
  block: PlannedBlock | null;
}

function planBlock(
  rowSections: Section[],
  allRows: SampleRow[],
  columnDim: GroupDimension | null,
  cardDims: GroupDimension[],
  established: ReadonlySet<GroupDimension>,
): PlannedBlock {
  const columns: (string | null)[] = columnDim ? distinct(allRows, columnDim) : [null];

  const rows = rowSections.map((section) => {
    const cells = columns.map((column) => {
      const cardRows =
        columnDim && column !== null
          ? section.rows.filter((r) => dimensionValue(r, columnDim) === column)
          : section.rows;
      const showInput = cellOwnsInput(established, columnDim, section.crumbs);
      const labelDims = cardLabelDims(cardDims, cardRows, established);
      return {
        column,
        cards: cardRows.map((row) => ({ row, showInput, labelDims })),
      };
    });
    return { crumbs: section.crumbs, rows: section.rows, cells };
  });

  return { columnDim, columns, rows };
}

function planSection(
  section: Section,
  columnDim: GroupDimension | null,
  cardDims: GroupDimension[],
  shownConstants: ReadonlySet<GroupDimension>,
  establishedDims: ReadonlySet<GroupDimension>,
): PlannedSection {
  // `step` is excluded here for the same reason it is excluded below. Without
  // it, a section whose rows all sit at step 1 grew a "STEP 1" chip — the
  // `def fib(n): STEP 1` heading, arriving by the column path instead of the
  // card path. Two routes to the same wrong output; the sweep found the one
  // that was still open.
  const columnIsConstant =
    columnDim !== null && columnDim !== "step" && distinct(section.rows, columnDim).length <= 1;

  // `step` is never surfaced as a constant: a section where every row shares
  // step 1 is not "the step-1 section", it is three models' answers to the
  // second prompt, which the input heading already says.
  const constantCardDims = cardDims.filter(
    (d) => d !== "step" && !shownConstants.has(d) && distinct(section.rows, d).length === 1,
  );

  const constants: Crumb[] = [
    ...(columnIsConstant && columnDim
      ? [{ dim: columnDim, value: distinct(section.rows, columnDim)[0] }]
      : []),
    ...constantCardDims.map((d) => ({ dim: d, value: distinct(section.rows, d)[0] })),
  ];

  const effectiveColumnDim = columnIsConstant ? null : columnDim;
  const nextShown = new Set([...shownConstants, ...constantCardDims]);
  const established = new Set<GroupDimension>([
    ...establishedDims,
    ...section.crumbs.map((c) => c.dim),
    ...constantCardDims,
    // A constant COLUMN dim was missing from this set. When the column axis
    // collapsed to one value it became a chip beside the heading, but the
    // cells below were never told, so an input shown in the chip was also
    // drawn on every card. Nobody had looked at [set›step›model] col=input;
    // the sweep did.
    ...(columnIsConstant && columnDim ? [columnDim] : []),
  ]);

  // When every child is a leaf, this section IS the matrix and its children
  // are its rows — they must share one column set.
  const childrenAreRows =
    section.children !== null && section.children.every((c) => c.children === null);

  if (section.children && childrenAreRows) {
    return {
      crumbs: section.crumbs,
      rows: section.rows,
      constants,
      established,
      children: null,
      block: planBlock(section.children, section.rows, effectiveColumnDim, cardDims, established),
    };
  }

  if (section.children) {
    return {
      crumbs: section.crumbs,
      rows: section.rows,
      constants,
      established,
      children: section.children.map((child) =>
        planSection(child, effectiveColumnDim, cardDims, nextShown, established),
      ),
      block: null,
    };
  }

  return {
    crumbs: section.crumbs,
    rows: section.rows,
    constants,
    established,
    children: null,
    block: planBlock([section], section.rows, effectiveColumnDim, cardDims, established),
  };
}

/** The whole tab, as data. The component renders this and decides nothing. */
export function planView(rows: SampleRow[], view: SampleView): PlannedSection[] {
  const { order, fold } = view;
  const sectionDims = order.slice(0, fold);
  const columnDim = fold < order.length ? order[fold] : null;
  const cardDims = order.slice(fold + 1);
  return buildSections(rows, sectionDims).map((section) =>
    planSection(section, columnDim, cardDims, new Set(), new Set()),
  );
}
