"use client";

import { useState } from "react";
import { Check, Copy, KeyRound, TriangleAlert } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { copyText } from "@/lib/clipboard";
import type { CreatedPersonalAccessToken } from "@/lib/api";
import { Modal } from "@/components/ui/Modal";

/**
 * API Key 明文的一次性展示。
 *
 * 库里只有 sha256，明文只在创建响应里出现过：关掉就再也拿不回来。因此这里不做
 * "稍后再看"的入口，也不能把明文存进任何持久化状态——文案要如实说清这一点，
 * 而不是留给用户一个拿不回来的期待（与 OAuth client_secret 同一约定）。
 */
export function ApiKeyRevealModal({
  created,
  onClose,
}: {
  created: CreatedPersonalAccessToken | null;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const [copied, setCopied] = useState<"ok" | "failed" | null>(null);

  if (!created) return null;

  return (
    <Modal
      open
      onClose={onClose}
      title={t("developer.apiKeyCreatedTitle")}
      icon={<KeyRound className="w-4 h-4 text-warn" />}
    >
      <div className="space-y-3 text-xs">
        <div className="p-3 rounded-xl bg-amber-500/10 border border-amber-500/30 flex items-start gap-2">
          <TriangleAlert className="w-3.5 h-3.5 shrink-0 mt-0.5 text-warn" />
          <span className="text-amber-500 dark:text-warn-soft leading-relaxed font-medium">
            {t("developer.apiKeyCreatedBanner")}
          </span>
        </div>

        <div className="flex items-center gap-2">
          <code
            data-mf-apikey-token=""
            className="flex-1 px-2.5 py-1.5 rounded-lg bg-surfaceSubtle border border-primary/40 font-mono text-[11px] text-text-strong break-all select-all"
          >
            {created.token}
          </code>
          <button
            type="button"
            onClick={async () => setCopied((await copyText(created.token)) ? "ok" : "failed")}
            className="px-2.5 py-1.5 rounded-lg bg-primary hover:bg-primary/90 text-primary-foreground text-[11px] font-medium inline-flex items-center gap-1.5 cursor-pointer"
          >
            {copied === "ok" ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
            <span>{copied === "ok" ? t("developer.apiKeyCopied") : t("developer.apiKeyCopy")}</span>
          </button>
        </div>

        {copied === "failed" && (
          <p className="text-[11px] text-rose-500 dark:text-danger-soft leading-relaxed">{t("developer.apiKeyCopyFailed")}</p>
        )}

        <p className="text-[11px] text-text-faint leading-relaxed">{t("developer.apiKeyEnvHint")}</p>

        <div className="flex justify-end pt-1">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-1.5 rounded-lg bg-surfaceSubtle hover:bg-surfaceHover border border-line text-text-body text-xs font-medium cursor-pointer"
          >
            {t("developer.apiKeyCloseSaved")}
          </button>
        </div>
      </div>
    </Modal>
  );
}

export default ApiKeyRevealModal;
