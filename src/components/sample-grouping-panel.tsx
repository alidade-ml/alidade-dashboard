/**
 * View picker for the Samples tab. Up to three options, no controls behind
 * them, and hidden entirely when the data supports only one.
 *
 * The drag-and-drop ordering it replaces was a real instrument while the
 * question was open — building it is how the four coordinates turned out not
 * to be four axes: `step` is an ordinal, `input` belongs to whichever axis
 * owns it, and `sampleSet` is a container. What survived that was three
 * layouts, and a control offering 96 doors to reach 3 rooms is the navigation
 * problem this tab exists to avoid.
 *
 * Kept deliberately dumb: no persistence, no default-setting. Storing a shared
 * default would make the hub a writer — it is a read replica today — and a
 * stored view is a surface someone could use to hide a model from everyone
 * else's default page.
 */
import { namedViewFor, viewFor, type NamedView, type SampleView } from "@/lib/sample-grouping";
import { cn } from "@/lib/utils";

interface Props {
  view: SampleView;
  onChange: (next: SampleView) => void;
  /** Which views this batch is worth offering — see availableViews. */
  views: NamedView[];
}

export function SampleGroupingPanel({ view, onChange, views }: Props) {
  const current = namedViewFor(view);

  // One option is not a choice. With a single model there is only ever one
  // distinct layout, and a picker offering it alone is a control that cannot
  // be used for anything.
  if (views.length < 2) return null;

  return (
    <div className="rounded-lg border border-border bg-card">
      <header className="border-b border-border px-3 py-2.5">
        <h2 className="text-xs font-semibold uppercase tracking-wider text-foreground">View</h2>
      </header>

      <ul className="flex flex-col gap-1 p-2">
        {views.map((v) => {
          const active = current?.id === v.id;
          return (
            <li key={v.id}>
              <button
                type="button"
                onClick={() => onChange(viewFor(v))}
                aria-pressed={active}
                className={cn(
                  "flex w-full flex-col rounded-md px-2 py-1.5 text-left transition-colors",
                  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  active
                    ? "bg-primary text-primary-foreground"
                    : "hover:bg-accent hover:text-accent-foreground",
                )}
              >
                <span className="text-xs font-medium">{v.name}</span>
                <span
                  className={cn(
                    "text-[10px] leading-tight",
                    active ? "text-primary-foreground/80" : "text-muted-foreground",
                  )}
                >
                  {v.hint}
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
