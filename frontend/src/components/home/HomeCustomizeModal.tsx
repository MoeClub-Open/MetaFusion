"use client";

// 自定义首页推荐弹窗：分区编辑器。
// 设计口径（与用户诉求一致）：系统预设就是**首页默认布局本身**——「恢复默认」即清空偏好
// 回落到它，所以这里不再有第二条"模板"入口：唯一的追加入口是「添加分区」，候选里既列
// 系统预设分区（把隐藏掉的预设加回列表），也提供空白自建分区；用户改的是自己的偏好副本
// ——改标题 / 换规则 / 换图标 / 调顺序 / 隐藏，系统货架本身不被改动，也不影响别人。
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Check,
  ChevronDown,
  ChevronUp,
  Plus,
  RotateCcw,
  Sliders,
  Trash2,
  X,
} from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import { Modal } from "@/components/ui/Modal";
import type { DynamicDefinitions } from "@/lib/definitions";
import { SectionRuleEditor } from "@/components/home/SectionRuleEditor";
import {
  buildRows,
  describeRule,
  hasRequiredName,
  iconFor,
  isValidSlug,
  revertToTemplate,
  rowFromPreset,
  rowFromScratch,
  shelfTitle,
  toPreferences,
  type HomePreferences,
  type SectionRow,
  type ShelfLike,
} from "@/lib/homeSections";

/** feed 里的一条分区：弹窗只用得到货架定义与条目数。 */
type FeedLike = { shelf: ShelfLike; items?: unknown[]; total?: number };

type Props = {
  open: boolean;
  loading: boolean;
  prefs: HomePreferences;
  /** GET /catalog/shelves 的系统预设：既是「添加分区」的候选，也是隐藏行的定义来源。 */
  templates: ShelfLike[];
  feedSections: FeedLike[];
  defs: DynamicDefinitions | null;
  saving: boolean;
  loadFailed: boolean;
  error: string;
  onClose: () => void;
  onSave: (payload: HomePreferences) => void;
  onReset: () => void;
};

export function HomeCustomizeModal({
  open,
  loading,
  prefs,
  templates,
  feedSections,
  defs,
  saving,
  loadFailed,
  error,
  onClose,
  onSave,
  onReset,
}: Props) {
  const { t, locale } = useI18n();
  const [rows, setRows] = useState<SectionRow[]>([]);
  // 展开单个分区：面板本身很长，同时展开多行会把列表挤没。
  const [expanded, setExpanded] = useState<number | null>(null);
  // 「添加分区」的候选面板：展开时列出系统预设分区与"空白分区"两个来源。
  const [adding, setAdding] = useState(false);
  // 待确认的破坏性动作：删除某一行 / 恢复默认。确认框见文件尾。
  const [pendingRemove, setPendingRemove] = useState<number | null>(null);
  const [pendingReset, setPendingReset] = useState(false);
  const [localError, setLocalError] = useState("");
  const closeModal = useCallback(() => { if (!saving) onClose(); }, [onClose, saving]);

  const counts = useMemo(() => {
    const map = new Map<string, number>();
    for (const section of feedSections)
      map.set(section.shelf.slug, section.total ?? (section.items || []).length);
    return map;
  }, [feedSections]);

  // 打开（或偏好加载完成）时用服务端数据重建行；加载中不重建，避免把用户的编辑冲掉。
  useEffect(() => {
    if (!open || loading) return;
    setRows(buildRows(prefs, templates, feedSections.map((s) => s.shelf)));
    setExpanded(null);
    setAdding(false);
    setLocalError("");
  }, [open, loading, prefs, templates, feedSections]);

  if (!open) return null;

  const taken = new Set(rows.map((r) => r.slug));

  const patchRow = (index: number, patch: Partial<SectionRow>) => {
    setRows((prev) =>
      prev.map((row, i) => (i === index ? { ...row, ...patch, overridden: true } : row)),
    );
  };

  const toggleHidden = (index: number) => {
    setRows((prev) => prev.map((row, i) => (i === index ? { ...row, hidden: !row.hidden } : row)));
  };

  const move = (index: number, dir: -1 | 1) => {
    const target = index + dir;
    if (target < 0 || target >= rows.length) return;
    setRows((prev) => {
      const next = [...prev];
      const [item] = next.splice(index, 1);
      next.splice(target, 0, item!);
      return next;
    });
    // 展开态跟着被移动的那一行走（相邻交换，两行互换即可）。
    setExpanded((prev) => (prev === index ? target : prev === target ? index : prev));
  };

  const removeRow = (index: number) => {
    setPendingRemove(index);
  };

  const confirmRemove = () => {
    const index = pendingRemove;
    setPendingRemove(null);
    if (index === null) return;
    setRows((prev) => prev.filter((_, i) => i !== index));
    setExpanded((prev) => (prev === null || prev === index ? null : prev > index ? prev - 1 : prev));
  };

  const revertRow = (index: number) => {
    setRows((prev) => prev.map((row, i) => (i === index ? revertToTemplate(row) : row)));
  };

  const addBlank = () => {
    const row = rowFromScratch(locale, taken, t("home.customizeNewSection"));
    setRows((prev) => [...prev, row]);
    setExpanded(rows.length);
    setAdding(false);
  };

  // 预设分区"加回列表"：列表里已有该 slug（隐藏/被改过）就把它显示出来，用户的改动保留
  // （要回到预设用行内的"还原为预设"）；列表里没有才按预设身份插回末尾。
  // 两种情况都不新建副本——同一个预设出现两份会让"恢复默认"的语义变模糊。
  const addPreset = (template: ShelfLike) => {
    const index = rows.findIndex((row) => row.slug === template.slug);
    if (index >= 0) {
      setRows((prev) => prev.map((row, i) => (i === index ? { ...row, hidden: false } : row)));
      setExpanded(index);
    } else {
      setRows((prev) => [...prev, rowFromPreset(template)]);
      setExpanded(rows.length);
    }
    setAdding(false);
  };

  const validate = (): string => {
    const seen = new Set<string>();
    for (const row of rows) {
      if (!hasRequiredName(row.names)) {
        return t("home.customizeError.nameRequired");
      }
      if (!isValidSlug(row.slug)) return t("home.customizeError.slugInvalid");
      if (seen.has(row.slug)) return t("home.customizeError.slugTaken");
      seen.add(row.slug);
    }
    return "";
  };

  const handleSave = () => {
    const message = validate();
    if (message) {
      setLocalError(message);
      return;
    }
    setLocalError("");
    onSave(toPreferences(rows));
  };

  const handleReset = () => {
    setPendingReset(true);
  };

  const confirmReset = () => {
    setPendingReset(false);
    setLocalError("");
    onReset();
  };

  const shownError = localError || error;
  const footer = (
    <div className="flex flex-wrap items-center justify-between gap-2">
      {shownError && <p role="alert" className="w-full min-w-0 break-words text-[11px] text-danger font-mono">{shownError}</p>}
      <button type="button" onClick={handleReset} disabled={saving || loading || loadFailed} className="inline-flex min-h-11 items-center gap-1.5 text-xs font-mono text-text-muted hover:text-emphasis transition-colors duration-fast ease-soft disabled:opacity-50 cursor-pointer">
        <RotateCcw className="w-3.5 h-3.5 shrink-0" />
        <span>{t("home.customizeReset")}</span>
      </button>
      <div className="ml-auto flex items-center gap-2">
        <button type="button" onClick={closeModal} disabled={saving} className="min-h-11 px-3 py-2 rounded-lg border border-line text-xs text-text-body hover:text-emphasis hover:bg-surfaceHover transition-colors duration-fast ease-soft disabled:opacity-50 cursor-pointer">{t("catalog.cancel")}</button>
        <button type="button" onClick={handleSave} disabled={saving || loading || loadFailed} className="min-h-11 px-4 py-2 rounded-lg bg-primary hover:bg-primary/90 text-white text-xs font-semibold transition-colors duration-fast ease-soft disabled:opacity-50 cursor-pointer">{saving ? t("common.saving") : t("common.save")}</button>
      </div>
    </div>
  );

  return (
    <>
      <Modal open={open} onClose={closeModal} title={t("home.customizeTitle")} icon={<Sliders className="w-4 h-4 shrink-0 text-primary" />} maxWidth="max-w-3xl" footer={footer}>
        <fieldset disabled={saving || loading || loadFailed} className="min-w-0 space-y-3">
          <p className="text-xs text-text-faint">{t("home.customizeHint")}</p>

          <button
            type="button"
            onClick={() => setAdding((v) => !v)}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-line bg-emphasis/[0.03] hover:bg-emphasis/[0.08] text-xs font-medium text-text-body hover:text-emphasis transition-colors duration-fast ease-soft cursor-pointer"
          >
            <Plus className="w-3.5 h-3.5" />
            <span>{t("home.customizeAdd")}</span>
          </button>

          {adding && (
            <div className="p-3 rounded-lg border border-primary/25 bg-primary/[0.04] space-y-2">
              <div className="flex items-center justify-between">
                <span className="text-[11px] font-mono font-bold text-text-body">{t("home.customizeAdd")}</span>
                <button
                  type="button"
                  onClick={() => setAdding(false)}
                  className="p-1 rounded hover:bg-surfaceHover text-text-muted hover:text-emphasis cursor-pointer"
                  aria-label={t("catalog.cancel")}
                >
                  <X className="w-3 h-3" />
                </button>
              </div>
              <p className="text-[11px] text-text-faint">{t("home.customizeAddHint")}</p>
              <div className="flex flex-wrap gap-1.5 max-h-40 overflow-y-auto">
                <button
                  type="button"
                  onClick={addBlank}
                  className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg border border-line bg-emphasis/[0.03] hover:bg-emphasis/[0.08] text-xs text-text-body hover:text-emphasis transition-colors duration-fast ease-soft cursor-pointer"
                >
                  <Plus className="w-3.5 h-3.5 text-primary" />
                  <span>{t("home.customizeAddBlank")}</span>
                </button>
                {templates.length === 0 ? (
                  <span className="text-[11px] text-text-faint font-mono">{t("home.customizeAddEmpty")}</span>
                ) : (
                  templates.map((template) => {
                    const Icon = iconFor(template);
                    // 已在列表里的预设标出隐藏态：点一下就是把它显示回来，不会再造一份。
                    const hidden = rows.some((row) => row.slug === template.slug && row.hidden);
                    return (
                      <button
                        key={template.slug}
                        type="button"
                        onClick={() => addPreset(template)}
                        className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg border border-line bg-emphasis/[0.03] hover:bg-emphasis/[0.08] text-xs text-text-body hover:text-emphasis transition-colors duration-fast ease-soft cursor-pointer"
                      >
                        <Icon className="w-3.5 h-3.5 text-primary" />
                        <span className="min-w-0 break-all">{shelfTitle(template, locale)}</span>
                        {hidden && (
                          <span className="text-[10px] font-mono text-text-faint">{t("home.customizeAddHidden")}</span>
                        )}
                      </button>
                    );
                  })
                )}
              </div>
            </div>
          )}

          {loading ? (
            <p className="text-xs text-text-faint py-8 text-center font-mono">{t("common.loadingGeneric")}</p>
          ) : rows.length === 0 ? (
            <p className="text-xs text-text-faint py-8 text-center">{t("home.customizeSectionsEmpty")}</p>
          ) : (
            rows.map((row, index) => {
              const Icon = iconFor(row);
              const rule = describeRule(row.query, defs, locale);
              const isOpen = expanded === index;
              const count = counts.get(row.slug);
              return (
                <div
                  key={index}
                  className="rounded-lg border border-emphasis/[0.08] bg-emphasis/[0.02] overflow-hidden"
                >
                  <div className="grid grid-cols-[auto_auto_minmax(0,1fr)] sm:grid-cols-[auto_auto_minmax(0,1fr)_auto] items-center gap-2 px-3 py-2">
                    <button
                      type="button"
                      onClick={() => toggleHidden(index)}
                      className={
                        "w-5 h-5 rounded border grid place-items-center shrink-0 transition-colors duration-fast ease-soft cursor-pointer " +
                        (row.hidden
                          ? "border-emphasis/15 bg-transparent text-transparent"
                          : "border-primary bg-primary text-white")
                      }
                      aria-pressed={!row.hidden}
                      aria-label={t("home.toggleSection")}
                    >
                      <Check className="w-3 h-3" />
                    </button>
                    <Icon className={"w-4 h-4 shrink-0 " + (row.hidden ? "text-gray-600" : "text-primary")} />
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span
                          className={
                            "text-xs truncate " + (row.hidden ? "text-gray-600 line-through" : "text-text-strong")
                          }
                        >
                          {shelfTitle(row, locale)}
                        </span>
                        <span className="px-1.5 py-0.5 rounded bg-emphasis/[0.06] text-text-faint text-[10px] font-mono shrink-0">
                          {row.custom ? t("home.customizeSourceCustom") : t("home.customizeSourceSystem")}
                        </span>
                      </div>
                      <div className="text-[10px] text-text-faint font-mono truncate">
                        {rule.length > 0 ? rule.join(" · ") : t("home.customizeRuleAll")}
                      </div>
                    </div>
                    <div className="col-span-3 flex flex-wrap items-center justify-end gap-1 sm:col-span-1">
                    {typeof count === "number" && (
                      <span className="px-2 py-0.5 rounded-chip bg-emphasis/[0.06] text-text-muted text-[10px] font-mono shrink-0">
                        {t("home.itemCount", { count: count.toString() })}
                      </span>
                    )}
                    <button
                      type="button"
                      disabled={index === 0}
                      onClick={() => move(index, -1)}
                      className="p-1 rounded hover:bg-surfaceHover text-text-muted hover:text-emphasis disabled:opacity-25 disabled:pointer-events-none cursor-pointer"
                      aria-label={t("home.moveUp")}
                    >
                      <ChevronUp className="w-3.5 h-3.5" />
                    </button>
                    <button
                      type="button"
                      disabled={index === rows.length - 1}
                      onClick={() => move(index, 1)}
                      className="p-1 rounded hover:bg-surfaceHover text-text-muted hover:text-emphasis disabled:opacity-25 disabled:pointer-events-none cursor-pointer"
                      aria-label={t("home.moveDown")}
                    >
                      <ChevronDown className="w-3.5 h-3.5" />
                    </button>
                    {row.template && row.overridden && (
                      <button
                        type="button"
                        onClick={() => revertRow(index)}
                        className="p-1 rounded hover:bg-surfaceHover text-text-muted hover:text-emphasis cursor-pointer"
                        title={t("home.customizeRevert")}
                        aria-label={t("home.customizeRevert")}
                      >
                        <RotateCcw className="w-3.5 h-3.5" />
                      </button>
                    )}
                    {row.custom && (
                      <button
                        type="button"
                        onClick={() => removeRow(index)}
                        className="p-1 rounded hover:bg-rose-500/10 text-text-muted hover:text-danger cursor-pointer"
                        title={t("home.customizeDelete")}
                        aria-label={t("home.customizeDelete")}
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    )}
                    <button
                      type="button"
                      onClick={() => setExpanded(isOpen ? null : index)}
                      className={
                        "p-1 rounded hover:bg-surfaceHover cursor-pointer " + (isOpen ? "text-primary" : "text-text-muted hover:text-emphasis")
                      }
                      title={isOpen ? t("home.customizeCollapse") : t("common.edit")}
                      aria-label={isOpen ? t("home.customizeCollapse") : t("common.edit")}
                      aria-expanded={isOpen}
                    >
                      <Sliders className="w-3.5 h-3.5" />
                    </button>
                    </div>
                  </div>
                  {isOpen && (
                    <div className="px-3 py-3 border-t border-emphasis/[0.08]">
                      <SectionRuleEditor row={row} defs={defs} onChange={(patch) => patchRow(index, patch)} />
                    </div>
                  )}
                </div>
              );
            })
          )}
        </fieldset>

      </Modal>

      {/* 删除分区 / 恢复默认都是破坏性动作：确认框自绘（原生 confirm 不可本地化，
          按钮文案跟随浏览器语言），并把被删的分区名写进说明。 */}
      <ConfirmDialog
        open={pendingRemove !== null}
        title={t("home.customizeDelete")}
        message={t("home.customizeDeleteConfirm", {
          name: pendingRemove !== null && rows[pendingRemove] ? shelfTitle(rows[pendingRemove]!, locale) : "",
        })}
        confirmLabel={t("common.delete")}
        onClose={() => setPendingRemove(null)}
        onConfirm={confirmRemove}
      />
      <ConfirmDialog
        open={pendingReset}
        title={t("home.customizeReset")}
        message={t("home.customizeResetConfirm")}
        confirmLabel={t("home.customizeReset")}
        onClose={() => setPendingReset(false)}
        onConfirm={confirmReset}
      />
    </>
  );
}
