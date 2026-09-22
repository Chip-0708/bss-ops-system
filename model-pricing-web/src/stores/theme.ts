import { defineStore } from "pinia";
import { computed, onScopeDispose, ref, watch } from "vue";

export type ThemeMode = "light" | "dark" | "system";

const STORAGE_KEY = "app-theme-mode";
const isThemeMode = (value: string | null): value is ThemeMode =>
  value === "light" || value === "dark" || value === "system";

export const useThemeStore = defineStore("theme", () => {
  const saved = localStorage.getItem(STORAGE_KEY);
  const mode = ref<ThemeMode>(isThemeMode(saved) ? saved : "system");
  const systemDark = ref(matchMedia("(prefers-color-scheme: dark)").matches);
  const media = matchMedia("(prefers-color-scheme: dark)");
  const effectiveTheme = computed<"light" | "dark">(() =>
    mode.value === "system" ? (systemDark.value ? "dark" : "light") : mode.value,
  );

  const handleSystemTheme = (event: MediaQueryListEvent) => {
    systemDark.value = event.matches;
  };
  media.addEventListener("change", handleSystemTheme);
  onScopeDispose(() => media.removeEventListener("change", handleSystemTheme));

  watch(
    [mode, effectiveTheme],
    () => {
      localStorage.setItem(STORAGE_KEY, mode.value);
      document.documentElement.classList.toggle(
        "dark",
        effectiveTheme.value === "dark",
      );
      document.documentElement.dataset.theme = effectiveTheme.value;
    },
    { immediate: true },
  );

  return { mode, effectiveTheme };
});

