export type Theme = "light" | "dark";
export interface ThemeApi {
  current(): Theme;
  isAuto(): boolean;
  toggle(): void;
}
declare global {
  interface Window {
    theme: ThemeApi;
  }
  interface DocumentEventMap {
    themechange: Event;
  }
}
const key = "signtools.theme";
const media = matchMedia("(prefers-color-scheme: dark)");
const stored = (): Theme | null => {
  try {
    const value = localStorage.getItem(key);
    return value === "dark" || value === "light" ? value : null;
  } catch {
    return null;
  }
};
const current = (): Theme => stored() ?? (media.matches ? "dark" : "light");
function apply() {
  document.documentElement.classList.toggle("dark", current() === "dark");
  document.documentElement.style.colorScheme = current();
  document.dispatchEvent(new Event("themechange"));
}
window.theme = {
  current,
  isAuto: () => stored() === null,
  toggle() {
    const next = current() === "dark" ? "light" : "dark";
    try {
      if (next === (media.matches ? "dark" : "light")) localStorage.removeItem(key);
      else localStorage.setItem(key, next);
    } catch {}
    apply();
  },
};
media.addEventListener("change", apply);
window.addEventListener("storage", (event) => {
  if (event.key === key) apply();
});
apply();
