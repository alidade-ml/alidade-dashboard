import { Link } from "@tanstack/react-router";
import { Moon, Sun } from "lucide-react";
import { useTheme } from "@/hooks/use-theme";
import { BrandMark, Wordmark } from "@/components/brand-mark";
import { PalettePicker } from "@/components/palette-picker";

interface Props {
  children: React.ReactNode;
  /** Right-side slot in the top bar (e.g. freshness pill, action buttons). */
  rightSlot?: React.ReactNode;
}

export function AppShell({ children, rightSlot }: Props) {
  const { theme, toggle } = useTheme();
  return (
    <div className="min-h-screen bg-background text-foreground flex flex-col">
      <header className="sticky top-0 z-30 border-b border-border bg-background/85 backdrop-blur supports-[backdrop-filter]:bg-background/70">
        <div className="mx-auto flex h-12 w-full max-w-[1600px] items-center gap-4 px-6">
          {/* The logo doubles as the home link — a separate "Home" nav item and
              the redundant "experiments" tag were both removed; the title alone
              carries enough weight. */}
          <Link to="/" className="flex items-center gap-2.5 hover:opacity-80 transition-opacity">
            <BrandMark />
            <Wordmark className="text-[15px] leading-none" />
          </Link>
          {/* No top-nav links. The cost / spend page is reachable via the
              clickable Spend KPI card on the home page (and direct URL).
              Detail pages stay focused on the experiment — no cost-page
              link competing for attention. */}
          <div className="ml-auto flex items-center gap-2">
            {rightSlot}
            <PalettePicker />
            <button
              type="button"
              onClick={toggle}
              aria-label="Toggle theme"
              className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-border bg-surface text-muted-foreground hover:text-foreground transition-colors"
            >
              {theme === "dark" ? (
                <Sun className="h-3.5 w-3.5" />
              ) : (
                <Moon className="h-3.5 w-3.5" />
              )}
            </button>
          </div>
        </div>
      </header>
      <main className="flex-1">{children}</main>
    </div>
  );
}
