"use client";

import React, { useState, useEffect, useRef } from "react";
import { X, GitMerge, AlertTriangle, CheckCircle2, Lock, LogIn } from "lucide-react";
import Link from "next/link";
import { catalogEntityHref, mergeEntities } from "@/lib/api";
import { localizeCatalogError } from "@/lib/catalogErrors";
import { useAuth } from "@/lib/authContext";
import { api, type Entity, title } from "@/components/catalog/api";
import { EntityPicker, ChangeNote } from "@/components/catalog/Fields";
import { isCompatibleMergeTarget, type MergeIdentity } from "@/lib/mergeTarget";
import { entityIdentitySuffix } from "@/lib/entityIdentity";
import { useI18n } from "@/i18n/I18nProvider";

const UUID_PATTERN = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

interface Props {
  isOpen: boolean;
  onClose: () => void;
  /** 实体 kind：决定合并后跳转的详情路由。 */
  targetType: string;
  sourceEntity: MergeIdentity & {
    id: string;
    title: string;
    sub?: string;
  };
  onMergeSuccess?: (targetId: string) => void;
}

export function EntityMergeModal({ isOpen, onClose, targetType, sourceEntity, onMergeSuccess }: Props) {
  const { user } = useAuth();
  const { t, locale } = useI18n();
  const [targetId, setTargetId] = useState("");
  const [mergeNote, setMergeNote] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  const [target, setTarget] = useState<Entity | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewFailed, setPreviewFailed] = useState(false);
  const [previewAttempt, setPreviewAttempt] = useState(0);
  const submitLock = useRef(false);

  useEffect(() => {
    setTargetId(""); setTarget(null); setMergeNote("");
    setError("");
  }, [isOpen, sourceEntity.id]);

  useEffect(() => {
    let active = true;
    setTarget(null);
    setPreviewFailed(false);
    const id = targetId.trim();
    if (!isOpen || !UUID_PATTERN.test(id)) { setPreviewLoading(false); return; }
    setPreviewLoading(true);
    api<Entity>(`/catalog/entities/${encodeURIComponent(id)}`)
      .then((entity) => { if (active) setTarget(entity); })
      .catch(() => { if (active) setPreviewFailed(true); })
      .finally(() => { if (active) setPreviewLoading(false); });
    return () => { active = false; };
  }, [targetId, isOpen, previewAttempt]);

  const targetReady = !!target && target.id?.toLowerCase() === targetId.trim().toLowerCase()
    && isCompatibleMergeTarget({ ...sourceEntity, kind: targetType }, target);
  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitLock.current) return;
    if (!user) {
      setError(t("editor.universal.needLoginToSave"));
      return;
    }
    if (!targetId.trim()) {
      setError(t("editor.merge.targetUuidPlaceholder"));
      return;
    }
    // 自合并（源与目标同一条）服务端必然回 invalid_merge_target：先在本地拦掉并讲清楚原因。
    if (!targetReady) {
      setError(t("catalog.error.invalidMergeTarget"));
      return;
    }

    submitLock.current = true;
    setSubmitting(true);
    setError("");
    try {
      const res = await mergeEntities({
        source_id: sourceEntity.id,
        target_id: target!.id!,
        merge_note: mergeNote.trim(),
        sources: [],
      });
      alert(res.message || t("editor.merge.successAlert"));
      onClose();
      if (onMergeSuccess) {
        onMergeSuccess(res.target_id);
      } else {
        // id 必须是 UUID，不把接口返回值原样当 URL；未知 kind 使用通用详情。
        const id = String(res.target_id || "");
        window.location.href = UUID_PATTERN.test(id) ? catalogEntityHref(id) : "/home";
      }
    } catch (err: any) {
      // 后端给稳定错误码（invalid_merge_target / invalid_state / evidence_required …）：
      // 走码表翻成人话，未知码才回退原文，不把裸码直接贴给用户。
      setError(localizeCatalogError(String(err?.message || ""), t) || t("editor.merge.failedMsg"));
    } finally {
      submitLock.current = false;
      setSubmitting(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/80 backdrop-blur-sm animate-in">
      <div className="w-full max-w-2xl flex flex-col rounded-panel border border-line bg-surface shadow-2xl overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-3.5 border-b border-emphasis/[0.08] bg-background/50">
          <div className="flex items-center gap-2.5">
            <div className="w-8 h-8 rounded-md bg-purple-500/10 border border-purple-500/20 grid place-items-center">
              <GitMerge className="w-4 h-4 text-alt" />
            </div>
            <div>
              <h2 className="text-sm font-semibold text-emphasis flex items-center gap-2">
                {t("editor.merge.title")}
              </h2>
              <p className="font-mono text-[10px] text-text-muted">
                {targetType.toUpperCase()}: {sourceEntity.title}
              </p>
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={submitting}
            aria-label={t("revisions.close")}
            className="w-7 h-7 grid place-items-center rounded-md hover:bg-emphasis/10 text-text-muted hover:text-emphasis transition-colors duration-fast ease-soft"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Warning Callout */}
        <div className="mx-5 mt-3.5 p-3 rounded-md bg-amber-500/10 border border-amber-500/20 space-y-1">
          <div className="flex items-center gap-1.5 text-xs font-semibold text-warn-soft">
            <AlertTriangle className="w-3.5 h-3.5 text-warn" />
            <span>{t("editor.merge.cautionLabel")}</span>
          </div>
          <p className="text-[11px] text-warn-soft/90 leading-relaxed font-mono">
            {t("editor.merge.warning")}
          </p>
        </div>

        {/* Auth warning if not logged in */}
        {!user && (
          <div className="mx-5 mt-2.5 p-3 rounded-md bg-rose-500/10 border border-rose-500/20 flex items-center justify-between gap-2.5 text-xs text-danger-soft">
            <div className="flex items-center gap-1.5">
              <Lock className="w-3.5 h-3.5 text-danger shrink-0" />
              <span>{t("editor.universal.unauthWarning")}</span>
            </div>
            <Link
              href="/login"
              className="px-2.5 py-1 rounded-md bg-rose-400 text-black font-semibold text-xs inline-flex items-center gap-1 shrink-0"
            >
              <LogIn className="w-3.5 h-3.5" />
              <span>{t("editor.universal.loginNow")}</span>
            </Link>
          </div>
        )}

        {/* Form Body */}
        <form onSubmit={handleSubmit} className="p-5 space-y-3 font-mono text-xs">
          {/* Source Entity Info */}
          <div className="space-y-1">
            <label className="text-text-muted text-[10px] uppercase tracking-wider">{t("editor.merge.sourceEntity")}</label>
            <div className="p-2.5 rounded-md bg-background/70 border border-line text-emphasis flex items-center justify-between">
              <div>
                <span className="font-semibold text-xs">{sourceEntity.title}</span>
                {sourceEntity.sub && <span className="text-text-muted ml-2 text-[11px]">({sourceEntity.sub})</span>}
              </div>
              <span className="text-[10px] text-text-faint font-mono">{sourceEntity.id}</span>
            </div>
          </div>

          {/* Target UUID Input */}
          <div className="space-y-1">
            <label className="block text-text-body font-semibold text-[10px] uppercase tracking-wider">
              {t("editor.merge.targetUuidLabel")} <span className="text-alt">*</span>
            </label>
            <input
              type="text"
              required
              value={targetId}
              onChange={(e) => setTargetId(e.target.value)}
              placeholder={t("editor.merge.targetUuidPlaceholder")}
              className="w-full px-3 h-10 rounded-md bg-background border border-line text-emphasis font-mono text-xs focus:outline-none focus:border-purple-400"
            />
          </div>

          <EntityPicker value={targetId} onChange={setTargetId} kinds={[targetType]} query="&state=active" />
          {previewLoading && <p role="status">{t("catalog.loading")}</p>}
          {previewFailed && <div role="alert" className="text-danger-soft"><p>{t("catalog.connectionError")}</p><button type="button" onClick={() => setPreviewAttempt((n) => n + 1)}>{t("catalog.retry")}</button></div>}
          {target && (
            <div className="rounded-md border border-line p-3 space-y-1" aria-live="polite">
              <Link href={catalogEntityHref(target.id || "")} target="_blank" rel="noopener noreferrer" className="text-primary hover:underline">
                {title(target, locale)}
              </Link>
              <p className="break-all text-text-muted">{entityIdentitySuffix(target)}</p>
              {!targetReady && <p role="alert" className="text-danger-soft">{t("catalog.error.invalidMergeTarget")}</p>}
            </div>
          )}

          <div className="catalog-v2">
            <ChangeNote note={mergeNote} setNote={setMergeNote} />
          </div>

          {error && (
            <div className="p-2.5 rounded-md bg-rose-500/10 border border-rose-500/20 text-danger-soft text-xs">
              {error}
            </div>
          )}

          {/* Footer Actions */}
          <div className="flex items-center justify-between pt-3 border-t border-emphasis/[0.08]">
            <span className="text-text-faint text-[10px] flex items-center gap-1">
              <CheckCircle2 className="w-3.5 h-3.5 text-alt" />
              <span>{t("editor.merge.footerSnapshot")}</span>
            </span>

            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={onClose}
            disabled={submitting}
                className="px-3 h-7 rounded-md border border-line text-xs text-text-body hover:bg-emphasis/10 transition-colors duration-fast ease-soft"
              >
                {t("editor.universal.cancel")}
              </button>
              <button
                type="submit"
                disabled={submitting || !user || !targetReady || previewLoading}
                className="px-3.5 h-7 rounded-md bg-purple-500 hover:bg-purple-400 text-emphasis text-xs font-semibold flex items-center gap-1.5 transition-colors duration-fast ease-soft disabled:opacity-40 disabled:cursor-not-allowed shadow-xs"
              >
                <GitMerge className="w-3.5 h-3.5" />
                <span>{submitting ? t("editor.merge.submitting") : t("editor.merge.submitBtn")}</span>
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>
  );
}
