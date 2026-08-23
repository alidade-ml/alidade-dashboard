import { useEffect, useState } from "react";

import { THEME_STORAGE_KEY, ThemeContext, type Theme, type ThemeCtx } from "./theme-context";

function getInitial(): Theme {
  if (typeof window === "undefined") return "dark";
  const stored = window.localStorage.getItem(THEME_STORAGE_KEY);
  if (stored === "light" || stored === "dark") return stored;
  return window.matchMedia?.("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  // Read once, lazily, as the initial value. Reading in a mount effect instead
  // raced the write effect below: the write ran with the default still in state
  // and clobbered the stored choice, so nothing ever survived a reload.
  const [theme, setThemeState] = useState<Theme>(getInitial);

  useEffect(() => {
    const root = document.documentElement;
    root.classList.toggle("dark", theme === "dark");
    // The brand stylesheet keys on [data-theme]; Tailwind's dark: variant keys
    // on the class. Both are stamped so neither has to be rewritten.
    root.dataset.theme = theme;
    root.style.colorScheme = theme;
    try {
      window.localStorage.setItem(THEME_STORAGE_KEY, theme);
    } catch {
      /* ignore */
    }
  }, [theme]);

  // Tabs share one localStorage but get no notification of their own writes, so
  // without this a second tab keeps the old theme until it is refreshed. Shipped
  // behaviour, fixed alongside the palette provider rather than doubled by it.
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key === THEME_STORAGE_KEY && (e.newValue === "light" || e.newValue === "dark")) {
        setThemeState(e.newValue);
      }
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const value: ThemeCtx = {
    theme,
    setTheme: setThemeState,
    toggle: () => setThemeState((t) => (t === "dark" ? "light" : "dark")),
  };

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}
