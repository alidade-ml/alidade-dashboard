import { createFileRoute } from "@tanstack/react-router";

import { AppShell } from "@/components/app-shell";
import { SamplesTab } from "@/components/samples-tab";

/**
 * Development-only workbench for the Samples tab's layouts.
 *
 * A separate route rather than a tab on the experiment page: the layouts are
 * what is under test here, and they can be judged without the run-selection,
 * comparison and URL-state machinery the real tab will sit inside. The real
 * tab is EXAMPLES-1.04, and it will live beside Training and Eval — this page
 * is scaffolding for it, not a preview of it.
 *
 * Gated on `import.meta.env.DEV` because everything on it is generated. The
 * hub ships as a binary to a NUC, and a page of invented samples reachable by
 * URL on a customer install is indistinguishable from a bug in the real tab.
 *
 * Vite statically replaces the flag, so the branch below is dead in a
 * production build: measured, the chunk drops from 11.4kB to 2.3kB and the
 * components, the generator and the grouping logic are all absent from it.
 * A few hundred bytes of string constants do survive — Rollup keeps the
 * fixture objects the tree-shake cannot prove unused — so this hides the page,
 * it does not fully strip it. EXAMPLES-1.04 decides whether the workbench
 * stays in this build at all.
 */
export const Route = createFileRoute("/samples-lab")({
  component: SamplesLab,
});

function SamplesLab() {
  if (!import.meta.env.DEV) {
    return (
      <AppShell>
        <div className="mx-auto max-w-prose px-6 py-16 text-center text-sm text-muted-foreground">
          This workbench is only available in a development build.
        </div>
      </AppShell>
    );
  }
  return (
    <AppShell>
      <SamplesTab />
    </AppShell>
  );
}
