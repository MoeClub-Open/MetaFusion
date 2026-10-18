"use client";

import { useI18n } from "@/i18n/I18nProvider";
import { Select } from "@/components/ui/Select";
import type { AssistantCredential, AssistantModel } from "@/lib/assistantApi";
import { modelCreditRate } from "@/lib/assistantUser";

export function AssistantModelPicker({ models, credentials, modelID, credentialID, onModel, onCredential, disabled = false, compact = false }: {
  models: AssistantModel[]; credentials: AssistantCredential[]; modelID: string; credentialID: string;
  onModel: (id: string) => void; onCredential: (id: string) => void; disabled?: boolean; compact?: boolean;
}) {
  const { t, locale } = useI18n();
  const model = models.find((item) => item.id === modelID);
  return <div className={compact ? "min-w-0 flex-1 space-y-1" : "space-y-2"}>
    <div className={compact ? "grid grid-cols-2 gap-2" : "grid sm:grid-cols-2 gap-3"}>
      <label className="min-w-0 space-y-1 text-xs text-text-muted"><span className={compact ? "sr-only" : undefined}>{t("assistant.user.model")}</span>
        <Select aria-label={t("assistant.user.model")} value={modelID} onChange={onModel} disabled={disabled}
          className={compact ? "!h-8 !px-2 !text-xs" : undefined}
          placeholder={t("assistant.user.noModels")} options={models.map((item) => ({ value: item.id, label: item.name }))} />
      </label>
      <label className="min-w-0 space-y-1 text-xs text-text-muted"><span className={compact ? "sr-only" : undefined}>{t("assistant.user.billing")}</span>
        <Select aria-label={t("assistant.user.billing")} value={credentialID} onChange={onCredential} disabled={disabled}
          className={compact ? "!h-8 !px-2 !text-xs" : undefined}
          options={[{ value: "", label: t("assistant.user.platformCredits") }, ...credentials.filter((item) => item.enabled).map((item) => ({ value: item.id, label: item.name }))]} />
      </label>
    </div>
    <p className="text-[11px] text-text-muted">{credentialID ? t("assistant.user.byoBilling") : model ? t("assistant.user.rate", { rate: modelCreditRate(model, locale) }) : t("assistant.user.noModels")}</p>
  </div>;
}
