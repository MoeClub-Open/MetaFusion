"use client";

import React, { useEffect, useState } from "react";
import { Plus, Edit2, Trash2, Globe, Power } from "lucide-react";
import {
  fetchAdminExternalDatabases,
  fetchExternalSources,
  createExternalDatabase,
  updateExternalDatabase,
  deleteExternalDatabase,
  ExternalDatabaseDefinition,
} from "@/lib/api";
import { useI18n } from "@/i18n/I18nProvider";
import { DynamicNamesEditor, MultilingualBadges, missingRequiredLocales } from "@/components/common/DynamicNamesEditor";
import { Modal } from "@/components/ui/Modal";
import { UI_LOCALE_CODES } from "@/lib/languages";
import { getKindName, resolveKindOptions, useDefinitions } from "@/lib/definitions";
import { localizeCatalogError } from "@/lib/catalogErrors";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import { Select } from "@/components/ui/Select";

export function ExternalDatabasesTab() {
  const { t, tr, locale } = useI18n();
  const { kinds: serverKinds } = useDefinitions();
  // 适用范围候选与显示名都取服务端 kinds（后台停用的骨架不再出现），字典只作兜底。
  const kindOptions = resolveKindOptions(serverKinds);
  const [items, setItems] = useState<ExternalDatabaseDefinition[]>([]);
  // 有外部查询适配器的 code 集合，来自 GET /external-sources（后端同一份事实：
  // 适配器在代码里、元数据在注册表）。null = 还没取到或取不到，与"无适配器"分开显示。
  const [adapterIds, setAdapterIds] = useState<Set<string> | null>(null);
  const [loading, setLoading] = useState(true);
  const [editingItem, setEditingItem] = useState<ExternalDatabaseDefinition | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [form, setForm] = useState<Partial<ExternalDatabaseDefinition>>({
    code: "",
    names: Object.fromEntries(UI_LOCALE_CODES.map((l) => [l, ""])) as Record<string, string>,
    category: "all",
    url_pattern: "",
    icon: "Globe",
    icon_url: "",
    validation_regex: "",
    description: "",
    sort_order: 10,
  });
  const [error, setError] = useState<string | null>(null);
  // 待确认的删除目标 code：确认框自绘（见文件尾），这里只记"要删哪一个"。
  const [pendingDelete, setPendingDelete] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);

  const loadData = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetchAdminExternalDatabases();
      setItems(Array.isArray(res.items) ? res.items : []);
    } catch (err: any) {
      setError(err.message || t("admin.extdb.loadFailed"));
    } finally {
      setLoading(false);
    }
    // 适配器集合单独取：取不到只让这一列显示"未知"，不影响库列表本身（两者失败原因不同）。
    fetchExternalSources()
      .then((res) => setAdapterIds(new Set((Array.isArray(res.items) ? res.items : []).map((s) => s.id))))
      .catch(() => setAdapterIds(null));
  };

  useEffect(() => {
    loadData();
  }, []);

  const handleOpenCreate = () => {
    setForm({
      code: "",
      names: Object.fromEntries(UI_LOCALE_CODES.map((l) => [l, ""])) as Record<string, string>,
      category: "all",
      url_pattern: "",
      icon: "Globe",
      icon_url: "",
      validation_regex: "",
      description: "",
      sort_order: (items.length + 1) * 10,
    });
    setIsCreating(true);
  };

  const handleOpenEdit = (item: ExternalDatabaseDefinition) => {
    const initialNames: Record<string, string> = { ...(item.names || {}) };
    for (const k of UI_LOCALE_CODES) {
      if (!(k in initialNames)) initialNames[k] = "";
    }

    setEditingItem(item);
    setForm({
      code: item.code,
      names: initialNames,
      category: item.category,
      url_pattern: item.url_pattern,
      icon: item.icon || "Globe",
      icon_url: item.icon_url || "",
      validation_regex: item.validation_regex || "",
      description: item.description || "",
      sort_order: item.sort_order ?? 0,
    });
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    // names 全语言回写：四语齐备是硬约束（与后端 four_locale_names_required 同一口径）。
    const names = { ...(form.names || {}) };
    if (missingRequiredLocales(names).length > 0) {
      setError(t("admin.names.fourLocaleRequired"));
      return;
    }

    const payload = { ...form, names };

    try {
      if (editingItem) {
        await updateExternalDatabase(editingItem.code, payload);
        setEditingItem(null);
      } else {
        await createExternalDatabase(payload);
        setIsCreating(false);
      }
      loadData();
    } catch (err: any) {
      const fallback = editingItem ? t("admin.extdb.updateFailed") : t("admin.extdb.createFailed");
      setError(err.message ? localizeCatalogError(err.message, t) : fallback);
    }
  };

  const handleDelete = async (code: string) => {
    setPendingDelete(null);
    setError(null);
    setDeleting(true);
    try {
      await deleteExternalDatabase(code);
      loadData();
    } catch (err: any) {
      setError(err.message || t("admin.alert.deleteFailed"));
    } finally {
      setDeleting(false);
    }
  };

  // 系统预设只能停用不能删除：走更新接口翻转 is_enabled。
  const handleToggleEnabled = async (item: ExternalDatabaseDefinition) => {
    setError(null);
    try {
      const names = { ...(item.names || {}) };
      await updateExternalDatabase(item.code, {
        ...item,
        names,
        is_enabled: !item.is_enabled,
      });
      loadData();
    } catch (err: any) {
      setError(err.message ? localizeCatalogError(err.message, t) : t("admin.extdb.updateFailed"));
    }
  };

  return (
    <div className="space-y-4">
      {/* 标题说明 */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h2 className="text-sm font-semibold text-text-strong flex items-center gap-2">
            <Globe className="w-4 h-4 text-info" />
            <span>{t("admin.extdb.title")}</span>
          </h2>
          <p className="text-xs text-text-muted font-mono mt-0.5">
            {t("admin.extdb.desc")}
          </p>
        </div>

        <button
          onClick={handleOpenCreate}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-sky-500 hover:bg-sky-400 text-black font-semibold text-xs transition-colors duration-fast ease-soft shadow-xs"
        >
          <Plus className="w-3.5 h-3.5" />
          <span>{t("admin.extdb.new")}</span>
        </button>
      </div>

      {error && (
        <div className="p-3 bg-rose-500/10 border border-rose-500/30 rounded-lg text-danger text-xs font-mono">
          {error}
        </div>
      )}

      {/* 列表表格 */}
      <div className="rounded-xl border border-line bg-surface overflow-hidden">
        <table className="w-full text-left border-collapse text-xs">
          <thead>
            <tr className="border-b border-line bg-surfaceSubtle font-mono text-[11px] text-text-muted uppercase tracking-wider">
              <th className="py-3 px-4">{t("admin.extdb.colDb")}</th>
              <th className="py-3 px-4">{t("admin.extdb.colNames")}</th>
              <th className="py-3 px-4">{t("admin.extdb.colScope")}</th>
              <th className="py-3 px-4">{t("admin.extdb.colUrl")}</th>
              <th className="py-3 px-4">{t("admin.extdb.colRegex")}</th>
              <th className="py-3 px-4 text-center">{t("admin.extdb.colSort")}</th>
              <th className="py-3 px-4 text-center">{t("admin.extdb.colQueryAdapter")}</th>
              <th className="py-3 px-4 text-right">{t("admin.extdb.colActions")}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle font-sans">
            {loading ? (
              <tr>
                <td colSpan={8} className="py-8 text-center text-text-faint font-mono">
                  {t("common.loadingGeneric")}
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={8} className="py-8 text-center text-text-faint font-mono">
                  {t("admin.extdb.noData")}
                </td>
              </tr>
            ) : (
              items.map((item) => (
                <tr key={item.code} className="hover:bg-surfaceSubtle transition-colors duration-fast ease-soft">
                  {/* 名称与图标 */}
                  <td className="py-3 px-4">
                    <div className="flex items-center gap-2.5">
                      {item.icon_url ? (
                        <img src={item.icon_url} alt="" className="w-4 h-4 object-contain" />
                      ) : (
                        <Globe className="w-4 h-4 text-info opacity-80" />
                      )}
                      <div>
                        <div className="font-mono font-bold text-text-strong text-[11px]">{item.code}</div>
                        {item.description && (
                          <div className="text-[10px] text-text-faint line-clamp-1 max-w-[150px]">{item.description}</div>
                        )}
                      </div>
                    </div>
                  </td>

                  {/* 多语言胶囊展示 */}
                  <td className="py-3 px-4">
                    <MultilingualBadges names={item.names} />
                  </td>

                  {/* 范畴 */}
                  <td className="py-3 px-4">
                    <span className="px-2 py-0.5 rounded font-mono text-[10px] bg-surfaceSubtle border border-line text-text-body">
                      {item.category.toUpperCase()}
                    </span>
                  </td>

                  {/* URL 模板 */}
                  <td className="py-3 px-4">
                    <div className="font-mono text-[11px] text-text-body truncate max-w-xs" title={item.url_pattern}>
                      {item.url_pattern}
                    </div>
                  </td>

                  {/* 校验正则 */}
                  <td className="py-3 px-4">
                    <div className="font-mono text-[10px] text-text-muted truncate max-w-[120px]" title={item.validation_regex}>
                      {item.validation_regex || "--"}
                    </div>
                  </td>

                  {/* 排序 */}
                  <td className="py-3 px-4 text-center font-mono text-text-muted">
                    {item.sort_order}
                  </td>

                  {/* 导入适配器：数据来自 /external-sources 的 id 集合，与外部来源查询同源 */}
                  <td className="py-3 px-4 text-center">
                    {adapterIds === null ? (
                      <span
                        className="font-mono text-[10px] text-text-faint"
                        title={t("admin.extdb.queryAdapterUnknownHint")}
                      >
                        {t("admin.extdb.queryAdapterUnknown")}
                      </span>
                    ) : adapterIds.has(item.code) ? (
                      <span
                        className="inline-block px-2 py-0.5 rounded font-mono text-[10px] bg-emerald-500/10 border border-emerald-500/30 text-success"
                        title={t("admin.extdb.queryAdapterSupportedHint")}
                      >
                        {t("admin.extdb.queryAdapterSupported")}
                      </span>
                    ) : (
                      <span
                        className="inline-block px-2 py-0.5 rounded font-mono text-[10px] bg-surfaceSubtle border border-line text-text-faint"
                        title={t("admin.extdb.queryAdapterUnsupportedHint")}
                      >
                        {t("admin.extdb.queryAdapterUnsupported")}
                      </span>
                    )}
                  </td>

                  {/* 操作 */}
                  <td className="py-3 px-4 text-right">
                    <div className="flex items-center justify-end gap-1.5">
                      <button
                        onClick={() => handleOpenEdit(item)}
                        title={t("common.edit")}
                        className="p-1.5 rounded-md hover:bg-surfaceHover text-text-muted hover:text-text-strong transition-colors duration-fast ease-soft"
                      >
                        <Edit2 className="w-3.5 h-3.5" />
                      </button>
                      <button
                        onClick={() => handleToggleEnabled(item)}
                        title={item.is_enabled ? t("admin.extdb.disable") : t("admin.extdb.enable")}
                        className={`p-1.5 rounded-md transition-colors duration-fast ease-soft ${
                          item.is_enabled
                            ? "hover:bg-amber-500/10 text-text-muted hover:text-warn"
                            : "hover:bg-emerald-500/10 text-text-faint hover:text-success"
                        }`}
                      >
                        <Power className="w-3.5 h-3.5" />
                      </button>
                      {!item.is_system && (
                        <button
                          onClick={() => setPendingDelete(item.code)}
                          title={t("common.delete")}
                          className="p-1.5 rounded-md hover:bg-rose-500/10 text-text-muted hover:text-danger transition-colors duration-fast ease-soft"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      )}
                    </div>
                    {item.is_system && (
                      <div className="text-[10px] text-text-faint font-mono mt-1">
                        {t("admin.extdb.systemProtected")}
                      </div>
                    )}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* 创建 / 编辑弹窗 */}
      <Modal
        open={isCreating || !!editingItem}
        onClose={() => {
          setIsCreating(false);
          setEditingItem(null);
        }}
        title={editingItem ? t("common.edit") : t("admin.extdb.newPreset")}
        icon={<Globe className="w-4 h-4 text-info" />}
      >
        <form onSubmit={handleSave} className="space-y-4 text-xs">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <div>
              <label className="block text-text-muted font-mono text-[11px] mb-1">
                {t("admin.extdb.fieldCode")}
              </label>
              <input
                type="text"
                required
                disabled={!!editingItem}
                placeholder={t("admin.extdb.codePlaceholder")}
                value={form.code || ""}
                onChange={(e) =>
                  setForm({
                    ...form,
                    code: e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, ""),
                  })
                }
                className="w-full bg-surface border border-theme rounded px-2.5 py-1.5 text-xs text-foreground font-mono focus:border-sky-400 outline-none disabled:opacity-50"
              />
            </div>
            <div>
              <label className="block text-text-muted font-mono text-[11px] mb-1">
                {t("admin.extdb.fieldCategory")}
              </label>
              <Select
                value={form.category || "all"}
                onChange={(v) => setForm({ ...form, category: v })}
                options={["all", ...kindOptions].map((k) => ({
                  value: k,
                  label: getKindName(serverKinds, k, locale, tr(`catalog.kind.${k}`, k)),
                }))}
                aria-label={t("admin.extdb.fieldCategory")}
              />
            </div>
          </div>

          <DynamicNamesEditor
            label={t("admin.extdb.fieldNames")}
            value={form.names}
            onChange={(names) => setForm({ ...form, names })}
            required
          />

          <div>
            <label className="block text-text-muted font-mono text-[11px] mb-1">
              {t("admin.extdb.fieldUrl")}
            </label>
            <input
              type="text"
              required
              placeholder={t("admin.extdb.urlPlaceholder")}
              value={form.url_pattern || ""}
              onChange={(e) => setForm({ ...form, url_pattern: e.target.value })}
              className="w-full bg-surface border border-theme rounded px-2.5 py-1.5 text-xs text-foreground font-mono focus:border-sky-400 outline-none"
            />
            <p className="text-[10px] text-text-faint mt-1 font-mono">
              {t("admin.extdb.urlHint")}
            </p>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <div>
              <label className="block text-text-muted font-mono text-[11px] mb-1">
                {t("admin.extdb.fieldRegex")}
              </label>
              <input
                type="text"
                placeholder={t("admin.extdb.regexPlaceholder")}
                value={form.validation_regex || ""}
                onChange={(e) => setForm({ ...form, validation_regex: e.target.value })}
                className="w-full bg-surface border border-theme rounded px-2.5 py-1.5 text-xs text-foreground font-mono focus:border-sky-400 outline-none"
              />
            </div>
            <div>
              <label className="block text-text-muted font-mono text-[11px] mb-1">
                {t("admin.extdb.fieldIcon")}
              </label>
              <input
                type="text"
                placeholder="https://..."
                value={form.icon_url || ""}
                onChange={(e) => setForm({ ...form, icon_url: e.target.value })}
                className="w-full bg-surface border border-theme rounded px-2.5 py-1.5 text-xs text-foreground font-mono focus:border-sky-400 outline-none"
              />
            </div>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <div>
              <label className="block text-text-muted font-mono text-[11px] mb-1">
                {t("admin.extdb.fieldDesc")}
              </label>
              <input
                type="text"
                placeholder={t("admin.extdb.descPlaceholder")}
                value={form.description || ""}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
                className="w-full bg-surface border border-theme rounded px-2.5 py-1.5 text-xs text-foreground focus:border-sky-400 outline-none"
              />
            </div>
            <div>
              <label className="block text-text-muted font-mono text-[11px] mb-1">
                {t("admin.extdb.fieldSort")}
              </label>
              <input
                type="number"
                value={form.sort_order ?? 0}
                onChange={(e) =>
                  setForm({ ...form, sort_order: parseInt(e.target.value) || 0 })
                }
                className="w-full bg-surface border border-theme rounded px-2.5 py-1.5 text-xs text-foreground font-mono focus:border-sky-400 outline-none"
              />
            </div>
          </div>

          <div className="flex justify-end gap-2 pt-2 border-t border-line">
            <button
              type="button"
              onClick={() => {
                setIsCreating(false);
                setEditingItem(null);
              }}
              className="px-3 py-1.5 rounded bg-surfaceSubtle hover:bg-surfaceHover text-text-body text-xs font-mono"
            >
              {t("common.cancel")}
            </button>
            <button
              type="submit"
              className="px-4 py-1.5 rounded bg-sky-500 text-black text-xs font-bold font-mono hover:bg-sky-400 transition-colors duration-fast ease-soft"
            >
              {t("common.save")}
            </button>
          </div>
        </form>
      </Modal>

      {/* 删除外部库是破坏性动作：确认框自绘（原生 confirm 不可本地化），说明里带上被删的 code。 */}
      <ConfirmDialog
        open={pendingDelete !== null}
        title={t("common.delete")}
        message={t("admin.extdb.deleteConfirm", { code: pendingDelete ?? "" })}
        confirmLabel={t("common.delete")}
        busy={deleting}
        onClose={() => setPendingDelete(null)}
        onConfirm={() => { if (pendingDelete) void handleDelete(pendingDelete); }}
      />
    </div>
  );
}
