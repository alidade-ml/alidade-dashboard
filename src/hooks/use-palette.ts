import { useContext } from "react";

import { PaletteContext } from "./palette-context";

export function usePalette() {
  const ctx = useContext(PaletteContext);
  if (!ctx) throw new Error("usePalette must be used inside PaletteProvider");
  return ctx;
}
