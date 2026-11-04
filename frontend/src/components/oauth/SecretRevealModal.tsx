"use client";

import { useState } from "react";
import { AlertTriangle, Check, Copy, KeyRound } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { Modal } from "@/components/ui/Modal";

/** 一次性密钥的展示内容：界面只持有它到弹窗关闭为止。 */
export interface SecretReveal {
  clientId: string;
  secret: string;
  /** created = 新建时签发，rotated = 轮换后签发；两者的提示文案不同。 */
  mode: "created" | "rotated";
}

/**
 * 一次性 client_secret 展示。
 * 库里只有 bcrypt 哈希，明文只在这一个响应里出现过：所以这里不做"稍后再看"的入口，
 * 关掉就只能重新轮换 —— 文案要如实说清，而不是留给用户一个拿不回来的期待。
 */
export function SecretRevealModal({
  reveal,
  onClose,
}: {
  reveal: SecretReveal | null;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const [copied, setCopied] = useState<"secret" | "client_id" | "failed" | null>(null);

  if (!reveal) return null;

  const copy = async (kind: "secret" | "client_id", value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(kind);
    } catch {
      setCopied("failed");
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={t("admin.oauth.secretTitle")}
      icon={<KeyRound className="w-4 h-4 text-warn" />}
    >
      <div className="space-y-3 text-xs">
        <p className="text-text-body leading-relaxed">
          {reveal.mode === "created"
            ? t("admin.oauth.secretCreatedNote", { client_id: reveal.clientId })
            : t("admin.oauth.secretRotatedNote", { client_id: reveal.clientId })}
        </p>

        <div className="p-3 rounded-xl bg-amber-500/10 border border-amber-500/30 flex items-start gap-2">
          <AlertTriangle className="w-3.5 h-3.5 shrink-0 mt-0.5 text-warn" />
          <span className="text-warn leading-relaxed font-medium">{t("admin.oauth.secretWarn")}</span>
        </div>

        <div className="space-y-1.5">
          <div className="text-[11px] font-mono text-text-muted">{t("admin.oauth.fieldClientId")}</div>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 px-2.5 py-1.5 rounded-lg bg-surfaceSubtle border border-line font-mono text-[11px] text-text-strong break-all">
              {reveal.clientId}
            </code>
            <button
              type="button"
              onClick={() => copy("client_id", reveal.clientId)}
              title={t("admin.oauth.secretCopy")}
              className="shrink-0 p-2 rounded-lg bg-surfaceSubtle hover:bg-surfaceHover border border-line text-text-body transition-colors duration-fast ease-soft cursor-pointer"
            >
              {copied === "client_id" ? <Check className="w-3.5 h-3.5 text-success" /> : <Copy className="w-3.5 h-3.5" />}
            </button>
          </div>
        </div>

        <div className="space-y-1.5">
          <div className="text-[11px] font-mono text-text-muted">{t("admin.oauth.secretLabel")}</div>
          <div className="flex items-center gap-2">
            <code
              data-mf-oauth-secret=""
              className="min-w-0 flex-1 px-2.5 py-1.5 rounded-lg bg-surfaceSubtle border border-primary/40 font-mono text-[11px] text-text-strong break-all select-all"
            >
              {reveal.secret}
            </code>
            <button
              type="button"
              onClick={() => copy("secret", reveal.secret)}
              className="shrink-0 whitespace-nowrap px-2.5 py-1.5 rounded-lg bg-primary hover:bg-primary/90 text-primary-foreground text-[11px] font-medium transition-colors duration-fast ease-soft cursor-pointer inline-flex items-center gap-1.5"
            >
              {copied === "secret" ? <Check className="w-3.5 h-3.5 shrink-0" /> : <Copy className="w-3.5 h-3.5 shrink-0" />}
              <span>{copied === "secret" ? t("admin.oauth.secretCopied") : t("admin.oauth.secretCopy")}</span>
            </button>
          </div>
          {copied === "failed" ? (
            <p className="text-[11px] text-danger leading-relaxed">{t("admin.oauth.secretCopyFailed")}</p>
          ) : null}
        </div>

        <div className="flex justify-end pt-1">
          <button
            type="button"
            onClick={onClose}
            className="shrink-0 whitespace-nowrap px-4 py-1.5 rounded-lg bg-surfaceSubtle hover:bg-surfaceHover border border-line text-text-body text-xs font-medium transition-colors duration-fast ease-soft cursor-pointer"
          >
            {t("admin.oauth.secretSaved")}
          </button>
        </div>
      </div>
    </Modal>
  );
}
