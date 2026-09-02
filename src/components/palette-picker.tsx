import { Check, Palette } from "lucide-react";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { usePalette } from "@/hooks/use-palette";
import { PRESETS, tokensFor } from "@/lib/palette-recipe";
import { useTheme } from "@/hooks/use-theme";
import { cn } from "@/lib/utils";

/**
 * Palette choice, beside the theme toggle.
 *
 * Per browser profile, not per user — there is no login anywhere in the
 * dashboard, so localStorage is the only place a preference can live. Two people
 * on different machines will see the same run in different palette colours;
 * status and trace colours are fixed across presets, so nothing semantic moves.
 */
export function PalettePicker() {
  const { palette, setPalette } = usePalette();
  const { theme } = useTheme();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label="Choose palette"
        className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-border bg-surface text-muted-foreground hover:text-foreground transition-colors"
      >
        <Palette className="h-3.5 w-3.5" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-[9rem]">
        {PRESETS.map((p) => (
          <DropdownMenuItem key={p.id} onSelect={() => setPalette(p.id)} className="gap-2 text-xs">
            {/* The swatch shows the accent as it will render in the current
                theme, not as it renders in light. */}
            <span
              aria-hidden
              className="h-3 w-3 shrink-0 rounded-full ring-1 ring-inset ring-black/15"
              style={{ background: tokensFor(p, theme)["--alidade-accent"] }}
            />
            <span className="flex-1">{p.label}</span>
            <Check className={cn("h-3 w-3", p.id === palette ? "opacity-100" : "opacity-0")} />
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
