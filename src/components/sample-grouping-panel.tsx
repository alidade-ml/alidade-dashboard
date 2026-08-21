/**
 * Grouping control for the Examples tab.
 *
 * The four buckets — sample set, input, model, step — are always all present.
 * What changes is their order *and* the role each one plays, which is set by a
 * movable fold line:
 *
 *   above the fold   → sections, which nest
 *   first below      → the column axis, laid out side by side
 *   after that       → ordering within a cell, and labels on the card
 *
 * The fold exists because nesting all four guaranteed more chrome than
 * content: on the fixture data the default order rendered 44 section headers
 * for 17 samples, 17 of them at the `step` level wrapping exactly one item
 * each. A bucket is not always a section — `model` wants to be columns,
 * because comparison is the reason anyone opens this tab, and `step` is
 * usually just ordering.
 *
 * Dragging the fold to the bottom restores the fully-nested behaviour, so this
 * is a change of default rather than a loss of capability.
 *
 * `Set as my default view` writes to localStorage, and the copy says so.
 *
 * A *shared* default — one researcher's choice greeting the next person — is a
 * bigger change than it looks. The hub is a read replica: it serves the state
 * DB and Aim and writes to neither. Storing a team default makes it the system
 * of record for something, which needs a write endpoint, somewhere to put it,
 * and an answer to who may change what everyone else sees. Worth doing, worth
 * deciding deliberately, and not worth pretending is already true.
 */
import { useCallback, useState } from "react";

import { MIN_FOLD, moveDim, setFold } from "@/lib/sample-grouping";
import { cn } from "@/lib/utils";

export type GroupDimension = "sampleSet" | "input" | "model" | "step";

export const ALL_DIMENSIONS: GroupDimension[] = ["sampleSet", "input", "model", "step"];

export const DEFAULT_ORDER: GroupDimension[] = ["sampleSet", "input", "model", "step"];

/** Sections = sample set › input. Columns = model. Step falls to the card. */
export const DEFAULT_FOLD = 2;

export const DEFAULT_VIEW_KEY = "astrolabe.examples.view";

export const DIMENSION_LABELS: Record<GroupDimension, string> = {
  sampleSet: "Sample set",
  input: "Input",
  model: "Model",
  step: "Step",
};

export const DIMENSION_HINTS: Record<GroupDimension, string> = {
  sampleSet: "faces, sentence-completion, denoise",
  input: "the prompt or source image",
  model: "the run that produced it",
  step: "position within the batch",
};

export interface SampleView {
  order: GroupDimension[];
  /** Count of buckets above the fold. `order[fold]` is the column axis. */
  fold: number;
}

export const DEFAULT_VIEW: SampleView = { order: DEFAULT_ORDER, fold: DEFAULT_FOLD };

export function readStoredView(): SampleView | null {
  try {
    const raw = window.localStorage.getItem(DEFAULT_VIEW_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<SampleView>;
    const order = Array.isArray(parsed.order)
      ? parsed.order.filter((d): d is GroupDimension =>
          ALL_DIMENSIONS.includes(d as GroupDimension),
        )
      : [];
    // Reject a partial or duplicated order rather than repairing it — a
    // silently "fixed" view is harder to explain than one that fell back.
    if (order.length !== ALL_DIMENSIONS.length || new Set(order).size !== order.length) return null;
    const fold =
      typeof parsed.fold === "number" && parsed.fold >= 1 && parsed.fold <= order.length
        ? parsed.fold
        : DEFAULT_FOLD;
    return { order, fold };
  } catch {
    return null;
  }
}

interface Props {
  view: SampleView;
  onChange: (next: SampleView) => void;
  /** Distinct values per bucket across the whole dataset. A bucket with one
   *  value adds a level that says nothing, and the view now drops it. */
  cardinality: Record<GroupDimension, number>;
}

export function SampleGroupingPanel({ view, onChange, cardinality }: Props) {
  const { order, fold } = view;
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [overIndex, setOverIndex] = useState<number | null>(null);
  const [saved, setSaved] = useState(false);

  const move = useCallback(
    (from: number, to: number) => onChange(moveDim(view, from, to)),
    [view, onChange],
  );

  const changeFold = (next: number) => onChange(setFold(view, next));

  const stored = readStoredView();
  const isDefault = JSON.stringify(stored ?? DEFAULT_VIEW) === JSON.stringify(view);

  const save = () => {
    try {
      window.localStorage.setItem(DEFAULT_VIEW_KEY, JSON.stringify(view));
      setSaved(true);
      window.setTimeout(() => setSaved(false), 2200);
    } catch {
      /* storage disabled — the view still works, it just will not persist */
    }
  };

  const reset = () => {
    try {
      window.localStorage.removeItem(DEFAULT_VIEW_KEY);
    } catch {
      /* ignore */
    }
    onChange(DEFAULT_VIEW);
  };

  const roleFor = (i: number) => (i < fold ? "section" : i === fold ? "column" : "card");

  const renderRow = (dim: GroupDimension, i: number) => {
    const role = roleFor(i);
    const isDragging = dragIndex === i;
    const isOver = overIndex === i && dragIndex !== null && dragIndex !== i;
    const thin = cardinality[dim] <= 1;

    return (
      <li
        key={dim}
        draggable
        onDragStart={() => setDragIndex(i)}
        onDragEnd={() => {
          setDragIndex(null);
          setOverIndex(null);
        }}
        onDragOver={(e) => {
          e.preventDefault();
          setOverIndex(i);
        }}
        onDrop={(e) => {
          e.preventDefault();
          if (dragIndex !== null) move(dragIndex, i);
          setDragIndex(null);
          setOverIndex(null);
        }}
        className={cn(
          "flex items-center gap-2 rounded-md border px-2 py-1.5 transition-colors",
          "cursor-grab active:cursor-grabbing",
          isDragging ? "border-primary/60 bg-accent opacity-60" : "border-border",
          role === "column" ? "bg-accent/60" : "bg-surface-elevated",
          isOver && "border-primary bg-accent",
        )}
      >
        <span
          aria-hidden="true"
          className="select-none font-mono text-[10px] leading-none text-muted-foreground"
        >
          ⠿
        </span>

        <span className="flex min-w-0 flex-1 flex-col leading-tight">
          <span className="truncate text-xs font-medium text-foreground">
            {DIMENSION_LABELS[dim]}
          </span>
          <span className="truncate text-[10px] text-muted-foreground">
            {thin ? "one value — this level is dropped" : DIMENSION_HINTS[dim]}
          </span>
        </span>

        <span className="flex shrink-0 flex-col">
          <button
            type="button"
            aria-label={`Move ${DIMENSION_LABELS[dim]} up`}
            disabled={i === 0}
            onClick={() => move(i, i - 1)}
            className={cn(
              "flex h-3.5 w-5 items-center justify-center rounded-t border border-border text-[9px] leading-none",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              i === 0
                ? "cursor-not-allowed text-muted-foreground/40"
                : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
            )}
          >
            ▲
          </button>
          <button
            type="button"
            aria-label={`Move ${DIMENSION_LABELS[dim]} down`}
            disabled={i === order.length - 1}
            onClick={() => move(i, i + 1)}
            className={cn(
              "-mt-px flex h-3.5 w-5 items-center justify-center rounded-b border border-border text-[9px] leading-none",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              i === order.length - 1
                ? "cursor-not-allowed text-muted-foreground/40"
                : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
            )}
          >
            ▼
          </button>
        </span>
      </li>
    );
  };

  const zoneLabel = (text: string) => (
    <li className="px-0.5 pt-1 font-mono text-[9px] uppercase tracking-wider text-muted-foreground">
      {text}
    </li>
  );

  return (
    <div className="rounded-lg border border-border bg-card">
      <header className="border-b border-border px-3 py-2.5">
        <h2 className="text-xs font-semibold uppercase tracking-wider text-foreground">Group by</h2>
        <p className="mt-1 text-xs text-muted-foreground">
          Drag to reorder. Move the fold to change what nests and what sits side by side.
        </p>
      </header>

      <ol className="flex flex-col gap-1 p-2">
        {fold > 0 && zoneLabel("Sections")}
        {order.slice(0, fold).map((dim, i) => renderRow(dim, i))}

        {/* The fold. Its own row rather than a draggable element among the
            items — dragging both a list and a divider in one list is fiddly,
            and the arrows are unambiguous. */}
        <li className="flex items-center gap-2 py-0.5">
          <span className="h-px flex-1 bg-border-strong" />
          <button
            type="button"
            aria-label="Move fold up"
            disabled={fold <= MIN_FOLD}
            onClick={() => changeFold(fold - 1)}
            className={cn(
              "flex h-4 w-5 items-center justify-center rounded border border-border text-[9px]",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              fold <= MIN_FOLD
                ? "cursor-not-allowed text-muted-foreground/40"
                : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
            )}
          >
            ▲
          </button>
          <span className="font-mono text-[9px] uppercase tracking-wider text-muted-foreground">
            fold
          </span>
          <button
            type="button"
            aria-label="Move fold down"
            disabled={fold === order.length}
            onClick={() => changeFold(fold + 1)}
            className={cn(
              "flex h-4 w-5 items-center justify-center rounded border border-border text-[9px]",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              fold === order.length
                ? "cursor-not-allowed text-muted-foreground/40"
                : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
            )}
          >
            ▼
          </button>
          <span className="h-px flex-1 bg-border-strong" />
        </li>

        {fold < order.length && zoneLabel("Columns")}
        {fold < order.length && renderRow(order[fold], fold)}

        {fold + 1 < order.length && zoneLabel("On the card")}
        {order.slice(fold + 1).map((dim, i) => renderRow(dim, fold + 1 + i))}
      </ol>

      <div className="flex flex-col gap-2 border-t border-border p-2">
        <button
          type="button"
          onClick={save}
          disabled={isDefault}
          className={cn(
            "w-full rounded-md px-2 py-1.5 text-xs font-medium transition-colors",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            isDefault
              ? "cursor-not-allowed border border-border text-muted-foreground"
              : "bg-primary text-primary-foreground hover:opacity-90",
          )}
        >
          {isDefault ? "This is your default view" : "Set as my default view"}
        </button>

        {stored && (
          <button
            type="button"
            onClick={reset}
            className="w-full rounded-md border border-border px-2 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            Reset to astrolabe&rsquo;s default
          </button>
        )}

        <p
          className={cn(
            "text-[10px] leading-snug",
            saved ? "text-success" : "text-muted-foreground",
          )}
        >
          {saved
            ? "Saved in this browser."
            : "Saved in this browser only. A shared default needs the hub to write to the engine — it reads today."}
        </p>
      </div>
    </div>
  );
}
