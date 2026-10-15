"use client";

// 「实例交换」管理台页签：导出实体快照 + 提交外部编辑提案。
//
// 两块能力彼此独立（导出是匿名可达的 GET，提案是只要求登录的 POST），
// 所以各自持有自己的请求状态：导出失败不阻塞粘贴提案，反之亦然。
//
// 文案一律走 t()/tr()；状态码与错误码交给 exchange.ts 的映射翻成人话，
// 未登记的码带上状态码原样显示，不把失败说成成功。

import { useState } from "react";
import {
  ArrowLeftRight,
  ClipboardCopy,
  Download,
  Info,
  Loader2,
} from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import {
  describeExchangeError,
  fetchExchangeEntity,
  type ExchangeEntity,
} from "@/lib/api/exchange";
import { ErrorNotice, SectionHeader, StatusMessage } from "@/components/common/Blocks";

/** 新建（entity.id 为空）时 expected_version 必须是 0，非 0 会被判成版本冲突。 */

const FIELD_CLASS =
  "w-full px-2.5 py-1.5 rounded-lg bg-surfaceSubtle border border-line text-xs text-text-strong placeholder:text-text-faint focus:border-primary outline-none";
const BUTTON_CLASS =
  "inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-surfaceSubtle hover:bg-surfaceHover border border-line text-text-body text-xs transition-colors duration-fast ease-soft cursor-pointer disabled:opacity-50";

export function ExchangeTab() {
  const { t } = useI18n();
  return (
    <div className="space-y-4">
      <div className="p-4 rounded-xl bg-surfaceSubtle border border-line-subtle space-y-3">
        <h2 className="text-base font-semibold text-text-strong flex items-center gap-2">
          <ArrowLeftRight className="w-4 h-4 text-primary" />
          <span>{t("admin.exchange.title")}</span>
        </h2>
        <p className="text-xs text-text-muted leading-relaxed">{t("admin.exchange.subtitle")}</p>

        <div className="p-3 rounded-xl bg-amber-500/[0.08] border border-amber-500/25 space-y-1">
          <div className="flex items-center gap-2 text-xs font-semibold text-amber-700 dark:text-warn-soft">
            <Info className="w-3.5 h-3.5 shrink-0" />
            <span>{t("admin.exchange.noticeTitle")}</span>
          </div>
          <p className="text-[11px] text-amber-800/80 dark:text-amber-100/70 leading-relaxed">
            {t("admin.exchange.noticeDesc")}
          </p>
        </div>
      </div>

      <ExportPanel />

    </div>
  );
}

function ExportPanel() {
  const { t } = useI18n();
  const [entityId, setEntityId] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [snapshot, setSnapshot] = useState<ExchangeEntity | null>(null);
  const [snapshotJson, setSnapshotJson] = useState("");
  const [expanded, setExpanded] = useState(true);
  const [notice, setNotice] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  const handleExport = async () => {
    const id = entityId.trim();
    setNotice(null);
    if (id === "") {
      setError(t("admin.exchange.exportIdRequired"));
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const data = await fetchExchangeEntity(id);
      setSnapshot(data);
      setSnapshotJson(JSON.stringify(data, null, 2));
      setExpanded(true);
    } catch (err) {
      // 失败时丢掉上一份快照：留着旧 JSON 会被当成这次导出的结果。
      setSnapshot(null);
      setSnapshotJson("");
      setError(describeExchangeError(err, t));
    } finally {
      setLoading(false);
    }
  };

  const handleCopy = async () => {
    try {
      // 非安全上下文（http 非 localhost）下 navigator.clipboard 不存在，必须走降级提示。
      if (typeof navigator === "undefined" || !navigator.clipboard || typeof navigator.clipboard.writeText !== "function") {
        throw new Error("clipboard_unavailable");
      }
      await navigator.clipboard.writeText(snapshotJson);
      setNotice({ kind: "ok", text: t("common.copied") });
    } catch {
      // 剪贴板被拒时展开全文，给用户一条手动复制的出口。
      setExpanded(true);
      setNotice({ kind: "err", text: t("admin.exchange.copyFailed") });
    }
  };

  const handleDownload = () => {
    const id = (snapshot?.id ?? entityId).trim();
    try {
      const blob = new Blob([snapshotJson], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `entity-${id || "snapshot"}.json`;
      document.body.appendChild(anchor);
      anchor.click();
      document.body.removeChild(anchor);
      // 立刻回收：objectURL 会一直占着内存直到页面卸载。
      URL.revokeObjectURL(url);
      setNotice(null);
    } catch {
      setNotice({ kind: "err", text: t("admin.exchange.downloadFailed") });
    }
  };

  const keyFields = snapshot
    ? [
        { label: t("admin.exchange.fieldId"), value: snapshot.id || "—" },
        { label: t("admin.entities.colKind"), value: snapshot.kind || "—" },
        { label: t("admin.entities.colVersion"), value: snapshot.version != null ? String(snapshot.version) : "—" },
        { label: t("admin.entities.colStatus"), value: snapshot.review_status || "—" },
        { label: t("admin.entities.colTitle"), value: snapshot.title || "—" },
      ]
    : [];

  return (
    <div className="space-y-3">
      <SectionHeader
        icon={<Download className="w-4 h-4 text-info" />}
        title={t("admin.exchange.exportTitle")}
        desc={t("admin.exchange.exportDesc")}
      />

      <div className="flex flex-col sm:flex-row gap-2">
        <label className="flex-1 min-w-0 space-y-1">
          <span className="block text-[11px] font-mono text-text-muted">{t("admin.exchange.exportIdLabel")}</span>
          <input
            type="text"
            value={entityId}
            onChange={(e) => setEntityId(e.target.value)}
            placeholder={t("admin.exchange.exportIdPlaceholder")}
            className={`${FIELD_CLASS} font-mono`}
          />
        </label>
        <button
          type="button"
          onClick={handleExport}
          disabled={loading}
          className="sm:self-end inline-flex items-center justify-center gap-1.5 px-4 py-2 rounded-lg bg-sky-500 hover:bg-sky-400 text-black font-semibold text-xs transition-colors duration-fast ease-soft disabled:opacity-50 cursor-pointer"
        >
          {loading ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Download className="w-3.5 h-3.5" />}
          <span>{loading ? t("admin.exchange.exportLoading") : t("admin.exchange.exportAction")}</span>
        </button>
      </div>
      <p className="text-[11px] text-text-faint leading-relaxed">{t("admin.exchange.exportVisibilityHint")}</p>

      {error ? <ErrorNotice message={error} onRetry={handleExport} /> : null}

      {snapshot ? (
        <div className="space-y-3">
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2">
            {keyFields.map((field) => (
              <div key={field.label} className="p-2.5 rounded-lg bg-surfaceSubtle border border-line-subtle min-w-0">
                <div className="text-[10px] font-mono text-text-faint">{field.label}</div>
                <div className="text-xs text-text-strong break-all">{field.value}</div>
              </div>
            ))}
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <button type="button" onClick={handleCopy} className={BUTTON_CLASS}>
              <ClipboardCopy className="w-3.5 h-3.5" />
              <span>{t("admin.exchange.copyJson")}</span>
            </button>
            <button type="button" onClick={handleDownload} className={BUTTON_CLASS}>
              <Download className="w-3.5 h-3.5" />
              <span>{t("admin.exchange.downloadJson")}</span>
            </button>
            <button type="button" onClick={() => setExpanded((prev) => !prev)} className={BUTTON_CLASS}>
              <span>{expanded ? t("common.collapse") : t("common.expand")}</span>
            </button>
          </div>

          {notice ? <StatusMessage kind={notice.kind} text={notice.text} /> : null}

          {expanded ? (
            <pre className="p-3 rounded-xl bg-surfaceSubtle border border-line-subtle overflow-auto text-[11px] font-mono text-text-body max-h-[420px]">
              {snapshotJson}
            </pre>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
