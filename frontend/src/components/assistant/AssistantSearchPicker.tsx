"use client";

import { Globe, KeyRound } from "lucide-react";
import { Select } from "@/components/ui/Select";
import { useI18n } from "@/i18n/I18nProvider";
import type { AssistantBYOProvider, SearchProvider } from "@/lib/assistantApi";

export function AssistantSearchPicker({ providers, byoProviders, providerID, byoProviderID, onProvider, onBYOProvider, disabled, inline = false }: {
  providers: SearchProvider[]; byoProviders: AssistantBYOProvider[]; providerID: string; byoProviderID: string;
  onProvider: (id: string) => void; onBYOProvider: (id: string) => void; disabled?: boolean; inline?: boolean;
}) {
  const { t } = useI18n();
  const provider = providers.find(item => providerID ? item.id === providerID : item.is_default);
  const providerOptions = [
    { value: "", label: t("assistant.search.default") },
    ...providers.map(item => ({ value: item.id, label: `${item.name} · ${item.credits_per_request} ${t("assistant.search.perRequest")}` })),
  ];
  const byoOptions = [
    { value: "", label: t("assistant.user.noBYOProvider") },
    ...(byoProviderID && !byoProviders.some(item => item.id === byoProviderID) ? [{ value: byoProviderID, label: t("curator.boundBYOProvider") }] : []),
    ...byoProviders.filter(item => item.enabled && item.has_key).map(item => ({ value: item.id, label: item.name })),
  ];
  if (inline) {
    return (
      <>
        <div className="min-w-0 max-w-full" title={t("assistant.search.provider")}>
          <Select
            fullWidth={false}
            prefix={<Globe className="size-3.5 text-text-muted" />}
            aria-label={t("assistant.search.provider")}
            value={providerID}
            onChange={id => { onProvider(id); onBYOProvider(""); }}
            disabled={disabled}
            className="!h-8 max-w-[12rem] sm:max-w-[14rem] !bg-surface hover:!bg-surfaceHover !px-2.5 !text-xs"
            options={providerOptions}
          />
        </div>
        {provider?.kind === "deepseek" && (
          <div className="min-w-0 max-w-full" title={t("assistant.search.byoProvider")}>
            <Select
              fullWidth={false}
              prefix={<KeyRound className="size-3.5 text-text-muted" />}
              aria-label={t("assistant.search.byoProvider")}
              value={byoProviderID}
              onChange={onBYOProvider}
              disabled={disabled}
              className="!h-8 max-w-[11rem] !bg-surface hover:!bg-surfaceHover !px-2.5 !text-xs"
              options={byoOptions}
            />
          </div>
        )}
      </>
    );
  }
  return <div className="grid gap-2 sm:grid-cols-2">
    <label className="min-w-0 space-y-1 text-xs text-text-muted"><span>{t("assistant.search.provider")}</span>
      <Select value={providerID} onChange={id => { onProvider(id); onBYOProvider(""); }} disabled={disabled}
        className="!h-8 !px-2.5 !text-xs"
        options={providerOptions} />
    </label>
    {provider?.kind === "deepseek" && <label className="min-w-0 space-y-1 text-xs text-text-muted"><span>{t("assistant.search.byoProvider")}</span>
      <Select value={byoProviderID} onChange={onBYOProvider} disabled={disabled}
        className="!h-8 !px-2.5 !text-xs"
        options={byoOptions} />
    </label>}
  </div>;
}
