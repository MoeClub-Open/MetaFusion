"use client";

import { useI18n } from "@/i18n/I18nProvider";
import { Select } from "@/components/ui/Select";
import type { AssistantBYOModel, AssistantBYOProvider, AssistantModel } from "@/lib/assistantApi";
import { modelCreditRate } from "@/lib/assistantUser";

export function AssistantModelPicker({ models, byoProviders = [], byoModels = [], modelID, onModel, disabled = false, compact = false }: {
  models: AssistantModel[]; byoProviders?: AssistantBYOProvider[]; byoModels?: AssistantBYOModel[]; modelID: string;
  onModel: (id: string) => void; disabled?: boolean; compact?: boolean;
}) {
  const { t, locale } = useI18n();
  const activeProviderIDs = new Set(byoProviders.filter((item) => item.enabled && item.has_key).map((item) => item.id));
  const activeBYOModels = byoModels.filter((item) => item.enabled && activeProviderIDs.has(item.provider_id));
  const model = models.find((item) => item.id === modelID);
  const byoModel = activeBYOModels.find((item) => item.id === modelID);
  const options = [
    ...models.map((item) => ({ value: item.id, label: item.name })),
    ...activeBYOModels.map((item) => ({ value: item.id, label: item.name + " · " + t("assistant.user.byo") })),
  ];
  return <div className={compact ? "min-w-0 flex-1 space-y-1" : "space-y-2"}>
    <label className="block min-w-0 space-y-1 text-xs text-text-muted"><span className={compact ? "sr-only" : undefined}>{t("assistant.user.model")}</span>
      <Select aria-label={t("assistant.user.model")} value={modelID} onChange={onModel} disabled={disabled}
        className={compact ? "!h-8 !px-2 !text-xs" : undefined}
        placeholder={t("assistant.user.noModels")} options={options} />
    </label>
    {!compact && <p className="text-[11px] text-text-muted">{byoModel ? t("assistant.user.byoBilling") : model ? t("assistant.user.rate", { rate: modelCreditRate(model, locale) }) : t("assistant.user.noModels")}</p>}
  </div>;
}
