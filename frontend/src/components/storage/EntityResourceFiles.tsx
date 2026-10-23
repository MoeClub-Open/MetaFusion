"use client";

// 实体资源文件区块：列已绑定文件 + 上传入口。
//
// 上传链路（契约见 metafusion-storage/README 与 internal/handler）：
//   Web Crypto 算 sha256 → POST /upload/initiate（命中即秒传）
//   → direct_upload_url 流式 PUT / 预签名 PUT（不可达时回退到服务端流式接收）
//   → POST /upload/complete → POST /bind（binding_role 字段码）。
// 前端判定只用于"别把用户引到注定失败的按钮"，真正的授权仍在服务端。

import { useCallback, useEffect, useRef, useState } from "react";
import { AlertCircle, Check, Download, HardDrive, Loader2, Link2Off, RefreshCw, Upload, X } from "lucide-react";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import { STORAGE_ASSET_MODERATE, can } from "@/lib/permissions";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { Select } from "@/components/ui/Select";
import { getAuthLoginUrl } from "@/lib/services";
import type { InitiateUploadResponse, StorageFileBinding } from "@/lib/storage";
import {
  BINDING_ROLE_PATTERN,
  BINDING_ROLE_PRESETS,
  DEFAULT_BINDING_ROLE,
  StorageRequestError,
  bindAsset,
  completeUpload,
  deleteStorageBinding,
  downloadAssetFile,
  fetchEntityFiles,
  initiateUpload,
  putFile,
  sha256HexOfFile,
  storageErrorKey,
  streamUploadUrl,
} from "@/lib/storage";

type Stage = "idle" | "hashing" | "initiating" | "uploading" | "completing" | "binding" | "done";

type Message = { key: string; vars?: Record<string, string | number> };

const CUSTOM_ROLE = "__custom__";

/** 字节数展示：单位与语言无关，不进字典。 */
function formatSize(bytes: number): string {
  if (!bytes || bytes < 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value = value / 1024;
    unit += 1;
  }
  return `${unit === 0 ? value : value.toFixed(value < 10 ? 2 : 1)} ${units[unit]}`;
}

/** 列表错误单独归一：404 是"实体对存储服务不可见"，不是绑定失败。 */
function listErrorKey(err: unknown): Message {
  const mapped = storageErrorKey(err);
  if (mapped.key === "storage.files.errNotFound") return { key: "storage.files.listNotFound" };
  if (mapped.key === "storage.files.errForbidden") return { key: "storage.files.listForbidden" };
  if (mapped.key === "storage.files.errUnavailable" || mapped.key === "storage.files.errNetwork") {
    return { key: "storage.files.unavailable" };
  }
  return { key: "storage.files.loadFailed", vars: mapped.vars };
}

/** 下载错误的 404 是"文件读不到"（本区块已确认实体可见），沿用列表侧的文案键。 */
function downloadErrorKey(err: unknown): Message {
  const mapped = storageErrorKey(err);
  if (mapped.key === "storage.files.errNotFound") return { key: "storage.files.listNotFound" };
  return mapped;
}

/** 解绑失败归一：403 是"这条绑定不归你/没有治理码"，404 是"它已经不在了"（端点不幂等）。 */
function unbindErrorKey(err: unknown): Message {
  const mapped = storageErrorKey(err);
  if (mapped.key === "storage.files.errForbidden") return { key: "storage.files.unbindForbidden" };
  if (mapped.key === "storage.files.errNotFound") return { key: "storage.files.unbindGone" };
  return { key: "storage.files.unbindFailed", vars: mapped.vars };
}

export function EntityResourceFiles({ entityId, className }: { entityId: string; className?: string }) {
  const { t, locale } = useI18n();
  const { user, loading: authLoading } = useAuth();

  const [files, setFiles] = useState<StorageFileBinding[]>([]);
  const [listState, setListState] = useState<"loading" | "ready" | "error">("loading");
  const [listError, setListError] = useState<Message>({ key: "storage.files.loadFailed" });

  const [panelOpen, setPanelOpen] = useState(false);
  const [file, setFile] = useState<File | null>(null);
  const [roleChoice, setRoleChoice] = useState<string>(DEFAULT_BINDING_ROLE);
  const [customRole, setCustomRole] = useState("");

  const [stage, setStage] = useState<Stage>("idle");
  const [progress, setProgress] = useState(0);
  const [deduped, setDeduped] = useState(false);
  const [fellBack, setFellBack] = useState(false);
  const [error, setError] = useState<Message | null>(null);
  const [notice, setNotice] = useState<Message | null>(null);
  const [done, setDone] = useState<Message | null>(null);

  const [downloadingId, setDownloadingId] = useState("");
  const [downloadError, setDownloadError] = useState<Message | null>(null);
  const [downloadNote, setDownloadNote] = useState<Message | null>(null);
  // 待确认的解绑目标：解绑是破坏性动作（删的是绑定关系），确认框自绘并写清删的是哪一条。
  const [pendingUnbind, setPendingUnbind] = useState<StorageFileBinding | null>(null);
  const [unbinding, setUnbinding] = useState(false);

  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const abortRef = useRef<(() => void) | null>(null);
  const cancelledRef = useRef(false);

  const msg = useCallback(
    (m: Message) => (m.vars ? t(m.key, m.vars) : t(m.key)),
    [t]
  );

  const loadFiles = useCallback(async () => {
    if (!entityId) return;
    setListState("loading");
    try {
      const data = await fetchEntityFiles(entityId);
      // files 可能是 null（Go 的 nil slice，实测线上为空数组，两种都要能接住）。
      setFiles(Array.isArray(data.files) ? data.files : []);
      setListState("ready");
    } catch (err) {
      setListError(listErrorKey(err));
      setListState("error");
    }
  }, [entityId]);

  useEffect(() => {
    void loadFiles();
  }, [loadFiles]);

  const effectiveRole = (roleChoice === CUSTOM_ROLE ? customRole : roleChoice).trim().toLowerCase();
  const roleValid = BINDING_ROLE_PATTERN.test(effectiveRole);
  const busy = stage !== "idle" && stage !== "done";

  // 谁能解绑：与后端 Handler.unbind 同一口径——绑定创建者、文件上传者，或持
  // storage.asset.moderate 的治理者。前端判定只用于"别把按钮给注定 403 的人"，真正的授权在服务端。
  const canUnbind = (entry: StorageFileBinding) =>
    !!user && (entry.created_by === user.id || entry.asset.uploader_id === user.id || can(user, STORAGE_ASSET_MODERATE));

  const stageLabel: Record<Stage, string> = {
    idle: "",
    hashing: t("storage.files.stageHashing"),
    initiating: t("storage.files.stageInitiating"),
    uploading: t("storage.files.stageUploading"),
    completing: t("storage.files.stageCompleting"),
    binding: t("storage.files.stageBinding"),
    done: t("storage.files.done"),
  };

  /** 真正把字节送出去：优先 direct_upload_url，其次预签名，最后回退服务端流式接收。 */
  const transfer = useCallback(
    async (init: InitiateUploadResponse, target: File) => {
      const report = (loaded: number, total: number) => {
        setProgress(total > 0 ? Math.min(99, Math.round((loaded / total) * 100)) : 0);
      };
      const direct = init.direct_upload_url;
      const presigned = init.presigned_urls && init.presigned_urls[0];
      if (direct) {
        const url = new URL(direct, window.location.origin).toString();
        const job = putFile(url, target, {
          onProgress: report,
          errorCode: (code, status) => new StorageRequestError(code, status),
        });
        abortRef.current = job.abort;
        await job.promise;
        return;
      }
      if (presigned) {
        try {
          // 预签名地址自带签名，不能额外带 Authorization（会破坏签名校验）。
          const job = putFile(presigned, target, {
            onProgress: report,
            errorCode: (code, status) => new StorageRequestError(code, status),
          });
          abortRef.current = job.abort;
          await job.promise;
          return;
        } catch (err) {
          // 对象存储对外地址不可达或未开 CORS：README 明确流式端点可作兜底。
          const status = err instanceof StorageRequestError ? err.status : -1;
          if (status !== 0 && status !== 403) throw err;
          setFellBack(true);
        }
      }
      const job = putFile(streamUploadUrl(init.asset_id), target, {
        onProgress: report,
        errorCode: (code, status) => new StorageRequestError(code, status),
      });
      abortRef.current = job.abort;
      await job.promise;
    },
    []
  );

  const startUpload = useCallback(async () => {
    if (!file) {
      setError({ key: "storage.files.errFileRequired" });
      return;
    }
    if (file.size <= 0) {
      setError({ key: "storage.files.errEmpty" });
      return;
    }
    if (!roleValid) {
      setError({ key: "storage.files.errRole" });
      return;
    }
    cancelledRef.current = false;
    setError(null);
    setNotice(null);
    setDone(null);
    setDeduped(false);
    setFellBack(false);
    setProgress(0);
    setStage("hashing");
    try {
      const sha256 = await sha256HexOfFile(file);
      if (cancelledRef.current) return;
      // 同一份内容（sha256 相同）以同一用途重复绑定会被服务端的唯一键拒绝（500），
      // 在这里先说清楚"已经在了"，而不是把一次无意义的重复提交发给服务端。
      const known = files.find((x) => x.asset.sha256 === sha256 && x.binding_role === effectiveRole);
      if (known) {
        setNotice({ key: "storage.files.errDuplicate", vars: { role: effectiveRole } });
        setStage("idle");
        return;
      }
      setStage("initiating");
      const init = await initiateUpload({
        fileName: file.name,
        fileSize: file.size,
        sha256Hash: sha256,
        mimeType: file.type || "application/octet-stream",
        bindingRole: effectiveRole,
      });
      if (cancelledRef.current) return;
      if (init.is_instant_upload) {
        setDeduped(true);
      } else {
        setStage("uploading");
        await transfer(init, file);
      }
      if (cancelledRef.current) return;
      setProgress(100);
      setStage("completing");
      await completeUpload(init.asset_id, init.upload_id);
      // 取消只保证"不绑定"：内容可能已经落定在服务端，如实提示而不是假装全没发生。
      if (cancelledRef.current) {
        cancelledRef.current = false;
        setStage("idle");
        return;
      }
      setStage("binding");
      await bindAsset(init.asset_id, entityId, effectiveRole);
      setStage("done");
      setDone({ key: "storage.files.done" });
      setFile(null);
      if (fileInputRef.current) fileInputRef.current.value = "";
      await loadFiles();
    } catch (err) {
      if (cancelledRef.current) {
        cancelledRef.current = false;
        setStage("idle");
        return;
      }
      setError(storageErrorKey(err));
      setStage("idle");
    } finally {
      abortRef.current = null;
    }
  }, [effectiveRole, entityId, file, files, loadFiles, roleValid, transfer]);

  const cancelUpload = useCallback(() => {
    cancelledRef.current = true;
    if (abortRef.current) abortRef.current();
    abortRef.current = null;
    setStage("idle");
    setProgress(0);
    setError({ key: "storage.files.cancelled" });
  }, []);

  const onDownload = useCallback(async (entry: StorageFileBinding) => {
    setDownloadError(null);
    setDownloadNote(null);
    setDownloadingId(entry.asset_id);
    try {
      const result = await downloadAssetFile(entry.asset_id, entry.asset.file_name);
      // 对象存储模式的直链是"交出去"而不是"下载成功"：如实说明，别把它当成已完成。
      if (result.mode === "presigned") setDownloadNote({ key: "storage.files.downloadPresigned" });
    } catch (err) {
      setDownloadError(downloadErrorKey(err));
    } finally {
      setDownloadingId("");
    }
  }, []);

  /**
   * 解绑：绑错文件或绑错实体只能在这里纠正（下载/上传都改不了已有绑定）。
   * 契约（lib/storage.ts 的 deleteStorageBinding）：该端点**不幂等**——重复删同一个 id 的
   * 第二次是 404 not_found，所以无论成败都重新取数，不做乐观删除，否则列表里会留幽灵行。
   */
  const unbind = useCallback(async (entry: StorageFileBinding) => {
    setPendingUnbind(null);
    setUnbinding(true);
    setError(null);
    setNotice(null);
    try {
      await deleteStorageBinding(entry.id);
      setNotice({ key: "storage.files.unbindDone", vars: { name: entry.asset.file_name } });
    } catch (err) {
      setError(unbindErrorKey(err));
    } finally {
      setUnbinding(false);
      await loadFiles();
    }
  }, [loadFiles]);

  // 预设下拉不是封闭枚举：末项切到自由输入，仍按同一字段码规则校验。
  const roleOptions: { value: string; label: string }[] = BINDING_ROLE_PRESETS.map((code) => ({
    value: code,
    label: t(`storage.files.role.${code}`),
  }));
  roleOptions.push({ value: CUSTOM_ROLE, label: t("storage.files.roleCustom") });

  const dateLabel = (iso: string) => {
    if (!iso) return "—";
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString(locale);
  };

  return (
    <section
      id="resources"
      className={`rounded-xl border border-line bg-surface p-4 sm:p-5 space-y-4 shadow-soft ${className || ""}`}
    >
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line-subtle pb-2.5">
        <div className="flex items-center gap-2">
          <HardDrive className="w-4 h-4 text-primary" strokeWidth={1.5} />
          <h2 className="font-display text-sm font-bold text-text-strong uppercase tracking-wider font-mono">
            {t("storage.files.title")}
          </h2>
          {listState === "ready" && files.length > 0 && (
            <span className="px-2 py-0.5 rounded-chip bg-primary/10 text-primary font-mono text-[11px] font-semibold">
              {files.length}
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => void loadFiles()}
            disabled={listState === "loading"}
            title={t("storage.files.refresh")}
            className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md border border-line bg-background text-xs text-text-faint hover:text-text-strong hover:border-primary/40 transition-colors duration-fast ease-soft disabled:opacity-50"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${listState === "loading" ? "animate-spin" : ""}`} />
            <span className="max-sm:hidden">{t("storage.files.refresh")}</span>
          </button>
          {!panelOpen && (
            <button
              type="button"
              onClick={() => setPanelOpen(true)}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-primary text-white text-xs font-semibold hover:bg-primary/90 transition-colors duration-fast ease-soft"
            >
              <Upload className="w-3.5 h-3.5" />
              <span>{t("storage.files.uploadToggle")}</span>
            </button>
          )}
        </div>
      </div>

      <p className="text-[11px] text-text-faint leading-relaxed">{t("storage.files.subtitle")}</p>

      {panelOpen && (
        <div className="p-4 rounded-xl border border-line-subtle bg-black/[0.015] dark:bg-white/[0.015] space-y-3">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2 font-mono text-xs font-bold text-text-strong">
              <Upload className="w-3.5 h-3.5 text-primary" />
              <span>{t("storage.files.uploadTitle")}</span>
            </div>
            <button
              type="button"
              onClick={() => setPanelOpen(false)}
              disabled={busy}
              className="p-1.5 rounded-md text-text-muted hover:text-text-strong hover:bg-black/5 dark:hover:bg-white/5 disabled:opacity-40"
              title={t("storage.files.uploadClose")}
            >
              <X className="w-3.5 h-3.5" />
            </button>
          </div>

          <p className="text-[11px] text-text-faint leading-relaxed">{t("storage.files.dedupHint")}</p>

          {!authLoading && !user ? (
            <div className="flex flex-wrap items-center gap-3 p-3 rounded-lg border border-amber-500/30 bg-amber-500/[0.06] text-xs text-amber-600 dark:text-warn">
              <span>{t("storage.files.signInHint")}</span>
              <a
                href={getAuthLoginUrl()}
                className="inline-flex items-center gap-1 px-2.5 py-1 rounded-md bg-amber-500/15 font-semibold hover:bg-amber-500/25"
              >
                {t("storage.files.signIn")}
              </a>
            </div>
          ) : (
            <>
              <div className="space-y-1.5">
                <label className="block font-mono text-[11px] text-text-faint">{t("storage.files.pickFile")}</label>
                <input
                  ref={fileInputRef}
                  type="file"
                  disabled={busy}
                  onChange={(e) => {
                    setFile(e.target.files && e.target.files[0] ? e.target.files[0] : null);
                    setError(null);
                    setDone(null);
                  }}
                  className="block w-full text-xs text-text-faint file:mr-3 file:rounded-md file:border file:border-line file:bg-background file:px-3 file:py-2 file:text-xs file:font-semibold file:text-text-strong hover:file:border-primary/40 disabled:opacity-50"
                />
                {file && (
                  <p className="font-mono text-[11px] text-text-faint">
                    {t("storage.files.fileMeta", { name: file.name, size: formatSize(file.size) })}
                  </p>
                )}
              </div>

              <div className="space-y-1.5">
                <label className="block font-mono text-[11px] text-text-faint">{t("storage.files.roleLabel")}</label>
                <Select
                  value={roleChoice}
                  onChange={(v) => {
                    setRoleChoice(v);
                    setError(null);
                  }}
                  options={roleOptions}
                  disabled={busy}
                  className="text-xs"
                  aria-label={t("storage.files.roleLabel")}
                />
                {roleChoice === CUSTOM_ROLE && (
                  <input
                    type="text"
                    value={customRole}
                    disabled={busy}
                    onChange={(e) => setCustomRole(e.target.value)}
                    placeholder={t("storage.files.roleCustomPlaceholder")}
                    className="w-full h-9 px-3 rounded-md bg-background border border-line text-xs font-mono text-text-strong focus:outline-none focus-visible:border-primary disabled:opacity-50"
                  />
                )}
                <p className="text-[11px] text-text-faint">{t("storage.files.roleHint")}</p>
              </div>

              {error && (
                <p className="flex items-start gap-1.5 text-[11px] text-rose-500">
                  <AlertCircle className="w-3.5 h-3.5 mt-0.5 shrink-0" />
                  <span>{msg(error)}</span>
                </p>
              )}
              {notice && (
                <p className="flex items-start gap-1.5 text-[11px] text-amber-600 dark:text-warn">
                  <AlertCircle className="w-3.5 h-3.5 mt-0.5 shrink-0" />
                  <span>{msg(notice)}</span>
                </p>
              )}
              {done && (
                <p className="flex items-start gap-1.5 text-[11px] text-emerald-600 dark:text-success">
                  <Check className="w-3.5 h-3.5 mt-0.5 shrink-0" />
                  <span>{msg(done)}</span>
                </p>
              )}
              {busy && deduped && <p className="text-[11px] text-sky-600 dark:text-info">{t("storage.files.stageInstant")}</p>}
              {fellBack && <p className="text-[11px] text-sky-600 dark:text-info">{t("storage.files.stageFallback")}</p>}

              {busy && (
                <div className="space-y-1.5">
                  <div className="flex items-center justify-between font-mono text-[11px] text-text-faint">
                    <span className="inline-flex items-center gap-1.5">
                      <Loader2 className="w-3 h-3 animate-spin" />
                      {stageLabel[stage]}
                    </span>
                    {stage === "uploading" && <span>{progress}%</span>}
                  </div>
                  {stage === "uploading" && (
                    <div className="h-1.5 w-full rounded-full bg-black/[0.06] dark:bg-white/[0.08] overflow-hidden">
                      <div className="h-full bg-primary transition-all duration-200" style={{ width: `${progress}%` }} />
                    </div>
                  )}
                </div>
              )}

              <div className="flex items-center gap-2 pt-1">
                <button
                  type="button"
                  onClick={() => void startUpload()}
                  disabled={busy || !file || !roleValid}
                  className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-primary text-white text-xs font-semibold hover:bg-primary/90 transition-all disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {busy ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Upload className="w-3.5 h-3.5" />}
                  <span>{busy ? t("storage.files.busy") : t("storage.files.start")}</span>
                </button>
                {busy && (
                  <button
                    type="button"
                    onClick={cancelUpload}
                    className="inline-flex items-center gap-1.5 px-3 py-2 rounded-lg border border-line text-xs text-text-faint hover:text-text-strong transition-colors"
                  >
                    <X className="w-3.5 h-3.5" />
                    <span>{t("storage.files.cancel")}</span>
                  </button>
                )}
              </div>
            </>
          )}
        </div>
      )}

      {downloadError && (
        <p className="flex items-start gap-1.5 text-[11px] text-rose-500">
          <AlertCircle className="w-3.5 h-3.5 mt-0.5 shrink-0" />
          <span>{msg(downloadError)}</span>
        </p>
      )}

      {downloadNote && (
        <p className="flex items-start gap-1.5 text-[11px] text-sky-600 dark:text-info">
          <HardDrive className="w-3.5 h-3.5 mt-0.5 shrink-0" />
          <span>{msg(downloadNote)}</span>
        </p>
      )}

      {listState === "loading" && (
        <div className="flex items-center gap-2 font-mono text-xs text-text-faint">
          <Loader2 className="w-3.5 h-3.5 animate-spin" />
          <span>{t("storage.files.loading")}</span>
        </div>
      )}

      {listState === "error" && (
        <div className="flex flex-wrap items-center gap-3 p-3 rounded-lg border border-amber-500/30 bg-amber-500/[0.06] font-mono text-[11px] text-amber-600 dark:text-warn">
          <AlertCircle className="w-3.5 h-3.5" />
          <span>{msg(listError)}</span>
          <button
            type="button"
            onClick={() => void loadFiles()}
            className="px-2.5 py-1 rounded-md bg-amber-500/15 font-semibold hover:bg-amber-500/25"
          >
            {t("storage.files.retry")}
          </button>
        </div>
      )}

      {listState === "ready" && files.length === 0 && (
        <div className="p-6 rounded-xl border border-dashed border-line text-center text-xs text-text-faint">
          {t("storage.files.empty")}
        </div>
      )}

      {listState === "ready" && files.length > 0 && (
        <div className="divide-y divide-black/5 dark:divide-white/[0.06]">
          {files.map((entry) => {
            const pending = entry.asset.status !== "complete";
            return (
              <div key={entry.id} className="py-3 flex flex-wrap items-center gap-x-3 gap-y-2 text-xs">
                <div className="min-w-0 flex-1">
                  <p className="font-medium text-text-strong truncate" title={entry.asset.file_name}>
                    {entry.asset.file_name}
                  </p>
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1 mt-1 font-mono text-[11px] text-text-faint">
                    <span>{formatSize(entry.asset.size_bytes)}</span>
                    <span className="px-1.5 py-0.5 rounded bg-primary/10 text-primary font-semibold">{entry.binding_role}</span>
                    <span>{dateLabel(entry.created_at)}</span>
                    {pending ? (
                      <span className="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-600 dark:text-warn">
                        {t("storage.files.statusPending")}
                      </span>
                    ) : null}
                    {entry.asset.hash_verified && (
                      <span className="inline-flex items-center gap-0.5 text-emerald-600 dark:text-success">
                        <Check className="w-3 h-3" />
                        {t("storage.files.verified")}
                      </span>
                    )}
                  </div>
                </div>
                <button
                  type="button"
                  onClick={() => void onDownload(entry)}
                  disabled={pending || downloadingId === entry.asset_id}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-line bg-background text-[11px] font-semibold text-text-strong hover:border-primary/40 hover:text-primary transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {downloadingId === entry.asset_id ? (
                    <Loader2 className="w-3.5 h-3.5 animate-spin" />
                  ) : (
                    <Download className="w-3.5 h-3.5" />
                  )}
                  <span>{downloadingId === entry.asset_id ? t("storage.files.downloading") : t("storage.files.download")}</span>
                </button>
                {canUnbind(entry) && (
                  <button
                    type="button"
                    onClick={() => setPendingUnbind(entry)}
                    disabled={unbinding}
                    title={t("storage.files.unbind")}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-line bg-background text-[11px] font-semibold text-text-faint hover:border-rose-500/40 hover:text-rose-500 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    <Link2Off className="w-3.5 h-3.5" />
                    <span>{t("storage.files.unbind")}</span>
                  </button>
                )}
              </div>
            );
          })}
        </div>
      )}

      {/* 解绑确认：删的是绑定关系（文件本体还在），所以说明里写清"解绑的是哪一个文件、哪种用途"。 */}
      <ConfirmDialog
        open={pendingUnbind !== null}
        title={t("storage.files.unbind")}
        message={t("storage.files.unbindConfirm", {
          name: pendingUnbind?.asset.file_name ?? "",
          role: pendingUnbind?.binding_role ?? "",
        })}
        confirmLabel={t("storage.files.unbind")}
        busy={unbinding}
        onClose={() => setPendingUnbind(null)}
        onConfirm={() => { if (pendingUnbind) void unbind(pendingUnbind); }}
      />
    </section>
  );
}
