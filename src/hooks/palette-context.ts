import { createContext } from "react";

import type { PresetId } from "@/lib/palette-recipe";

export interface PaletteCtx {
  palette: PresetId;
  setPalette: (p: PresetId) => void;
}

export const PaletteContext = createContext<PaletteCtx | null>(null);
