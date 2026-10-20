"use client";
import { useI18n } from "@/i18n/I18nProvider";

export function ConflictValue({ value, present = true, depth = 0 }: { value: unknown; present?: boolean; depth?: number }) {
  const { t } = useI18n();
  if (!present) return <span className="text-text-muted">{t("collaboration.missingField")}</span>;
  if (value === null || value === undefined) return <span className="text-text-muted">{t("collaboration.noValue")}</span>;
  if (typeof value !== "object") return <span className="whitespace-pre-wrap break-words">{typeof value === "boolean" ? t(value ? "collaboration.valueTrue" : "collaboration.valueFalse") : String(value)}</span>;
  if (Array.isArray(value)) return !value.length ? <span className="text-text-muted">{t("collaboration.emptyList")}</span> : <ol className="space-y-2 border-l border-line pl-3">{value.map((item, index) => <li key={index}><ConflictValue value={item} depth={depth + 1} /></li>)}</ol>;
  const entries = Object.entries(value);
  if (!entries.length) return <span className="text-text-muted">{t("collaboration.noValue")}</span>;
  return <dl className={`space-y-2 ${depth ? "border-l border-line pl-3" : ""}`}>{entries.map(([key, item]) => <div key={key} className="min-w-0"><dt className="break-words text-xs font-medium text-text-muted">{key}</dt><dd className="mt-1 min-w-0"><ConflictValue value={item} depth={depth + 1} /></dd></div>)}</dl>;
}
