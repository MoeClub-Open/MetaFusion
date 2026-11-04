"use client";

import React, { useEffect, useState } from "react";
import { Layers, Plus, Eye, Edit2, Trash2 } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { DynamicNamesEditor, MultilingualBadges, missingRequiredLocales } from "@/components/common/DynamicNamesEditor";
import { Modal } from "@/components/ui/Modal";
import { fetchDefinitions } from "@/lib/definitions";
import type { DynamicDefinitions } from "@/lib/definitions";
import { localizeCatalogError } from "@/lib/catalogErrors";
import { UI_LOCALE_CODES } from "@/lib/languages";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import { Select } from "@/components/ui/Select";
// 货架查询形状只有一份定义（lib/homeSections.ts）：首页自定义与后台货架页共用同一种查询。
import type { ShelfQuery } from "@/lib/homeSections";

export interface ShelfItem {
  id: number;
  slug: string;
  names?: Record<string, string>;
  query: ShelfQuery;
  sort: string;
  icon: string;
  enabled: boolean;
  sort_order: number;
}

const EMPTY_NAMES = Object.fromEntries(UI_LOCALE_CODES.map((l) => [l, ""])) as Record<string, string>;

function emptyShelf(count: number): Partial<ShelfItem> {
  return {
    slug: "",
    names: { ...EMPTY_NAMES },
    query: { tags: [], fields: {}, vocab_terms: {}, relations: [] },
    sort: "updated",
    icon: "",
    enabled: true,
    sort_order: (count + 1) * 10,
  };
}

function describeRule(shelf: ShelfItem): string[] {
  const q = shelf.query || {};
  const parts: string[] = [];
  (q.tags || []).forEach((x) => parts.push(`tag:${x}`));
  Object.entries(q.fields || {}).forEach(([k, vs]) =>
    (vs || []).forEach((v) => parts.push(`${k}=${v}`)),
  );
  Object.entries(q.vocab_terms || {}).forEach(([k, vs]) =>
    (vs || []).forEach((v) => parts.push(`${k}:${v}`)),
  );
  (q.relations || []).forEach((x) => parts.push(`rel:${x}`));
  return parts;
}

export function ShelvesTab() {
  const { t } = useI18n();
  const [items, setItems] = useState<ShelfItem[]>([]);
  const [defs, setDefs] = useState<DynamicDefinitions | null>(null);
  const [tagOptions, setTagOptions] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<ShelfItem | null>(null);
  const [creating, setCreating] = useState(false);
  // 待确认的删除目标：确认框自绘（见文件尾），这里只记"要删哪一个"。
  const [pendingDelete, setPendingDelete] = useState<ShelfItem | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [form, setForm] = useState<Partial<ShelfItem>>(emptyShelf(0));
  const [tagInput, setTagInput] = useState("");
  const [relInput, setRelInput] = useState("");
  const [fieldKey, setFieldKey] = useState("");
  const [fieldVal, setFieldVal] = useState("");
  const [vocabKey, setVocabKey] = useState("");
  const [vocabVal, setVocabVal] = useState("");

  const loadData = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetch("/api/admin/shelves", { credentials: "same-origin" }).then(async (r) => {
        const body = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`);
        return body as { items: ShelfItem[] };
      });
      setItems(Array.isArray(res.items) ? res.items : []);
    } catch (err: any) {
      setError(err.message || t("admin.shelves.loadFailed"));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadData();
    fetchDefinitions().then((d) => setDefs(d)).catch(() => {});
    fetch("/api/catalog/tags?limit=500", { credentials: "same-origin" })
      .then((r) => r.ok ? r.json() : Promise.reject(new Error(String(r.status))))
      .then((data) => setTagOptions((data.items || []).map((item: { name: string }) => item.name)))
      .catch(() => {});
  }, []);

  const openCreate = () => {
    setForm(emptyShelf(items.length));
    setTagInput("");
    setRelInput("");
    setFieldKey("");
    setFieldVal("");
    setVocabKey("");
    setVocabVal("");
    setEditing(null);
    setCreating(true);
  };

  const openEdit = (shelf: ShelfItem) => {
    const names: Record<string, string> = { ...(shelf.names || {}) };
    for (const k of Object.keys(EMPTY_NAMES)) {
      if (!(k in names)) names[k] = "";
    }
    setForm({
      ...shelf,
      names,
      query: {
        tags: [...(shelf.query?.tags || [])],
        fields: { ...(shelf.query?.fields || {}) },
        vocab_terms: { ...(shelf.query?.vocab_terms || {}) },
        relations: [...(shelf.query?.relations || [])],
      },
    });
    setTagInput("");
    setRelInput("");
    setFieldKey("");
    setFieldVal("");
    setVocabKey("");
    setVocabVal("");
    setEditing(shelf);
    setCreating(false);
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    const names = { ...(form.names || {}) };
    // 四语齐备（服务端有同名硬校验）：先在本地拦一次，省掉注定失败的请求。
    if (!form.slug?.trim() || missingRequiredLocales(names).length > 0) {
      setError(t("admin.shelves.required"));
      return;
    }
    const payload = {
      slug: form.slug.trim().toLowerCase(),
      names,
      query: {
        tags: form.query?.tags || [],
        fields: form.query?.fields || {},
        vocab_terms: form.query?.vocab_terms || {},
        relations: form.query?.relations || [],
      },
      sort: form.sort || "updated",
      icon: form.icon || "",
      enabled: form.enabled ?? true,
      sort_order: form.sort_order ?? 0,
    };
    try {
      if (editing) {
        await fetch(`/api/admin/shelves/${editing.id}`, {
          method: "PUT",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
        }).then(async (r) => {
          const body = await r.json().catch(() => ({}));
          if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`);
        });
      } else {
        await fetch("/api/admin/shelves", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
        }).then(async (r) => {
          const body = await r.json().catch(() => ({}));
          if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`);
        });
      }
      setCreating(false);
      setEditing(null);
      loadData();
    } catch (err: any) {
      setError(err.message ? localizeCatalogError(err.message, t) : t("admin.shelves.saveFailed"));
    }
  };

  const handleDelete = async (shelf: ShelfItem) => {
    setPendingDelete(null);
    setError(null);
    setDeleting(true);
    try {
      await fetch(`/api/admin/shelves/${shelf.id}`, {
        method: "DELETE",
        credentials: "same-origin",
      }).then(async (r) => {
        const body = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`);
      });
      loadData();
    } catch (err: any) {
      setError(err.message || t("admin.shelves.deleteFailed"));
    } finally {
      setDeleting(false);
    }
  };

  const q = form.query || { tags: [], fields: {}, vocab_terms: {}, relations: [] };
  const addTag = () => {
    const v = tagInput.trim();
    if (!v || (q.tags || []).includes(v)) return;
    setForm({ ...form, query: { ...q, tags: [...(q.tags || []), v] } });
    setTagInput("");
  };
  const addRelation = () => {
    const v = relInput.trim();
    if (!v || (q.relations || []).includes(v)) return;
    setForm({ ...form, query: { ...q, relations: [...(q.relations || []), v] } });
    setRelInput("");
  };
  const addFieldPair = () => {
    const k = fieldKey.trim();
    const v = fieldVal.trim();
    if (!k || !v) return;
    const cur = { ...(q.fields || {}) };
    cur[k] = [...(cur[k] || []), ...(cur[k] || []).includes(v) ? [] : [v]];
    setForm({ ...form, query: { ...q, fields: cur } });
    setFieldVal("");
  };
  const addVocabPair = () => {
    const k = vocabKey.trim();
    const v = vocabVal.trim();
    if (!k || !v) return;
    const cur = { ...(q.vocab_terms || {}) };
    cur[k] = [...(cur[k] || []), ...(cur[k] || []).includes(v) ? [] : [v]];
    setForm({ ...form, query: { ...q, vocab_terms: cur } });
    setVocabVal("");
  };

  const relationOptions = Object.keys(defs?.relations || {});
  const fieldOptions = Object.keys(defs?.fields || {});
  const vocabOptions = Object.keys(defs?.vocabularies || {});

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-text-strong flex items-center gap-2">
            <Layers className="w-4 h-4 shrink-0 text-success" />
            {t("admin.shelves.title")}
          </h2>
        </div>
        <button
          onClick={openCreate}
          className="shrink-0 whitespace-nowrap px-3 py-1.5 rounded-lg bg-emerald-400 text-black text-xs font-semibold hover:bg-emerald-300 transition-colors duration-fast ease-soft flex items-center gap-1.5"
        >
          <Plus className="w-3.5 h-3.5 shrink-0" />
          <span>{t("admin.shelves.new")}</span>
        </button>
      </div>

      {error && (
        <div className="p-3 bg-rose-500/10 border border-rose-500/30 rounded-lg text-danger text-xs font-mono">
          {error}
        </div>
      )}

      <div className="rounded-xl border border-line bg-surface overflow-hidden">
        <table className="w-full text-left text-xs">
          <thead className="bg-surfaceSubtle border-b border-line text-text-muted text-[11px] font-mono">
            <tr>
              <th className="py-3 px-4">{t("admin.shelves.colSlug")}</th>
              <th className="py-3 px-3">{t("admin.shelves.colNames")}</th>
              <th className="py-3 px-3">{t("admin.shelves.colRule")}</th>
              <th className="py-3 px-3">{t("admin.shelves.colSort")}</th>
              <th className="py-3 px-3">{t("admin.shelves.colIcon")}</th>
              <th className="py-3 px-3 text-center">{t("admin.shelves.colEnabled")}</th>
              <th className="py-3 px-3">{t("admin.shelves.colOrder")}</th>
              <th className="py-3 px-4 text-right">{t("admin.shelves.colAction")}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {loading ? (
              <tr>
                <td colSpan={8} className="py-12 text-center text-text-faint font-mono">
                  {t("common.loadingGeneric")}
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={8} className="py-12 text-center text-text-faint font-mono">
                  {t("admin.shelves.noData")}
                </td>
              </tr>
            ) : (
              items.map((shelf) => {
                const rule = describeRule(shelf);
                return (
                  <tr key={shelf.id} className="hover:bg-surfaceSubtle">
                    <td className="py-3 px-4 font-mono text-success-soft font-bold">{shelf.slug}</td>
                    <td className="py-3 px-3">
                      <MultilingualBadges names={shelf.names} />
                    </td>
                    <td className="py-3 px-3">
                      <div className="flex flex-wrap gap-1 max-w-md">
                        {rule.length === 0 ? (
                          <span className="text-text-faint font-mono text-[10px]">{t("admin.shelves.emptyRule")}</span>
                        ) : (
                          rule.map((r) => (
                            <span
                              key={r}
                              className="px-1.5 py-0.2 rounded bg-emerald-500/10 text-success-soft border border-emerald-500/20 text-[10px] font-mono"
                            >
                              {r}
                            </span>
                          ))
                        )}
                      </div>
                    </td>
                    <td className="py-3 px-3 font-mono text-[11px] text-text-body">{shelf.sort || "updated"}</td>
                    <td className="py-3 px-3 font-mono text-[11px] text-text-body">{shelf.icon || "—"}</td>
                    <td className="py-3 px-3 text-center">
                      <span className={`w-2 h-2 inline-block rounded-full ${shelf.enabled ? "bg-emerald-400" : "bg-text-faint"}`} />
                    </td>
                    <td className="py-3 px-3 font-mono text-text-muted">{shelf.sort_order ?? 0}</td>
                    <td className="py-3 px-4 text-right">
                      <div className="flex items-center justify-end gap-1.5">
                        <a
                          href={`/explore?shelf=${encodeURIComponent(shelf.slug)}`}
                          target="_blank"
                          rel="noreferrer"
                          className="p-1.5 rounded-md hover:bg-surfaceHover text-text-muted hover:text-text-strong transition-colors duration-fast ease-soft"
                          title={t("admin.shelves.viewShelf")}
                        >
                          <Eye className="w-3.5 h-3.5" />
                        </a>
                        <button
                          onClick={() => openEdit(shelf)}
                          className="p-1.5 rounded-md hover:bg-surfaceHover text-text-muted hover:text-text-strong transition-colors duration-fast ease-soft"
                          title={t("common.edit")}
                        >
                          <Edit2 className="w-3.5 h-3.5" />
                        </button>
                        <button
                          onClick={() => setPendingDelete(shelf)}
                          className="p-1.5 rounded-md hover:bg-rose-500/10 text-text-muted hover:text-danger transition-colors duration-fast ease-soft"
                          title={t("common.delete")}
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>

      <Modal
        open={creating || !!editing}
        onClose={() => {
          setCreating(false);
          setEditing(null);
        }}
        title={editing ? t("admin.shelves.editTitle") : t("admin.shelves.createTitle")}
        icon={<Layers className="w-4 h-4 text-success" />}
      >
        <form onSubmit={handleSave} className="space-y-3 text-xs max-h-[80vh] overflow-y-auto pr-1">
          <div>
            <label className="block text-[11px] font-mono text-text-body font-medium mb-1">
              {t("admin.shelves.fieldSlug")}
            </label>
            <input
              required
              disabled={!!editing}
              value={form.slug || ""}
              onChange={(e) =>
                setForm({ ...form, slug: e.target.value.toLowerCase().replace(/[^a-z0-9-_]/g, "") })
              }
              placeholder="e.g. music"
              className="w-full px-3 py-2 rounded-lg bg-surfaceSubtle border border-line text-text-strong font-mono focus:outline-none focus:border-emerald-400 disabled:opacity-50"
            />
          </div>

          <DynamicNamesEditor
            label={t("admin.shelves.colNames")}
            value={form.names}
            onChange={(names) => setForm({ ...form, names })}
            required
          />

          <div className="p-3 rounded-xl bg-surfaceSubtle border border-line-subtle space-y-3">
            <div className="font-mono text-xs font-bold text-text-body">
              {t("admin.shelves.queryTags")}
            </div>
            <div className="flex flex-wrap gap-1.5">
              {(q.tags || []).map((x) => (
                <span key={x} className="px-2 py-0.5 rounded bg-emerald-500/15 text-success-soft border border-emerald-500/30 text-xs font-mono flex items-center gap-1">
                  {x}
                  <button
                    type="button"
                    onClick={() => setForm({ ...form, query: { ...q, tags: (q.tags || []).filter((y) => y !== x) } })}
                    className="hover:text-danger"
                  >
                    ×
                  </button>
                </span>
              ))}
            </div>
            <div className="flex gap-2">
              <input
                list="shelf-tag-options"
                value={tagInput}
                onChange={(e) => setTagInput(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); addTag(); } }}
                placeholder="e.g. music"
                className="min-w-0 flex-1 px-2.5 py-1.5 rounded bg-surfaceSubtle border border-line text-xs text-text-strong font-mono focus:border-emerald-400 outline-none"
              />
              <datalist id="shelf-tag-options">
                {tagOptions.map((x) => (<option key={x} value={x} />))}
              </datalist>
              <button type="button" onClick={addTag} className="shrink-0 px-3 py-1.5 rounded bg-surfaceSubtle hover:bg-surfaceHover text-xs">
                +
              </button>
            </div>
          </div>

          <div className="p-3 rounded-xl bg-surfaceSubtle border border-line-subtle space-y-3">
            <div className="font-mono text-xs font-bold text-text-body">
              {t("admin.shelves.queryFields")}
            </div>
            <div className="flex flex-wrap gap-1.5">
              {Object.entries(q.fields || {}).flatMap(([k, vs]) =>
                (vs || []).map((v) => (
                  <span key={`${k}=${v}`} className="px-2 py-0.5 rounded bg-sky-500/15 text-info-soft border border-sky-500/30 text-xs font-mono flex items-center gap-1">
                    {k}={v}
                    <button
                      type="button"
                      onClick={() => {
                        const cur = { ...(q.fields || {}) };
                        cur[k] = (cur[k] || []).filter((y) => y !== v);
                        if (cur[k].length === 0) delete cur[k];
                        setForm({ ...form, query: { ...q, fields: cur } });
                      }}
                      className="hover:text-danger"
                    >
                      ×
                    </button>
                  </span>
                )),
              )}
            </div>
            <div className="flex gap-2">
              <input
                list="shelf-field-options"
                value={fieldKey}
                onChange={(e) => setFieldKey(e.target.value)}
                placeholder="field"
                className="min-w-0 flex-1 px-2.5 py-1.5 rounded bg-surfaceSubtle border border-line text-xs text-text-strong font-mono focus:border-emerald-400 outline-none"
              />
              <datalist id="shelf-field-options">
                {fieldOptions.map((x) => (<option key={x} value={x} />))}
              </datalist>
              <input
                value={fieldVal}
                onChange={(e) => setFieldVal(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); addFieldPair(); } }}
                placeholder="value"
                className="min-w-0 flex-1 px-2.5 py-1.5 rounded bg-surfaceSubtle border border-line text-xs text-text-strong font-mono focus:border-emerald-400 outline-none"
              />
              <button type="button" onClick={addFieldPair} className="shrink-0 px-3 py-1.5 rounded bg-surfaceSubtle hover:bg-surfaceHover text-xs">
                +
              </button>
            </div>
          </div>

          <div className="p-3 rounded-xl bg-surfaceSubtle border border-line-subtle space-y-3">
            <div className="font-mono text-xs font-bold text-text-body">
              {t("admin.shelves.queryVocabTerms")}
            </div>
            <div className="flex flex-wrap gap-1.5">
              {Object.entries(q.vocab_terms || {}).flatMap(([k, vs]) =>
                (vs || []).map((v) => (
                  <span key={`${k}:${v}`} className="px-2 py-0.5 rounded bg-amber-500/15 text-warn-soft border border-amber-500/30 text-xs font-mono flex items-center gap-1">
                    {k}:{v}
                    <button
                      type="button"
                      onClick={() => {
                        const cur = { ...(q.vocab_terms || {}) };
                        cur[k] = (cur[k] || []).filter((y) => y !== v);
                        if (cur[k].length === 0) delete cur[k];
                        setForm({ ...form, query: { ...q, vocab_terms: cur } });
                      }}
                      className="hover:text-danger"
                    >
                      ×
                    </button>
                  </span>
                )),
              )}
            </div>
            <div className="flex gap-2">
              <input
                list="shelf-vocab-options"
                value={vocabKey}
                onChange={(e) => setVocabKey(e.target.value)}
                placeholder="vocab"
                className="min-w-0 flex-1 px-2.5 py-1.5 rounded bg-surfaceSubtle border border-line text-xs text-text-strong font-mono focus:border-emerald-400 outline-none"
              />
              <datalist id="shelf-vocab-options">
                {vocabOptions.map((x) => (<option key={x} value={x} />))}
              </datalist>
              <input
                list={vocabKey ? `shelf-term-options-${vocabKey}` : undefined}
                value={vocabVal}
                onChange={(e) => setVocabVal(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); addVocabPair(); } }}
                placeholder="term"
                className="min-w-0 flex-1 px-2.5 py-1.5 rounded bg-surfaceSubtle border border-line text-xs text-text-strong font-mono focus:border-emerald-400 outline-none"
              />
              {vocabKey && defs?.vocabularies?.[vocabKey] && (
                <datalist id={`shelf-term-options-${vocabKey}`}>
                  {Object.keys(defs.vocabularies[vocabKey].terms || {}).map((x) => (<option key={x} value={x} />))}
                </datalist>
              )}
              <button type="button" onClick={addVocabPair} className="shrink-0 px-3 py-1.5 rounded bg-surfaceSubtle hover:bg-surfaceHover text-xs">
                +
              </button>
            </div>
          </div>

          <div className="p-3 rounded-xl bg-surfaceSubtle border border-line-subtle space-y-3">
            <div className="font-mono text-xs font-bold text-text-body">
              {t("admin.shelves.queryRelations")}
            </div>
            <div className="flex flex-wrap gap-1.5">
              {(q.relations || []).map((x) => (
                <span key={x} className="px-2 py-0.5 rounded bg-purple-500/15 text-alt-soft border border-purple-500/30 text-xs font-mono flex items-center gap-1">
                  {x}
                  <button
                    type="button"
                    onClick={() => setForm({ ...form, query: { ...q, relations: (q.relations || []).filter((y) => y !== x) } })}
                    className="hover:text-danger"
                  >
                    ×
                  </button>
                </span>
              ))}
            </div>
            <div className="flex gap-2">
              <input
                list="shelf-relation-options"
                value={relInput}
                onChange={(e) => setRelInput(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); addRelation(); } }}
                placeholder="e.g. adaptation_of"
                className="min-w-0 flex-1 px-2.5 py-1.5 rounded bg-surfaceSubtle border border-line text-xs text-text-strong font-mono focus:border-emerald-400 outline-none"
              />
              <datalist id="shelf-relation-options">
                {relationOptions.map((x) => (<option key={x} value={x} />))}
              </datalist>
              <button type="button" onClick={addRelation} className="shrink-0 px-3 py-1.5 rounded bg-surfaceSubtle hover:bg-surfaceHover text-xs">
                +
              </button>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-[11px] font-mono text-text-body font-medium mb-1">
                {t("admin.shelves.fieldSort")}
              </label>
              <Select
                value={form.sort || "updated"}
                onChange={(v) => setForm({ ...form, sort: v })}
                options={[
                  { value: "updated", label: t("admin.shelves.sortUpdated") },
                  { value: "created", label: t("admin.shelves.sortCreated") },
                  { value: "title", label: t("admin.shelves.sortTitle") },
                ]}
                aria-label={t("admin.shelves.fieldSort")}
              />
            </div>
            <div>
              <label className="block text-[11px] font-mono text-text-body font-medium mb-1">
                {t("admin.shelves.fieldIcon")}
              </label>
              <input
                value={form.icon || ""}
                onChange={(e) => setForm({ ...form, icon: e.target.value })}
                placeholder="e.g. Disc"
                className="w-full px-3 py-2 rounded-lg bg-surfaceSubtle border border-line text-xs text-text-strong font-mono focus:outline-none focus:border-emerald-400"
              />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-[11px] font-mono text-text-body font-medium mb-1">
                {t("admin.shelves.fieldOrder")}
              </label>
              <input
                type="number"
                value={form.sort_order ?? 0}
                onChange={(e) => setForm({ ...form, sort_order: parseInt(e.target.value) || 0 })}
                className="w-full px-3 py-2 rounded-lg bg-surfaceSubtle border border-line text-text-strong font-mono focus:outline-none focus:border-emerald-400"
              />
            </div>
            <label className="flex items-center gap-2 text-text-body cursor-pointer pb-1 self-end">
              <input
                type="checkbox"
                checked={form.enabled ?? true}
                onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
                className="rounded border-line-strong bg-surfaceSubtle text-emerald-500 focus:ring-0"
              />
              <span className="text-xs">{t("admin.shelves.enabledOpt")}</span>
            </label>
          </div>

          <div className="flex flex-wrap items-center justify-end gap-2 pt-4 border-t border-line">
            <button
              type="button"
              onClick={() => {
                setCreating(false);
                setEditing(null);
              }}
              className="shrink-0 whitespace-nowrap px-3 py-1.5 rounded-lg border border-line text-text-muted hover:text-text-strong transition-colors duration-fast ease-soft"
            >
              {t("common.cancel")}
            </button>
            <button
              type="submit"
              className="shrink-0 whitespace-nowrap px-4 py-1.5 rounded-lg bg-emerald-400 text-black font-semibold hover:bg-emerald-300 transition-colors duration-fast ease-soft"
            >
              {t("common.save")}
            </button>
          </div>
        </form>
      </Modal>

      {/* 删除货架是破坏性动作：确认框自绘（原生 confirm 不可本地化），说明里带上被删货架的 slug。 */}
      <ConfirmDialog
        open={pendingDelete !== null}
        title={t("common.delete")}
        message={t("admin.shelves.deleteConfirm", { slug: pendingDelete?.slug ?? "" })}
        confirmLabel={t("common.delete")}
        busy={deleting}
        onClose={() => setPendingDelete(null)}
        onConfirm={() => { if (pendingDelete) void handleDelete(pendingDelete); }}
      />
    </div>
  );
}
