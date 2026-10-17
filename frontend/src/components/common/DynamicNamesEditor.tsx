"use client";


import { X, Globe } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { LanguagePicker } from "@/components/common/LanguagePicker";
import {
  canonicalLanguageCode,
  findLanguage,
  languageNativeName,
  REQUIRED_NAME_LOCALES,
  sameLanguage,
} from "@/lib/languages";

export interface MultilingualNames {
  [langCode: string]: string;
}

/**
 * 四语齐备判据（与后端 four_locale_names_required 同口径）：
 * zh-CN / zh-TW / en-US 必须非空；日文允许 ja 或 ja-JP——两种键名在存量数据里都存在，
 * 判据与后端 validateNames 对齐：只查"键在且非空"，不查取值是否与英文不同。
 * 返回缺失的语种码，供调用方决定提示文案。
 */
export function missingRequiredLocales(names?: MultilingualNames): string[] {
  const values = names || {};
  const filled = (code: string) => String(values[code] ?? "").trim() !== "";
  const missing: string[] = REQUIRED_NAME_LOCALES.filter((code) => !filled(code));
  if (!Object.keys(values).some((k) => sameLanguage(k, "ja-JP") && filled(k))) {
    missing.push("ja-JP");
  }
  return missing;
}

/** 语种码归一：ja / JA / jpn 都落成 definitions 的 ja-JP，避免写出 ja-jp、zh-tw 这类对不上服务端校验的键。 */
function normalizeLocaleCode(raw: string): string {
  return canonicalLanguageCode(raw);
}

interface DynamicNamesEditorProps {
  value?: MultilingualNames;
  onChange: (names: MultilingualNames) => void;
  label?: string;
  helperText?: string;
  required?: boolean;
}

export function DynamicNamesEditor({
  value = {},
  onChange,
  label,
  helperText,
  required,
}: DynamicNamesEditorProps) {
  const { t } = useI18n();

  const currentNames = { ...value };
  const existingCodes = Object.keys(currentNames);

  const handleUpdate = (code: string, text: string) => {
    const updated = { ...currentNames, [code]: text };
    onChange(updated);
  };

  const handleRemove = (code: string) => {
    const updated = { ...currentNames };
    delete updated[code];
    onChange(updated);
  };

  const handleAdd = (raw: string) => {
    const code = normalizeLocaleCode(raw);
    if (!code) return;
    // 同一语种不重复添加：存量数据里日文可能是 ja，也可能是 ja-JP。
    if (existingCodes.some((c) => sameLanguage(c, code))) return;
    onChange({ ...currentNames, [code]: "" });
  };

  return (
    <div className="min-w-0 space-y-2">
      {label && (
        <div className="flex flex-col gap-1 sm:flex-row sm:flex-wrap sm:items-center sm:justify-between">
          <label className="block text-[11px] font-mono text-text-body font-medium flex items-center gap-1.5">
            <Globe className="w-3.5 h-3.5 text-primary" />
            <span>{label}</span>
            {required && <span className="text-danger">*</span>}
          </label>
          {helperText && (
            <span className="min-w-0 break-words text-[10px] text-text-faint font-mono">
              {helperText}
            </span>
          )}
        </div>
      )}

      {/* 语言条目列表 */}
      <div className="space-y-2">
        {existingCodes.length === 0 ? (
          <div className="p-3 rounded-lg border border-dashed border-line text-center text-xs text-text-faint font-mono">
            {t("multilingual.noEntries")}
          </div>
        ) : (
          existingCodes.map((code) => {
            // 标签走语言单一来源：表内语种显示自称，表外语种回落代码本身（不空白、不报错）。
            const preset = findLanguage(code);
            return (
              <div
                key={code}
                className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] sm:flex items-center gap-2 bg-background border border-line rounded-lg p-1.5 focus-within:border-primary/50 transition-colors duration-fast ease-soft"
              >
                <span className="col-span-2 justify-self-start max-w-full break-all px-2 py-0.5 rounded bg-emphasis/[0.06] text-text-body text-[11px] font-mono shrink-0 min-w-[64px] text-center">
                  {code}
                  {preset && (
                    <span className="text-[10px] text-text-faint ml-1">
                      ({languageNativeName(code)})
                    </span>
                  )}
                </span>
                <input
                  type="text"
                  value={currentNames[code] || ""}
                  onChange={(e) => handleUpdate(code, e.target.value)}
                  placeholder={t("multilingual.namePlaceholder", { code })}
                  className="min-w-0 flex-1 bg-transparent px-2 py-1 text-xs text-emphasis placeholder:text-text-faint focus:outline-none"
                />
                <button
                  type="button"
                  onClick={() => handleRemove(code)}
                  className="p-1 text-text-faint hover:text-danger rounded transition-colors duration-fast ease-soft"
                  title={t("multilingual.removeLang", { code })}
                >
                  <X className="w-3.5 h-3.5" />
                </button>
              </div>
            );
          })
        )}
      </div>

      {/* 添加新语言 */}
      <div className="pt-1">
        {/* 语种由可搜索的语言选择器挑选：不必先知道代码，ja / 日本 / Japanese 都能搜到。 */}
        <LanguagePicker
          selected={existingCodes}
          onSelect={handleAdd}
          label={t("multilingual.addLanguage")}
          ariaLabel={t("multilingual.addLanguage")}
          variant="field"
          inline
        />
      </div>
    </div>
  );
}

export function MultilingualBadges({ names }: { names?: MultilingualNames }) {
  const entries = Object.entries(names || {}).filter(([_, v]) => v && v.trim() !== "");
  if (entries.length === 0) {
    return <span className="text-text-faint font-mono text-[11px]">—</span>;
  }

  return (
    <div className="flex flex-wrap gap-1">
      {entries.map(([code, val]) => (
        <span
          key={code}
          className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-emphasis/[0.04] border border-line text-xs font-mono"
        >
          <span className="text-[10px] text-primary/80 font-bold uppercase">
            {code}:
          </span>
          <span className="text-emphasis font-sans text-[11px] font-medium">
            {val}
          </span>
        </span>
      ))}
    </div>
  );
}
