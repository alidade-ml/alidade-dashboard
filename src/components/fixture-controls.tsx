/**
 * Dials for the synthetic dataset, so the views can be stressed rather than
 * admired.
 *
 * A prototype shown against hand-picked data always passes. The counts that
 * decide whether these layouts hold up — how many models sit side by side,
 * how many inputs a set holds, how often a prompt repeats — belong on screen
 * next to the thing they break.
 *
 * Lives only on the /samples-lab route. It has no place in the real tab.
 */
import type { FixtureShape } from "@/lib/sample-generator";
import { MODEL_NAMES } from "@/lib/sample-generator";
import { cn } from "@/lib/utils";

interface Props {
  shape: FixtureShape;
  onChange: (next: FixtureShape) => void;
  /** How many samples the current dials produce. */
  total: number;
}

function Stepper({
  label,
  hint,
  value,
  min,
  max,
  onChange,
}: {
  label: string;
  hint?: string;
  value: number;
  min: number;
  max: number;
  onChange: (n: number) => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <span className="flex min-w-0 flex-1 flex-col leading-tight">
        <span className="truncate text-xs text-foreground">{label}</span>
        {hint && <span className="truncate text-[10px] text-muted-foreground">{hint}</span>}
      </span>
      <span className="flex shrink-0 items-center gap-1">
        <button
          type="button"
          aria-label={`Fewer ${label}`}
          disabled={value <= min}
          onClick={() => onChange(value - 1)}
          className={cn(
            "flex h-5 w-5 items-center justify-center rounded border border-border text-xs leading-none",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            value <= min
              ? "cursor-not-allowed text-muted-foreground/40"
              : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
          )}
        >
          −
        </button>
        <span className="w-5 text-center font-mono text-xs tabular-nums text-foreground">
          {value}
        </span>
        <button
          type="button"
          aria-label={`More ${label}`}
          disabled={value >= max}
          onClick={() => onChange(value + 1)}
          className={cn(
            "flex h-5 w-5 items-center justify-center rounded border border-border text-xs leading-none",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            value >= max
              ? "cursor-not-allowed text-muted-foreground/40"
              : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
          )}
        >
          +
        </button>
      </span>
    </div>
  );
}

export function FixtureControls({ shape, onChange, total }: Props) {
  const set = (patch: Partial<FixtureShape>) => onChange({ ...shape, ...patch });

  return (
    <div className="rounded-lg border border-dashed border-border-strong bg-card">
      <header className="flex items-baseline gap-2 border-b border-border px-3 py-2.5">
        <h2 className="text-xs font-semibold uppercase tracking-wider text-foreground">
          Test data
        </h2>
        <span className="ml-auto font-mono text-[10px] tabular-nums text-muted-foreground">
          {total} samples
        </span>
      </header>

      <div className="flex flex-col gap-2 p-2.5">
        <Stepper
          label="Models"
          hint="columns in Side by side"
          value={shape.models}
          min={1}
          max={MODEL_NAMES.length}
          onChange={(models) => set({ models })}
        />
        <Stepper
          label="Inputs"
          hint="rows, per sample set"
          value={shape.inputs}
          min={1}
          max={12}
          onChange={(inputs) => set({ inputs })}
        />
        <Stepper
          label="Repeats"
          hint="same prompt sampled N times"
          value={shape.repeats}
          min={1}
          max={5}
          onChange={(repeats) => set({ repeats })}
        />
        <Stepper
          label="Sample sets"
          value={shape.sets}
          min={1}
          max={8}
          onChange={(sets) => set({ sets })}
        />

        <div className="flex flex-col gap-1.5 border-t border-border pt-2">
          <span className="text-[10px] uppercase tracking-wider text-muted-foreground">
            Payload
          </span>
          <div className="flex rounded-md border border-border p-0.5">
            {(["mixed", "text", "image"] as const).map((k) => (
              <button
                key={k}
                type="button"
                onClick={() => set({ kind: k })}
                aria-pressed={shape.kind === k}
                className={cn(
                  "flex-1 rounded px-1.5 py-1 text-[11px] capitalize transition-colors",
                  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  shape.kind === k
                    ? "bg-primary text-primary-foreground"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {k}
              </button>
            ))}
          </div>
          <p className="text-[10px] leading-snug text-muted-foreground">
            Mixed cycles all four input/output combinations across sets.
          </p>
        </div>

        <label className="flex items-center gap-2 border-t border-border pt-2 text-xs text-foreground">
          <input
            type="checkbox"
            checked={shape.ragged}
            onChange={(e) => set({ ragged: e.target.checked })}
            className="h-3 w-3 accent-[var(--primary)]"
          />
          <span className="flex min-w-0 flex-col leading-tight">
            <span>Ragged coverage</span>
            <span className="text-[10px] text-muted-foreground">
              one model skips one input — leaves a hole in the grid
            </span>
          </span>
        </label>
      </div>
    </div>
  );
}
