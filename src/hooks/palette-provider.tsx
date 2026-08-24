import { useEffect, useState } from "react";

import {
  PALETTE_STORAGE_KEY,
  faviconHref,
  paletteFromStorageEvent,
  resolvePalette,
} from "@/lib/palette";
import { DEFAULT_PRESET, type PresetId } from "@/lib/palette-recipe";

import { PaletteContext, type PaletteCtx } from "./palette-context";

function readStoredPalette(): PresetId {
  if (typeof window === "undefined") return DEFAULT_PRESET;
  try {
    return resolvePalette(window.localStorage.getItem(PALETTE_STORAGE_KEY));
  } catch {
    return DEFAULT_PRESET;
  }
}

export function PaletteProvider({ children }: { children: React.ReactNode }) {
  // Read once, lazily. Reading in a mount effect races the write effect below,
  // which then persists the default over the stored choice.
  const [palette, setPalette] = useState<PresetId>(readStoredPalette);

  useEffect(() => {
    document.documentElement.dataset.palette = palette;
    // A favicon cannot read the page's tokens, so it is swapped rather than
    // recoloured. One file per preset already ships.
    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (link) link.href = faviconHref(palette);
    try {
      window.localStorage.setItem(PALETTE_STORAGE_KEY, palette);
    } catch {
      /* private browsing, quota — the choice just does not persist */
    }
  }, [palette]);

  // Tabs share one localStorage but get no notification of their own writes, so
  // without this a second tab keeps the old palette until it is refreshed.
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      const next = paletteFromStorageEvent(e.key, e.newValue);
      if (next) setPalette(next);
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const value: PaletteCtx = { palette, setPalette };
  return <PaletteContext.Provider value={value}>{children}</PaletteContext.Provider>;
}
