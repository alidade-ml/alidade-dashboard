import { createFileRoute } from "@tanstack/react-router";

import { AppShell } from "@/components/app-shell";
import { SamplesTab } from "@/components/samples-tab";

/**
 * Front-end spike for the Examples tab's grouping order.
 *
 * A separate route rather than a tab on the experiment page: the grouping is
 * the thing under test, and it can be judged without the run-selection,
 * comparison and URL-state machinery the real tab will sit inside. Wiring it
 * into that page before the interaction is settled would mean rebuilding it
 * twice.
 */
export const Route = createFileRoute("/samples-lab")({
  component: SamplesLab,
});

function SamplesLab() {
  return (
    <AppShell>
      <SamplesTab />
    </AppShell>
  );
}
