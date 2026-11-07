"use client";

import { Select } from "@/components/ui/Select";
import { useI18n } from "@/i18n/I18nProvider";
import type { AssistantBYOProvider, SearchProvider } from "@/lib/assistantApi";

export function AssistantSearchPicker({ providers, byoProviders, providerID, byoProviderID, onProvider, onBYOProvider, disabled }: {
  providers: SearchProvider[]; byoProviders: AssistantBYOProvider[]; providerID: string; byoProviderID: string;
  onProvider: (id: string) => void; onBYOProvider: (id: string) => void; disabled?: boolean;
}) {
  const { t } = useI18n();
  const provider = providers.find(item => providerID ? item.id === providerID : item.is_default);
  return <div className="grid gap-2 sm:grid-cols-2">
    <label className="min-w-0 space-y-1 text-xs text-text-muted"><span>{t("assistant.search.provider")}</span>
      <Select value={providerID} onChange={id => { onProvider(id); onBYOProvider(""); }} disabled={disabled}
        options={[{ value: "", label: t("assistant.search.default") }, ...providers.map(item => ({ value: item.id, label: `${item.name} · ${item.credits_per_request} ${t("assistant.search.perRequest")}` }))]} />
    </label>
    {provider?.kind === "deepseek" && <label className="min-w-0 space-y-1 text-xs text-text-muted"><span>{t("assistant.search.byoProvider")}</span>
      <Select value={byoProviderID} onChange={onBYOProvider} disabled={disabled}
        options={[{ value: "", label: t("assistant.user.noBYOProvider") }, ...(byoProviderID && !byoProviders.some(item => item.id === byoProviderID) ? [{ value: byoProviderID, label: t("curator.boundBYOProvider") }] : []), ...byoProviders.filter(item => item.enabled && item.has_key).map(item => ({ value: item.id, label: item.name }))]} />
    </label>}
  </div>;
}
