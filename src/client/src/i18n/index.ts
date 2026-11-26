import { initReactI18next } from "react-i18next";
import i18n from "i18next";

const modules = import.meta.glob("./locales/*.json", { eager: true }) as Record<
  string,
  { default: Record<string, unknown> }
>;

const resources: Record<string, { translation: Record<string, unknown> }> = {};

for (const [path, mod] of Object.entries(modules)) {
  const lang = path.match(/\/([^/]+)\.json$/)?.[1];

  if (lang) resources[lang] = { translation: mod.default };
}

// Map browser languages onto the supported locales so Chinese and Portuguese
// variants (zh, zh-Hans, pt-PT, …) resolve to their base locale instead of
// silently falling back to English. Anything else defaults to English.
const SUPPORTED_LANGUAGES = ["en", "pt-BR", "zh-CN"] as const;
const LANGUAGE_MAP: [RegExp, string][] = [
  [/^zh/i, "zh-CN"],
  [/^pt/i, "pt-BR"],
];

function resolveLanguage(browserLanguage: string): string {
  const normalized = browserLanguage.toLowerCase();
  const mapped = LANGUAGE_MAP.find(([pattern]) => pattern.test(normalized));

  return mapped?.[1] ?? "en";
}

i18n.use(initReactI18next).init({
  lng: resolveLanguage(navigator.language),
  fallbackLng: "en",
  supportedLngs: [...SUPPORTED_LANGUAGES],
  resources,
  interpolation: {
    escapeValue: false,
  },
});

export default i18n;
