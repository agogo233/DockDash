import { describe, expect, it } from "vitest";

import en from "../../i18n/locales/en.json";
import ptBR from "../../i18n/locales/pt-BR.json";
import zhCN from "../../i18n/locales/zh-CN.json";

const locales = [
  { name: "pt-BR", data: ptBR },
  { name: "zh-CN", data: zhCN },
];

function flatten(obj: Record<string, unknown>, prefix = ""): string[] {
  const keys: string[] = [];

  for (const [key, value] of Object.entries(obj)) {
    const fullKey = prefix ? `${prefix}.${key}` : key;

    if (value && typeof value === "object") {
      keys.push(...flatten(value as Record<string, unknown>, fullKey));
    } else {
      keys.push(fullKey);
    }
  }

  return keys;
}

describe("client i18n locale key parity", () => {
  const enKeys = flatten(en).sort();

  for (const { name, data } of locales) {
    it(`${name} matches the English key set exactly`, () => {
      expect(flatten(data).sort()).toEqual(enKeys);
    });
  }
});
