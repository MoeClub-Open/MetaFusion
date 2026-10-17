import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import { DEFAULT_ACCENT, DEFAULT_PATTERN, DEFAULT_TONE, PATTERN_IDS } from "../src/lib/theme.generated";

const boot = readFileSync(new URL("../public/theme-boot.js", import.meta.url), "utf8");

function bootTheme(saved: Record<string, string> = {}, options: { dark?: boolean; storageBlocked?: boolean } = {}) {
  const attributes: Record<string, string> = {};
  const classes = new Set(["light"]);
  const style: { colorScheme?: string } = {};
  runInNewContext(boot, {
    localStorage: { getItem: (key: string) => {
      if (options.storageBlocked) throw new Error("Storage blocked");
      return saved[key] ?? null;
    } },
    window: { matchMedia: () => ({ matches: options.dark ?? false }) },
    document: { documentElement: {
      setAttribute: (key: string, value: string) => { attributes[key] = value; },
      classList: {
        remove: (...values: string[]) => { values.forEach((value) => classes.delete(value)); },
        add: (value: string) => { classes.add(value); },
      },
      style,
    } },
  });
  return { attributes, classes: [...classes], style };
}

test("first paint restores each background pattern together with the existing theme choices", () => {
  for (const pattern of PATTERN_IDS) {
    const actual = bootTheme({
      metafusion_theme_mode: "light",
      metafusion_theme_accent: "rose",
      metafusion_theme_tone: "warm",
      metafusion_theme_pattern: pattern,
    });
    assert.deepEqual(actual.attributes, {
      "data-theme-mode": "light",
      "data-theme-accent": "rose",
      "data-theme-tone": "warm",
      "data-theme-pattern": pattern,
    });
    assert.deepEqual(actual.classes, ["light"]);
    assert.equal(actual.style.colorScheme, "light");
  }
});

test("system mode resolves before first paint without changing the saved pattern", () => {
  const saved = { metafusion_theme_mode: "system", metafusion_theme_pattern: "dots" };
  for (const dark of [false, true]) {
    const actual = bootTheme(saved, { dark });
    assert.equal(actual.attributes["data-theme-mode"], dark ? "dark" : "light");
    assert.equal(actual.attributes["data-theme-pattern"], "dots");
  }
});

test("missing or invalid theme selections use the generated defaults on first paint", () => {
  const choices: Record<string, string>[] = [{}, {
    metafusion_theme_mode: "invalid",
    metafusion_theme_accent: "invalid",
    metafusion_theme_tone: "invalid",
    metafusion_theme_pattern: "invalid",
  }];
  for (const saved of choices) {
    assert.deepEqual(bootTheme(saved).attributes, {
      "data-theme-mode": "dark",
      "data-theme-accent": DEFAULT_ACCENT,
      "data-theme-tone": DEFAULT_TONE,
      "data-theme-pattern": DEFAULT_PATTERN,
    });
  }
});

test("blocked browser storage still applies a complete default theme", () => {
  const actual = bootTheme({}, { storageBlocked: true });
  assert.equal(actual.attributes["data-theme-pattern"], DEFAULT_PATTERN);
  assert.equal(actual.attributes["data-theme-accent"], DEFAULT_ACCENT);
  assert.equal(actual.attributes["data-theme-tone"], DEFAULT_TONE);
  assert.deepEqual(actual.classes, ["dark"]);
});
