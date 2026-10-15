"use client";

import { useId, useMemo, useState } from "react";
import { useI18n } from "@/i18n/I18nProvider";

export function PermissionScopePicker({ options, value, onChange, grantable = options, disabled = false }: {
  options: string[]; value: string[]; onChange: (value: string[]) => void;
  grantable?: string[]; disabled?: boolean;
}) {
  const { t } = useI18n();
  const id = useId();
  const [search, setSearch] = useState("");
  const label = (scope: string) => {
    const key = scope === "mcp.read" ? "developer.mcp.read" : scope === "mcp.web.write" ? "developer.mcp.webWrite" : `developer.apiKeyScope.${scope}`;
    const text = t(key); return text === key ? scope : text;
  };
  const groups = useMemo(() => {
    const grouped = new Map<string, string[]>();
    for (const scope of Array.from(new Set(options))) {
      const service = scope.split(".")[0];
      grouped.set(service, [...(grouped.get(service) || []), scope]);
    }
    return Array.from(grouped);
  }, [options]);
  const query = search.trim().toLocaleLowerCase();
  return <div className="space-y-3">
    <label className="block text-xs text-text-muted" htmlFor={id}>{t("developer.permissions.search")}</label>
    <input id={id} type="search" value={search} onChange={(event) => setSearch(event.target.value)} className="w-full rounded-lg border border-line bg-surfaceSubtle px-3 py-2 text-sm text-text-body" />
    <p className="text-xs text-text-muted">{t("developer.apiKeySelected", { count: value.length, total: options.length })}</p>
    {groups.map(([service, codes]) => {
      const visible = codes.filter((code) => !query || `${label(code)} ${code}`.toLocaleLowerCase().includes(query));
      if (!visible.length) return null;
      const key = `developer.apiKeyScopeGroup.${service}`;
      const title = t(key) === key ? service : t(key);
      return <details key={service} className="rounded-lg border border-line bg-surfaceSubtle" open={query ? true : undefined}>
        <summary className="cursor-pointer px-3 py-2 text-sm text-text-strong">{title}<span className="ml-2 text-xs text-text-muted">{codes.filter((code) => value.includes(code)).length}/{codes.length}</span></summary>
        <fieldset disabled={disabled} className="space-y-2 border-t border-line-subtle p-3">
          {visible.map((code) => <label key={code} className="flex items-start gap-2 text-xs text-text-body">
            <input type="checkbox" checked={value.includes(code)} disabled={!grantable.includes(code) && !value.includes(code)}
              onChange={(event) => onChange(event.target.checked ? [...value, code] : value.filter((item) => item !== code))} className="mt-0.5 accent-primary" />
            <span><span className="block">{label(code)}</span><code className="break-all text-[11px] text-text-faint">{code}</code></span>
          </label>)}
        </fieldset>
      </details>;
    })}
  </div>;
}
