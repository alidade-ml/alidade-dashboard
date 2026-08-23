/**
 * Which tabs an experiment page should offer.
 *
 * A tab that opens onto an empty state makes the reader click to find out
 * there is nothing there, on every experiment. Most experiments have one kind
 * of data, not three.
 *
 * The counts include runs pulled in by `--include` and by the comparison
 * picker, because those render in the tab like any other model.
 */

/** `null` means not known yet — still loading, or the lookup failed. */
export interface TabEvidence {
  training: boolean;
  evalEntries: number | null;
  sampleEntries: number | null;
}

export interface VisibleTabs {
  training: boolean;
  eval: boolean;
  samples: boolean;
}

export type TabId = keyof VisibleTabs;

export const TAB_ORDER: TabId[] = ["training", "eval", "samples"];

/**
 * Unknown counts as present.
 *
 * A tab hidden because a request failed is worse than one shown empty: the
 * reader has no way to tell "my samples never arrived" from "the dashboard
 * could not reach Aim", and goes looking in the wrong place. Absence has to be
 * proven, never inferred from silence.
 */
export function visibleTabs(evidence: TabEvidence): VisibleTabs {
  return {
    training: evidence.training,
    eval: evidence.evalEntries === null || evidence.evalEntries > 0,
    samples: evidence.sampleEntries === null || evidence.sampleEntries > 0,
  };
}

export function anyTabVisible(tabs: VisibleTabs): boolean {
  return TAB_ORDER.some((id) => tabs[id]);
}

/**
 * The tab to show, given what the reader last chose.
 *
 * Keeps their choice when it still has data, so a poll that adds a sample set
 * does not move them. Falls to the first tab that does otherwise — including
 * when a deep link names a tab this experiment has nothing for.
 */
export function resolveActiveTab(tabs: VisibleTabs, preferred: TabId | undefined): TabId | null {
  if (preferred && tabs[preferred]) return preferred;
  return TAB_ORDER.find((id) => tabs[id]) ?? null;
}
